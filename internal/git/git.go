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
	AppID    string    `json:"app_id"`
	AppName  string    `json:"app_name,omitempty"`
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
	cancel context.CancelFunc
	wg     sync.WaitGroup

	locksMu sync.Mutex
	locks   map[string]*sync.Mutex

	lastMu  sync.Mutex
	lastRun map[string]time.Time
}

func NewUpdater(cfg *config.Config, log *slog.Logger) *Updater {
	return &Updater{
		cfg:     cfg,
		log:     log,
		locks:   map[string]*sync.Mutex{},
		lastRun: map[string]time.Time{},
	}
}

// Start launches the auto-update scheduler.
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
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		snap := u.cfg.Snapshot()
		for _, app := range snap.Apps {
			if !app.AutoUpdateEnabled {
				continue
			}
			interval := time.Duration(app.AutoUpdateMinutes) * time.Minute
			if interval < time.Minute {
				interval = time.Minute
			}
			u.lastMu.Lock()
			last := u.lastRun[app.ID]
			u.lastMu.Unlock()
			if !last.IsZero() && time.Since(last) < interval {
				continue
			}
			a := app
			u.wg.Add(1)
			go func() {
				defer u.wg.Done()
				if _, err := u.RunUpdate(ctx, a.ID, "auto"); err != nil {
					u.log.Warn("auto-update failed", "app", a.Name, "err", err)
				}
			}()
		}
	}
}

func (u *Updater) lockFor(id string) *sync.Mutex {
	u.locksMu.Lock()
	defer u.locksMu.Unlock()
	m, ok := u.locks[id]
	if !ok {
		m = &sync.Mutex{}
		u.locks[id] = m
	}
	return m
}

// Status reads the current repo status without modifying anything.
func (u *Updater) Status(ctx context.Context, appID string) (Status, error) {
	snap := u.cfg.Snapshot()
	app, ok := snap.FindApp(appID)
	if !ok {
		return Status{}, fmt.Errorf("app %q not found", appID)
	}
	if app.RepoPath == "" {
		return Status{}, errors.New("repo_path is not configured")
	}
	if _, err := os.Stat(app.RepoPath); err != nil {
		return Status{}, fmt.Errorf("repo_path: %w", err)
	}
	branch, _ := runGit(ctx, app, "rev-parse", "--abbrev-ref", "HEAD")
	commit, _ := runGit(ctx, app, "rev-parse", "--short", "HEAD")
	subject, _ := runGit(ctx, app, "log", "-1", "--pretty=%s")
	tStr, _ := runGit(ctx, app, "log", "-1", "--pretty=%cI")
	dirtyOut, _ := runGit(ctx, app, "status", "--porcelain")
	ba, _ := runGit(ctx, app, "rev-list", "--left-right", "--count", "HEAD..."+app.Remote+"/"+app.Branch)
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

// RunUpdate runs the full update pipeline for one app.
func (u *Updater) RunUpdate(ctx context.Context, appID, source string) (LogEntry, error) {
	mu := u.lockFor(appID)
	mu.Lock()
	defer mu.Unlock()
	defer func() {
		u.lastMu.Lock()
		u.lastRun[appID] = time.Now()
		u.lastMu.Unlock()
	}()

	snap := u.cfg.Snapshot()
	app, ok := snap.FindApp(appID)
	if !ok {
		entry := LogEntry{Time: time.Now(), AppID: appID, Source: source, Output: "app not found"}
		u.appendLog(snap, entry)
		return entry, fmt.Errorf("app %q not found", appID)
	}

	start := time.Now()
	entry := LogEntry{Time: start, AppID: app.ID, AppName: app.Name, Source: source}

	if app.RepoPath == "" {
		entry.Output = "repo_path is not configured"
		u.appendLog(snap, entry)
		return entry, errors.New(entry.Output)
	}
	if _, err := os.Stat(app.RepoPath); err != nil {
		entry.Output = "repo_path: " + err.Error()
		u.appendLog(snap, entry)
		return entry, err
	}

	var buf bytes.Buffer

	oldHead, _ := runGit(ctx, app, "rev-parse", "HEAD")
	entry.OldHead = strings.TrimSpace(oldHead)

	for _, args := range [][]string{
		{"fetch", "--prune", app.Remote},
		{"checkout", app.Branch},
		{"reset", "--hard", app.Remote + "/" + app.Branch},
	} {
		out, err := runGit(ctx, app, args...)
		fmt.Fprintf(&buf, "$ git %s\n%s\n", strings.Join(args, " "), out)
		if err != nil {
			entry.Output = buf.String()
			entry.Duration = time.Since(start).Round(time.Millisecond).String()
			u.appendLog(snap, entry)
			return entry, err
		}
	}

	newHead, _ := runGit(ctx, app, "rev-parse", "HEAD")
	entry.NewHead = strings.TrimSpace(newHead)

	vars := tmpl.Vars{Port: app.AppPort, RepoPath: app.RepoPath, Name: app.ServiceName}

	if app.AutoBuildEnabled || app.BuildCommand != "" {
		out, err := build.Run(ctx, app.RepoPath, app.BuildCommand, app.BuildArgs, app.GitEnv, app.AutoBuildEnabled, vars)
		buf.WriteString(out)
		if err != nil {
			entry.Output = buf.String()
			entry.Duration = time.Since(start).Round(time.Millisecond).String()
			u.appendLog(snap, entry)
			return entry, fmt.Errorf("build: %w", err)
		}
	}

	if app.PostUpdateCommand != "" {
		out, err := runHook(ctx, app)
		fmt.Fprintf(&buf, "$ %s %s\n%s\n", app.PostUpdateCommand, strings.Join(app.PostUpdateArgs, " "), out)
		if err != nil {
			entry.Output = buf.String()
			entry.Duration = time.Since(start).Round(time.Millisecond).String()
			u.appendLog(snap, entry)
			return entry, err
		}
	}

	if app.ServiceManageEnabled && app.ServiceName != "" {
		scope := service.Scope(app.ServiceScope)
		if scope == "" {
			scope = service.ScopeSystem
		}
		out, err := service.Restart(ctx, scope, app.ServiceName)
		buf.WriteString(out)
		if err != nil {
			entry.Output = buf.String()
			entry.Duration = time.Since(start).Round(time.Millisecond).String()
			u.appendLog(snap, entry)
			return entry, fmt.Errorf("service restart: %w", err)
		}
	}

	if app.CaddyEnabled && app.CaddyAutoApply && app.CaddyDomain != "" {
		snippetName := app.CaddySnippetName
		if snippetName == "" {
			snippetName = app.ServiceName
			if snippetName == "" {
				snippetName = app.Name
			}
		}
		out, err := caddy.Apply(ctx, caddy.Config{
			Enabled:       app.CaddyEnabled,
			Domain:        app.CaddyDomain,
			Upstream:      app.CaddyUpstream,
			Extra:         app.CaddyExtra,
			SnippetDir:    app.CaddySnippetDir,
			SnippetName:   snippetName,
			ReloadCommand: app.CaddyReloadCommand,
			Vars:          tmpl.Vars{Port: app.AppPort, RepoPath: app.RepoPath, Name: snippetName},
		})
		buf.WriteString(out)
		if err != nil {
			entry.Output = buf.String()
			entry.Duration = time.Since(start).Round(time.Millisecond).String()
			u.appendLog(snap, entry)
			return entry, fmt.Errorf("caddy apply: %w", err)
		}
	}

	entry.Success = true
	entry.Output = buf.String()
	entry.Duration = time.Since(start).Round(time.Millisecond).String()
	u.appendLog(snap, entry)
	return entry, nil
}

func runGit(ctx context.Context, app config.App, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = app.RepoPath
	cmd.Env = append(os.Environ(), app.GitEnv...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func runHook(ctx context.Context, app config.App) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, app.PostUpdateCommand, app.PostUpdateArgs...)
	cmd.Dir = app.RepoPath
	cmd.Env = append(os.Environ(), app.GitEnv...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (u *Updater) appendLog(snap config.Snapshot, entry LogEntry) {
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

// ReadLog returns the most recent N entries from the update log. If appID is
// non-empty, only entries for that app are returned.
func (u *Updater) ReadLog(limit int, appID string) ([]LogEntry, error) {
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
	out := make([]LogEntry, 0, len(lines))
	for _, ln := range lines {
		if len(ln) == 0 {
			continue
		}
		var e LogEntry
		if err := json.Unmarshal(ln, &e); err != nil {
			continue
		}
		if appID != "" && e.AppID != appID {
			continue
		}
		out = append(out, e)
	}
	// keep most recent N
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	// reverse so newest first
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// LastRun returns the most recent run time for an app, or zero if never run.
func (u *Updater) LastRun(appID string) time.Time {
	u.lastMu.Lock()
	defer u.lastMu.Unlock()
	return u.lastRun[appID]
}
