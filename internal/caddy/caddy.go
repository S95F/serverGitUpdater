// Package caddy renders a Caddyfile site snippet for an app and reloads
// Caddy. The snippet lives in a directory imported by the main Caddyfile,
// e.g. /etc/caddy/sites.d/<name>.caddy with an `import sites.d/*.caddy` in
// /etc/caddy/Caddyfile.
package caddy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/s95f/servergitupdater/internal/tmpl"
)

type Config struct {
	Enabled       bool
	Domain        string
	Upstream      string
	Extra         string
	SnippetDir    string
	SnippetName   string // file name without extension; defaults to vars.Name
	ReloadCommand []string
	Vars          tmpl.Vars
}

type Status struct {
	Available     bool   `json:"available"`
	SnippetPath   string `json:"snippet_path,omitempty"`
	SnippetExists bool   `json:"snippet_exists"`
	OnDiskMatches bool   `json:"on_disk_matches"`
	Notes         string `json:"notes,omitempty"`
}

var nameRE = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func (c Config) name() string {
	if c.SnippetName != "" {
		return c.SnippetName
	}
	return c.Vars.Name
}

func (c Config) snippetPath() (string, error) {
	n := c.name()
	if !nameRE.MatchString(n) {
		return "", errors.New("snippet name must match [A-Za-z0-9._-]+")
	}
	dir := c.SnippetDir
	if dir == "" {
		dir = "/etc/caddy/sites.d"
	}
	return filepath.Join(dir, n+".caddy"), nil
}

// Render produces the textual Caddyfile snippet.
func (c Config) Render() (string, error) {
	if c.Domain == "" {
		return "", errors.New("caddy_domain is required")
	}
	upstream := c.Upstream
	if upstream == "" {
		upstream = "127.0.0.1:{port}"
	}
	upstream = c.Vars.Apply(upstream)
	if strings.Contains(upstream, "{port}") || strings.HasSuffix(upstream, ":") {
		return "", errors.New("caddy upstream is missing a port (set app_port or hard-code it)")
	}

	var b strings.Builder
	fmt.Fprintln(&b, "# Managed by serverGitUpdater. Do not edit by hand; changes will be overwritten.")
	fmt.Fprintf(&b, "%s {\n", c.Vars.Apply(c.Domain))
	fmt.Fprintf(&b, "\treverse_proxy %s\n", upstream)
	if extra := strings.TrimSpace(c.Vars.Apply(c.Extra)); extra != "" {
		for _, line := range strings.Split(extra, "\n") {
			fmt.Fprintf(&b, "\t%s\n", strings.TrimRight(line, " \t"))
		}
	}
	fmt.Fprintln(&b, "}")
	return b.String(), nil
}

// Apply writes the snippet (creating the directory if needed) and runs the
// reload command. If the file content already matches what's on disk, the
// reload is skipped.
func Apply(ctx context.Context, c Config) (string, error) {
	content, err := c.Render()
	if err != nil {
		return "", err
	}
	path, err := c.snippetPath()
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	existing, _ := os.ReadFile(path)
	if string(existing) == content {
		fmt.Fprintf(&out, "# %s already up to date\n", path)
		return out.String(), nil
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	fmt.Fprintf(&out, "# wrote %s\n", path)
	if rOut, err := reload(ctx, c); err != nil {
		out.WriteString(rOut)
		return out.String(), err
	} else {
		out.WriteString(rOut)
	}
	return out.String(), nil
}

// Remove deletes the snippet file (if present) and reloads Caddy.
func Remove(ctx context.Context, c Config) (string, error) {
	path, err := c.snippetPath()
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("remove %s: %w", path, err)
	}
	fmt.Fprintf(&out, "# removed %s\n", path)
	if rOut, err := reload(ctx, c); err != nil {
		out.WriteString(rOut)
		return out.String(), err
	} else {
		out.WriteString(rOut)
	}
	return out.String(), nil
}

// Reload runs the configured reload command.
func Reload(ctx context.Context, c Config) (string, error) {
	return reload(ctx, c)
}

func reload(ctx context.Context, c Config) (string, error) {
	cmd := c.ReloadCommand
	if len(cmd) == 0 {
		cmd = []string{"systemctl", "reload", "caddy"}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, cmd[0], cmd[1:]...).CombinedOutput()
	header := fmt.Sprintf("$ %s\n", strings.Join(cmd, " "))
	return header + string(out), err
}

// ReadStatus reports whether the snippet is on disk and matches what we'd render.
func ReadStatus(c Config) Status {
	st := Status{}
	path, err := c.snippetPath()
	if err != nil {
		st.Notes = err.Error()
		return st
	}
	st.SnippetPath = path
	if _, lerr := exec.LookPath(firstWord(c.ReloadCommand)); lerr == nil {
		st.Available = true
	} else {
		st.Notes = "reload command not on PATH: " + firstWord(c.ReloadCommand)
	}
	existing, err := os.ReadFile(path)
	if err == nil {
		st.SnippetExists = true
		if rendered, rerr := c.Render(); rerr == nil && string(existing) == rendered {
			st.OnDiskMatches = true
		}
	}
	return st
}

func firstWord(cmd []string) string {
	if len(cmd) == 0 {
		return "systemctl"
	}
	return cmd[0]
}
