package build

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/s95f/servergitupdater/internal/tmpl"
)

// Kind identifies a project type the builder knows how to handle.
type Kind string

const (
	KindNone  Kind = ""
	KindGo    Kind = "go"
	KindCargo Kind = "cargo"
	KindNode  Kind = "node"
	KindMake  Kind = "make"
)

// Detection describes what the builder found in a repo and which tool would handle it.
type Detection struct {
	Kind             Kind     `json:"kind"`
	ToolAvailable    bool     `json:"tool_available"`
	ToolPath         string   `json:"tool_path,omitempty"`
	ToolVersion      string   `json:"tool_version,omitempty"`
	SuggestedCommand string   `json:"suggested_command"`
	SuggestedArgs    []string `json:"suggested_args"`
	SuggestedOutput  string   `json:"suggested_output,omitempty"`
	Notes            string   `json:"notes,omitempty"`
}

// Detect inspects repoPath and returns the first detection that matches and
// whose toolchain is available on PATH. If multiple project files exist
// (e.g. both go.mod and Makefile) the first available wins in priority order:
// Go, Cargo, Make, Node.
func Detect(repoPath string) Detection {
	if repoPath == "" {
		return Detection{}
	}
	checks := []func(string) Detection{detectGo, detectCargo, detectMake, detectNode}
	var first Detection
	for _, c := range checks {
		d := c(repoPath)
		if d.Kind == KindNone {
			continue
		}
		if d.ToolAvailable {
			return d
		}
		if first.Kind == KindNone {
			first = d
		}
	}
	return first
}

func detectGo(repoPath string) Detection {
	if !fileExists(filepath.Join(repoPath, "go.mod")) {
		return Detection{}
	}
	d := Detection{Kind: KindGo, SuggestedCommand: "go"}
	if path, err := exec.LookPath("go"); err == nil {
		d.ToolAvailable = true
		d.ToolPath = path
		d.ToolVersion = firstLine(runQuick(path, "version"))
	} else {
		d.Notes = "go.mod found but `go` is not on PATH"
	}
	output := defaultOutput(repoPath)
	d.SuggestedArgs = []string{"build", "-o", output, "./..."}
	d.SuggestedOutput = output
	return d
}

func detectCargo(repoPath string) Detection {
	if !fileExists(filepath.Join(repoPath, "Cargo.toml")) {
		return Detection{}
	}
	d := Detection{Kind: KindCargo, SuggestedCommand: "cargo"}
	if path, err := exec.LookPath("cargo"); err == nil {
		d.ToolAvailable = true
		d.ToolPath = path
		d.ToolVersion = firstLine(runQuick(path, "--version"))
	} else {
		d.Notes = "Cargo.toml found but `cargo` is not on PATH"
	}
	d.SuggestedArgs = []string{"build", "--release"}
	d.SuggestedOutput = filepath.Join(repoPath, "target", "release", filepath.Base(repoPath))
	return d
}

func detectMake(repoPath string) Detection {
	if !fileExists(filepath.Join(repoPath, "Makefile")) && !fileExists(filepath.Join(repoPath, "makefile")) {
		return Detection{}
	}
	d := Detection{Kind: KindMake, SuggestedCommand: "make"}
	if path, err := exec.LookPath("make"); err == nil {
		d.ToolAvailable = true
		d.ToolPath = path
		d.ToolVersion = firstLine(runQuick(path, "--version"))
	} else {
		d.Notes = "Makefile found but `make` is not on PATH"
	}
	d.SuggestedArgs = []string{}
	return d
}

func detectNode(repoPath string) Detection {
	if !fileExists(filepath.Join(repoPath, "package.json")) {
		return Detection{}
	}
	d := Detection{Kind: KindNode, SuggestedCommand: "npm"}
	if path, err := exec.LookPath("npm"); err == nil {
		d.ToolAvailable = true
		d.ToolPath = path
		d.ToolVersion = firstLine(runQuick(path, "--version"))
	} else {
		d.Notes = "package.json found but `npm` is not on PATH"
	}
	d.SuggestedArgs = []string{"run", "build"}
	return d
}

// Run executes the configured build command (or an auto-picked default) inside repoPath.
// If command is empty and autoEnabled is true, the detected default is used.
// args go through tmpl substitution so {port}/{repo_path}/{name} are filled in.
// Returns combined stdout/stderr and an error.
func Run(ctx context.Context, repoPath, command string, args, env []string, autoEnabled bool, vars tmpl.Vars) (string, error) {
	if repoPath == "" {
		return "", errors.New("repo_path is not configured")
	}
	if command == "" {
		if !autoEnabled {
			return "", nil
		}
		d := Detect(repoPath)
		if d.Kind == KindNone {
			return "(no buildable project detected; skipping build)\n", nil
		}
		if !d.ToolAvailable {
			return "", fmt.Errorf("detected %s project but toolchain is not available: %s", d.Kind, d.Notes)
		}
		command = d.SuggestedCommand
		args = d.SuggestedArgs
	}
	args = vars.ApplyAll(args)

	// Resolve the command. Daemons running under systemd have a stripped
	// PATH (typically /usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:
	// /sbin:/bin) which doesn't include /usr/local/go/bin or other
	// common toolchain install dirs. If the user typed a bare command
	// (no slash) and PATH lookup fails, try a small list of common
	// places before giving up — same idea as scripts/install.sh's
	// find_go(). Lets `go` / `cargo` / `npm` / `make` Just Work.
	if !strings.Contains(command, "/") {
		if resolved := resolveToolPath(command); resolved != "" {
			command = resolved
		}
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = repoPath
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	header := fmt.Sprintf("$ %s %s\n", command, strings.Join(args, " "))
	return header + string(out), err
}

// resolveToolPath looks for a bare command (e.g. "go", "cargo", "npm",
// "make") in PATH first, then in a list of locations that systemd's
// default PATH usually misses. Returns "" if nothing matched, in which
// case the caller passes the bare name through and lets exec produce
// its standard "executable file not found" error.
func resolveToolPath(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	// Per-tool well-known prefixes.
	candidates := []string{
		"/usr/local/" + name + "/bin/" + name, // /usr/local/go/bin/go
		"/usr/lib/" + name + "/bin/" + name,
		"/opt/" + name + "/bin/" + name,
		"/snap/bin/" + name,
		"/usr/local/bin/" + name,
	}
	// Distro-versioned go packages: /usr/lib/go-1.22/bin/go etc.
	if name == "go" {
		matches, _ := filepath.Glob("/usr/lib/go-*/bin/go")
		candidates = append(candidates, matches...)
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return c
		}
	}
	return ""
}

func defaultOutput(repoPath string) string {
	base := filepath.Base(repoPath)
	if base == "" || base == "." || base == "/" {
		base = "app"
	}
	return filepath.Join(repoPath, base)
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

func runQuick(name string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return string(out)
}
