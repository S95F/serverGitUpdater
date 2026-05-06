package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Snapshot is a value-type, lock-free copy of the config safe to read freely.
type Snapshot struct {
	ListenAddr      string `json:"listen_addr"`
	TLSCertFile     string `json:"tls_cert_file,omitempty"`
	TLSKeyFile      string `json:"tls_key_file,omitempty"`
	SessionSecret   string `json:"session_secret"`
	SessionTTLHours int    `json:"session_ttl_hours"`
	CookieSecure    bool   `json:"cookie_secure"`

	Username     string `json:"username"`
	PasswordHash string `json:"password_hash"`

	RepoPath          string   `json:"repo_path"`
	Branch            string   `json:"branch"`
	Remote            string   `json:"remote"`
	PostUpdateCommand string   `json:"post_update_command,omitempty"`
	PostUpdateArgs    []string `json:"post_update_args,omitempty"`
	GitEnv            []string `json:"git_env,omitempty"`

	AutoUpdateEnabled bool `json:"auto_update_enabled"`
	AutoUpdateMinutes int  `json:"auto_update_minutes"`

	// Build step. Runs after a successful pull, before the post-update hook
	// and before any service restart.
	AutoBuildEnabled bool     `json:"auto_build_enabled"`
	BuildCommand     string   `json:"build_command,omitempty"` // override; empty = use detected default
	BuildArgs        []string `json:"build_args,omitempty"`

	// Port the deployed app should listen on. Plumbed into service_exec_args
	// (and optionally build_args) via {port} substitution; if no arg contains
	// {port}, "port_flag <app_port>" is appended automatically.
	AppPort  int    `json:"app_port,omitempty"`
	PortFlag string `json:"port_flag,omitempty"` // e.g. "-port", "--port", "--listen"

	// Managed systemd service. When ServiceManageEnabled is true the updater
	// will restart the named service after a successful update + build.
	// "Install service" from the UI writes the unit and enables it.
	ServiceManageEnabled bool     `json:"service_manage_enabled"`
	ServiceName          string   `json:"service_name,omitempty"`
	ServiceScope         string   `json:"service_scope,omitempty"` // "system" or "user"
	ServiceDescription   string   `json:"service_description,omitempty"`
	ServiceExecStart     string   `json:"service_exec_start,omitempty"` // empty = use build output
	ServiceExecArgs      []string `json:"service_exec_args,omitempty"`
	ServiceWorkingDir    string   `json:"service_working_dir,omitempty"`
	ServiceUser          string   `json:"service_user,omitempty"`
	ServiceEnv           []string `json:"service_env,omitempty"`
	ServiceRestart       string   `json:"service_restart,omitempty"` // on-failure | always | no

	// Caddy reverse-proxy integration. Writes a Caddyfile snippet to
	// CaddySnippetDir/<service_name>.caddy and reloads Caddy.
	CaddyEnabled       bool     `json:"caddy_enabled"`
	CaddyAutoApply     bool     `json:"caddy_auto_apply"` // re-apply after every successful update
	CaddyDomain        string   `json:"caddy_domain,omitempty"`
	CaddyUpstream      string   `json:"caddy_upstream,omitempty"`     // default "127.0.0.1:{port}"
	CaddyExtra         string   `json:"caddy_extra,omitempty"`        // raw lines inside the site block
	CaddySnippetDir    string   `json:"caddy_snippet_dir,omitempty"`  // default "/etc/caddy/sites.d"
	CaddySnippetName   string   `json:"caddy_snippet_name,omitempty"` // default = service_name
	CaddyReloadCommand []string `json:"caddy_reload_command,omitempty"` // default ["systemctl","reload","caddy"]

	LogPath    string `json:"log_path"`
	MaxLogRows int    `json:"max_log_rows"`
}

// Config wraps a Snapshot with a mutex so live updates from the UI are safe.
type Config struct {
	mu   sync.RWMutex
	data Snapshot
}

func defaults() Snapshot {
	return Snapshot{
		ListenAddr:        ":8080",
		SessionTTLHours:   12,
		CookieSecure:      false,
		Branch:            "main",
		Remote:            "origin",
		AutoUpdateEnabled: false,
		AutoUpdateMinutes: 15,
		AutoBuildEnabled:   false,
		PortFlag:           "-port",
		ServiceScope:       "system",
		ServiceRestart:     "on-failure",
		CaddyUpstream:      "127.0.0.1:{port}",
		CaddySnippetDir:    "/etc/caddy/sites.d",
		CaddyReloadCommand: []string{"systemctl", "reload", "caddy"},
		LogPath:            "updates.log",
		MaxLogRows:         500,
	}
}

func Load(path string) (*Config, error) {
	cfg := &Config{data: defaults()}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		cfg.data.SessionSecret = newSecret()
		if err := saveLocked(path, &cfg.data); err != nil {
			return nil, fmt.Errorf("write default config: %w", err)
		}
		return cfg, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &cfg.data); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.data.SessionSecret == "" {
		cfg.data.SessionSecret = newSecret()
		_ = saveLocked(path, &cfg.data)
	}
	if cfg.data.AutoUpdateMinutes <= 0 {
		cfg.data.AutoUpdateMinutes = 15
	}
	if cfg.data.SessionTTLHours <= 0 {
		cfg.data.SessionTTLHours = 12
	}
	return cfg, nil
}

func Save(path string, cfg *Config) error {
	cfg.mu.RLock()
	defer cfg.mu.RUnlock()
	return saveLocked(path, &cfg.data)
}

func saveLocked(path string, data *Snapshot) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	buf, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Snapshot returns a deep copy of the current config data.
func (c *Config) Snapshot() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := c.data
	out.PostUpdateArgs = append([]string(nil), c.data.PostUpdateArgs...)
	out.GitEnv = append([]string(nil), c.data.GitEnv...)
	out.BuildArgs = append([]string(nil), c.data.BuildArgs...)
	out.ServiceExecArgs = append([]string(nil), c.data.ServiceExecArgs...)
	out.ServiceEnv = append([]string(nil), c.data.ServiceEnv...)
	out.CaddyReloadCommand = append([]string(nil), c.data.CaddyReloadCommand...)
	return out
}

// Update applies fn under write lock and persists the result.
func (c *Config) Update(path string, fn func(*Snapshot)) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	fn(&c.data)
	return saveLocked(path, &c.data)
}

func newSecret() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
