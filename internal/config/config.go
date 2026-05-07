package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// App is a single managed deployment: one repo + build + service + caddy site.
type App struct {
	ID   string `json:"id"`
	Name string `json:"name"`

	RepoPath          string   `json:"repo_path"`
	CloneURL          string   `json:"clone_url,omitempty"`
	Branch            string   `json:"branch"`
	Remote            string   `json:"remote"`
	PostUpdateCommand string   `json:"post_update_command,omitempty"`
	PostUpdateArgs    []string `json:"post_update_args,omitempty"`
	GitEnv            []string `json:"git_env,omitempty"`

	AutoUpdateEnabled bool `json:"auto_update_enabled"`
	AutoUpdateMinutes int  `json:"auto_update_minutes"`

	AutoBuildEnabled bool     `json:"auto_build_enabled"`
	BuildCommand     string   `json:"build_command,omitempty"`
	BuildArgs        []string `json:"build_args,omitempty"`

	AppPort  int    `json:"app_port,omitempty"`
	PortFlag string `json:"port_flag,omitempty"`

	ServiceManageEnabled bool     `json:"service_manage_enabled"`
	ServiceName          string   `json:"service_name,omitempty"`
	ServiceScope         string   `json:"service_scope,omitempty"`
	ServiceDescription   string   `json:"service_description,omitempty"`
	ServiceExecStart     string   `json:"service_exec_start,omitempty"`
	ServiceExecArgs      []string `json:"service_exec_args,omitempty"`
	ServiceWorkingDir    string   `json:"service_working_dir,omitempty"`
	ServiceUser          string   `json:"service_user,omitempty"`
	ServiceEnv           []string `json:"service_env,omitempty"`
	ServiceRestart       string   `json:"service_restart,omitempty"`

	CaddyEnabled       bool     `json:"caddy_enabled"`
	CaddyAutoApply     bool     `json:"caddy_auto_apply"`
	CaddyMode          string   `json:"caddy_mode,omitempty"` // "proxy" (default) or "file_server"
	CaddyDomain        string   `json:"caddy_domain,omitempty"`
	CaddyUpstream      string   `json:"caddy_upstream,omitempty"`
	CaddyRoot          string   `json:"caddy_root,omitempty"`     // file_server: dir to serve, defaults to repo path
	CaddyBrowse        bool     `json:"caddy_browse,omitempty"`   // file_server: show directory listing
	CaddyTryFiles      string   `json:"caddy_try_files,omitempty"` // file_server: SPA-style fallback
	CaddyExtra         string   `json:"caddy_extra,omitempty"`
	CaddySnippetDir    string   `json:"caddy_snippet_dir,omitempty"`
	CaddySnippetName   string   `json:"caddy_snippet_name,omitempty"`
	CaddyReloadCommand []string `json:"caddy_reload_command,omitempty"`

	// File-server mode permission fixer.
	CaddyFilesUser     string `json:"caddy_files_user,omitempty"`
	CaddyFilesGroup    string `json:"caddy_files_group,omitempty"`
	CaddyFilesDirMode  string `json:"caddy_files_dir_mode,omitempty"`  // octal e.g. "0755"
	CaddyFilesFileMode string `json:"caddy_files_file_mode,omitempty"` // octal e.g. "0644"

	CreatedAt time.Time `json:"created_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// Snapshot is server-wide config plus the list of managed apps. It is a
// value type that can be copied freely.
type Snapshot struct {
	ListenAddr      string `json:"listen_addr"`
	TLSCertFile     string `json:"tls_cert_file,omitempty"`
	TLSKeyFile      string `json:"tls_key_file,omitempty"`
	SessionSecret   string `json:"session_secret"`
	SessionTTLHours int    `json:"session_ttl_hours"`
	CookieSecure    bool   `json:"cookie_secure"`

	Username     string `json:"username"`
	PasswordHash string `json:"password_hash"`

	// ReposDir is prepended to any per-app RepoPath that is not absolute.
	// Lets you keep all working copies under one root (e.g. "/srv/apps")
	// and reference them by name in each app's RepoPath.
	ReposDir string `json:"repos_dir,omitempty"`

	LogPath    string `json:"log_path"`
	MaxLogRows int    `json:"max_log_rows"`

	Apps []App `json:"apps"`
}

// Config wraps a Snapshot with a mutex for live updates from the UI.
type Config struct {
	mu   sync.RWMutex
	data Snapshot
}

func defaults() Snapshot {
	return Snapshot{
		ListenAddr:      ":8080",
		SessionTTLHours: 12,
		CookieSecure:    false,
		LogPath:         "updates.log",
		MaxLogRows:      500,
		Apps:            []App{},
	}
}

// AppDefaults returns reasonable per-field defaults for a fresh app.
func AppDefaults() App {
	return App{
		Branch:             "main",
		Remote:             "origin",
		AutoUpdateMinutes:  15,
		PortFlag:           "-port",
		ServiceScope:       "system",
		ServiceRestart:     "on-failure",
		CaddyMode:          "proxy",
		CaddyUpstream:      "127.0.0.1:{port}",
		CaddySnippetDir:    "/etc/caddy/sites.d",
		CaddyReloadCommand: []string{"systemctl", "reload", "caddy"},
		CaddyFilesDirMode:  "0755",
		CaddyFilesFileMode: "0644",
	}
}

// legacyConfig captures the old single-app top-level fields so we can migrate.
type legacyConfig struct {
	RepoPath             string   `json:"repo_path"`
	Branch               string   `json:"branch"`
	Remote               string   `json:"remote"`
	PostUpdateCommand    string   `json:"post_update_command"`
	PostUpdateArgs       []string `json:"post_update_args"`
	GitEnv               []string `json:"git_env"`
	AutoUpdateEnabled    bool     `json:"auto_update_enabled"`
	AutoUpdateMinutes    int      `json:"auto_update_minutes"`
	AutoBuildEnabled     bool     `json:"auto_build_enabled"`
	BuildCommand         string   `json:"build_command"`
	BuildArgs            []string `json:"build_args"`
	AppPort              int      `json:"app_port"`
	PortFlag             string   `json:"port_flag"`
	ServiceManageEnabled bool     `json:"service_manage_enabled"`
	ServiceName          string   `json:"service_name"`
	ServiceScope         string   `json:"service_scope"`
	ServiceDescription   string   `json:"service_description"`
	ServiceExecStart     string   `json:"service_exec_start"`
	ServiceExecArgs      []string `json:"service_exec_args"`
	ServiceWorkingDir    string   `json:"service_working_dir"`
	ServiceUser          string   `json:"service_user"`
	ServiceEnv           []string `json:"service_env"`
	ServiceRestart       string   `json:"service_restart"`
	CaddyEnabled         bool     `json:"caddy_enabled"`
	CaddyAutoApply       bool     `json:"caddy_auto_apply"`
	CaddyDomain          string   `json:"caddy_domain"`
	CaddyUpstream        string   `json:"caddy_upstream"`
	CaddyExtra           string   `json:"caddy_extra"`
	CaddySnippetDir      string   `json:"caddy_snippet_dir"`
	CaddySnippetName     string   `json:"caddy_snippet_name"`
	CaddyReloadCommand   []string `json:"caddy_reload_command"`
}

func (l legacyConfig) toApp(now time.Time) App {
	a := AppDefaults()
	a.ID = newID()
	a.Name = "default"
	a.RepoPath = l.RepoPath
	if l.Branch != "" {
		a.Branch = l.Branch
	}
	// legacy had no CloneURL field
	if l.Remote != "" {
		a.Remote = l.Remote
	}
	a.PostUpdateCommand = l.PostUpdateCommand
	a.PostUpdateArgs = l.PostUpdateArgs
	a.GitEnv = l.GitEnv
	a.AutoUpdateEnabled = l.AutoUpdateEnabled
	if l.AutoUpdateMinutes > 0 {
		a.AutoUpdateMinutes = l.AutoUpdateMinutes
	}
	a.AutoBuildEnabled = l.AutoBuildEnabled
	a.BuildCommand = l.BuildCommand
	a.BuildArgs = l.BuildArgs
	a.AppPort = l.AppPort
	if l.PortFlag != "" {
		a.PortFlag = l.PortFlag
	}
	a.ServiceManageEnabled = l.ServiceManageEnabled
	a.ServiceName = l.ServiceName
	if l.ServiceScope != "" {
		a.ServiceScope = l.ServiceScope
	}
	a.ServiceDescription = l.ServiceDescription
	a.ServiceExecStart = l.ServiceExecStart
	a.ServiceExecArgs = l.ServiceExecArgs
	a.ServiceWorkingDir = l.ServiceWorkingDir
	a.ServiceUser = l.ServiceUser
	a.ServiceEnv = l.ServiceEnv
	if l.ServiceRestart != "" {
		a.ServiceRestart = l.ServiceRestart
	}
	a.CaddyEnabled = l.CaddyEnabled
	a.CaddyAutoApply = l.CaddyAutoApply
	a.CaddyDomain = l.CaddyDomain
	if l.CaddyUpstream != "" {
		a.CaddyUpstream = l.CaddyUpstream
	}
	a.CaddyExtra = l.CaddyExtra
	if l.CaddySnippetDir != "" {
		a.CaddySnippetDir = l.CaddySnippetDir
	}
	a.CaddySnippetName = l.CaddySnippetName
	if len(l.CaddyReloadCommand) > 0 {
		a.CaddyReloadCommand = l.CaddyReloadCommand
	}
	a.CreatedAt = now
	a.UpdatedAt = now
	return a
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

	// Legacy single-app migration: if we have no apps but the JSON has the
	// old top-level repo fields, fold them into a single app and re-save.
	if len(cfg.data.Apps) == 0 {
		var legacy legacyConfig
		_ = json.Unmarshal(data, &legacy)
		if legacy.RepoPath != "" {
			cfg.data.Apps = []App{legacy.toApp(time.Now())}
		}
	}

	// Backfill IDs / timestamps for any app missing them.
	now := time.Now()
	for i := range cfg.data.Apps {
		if cfg.data.Apps[i].ID == "" {
			cfg.data.Apps[i].ID = newID()
		}
		if cfg.data.Apps[i].CreatedAt.IsZero() {
			cfg.data.Apps[i].CreatedAt = now
		}
		if cfg.data.Apps[i].UpdatedAt.IsZero() {
			cfg.data.Apps[i].UpdatedAt = now
		}
		applyAppDefaults(&cfg.data.Apps[i])
	}

	if cfg.data.SessionSecret == "" {
		cfg.data.SessionSecret = newSecret()
	}
	if cfg.data.SessionTTLHours <= 0 {
		cfg.data.SessionTTLHours = 12
	}
	_ = saveLocked(path, &cfg.data)
	return cfg, nil
}

func applyAppDefaults(a *App) {
	if a.Branch == "" {
		a.Branch = "main"
	}
	if a.Remote == "" {
		a.Remote = "origin"
	}
	if a.AutoUpdateMinutes <= 0 {
		a.AutoUpdateMinutes = 15
	}
	if a.PortFlag == "" {
		a.PortFlag = "-port"
	}
	if a.ServiceScope == "" {
		a.ServiceScope = "system"
	}
	if a.ServiceRestart == "" {
		a.ServiceRestart = "on-failure"
	}
	if a.CaddyUpstream == "" {
		a.CaddyUpstream = "127.0.0.1:{port}"
	}
	if a.CaddySnippetDir == "" {
		a.CaddySnippetDir = "/etc/caddy/sites.d"
	}
	if len(a.CaddyReloadCommand) == 0 {
		a.CaddyReloadCommand = []string{"systemctl", "reload", "caddy"}
	}
	if a.CaddyMode == "" {
		a.CaddyMode = "proxy"
	}
	if a.CaddyFilesDirMode == "" {
		a.CaddyFilesDirMode = "0755"
	}
	if a.CaddyFilesFileMode == "" {
		a.CaddyFilesFileMode = "0644"
	}
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
	return cloneSnapshot(c.data)
}

func cloneSnapshot(s Snapshot) Snapshot {
	out := s
	out.Apps = make([]App, len(s.Apps))
	for i, a := range s.Apps {
		out.Apps[i] = cloneApp(a)
	}
	return out
}

func cloneApp(a App) App {
	out := a
	out.PostUpdateArgs = append([]string(nil), a.PostUpdateArgs...)
	out.GitEnv = append([]string(nil), a.GitEnv...)
	out.BuildArgs = append([]string(nil), a.BuildArgs...)
	out.ServiceExecArgs = append([]string(nil), a.ServiceExecArgs...)
	out.ServiceEnv = append([]string(nil), a.ServiceEnv...)
	out.CaddyReloadCommand = append([]string(nil), a.CaddyReloadCommand...)
	return out
}

// Update applies fn to the snapshot under write lock and persists the result.
func (c *Config) Update(path string, fn func(*Snapshot)) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	fn(&c.data)
	return saveLocked(path, &c.data)
}

// FindApp returns a copy of the app with the given ID, or zero + false.
func (s Snapshot) FindApp(id string) (App, bool) {
	for _, a := range s.Apps {
		if a.ID == id {
			return cloneApp(a), true
		}
	}
	return App{}, false
}

// AddApp appends a new app and assigns an ID + timestamps. Returns the new app.
func (c *Config) AddApp(path string, a App) (App, error) {
	if err := validateApp(&a); err != nil {
		return App{}, err
	}
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if a.ID == "" {
		a.ID = newID()
	}
	for _, existing := range c.data.Apps {
		if existing.ID == a.ID {
			return App{}, fmt.Errorf("app id %q already exists", a.ID)
		}
		if strings.EqualFold(existing.Name, a.Name) {
			return App{}, fmt.Errorf("app name %q already exists", a.Name)
		}
	}
	a.CreatedAt = now
	a.UpdatedAt = now
	applyAppDefaults(&a)
	c.data.Apps = append(c.data.Apps, a)
	if err := saveLocked(path, &c.data); err != nil {
		// rollback in-memory state
		c.data.Apps = c.data.Apps[:len(c.data.Apps)-1]
		return App{}, err
	}
	return cloneApp(a), nil
}

// UpdateApp replaces an existing app's fields via fn.
func (c *Config) UpdateApp(path, id string, fn func(*App)) (App, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.data.Apps {
		if c.data.Apps[i].ID != id {
			continue
		}
		original := cloneApp(c.data.Apps[i])
		fn(&c.data.Apps[i])
		c.data.Apps[i].ID = id
		c.data.Apps[i].CreatedAt = original.CreatedAt
		c.data.Apps[i].UpdatedAt = time.Now()
		applyAppDefaults(&c.data.Apps[i])
		if err := validateApp(&c.data.Apps[i]); err != nil {
			c.data.Apps[i] = original
			return App{}, err
		}
		// uniqueness check on Name
		for j, other := range c.data.Apps {
			if j != i && strings.EqualFold(other.Name, c.data.Apps[i].Name) {
				c.data.Apps[i] = original
				return App{}, fmt.Errorf("app name %q already exists", other.Name)
			}
		}
		if err := saveLocked(path, &c.data); err != nil {
			c.data.Apps[i] = original
			return App{}, err
		}
		return cloneApp(c.data.Apps[i]), nil
	}
	return App{}, fmt.Errorf("app %q not found", id)
}

// DeleteApp removes an app by ID.
func (c *Config) DeleteApp(path, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, a := range c.data.Apps {
		if a.ID != id {
			continue
		}
		c.data.Apps = append(c.data.Apps[:i], c.data.Apps[i+1:]...)
		if err := saveLocked(path, &c.data); err != nil {
			c.data.Apps = append(c.data.Apps, App{}) // make room
			copy(c.data.Apps[i+1:], c.data.Apps[i:])
			c.data.Apps[i] = a
			return err
		}
		return nil
	}
	return fmt.Errorf("app %q not found", id)
}

// SortedApps returns apps ordered by name (stable).
func (s Snapshot) SortedApps() []App {
	out := make([]App, len(s.Apps))
	copy(out, s.Apps)
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

var (
	nameRE = regexp.MustCompile(`^[A-Za-z0-9._-][A-Za-z0-9 ._-]{0,62}$`)
	idRE   = regexp.MustCompile(`^[A-Za-z0-9_-]{4,32}$`)
)

func validateApp(a *App) error {
	a.Name = strings.TrimSpace(a.Name)
	if a.Name == "" {
		return errors.New("app name is required")
	}
	if !nameRE.MatchString(a.Name) {
		return errors.New("app name contains invalid characters")
	}
	if a.AppPort < 0 || a.AppPort > 65535 {
		return errors.New("app_port must be between 0 and 65535")
	}
	if s := a.ServiceScope; s != "" && s != "system" && s != "user" {
		return errors.New("service_scope must be 'system' or 'user'")
	}
	return nil
}

// IsValidID reports whether id is shaped like a generated app ID.
func IsValidID(id string) bool { return idRE.MatchString(id) }

// ResolvedRepoPath returns app.RepoPath joined with reposDir if it's relative.
// Absolute paths and empty RepoPath values are returned unchanged.
func (a App) ResolvedRepoPath(reposDir string) string {
	return ResolveRepoPath(reposDir, a.RepoPath)
}

// ResolveRepoPath joins reposDir with repoPath when repoPath is relative.
func ResolveRepoPath(reposDir, repoPath string) string {
	if repoPath == "" || filepath.IsAbs(repoPath) || reposDir == "" {
		return repoPath
	}
	return filepath.Join(reposDir, repoPath)
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func newSecret() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
