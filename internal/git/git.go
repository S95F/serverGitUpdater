package git

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/s95f/servergitupdater/internal/build"
	"github.com/s95f/servergitupdater/internal/caddy"
	"github.com/s95f/servergitupdater/internal/config"
	"github.com/s95f/servergitupdater/internal/service"
	"github.com/s95f/servergitupdater/internal/tmpl"
)

type Status struct {
	Branch        string    `json:"branch"`
	Commit        string    `json:"commit"`
	CommitSubject string    `json:"commit_subject"`
	CommitTime    time.Time `json:"commit_time"`
	Dirty         bool      `json:"dirty"`
	BehindAhead   string    `json:"behind_ahead"`
}

type LogEntry struct {
	Time     time.Time `json:"time"`
	Source   string    `json:"source"`
	Success  bool      `json:"success"`
	Output   string    `json:"output"`
	Duration string    `json:"duration"`
	OldHead  string    `json:"old_head,omitempty"`
	NewHead  string    `json:"new_head,omitempty"`
}

type Updater struct {
	cfg    *config.Config
	log    *slog.Logger
	mu     sync.Mutex // serialize git operations
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewUpdater(cfg *config.Config, log *slog.Logger) *Updater {
	return &Updater{cfg: cfg, log: log}
}

// Start launches the auto-update loop. Safe to call multiple times; previous loop is stopped.
func (u *Updater) Start(parent context.Context) {
	u.Stop()
	ctx, cancel := context.WithCancel(parent)
	u.cancel = cancel
	u.wg.Add(1)
	go u.loop(ctx)
}

func (u *Updater) Stop() {
	if u.cancel != nil {
		u.cancel()
		u.cancel = nil
	}
	u.wg.Wait()
}

func (u *Updater) loop(ctx context.Context) {
	defer u.wg.Done()
	for {
		snap := u.cfg.Snapshot()
		interval := time.Duration(snap.AutoUpdateMinutes) * time.Minute
		if interval < time.Minute {
			interval = time.Minute
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
		snap = u.cfg.Snapshot()
		if !snap.AutoUpdateEnabled {
			continue
		}
		if _, err := u.RunUpdate(ctx, "auto"); err != nil {
			u.log.Warn("auto-update failed", "err", err)
		}
	}
}

// Status reads the current repo status without modifying anything.
func (u *Updater) Status(ctx context.Context) (Status, error) {
	snap := u.cfg.Snapshot()
	if snap.RepoPath == "" {
		return Status{}, errors.New("repo_path is not configured")
	}
	if _, err := os.Stat(snap.RepoPath); err != nil {
		return Status{}, fmt.Errorf("repo_path: %w", err)
	}

	branch, _ := u.runGit(ctx, snap, "rev-parse", "--abbrev-ref", "HEAD")
	commit, _ := u.runGit(ctx, snap, "rev-parse", "--short", "HEAD")
	subject, _ := u.runGit(ctx, snap, "log", "-1", "--pretty=%s")
	tStr, _ := u.runGit(ctx, snap, "log", "-1", "--pretty=%cI")
	dirtyOut, _ := u.runGit(ctx, snap, "status", "--porcelain")
	ba, _ := u.runGit(ctx, snap, "rev-list", "--left-right", "--count", "HEAD..."+snap.Remote+"/"+snap.Branch)

	t, _ := time.Parse(time.RFC3339, strings.TrimSpace(tStr))
	return Status{
		Branch:        strings.TrimSpace(branch),
		Commit:        strings.TrimSpace(commit),
		CommitSubject: strings.TrimSpace(subject),
		CommitTime:    t,
		Dirty:         strings.TrimSpace(dirtyOut) != "",
		BehindAhead:   strings.TrimSpace(ba),
	}, nil
}

// RunUpdate fetches and fast-forwards the configured branch, then runs the post-update hook.
// Source is recorded in the log ("manual" or "auto").
func (u *Updater) RunUpdate(ctx context.Context, source string) (LogEntry, error) {
	u.mu.Lock()
	defer u.mu.Unlock()

	snap := u.cfg.Snapshot()
	start := time.Now()
	entry := LogEntry{Time: start, Source: source}

	if snap.RepoPath == "" {
		entry.Output = "repo_path is not configured"
		u.appendLog(entry)
		return entry, errors.New(entry.Output)
	}
	if _, err := os.Stat(snap.RepoPath); err != nil {
		entry.Output = "repo_path: " + err.Error()
		u.appendLog(entry)
		return entry, err
	}

	var buf bytes.Buffer

	oldHead, _ := u.runGit(ctx, snap, "rev-parse", "HEAD")
	entry.OldHead = strings.TrimSpace(oldHead)

	for _, args := range [][]string{
		{"fetch", "--prune", snap.Remote},
		{"checkout", snap.Branch},
		{"reset", "--hard", snap.Remote + "/" + snap.Branch},
	} {
		out, err := u.runGit(ctx, snap, args...)
		fmt.Fprintf(&buf, "$ git %s\n%s\n", strings.Join(args, " "), out)
		if err != nil {
			entry.Output = buf.String()
			entry.Duration = time.Since(start).Round(time.Millisecond).String()
			u.appendLog(entry)
			return entry, err
		}
	}

	newHead, _ := u.runGit(ctx, snap, "rev-parse", "HEAD")
	entry.NewHead = strings.TrimSpace(newHead)

	vars := tmpl.Vars{Port: snap.AppPort, RepoPath: snap.RepoPath, Name: snap.ServiceName}

	if snap.AutoBuildEnabled || snap.BuildCommand != "" {
		out, err := build.Run(ctx, snap.RepoPath, snap.BuildCommand, snap.BuildArgs, snap.GitEnv, snap.AutoBuildEnabled, vars)
		buf.WriteString(out)
		if err != nil {
			entry.Output = buf.String()
			entry.Duration = time.Since(start).Round(time.Millisecond).String()
			u.appendLog(entry)
			return entry, fmt.Errorf("build: %w", err)
		}
	}

	if snap.PostUpdateCommand != "" {
		out, err := u.runHook(ctx, snap)
		fmt.Fprintf(&buf, "$ %s %s\n%s\n", snap.PostUpdateCommand, strings.Join(snap.PostUpdateArgs, " "), out)
		if err != nil {
			entry.Output = buf.String()
			entry.Duration = time.Since(start).Round(time.Millisecond).String()
			u.appendLog(entry)
			return entry, err
		}
	}

	if snap.ServiceManageEnabled && snap.ServiceName != "" {
		scope := service.Scope(snap.ServiceScope)
		if scope == "" {
			scope = service.ScopeSystem
		}
		out, err := service.Restart(ctx, scope, snap.ServiceName)
		buf.WriteString(out)
		if err != nil {
			entry.Output = buf.String()
			entry.Duration = time.Since(start).Round(time.Millisecond).String()
			u.appendLog(entry)
			return entry, fmt.Errorf("service restart: %w", err)
		}
	}

	if snap.CaddyEnabled && snap.CaddyAutoApply && snap.CaddyDomain != "" {
		out, err := caddy.Apply(ctx, caddy.Config{
			Enabled:       snap.CaddyEnabled,
			Domain:        snap.CaddyDomain,
			Upstream:      snap.CaddyUpstream,
			Extra:         snap.CaddyExtra,
			SnippetDir:    snap.CaddySnippetDir,
			SnippetName:   snap.CaddySnippetName,
			ReloadCommand: snap.CaddyReloadCommand,
			Vars:          vars,
		})
		buf.WriteString(out)
		if err != nil {
			entry.Output = buf.String()
			entry.Duration = time.Since(start).Round(time.Millisecond).String()
			u.appendLog(entry)
			return entry, fmt.Errorf("caddy apply: %w", err)
		}
	}

	entry.Success = true
	entry.Output = buf.String()
	entry.Duration = time.Since(start).Round(time.Millisecond).String()
	u.appendLog(entry)
	return entry, nil
}

func (u *Updater) runGit(ctx context.Context, snap config.Snapshot, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = snap.RepoPath
	cmd.Env = append(os.Environ(), snap.GitEnv...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (u *Updater) runHook(ctx context.Context, snap config.Snapshot) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, snap.PostUpdateCommand, snap.PostUpdateArgs...)
	cmd.Dir = snap.RepoPath
	cmd.Env = append(os.Environ(), snap.GitEnv...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (u *Updater) appendLog(entry LogEntry) {
	snap := u.cfg.Snapshot()
	if snap.LogPath == "" {
		return
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return
	}
	f, err := os.OpenFile(snap.LogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		u.log.Warn("open log", "err", err)
		return
	}
	defer f.Close()
	_, _ = f.Write(line)
	_, _ = f.Write([]byte("\n"))
}

// ReadLog returns the most recent N entries from the update log.
func (u *Updater) ReadLog(limit int) ([]LogEntry, error) {
	snap := u.cfg.Snapshot()
	if snap.LogPath == "" {
		return nil, nil
	}
	data, err := os.ReadFile(snap.LogPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	lines := bytes.Split(bytes.TrimRight(data, "\n"), []byte("\n"))
	if limit > 0 && len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	out := make([]LogEntry, 0, len(lines))
	for _, ln := range lines {
		if len(ln) == 0 {
			continue
		}
		var e LogEntry
		if err := json.Unmarshal(ln, &e); err == nil {
			out = append(out, e)
		}
	}
	// reverse so newest first
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}
