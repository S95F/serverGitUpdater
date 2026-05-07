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
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/s95f/servergitupdater/internal/tmpl"
)

// Mode selects how Caddy serves the site.
type Mode string

const (
	// ModeProxy is the default: reverse-proxy to an upstream port.
	ModeProxy Mode = "proxy"
	// ModeFileServer serves a directory directly via file_server.
	ModeFileServer Mode = "file_server"
)

type Config struct {
	Enabled       bool
	Mode          Mode
	Domain        string
	Upstream      string // proxy mode
	Root          string // file_server mode; the directory to serve
	Browse        bool   // file_server mode; show a directory listing
	TryFiles      string // file_server mode; e.g. "{path} /index.html"
	Extra         string
	SnippetDir    string
	SnippetName   string
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

func (c Config) effectiveMode() Mode {
	switch c.Mode {
	case ModeFileServer:
		return ModeFileServer
	default:
		return ModeProxy
	}
}

// EffectiveRoot is the directory that file_server mode would serve, after
// {port}/{repo_path}/{name} substitution and falling back to the resolved
// repo path when Root is blank.
func (c Config) EffectiveRoot() string {
	root := strings.TrimSpace(c.Vars.Apply(c.Root))
	if root == "" {
		root = c.Vars.RepoPath
	}
	return root
}

// Render produces the textual Caddyfile snippet.
func (c Config) Render() (string, error) {
	if c.Domain == "" {
		return "", errors.New("caddy_domain is required")
	}
	domain := c.Vars.Apply(c.Domain)

	var b strings.Builder
	fmt.Fprintln(&b, "# Managed by serverGitUpdater. Do not edit by hand; changes will be overwritten.")
	fmt.Fprintf(&b, "%s {\n", domain)

	switch c.effectiveMode() {
	case ModeFileServer:
		root := c.EffectiveRoot()
		if root == "" {
			return "", errors.New("caddy_root (or repo_path) is required for file_server mode")
		}
		fmt.Fprintf(&b, "\troot * %s\n", root)
		if try := strings.TrimSpace(c.Vars.Apply(c.TryFiles)); try != "" {
			fmt.Fprintf(&b, "\ttry_files %s\n", try)
		}
		if c.Browse {
			fmt.Fprintln(&b, "\tfile_server browse")
		} else {
			fmt.Fprintln(&b, "\tfile_server")
		}
	default:
		upstream := c.Upstream
		if upstream == "" {
			upstream = "127.0.0.1:{port}"
		}
		upstream = c.Vars.Apply(upstream)
		if strings.Contains(upstream, "{port}") || strings.HasSuffix(upstream, ":") {
			return "", errors.New("caddy upstream is missing a port (set app_port or hard-code it)")
		}
		fmt.Fprintf(&b, "\treverse_proxy %s\n", upstream)
	}

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

// PermissionOptions configures FixPermissions.
type PermissionOptions struct {
	Root     string
	User     string // username, optional
	Group    string // group name, optional
	DirMode  string // octal, e.g. "0755"; empty to skip
	FileMode string // octal, e.g. "0644"; empty to skip
}

// FixPermissions walks Root and sets directory/file modes and (optionally)
// owner / group on every entry. .git directories are skipped.
//
// Use case: making sure the directory served by Caddy in file_server mode
// is readable by the Caddy user. chmod works for files the running user
// owns; chown needs root or CAP_CHOWN.
func FixPermissions(ctx context.Context, opts PermissionOptions) (string, error) {
	if opts.Root == "" {
		return "", errors.New("root is required")
	}
	info, err := os.Stat(opts.Root)
	if err != nil {
		return "", fmt.Errorf("stat root: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("root is not a directory")
	}

	var dirMode, fileMode os.FileMode
	if opts.DirMode != "" {
		m, err := parseOctal(opts.DirMode)
		if err != nil {
			return "", fmt.Errorf("dir mode: %w", err)
		}
		dirMode = m
	}
	if opts.FileMode != "" {
		m, err := parseOctal(opts.FileMode)
		if err != nil {
			return "", fmt.Errorf("file mode: %w", err)
		}
		fileMode = m
	}

	uid, gid := -1, -1
	if opts.User != "" {
		u, err := user.Lookup(opts.User)
		if err != nil {
			return "", fmt.Errorf("user %q: %w", opts.User, err)
		}
		n, _ := strconv.Atoi(u.Uid)
		uid = n
	}
	if opts.Group != "" {
		g, err := user.LookupGroup(opts.Group)
		if err != nil {
			return "", fmt.Errorf("group %q: %w", opts.Group, err)
		}
		n, _ := strconv.Atoi(g.Gid)
		gid = n
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# fixing permissions under %s\n", opts.Root)
	if dirMode != 0 {
		fmt.Fprintf(&b, "#   dir mode: %#o\n", dirMode)
	}
	if fileMode != 0 {
		fmt.Fprintf(&b, "#   file mode: %#o\n", fileMode)
	}
	if uid >= 0 || gid >= 0 {
		fmt.Fprintf(&b, "#   chown to uid=%d gid=%d (-1 means leave alone)\n", uid, gid)
	}

	var visited, errs int
	var lastErr error
	walkErr := filepath.WalkDir(opts.Root, func(path string, d fs.DirEntry, werr error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if werr != nil {
			errs++
			lastErr = werr
			fmt.Fprintf(&b, "  ERR walk %s: %v\n", path, werr)
			return nil
		}
		if d.IsDir() && d.Name() == ".git" && path != opts.Root {
			return filepath.SkipDir
		}
		visited++
		if d.IsDir() {
			if dirMode != 0 {
				if err := os.Chmod(path, dirMode); err != nil {
					errs++
					lastErr = err
					fmt.Fprintf(&b, "  ERR chmod %s: %v\n", path, err)
				}
			}
		} else {
			if fileMode != 0 {
				if err := os.Chmod(path, fileMode); err != nil {
					errs++
					lastErr = err
					fmt.Fprintf(&b, "  ERR chmod %s: %v\n", path, err)
				}
			}
		}
		if uid >= 0 || gid >= 0 {
			if err := os.Chown(path, uid, gid); err != nil {
				errs++
				lastErr = err
				fmt.Fprintf(&b, "  ERR chown %s: %v\n", path, err)
			}
		}
		return nil
	})
	fmt.Fprintf(&b, "# visited %d entries; %d errors\n", visited, errs)
	if walkErr != nil {
		return b.String(), walkErr
	}
	if errs > 0 {
		return b.String(), fmt.Errorf("%d permission errors; last: %v", errs, lastErr)
	}
	return b.String(), nil
}

func parseOctal(s string) (os.FileMode, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "0o") || strings.HasPrefix(s, "0O") {
		s = s[2:]
	}
	n, err := strconv.ParseUint(s, 8, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid octal %q", s)
	}
	return os.FileMode(n), nil
}
