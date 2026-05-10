package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Scope string

const (
	ScopeSystem Scope = "system"
	ScopeUser   Scope = "user"
)

// Spec is the data needed to render a systemd unit file.
type Spec struct {
	Name        string
	Scope       Scope
	Description string
	ExecStart   string
	ExecArgs    []string
	WorkingDir  string
	User        string
	Env         []string
	Restart     string // on-failure | always | no
}

// Status is the runtime state reported by `systemctl is-active` / `is-enabled`.
type Status struct {
	Name        string `json:"name"`
	Scope       Scope  `json:"scope"`
	Installed   bool   `json:"installed"`
	UnitPath    string `json:"unit_path,omitempty"`
	ActiveState string `json:"active_state"`
	EnableState string `json:"enable_state"`
	Available   bool   `json:"available"`
	Notes       string `json:"notes,omitempty"`
}

var nameRE = regexp.MustCompile(`^[A-Za-z0-9._@-]+$`)

func (s Spec) validate() error {
	if !nameRE.MatchString(s.Name) {
		return errors.New("service name must match [A-Za-z0-9._@-]+")
	}
	if s.ExecStart == "" {
		return errors.New("exec_start is required")
	}
	if s.Scope != ScopeSystem && s.Scope != ScopeUser {
		return errors.New("scope must be 'system' or 'user'")
	}
	if s.Restart == "" {
		s.Restart = "on-failure"
	}
	return nil
}

// UnitPath returns the absolute path where the unit file would be written for the given scope.
func UnitPath(scope Scope, name string) (string, error) {
	if !nameRE.MatchString(name) {
		return "", errors.New("invalid service name")
	}
	switch scope {
	case ScopeSystem:
		return filepath.Join("/etc/systemd/system", name+".service"), nil
	case ScopeUser:
		dir, err := userUnitDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, name+".service"), nil
	default:
		return "", fmt.Errorf("unknown scope %q", scope)
	}
}

func userUnitDir() (string, error) {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "systemd", "user"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "systemd", "user"), nil
}

// Render produces the textual content of the unit file.
func Render(s Spec) (string, error) {
	if err := s.validate(); err != nil {
		return "", err
	}
	desc := s.Description
	if desc == "" {
		desc = s.Name
	}
	restart := s.Restart
	if restart == "" {
		restart = "on-failure"
	}
	wantedBy := "multi-user.target"
	if s.Scope == ScopeUser {
		wantedBy = "default.target"
	}

	exec := shellQuote(s.ExecStart)
	for _, a := range s.ExecArgs {
		exec += " " + shellQuote(a)
	}

	var b strings.Builder
	fmt.Fprintln(&b, "# Managed by serverGitUpdater. Do not edit by hand; changes will be overwritten.")
	fmt.Fprintln(&b, "[Unit]")
	fmt.Fprintf(&b, "Description=%s\n", desc)
	fmt.Fprintln(&b, "After=network-online.target")
	fmt.Fprintln(&b, "Wants=network-online.target")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "[Service]")
	fmt.Fprintln(&b, "Type=simple")
	if s.Scope == ScopeSystem && s.User != "" {
		fmt.Fprintf(&b, "User=%s\n", s.User)
	}
	if s.WorkingDir != "" {
		fmt.Fprintf(&b, "WorkingDirectory=%s\n", s.WorkingDir)
	}
	for _, e := range s.Env {
		if e == "" {
			continue
		}
		fmt.Fprintf(&b, "Environment=%s\n", shellQuote(e))
	}
	fmt.Fprintf(&b, "ExecStart=%s\n", exec)
	fmt.Fprintf(&b, "Restart=%s\n", restart)
	fmt.Fprintln(&b, "RestartSec=5s")
	fmt.Fprintln(&b, "NoNewPrivileges=true")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "[Install]")
	fmt.Fprintf(&b, "WantedBy=%s\n", wantedBy)
	return b.String(), nil
}

// Install writes the unit file, runs daemon-reload, enables and starts the service.
// Returns combined output from each command.
func Install(ctx context.Context, s Spec) (string, error) {
	content, err := Render(s)
	if err != nil {
		return "", err
	}
	path, err := UnitPath(s.Scope, s.Name)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}

	var out strings.Builder
	fmt.Fprintf(&out, "# wrote %s\n", path)
	for _, args := range [][]string{
		{"daemon-reload"},
		{"enable", s.Name + ".service"},
		{"restart", s.Name + ".service"},
	} {
		text, err := runSystemctl(ctx, s.Scope, args...)
		fmt.Fprintf(&out, "$ systemctl%s %s\n%s\n", scopeFlag(s.Scope), strings.Join(args, " "), text)
		if err != nil {
			return out.String(), err
		}
	}
	return out.String(), nil
}

// Uninstall stops and disables the service then removes the unit file.
func Uninstall(ctx context.Context, scope Scope, name string) (string, error) {
	if !nameRE.MatchString(name) {
		return "", errors.New("invalid service name")
	}
	path, err := UnitPath(scope, name)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	for _, args := range [][]string{
		{"stop", name + ".service"},
		{"disable", name + ".service"},
	} {
		text, err := runSystemctl(ctx, scope, args...)
		fmt.Fprintf(&out, "$ systemctl%s %s\n%s\n", scopeFlag(scope), strings.Join(args, " "), text)
		if err != nil {
			// non-fatal: keep going so we still remove the unit file
			fmt.Fprintf(&out, "(warning: %v)\n", err)
		}
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(&out, "remove %s: %v\n", path, err)
		return out.String(), err
	}
	fmt.Fprintf(&out, "# removed %s\n", path)
	if text, err := runSystemctl(ctx, scope, "daemon-reload"); err != nil {
		fmt.Fprintf(&out, "$ systemctl%s daemon-reload\n%s\n", scopeFlag(scope), text)
		return out.String(), err
	} else {
		fmt.Fprintf(&out, "$ systemctl%s daemon-reload\n%s\n", scopeFlag(scope), text)
	}
	return out.String(), nil
}

// Restart restarts a previously installed service.
func Restart(ctx context.Context, scope Scope, name string) (string, error) {
	if !nameRE.MatchString(name) {
		return "", errors.New("invalid service name")
	}
	out, err := runSystemctl(ctx, scope, "restart", name+".service")
	return fmt.Sprintf("$ systemctl%s restart %s.service\n%s\n", scopeFlag(scope), name, out), err
}

// Reports current install + active state.
func ReadStatus(ctx context.Context, scope Scope, name string) Status {
	st := Status{Name: name, Scope: scope}
	if !nameRE.MatchString(name) {
		st.Notes = "invalid service name"
		return st
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		st.Notes = "systemctl not found on PATH"
		return st
	}
	st.Available = true
	if path, err := UnitPath(scope, name); err == nil {
		st.UnitPath = path
		if _, err := os.Stat(path); err == nil {
			st.Installed = true
		}
	}
	st.ActiveState = firstLine(mustOutput(runSystemctl(ctx, scope, "is-active", name+".service")))
	st.EnableState = firstLine(mustOutput(runSystemctl(ctx, scope, "is-enabled", name+".service")))
	return st
}

// firstLine returns the first non-empty line of s, trimmed. Used so a
// multi-line systemctl error never leaks into compact UI badges.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		if t != "" {
			return t
		}
	}
	return ""
}

func runSystemctl(ctx context.Context, scope Scope, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	full := []string{}
	if scope == ScopeUser {
		full = append(full, "--user")
	}
	full = append(full, args...)
	cmd := exec.CommandContext(ctx, "systemctl", full...)
	out, err := cmd.CombinedOutput()
	output := string(out)

	// User-scope failure mode operators hit a lot: lingering not
	// enabled, or the daemon's namespace doesn't expose /run/user.
	// Add a one-liner hint pointing at the fix — but only for
	// mutating actions (install/uninstall/restart). Status probes
	// (is-active, is-enabled, show, status, list-*) are called from
	// the dashboard and their output ends up in compact UI badges,
	// where a multi-line hint just turns into noise.
	if scope == ScopeUser && err != nil && len(args) > 0 && !isStatusProbe(args[0]) &&
		(strings.Contains(output, "Failed to connect to bus") || strings.Contains(output, "No medium found")) {
		output += "\n# hint: this means the user systemd manager isn't reachable.\n"
		output += "# fix on the daemon's host:\n"
		output += "#   sudo loginctl enable-linger <user-the-daemon-runs-as>\n"
		output += "#   then in the daemon's unit drop-in, set:\n"
		output += "#     ProtectHome=tmpfs\n"
		output += "#     BindPaths=-/run/user/<that-user's-uid>\n"
		output += "# scripts/install.sh update writes both for you.\n"
	}
	return output, err
}

func isStatusProbe(verb string) bool {
	switch verb {
	case "is-active", "is-enabled", "is-failed", "is-system-running",
		"show", "status", "cat":
		return true
	}
	return strings.HasPrefix(verb, "list-")
}

func scopeFlag(scope Scope) string {
	if scope == ScopeUser {
		return " --user"
	}
	return ""
}

func mustOutput(s string, _ error) string { return s }

func shellQuote(s string) string {
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, " \t\"'$\\`") {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return s
}
