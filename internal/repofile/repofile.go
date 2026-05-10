// Package repofile reads and applies an in-repo configuration file
// (default name ".servergitupdater.json") so a project can ship its
// own build/service/caddy settings alongside its source. Pointer-typed
// fields let us distinguish "key absent" from "explicitly false/empty"
// so a missing key never clobbers an existing user-configured value.
package repofile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/s95f/servergitupdater/internal/config"
)

// ErrNotFound is returned by Read when the file doesn't exist. Callers
// can use errors.Is(err, ErrNotFound) to check.
var ErrNotFound = errors.New("repo config file not found")

// File mirrors the importable subset of config.App. Only build, service
// and caddy concerns are accepted — repo_path, branch, remote and
// scheduler/webhook settings remain server-side.
//
// Identity fields like name / repo_path / clone_url / branch / remote
// are intentionally NOT applied (overriding the server-managed values
// from inside the repo would be surprising at best). They are accepted
// in the JSON purely so a project can write a self-documenting file
// without the parser rejecting it; they're discarded by Apply().
type File struct {
	// --- Accepted-but-ignored identity fields ----------------------
	Name      *string `json:"name,omitempty"`
	RepoPath  *string `json:"repo_path,omitempty"`
	CloneURL  *string `json:"clone_url,omitempty"`
	Branch    *string `json:"branch,omitempty"`
	Remote    *string `json:"remote,omitempty"`

	// --- Importable fields -----------------------------------------
	PostUpdateCommand    *string   `json:"post_update_command,omitempty"`
	PostUpdateArgs       *[]string `json:"post_update_args,omitempty"`

	AutoBuildEnabled *bool     `json:"auto_build_enabled,omitempty"`
	BuildCommand     *string   `json:"build_command,omitempty"`
	BuildArgs        *[]string `json:"build_args,omitempty"`

	AppPort  *int    `json:"app_port,omitempty"`
	PortFlag *string `json:"port_flag,omitempty"`

	ServiceManageEnabled *bool     `json:"service_manage_enabled,omitempty"`
	ServiceName          *string   `json:"service_name,omitempty"`
	ServiceScope         *string   `json:"service_scope,omitempty"`
	ServiceDescription   *string   `json:"service_description,omitempty"`
	ServiceExecStart     *string   `json:"service_exec_start,omitempty"`
	ServiceExecArgs      *[]string `json:"service_exec_args,omitempty"`
	ServiceWorkingDir    *string   `json:"service_working_dir,omitempty"`
	ServiceUser          *string   `json:"service_user,omitempty"`
	ServiceEnv           *[]string `json:"service_env,omitempty"`
	ServiceRestart       *string   `json:"service_restart,omitempty"`

	CaddyEnabled       *bool     `json:"caddy_enabled,omitempty"`
	CaddyAutoApply     *bool     `json:"caddy_auto_apply,omitempty"`
	CaddyMode          *string   `json:"caddy_mode,omitempty"`
	CaddyDomain        *string   `json:"caddy_domain,omitempty"`
	CaddyUpstream      *string   `json:"caddy_upstream,omitempty"`
	CaddyRoot          *string   `json:"caddy_root,omitempty"`
	CaddyBrowse        *bool     `json:"caddy_browse,omitempty"`
	CaddyTryFiles      *string   `json:"caddy_try_files,omitempty"`
	CaddyExtra         *string   `json:"caddy_extra,omitempty"`
	CaddySnippetDir    *string   `json:"caddy_snippet_dir,omitempty"`
	CaddySnippetName   *string   `json:"caddy_snippet_name,omitempty"`
	CaddyReloadCommand *[]string `json:"caddy_reload_command,omitempty"`
}

// Read loads the file at <repoPath>/<filename>. If filename is empty,
// ".servergitupdater.json" is used. The filename is constrained to a
// path inside repoPath: leading slashes and "." / ".." segments are
// stripped so the read can never escape the repo.
func Read(repoPath, filename string) (*File, string, error) {
	filename = SanitizeFilename(filename)
	full := filepath.Join(repoPath, filename)
	data, err := os.ReadFile(full)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, full, ErrNotFound
	}
	if err != nil {
		return nil, full, err
	}
	var f File
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, full, fmt.Errorf("parse %s: %w", filename, err)
	}
	return &f, full, nil
}

// SanitizeFilename collapses repeated slashes, drops leading slashes,
// removes "." and ".." segments, and falls back to the default name if
// nothing's left. Used so a malicious or careless repo_file_path can't
// be turned into a directory-traversal read of /etc/passwd.
func SanitizeFilename(filename string) string {
	parts := []string{}
	for _, p := range strings.Split(filename, "/") {
		if p == "" || p == "." || p == ".." {
			continue
		}
		parts = append(parts, p)
	}
	if len(parts) == 0 {
		return ".servergitupdater.json"
	}
	return strings.Join(parts, "/")
}

// Apply merges file values into the given App, overwriting only the
// fields the file actually specifies.
func Apply(a *config.App, f *File) {
	if f == nil || a == nil {
		return
	}
	if f.PostUpdateCommand != nil {
		a.PostUpdateCommand = *f.PostUpdateCommand
	}
	if f.PostUpdateArgs != nil {
		a.PostUpdateArgs = append([]string(nil), *f.PostUpdateArgs...)
	}
	if f.AutoBuildEnabled != nil {
		a.AutoBuildEnabled = *f.AutoBuildEnabled
	}
	if f.BuildCommand != nil {
		a.BuildCommand = *f.BuildCommand
	}
	if f.BuildArgs != nil {
		a.BuildArgs = append([]string(nil), *f.BuildArgs...)
	}
	if f.AppPort != nil {
		a.AppPort = *f.AppPort
	}
	if f.PortFlag != nil {
		a.PortFlag = *f.PortFlag
	}
	if f.ServiceManageEnabled != nil {
		a.ServiceManageEnabled = *f.ServiceManageEnabled
	}
	if f.ServiceName != nil {
		a.ServiceName = *f.ServiceName
	}
	if f.ServiceScope != nil {
		a.ServiceScope = *f.ServiceScope
	}
	if f.ServiceDescription != nil {
		a.ServiceDescription = *f.ServiceDescription
	}
	if f.ServiceExecStart != nil {
		a.ServiceExecStart = *f.ServiceExecStart
	}
	if f.ServiceExecArgs != nil {
		a.ServiceExecArgs = append([]string(nil), *f.ServiceExecArgs...)
	}
	if f.ServiceWorkingDir != nil {
		a.ServiceWorkingDir = *f.ServiceWorkingDir
	}
	if f.ServiceUser != nil {
		a.ServiceUser = *f.ServiceUser
	}
	if f.ServiceEnv != nil {
		a.ServiceEnv = append([]string(nil), *f.ServiceEnv...)
	}
	if f.ServiceRestart != nil {
		a.ServiceRestart = *f.ServiceRestart
	}
	if f.CaddyEnabled != nil {
		a.CaddyEnabled = *f.CaddyEnabled
	}
	if f.CaddyAutoApply != nil {
		a.CaddyAutoApply = *f.CaddyAutoApply
	}
	if f.CaddyMode != nil {
		a.CaddyMode = *f.CaddyMode
	}
	if f.CaddyDomain != nil {
		a.CaddyDomain = *f.CaddyDomain
	}
	if f.CaddyUpstream != nil {
		a.CaddyUpstream = *f.CaddyUpstream
	}
	if f.CaddyRoot != nil {
		a.CaddyRoot = *f.CaddyRoot
	}
	if f.CaddyBrowse != nil {
		a.CaddyBrowse = *f.CaddyBrowse
	}
	if f.CaddyTryFiles != nil {
		a.CaddyTryFiles = *f.CaddyTryFiles
	}
	if f.CaddyExtra != nil {
		a.CaddyExtra = *f.CaddyExtra
	}
	if f.CaddySnippetDir != nil {
		a.CaddySnippetDir = *f.CaddySnippetDir
	}
	if f.CaddySnippetName != nil {
		a.CaddySnippetName = *f.CaddySnippetName
	}
	if f.CaddyReloadCommand != nil {
		a.CaddyReloadCommand = append([]string(nil), *f.CaddyReloadCommand...)
	}
}

