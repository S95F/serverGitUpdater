package server

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/s95f/servergitupdater/internal/build"
	"github.com/s95f/servergitupdater/internal/caddy"
	"github.com/s95f/servergitupdater/internal/config"
	"github.com/s95f/servergitupdater/internal/service"
	"github.com/s95f/servergitupdater/internal/tmpl"
)

// appPayload is the JSON shape used for create/update requests.
type appPayload struct {
	Name string `json:"name"`

	RepoPath          string   `json:"repo_path"`
	Branch            string   `json:"branch"`
	Remote            string   `json:"remote"`
	PostUpdateCommand string   `json:"post_update_command"`
	PostUpdateArgs    []string `json:"post_update_args"`
	GitEnv            []string `json:"git_env"`

	AutoUpdateEnabled bool `json:"auto_update_enabled"`
	AutoUpdateMinutes int  `json:"auto_update_minutes"`

	AutoBuildEnabled bool     `json:"auto_build_enabled"`
	BuildCommand     string   `json:"build_command"`
	BuildArgs        []string `json:"build_args"`

	AppPort  int    `json:"app_port"`
	PortFlag string `json:"port_flag"`

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

	CaddyEnabled       bool     `json:"caddy_enabled"`
	CaddyAutoApply     bool     `json:"caddy_auto_apply"`
	CaddyMode          string   `json:"caddy_mode"`
	CaddyDomain        string   `json:"caddy_domain"`
	CaddyUpstream      string   `json:"caddy_upstream"`
	CaddyRoot          string   `json:"caddy_root"`
	CaddyBrowse        bool     `json:"caddy_browse"`
	CaddyTryFiles      string   `json:"caddy_try_files"`
	CaddyExtra         string   `json:"caddy_extra"`
	CaddySnippetDir    string   `json:"caddy_snippet_dir"`
	CaddySnippetName   string   `json:"caddy_snippet_name"`
	CaddyReloadCommand []string `json:"caddy_reload_command"`
	CaddyFilesUser     string   `json:"caddy_files_user"`
	CaddyFilesGroup    string   `json:"caddy_files_group"`
	CaddyFilesDirMode  string   `json:"caddy_files_dir_mode"`
	CaddyFilesFileMode string   `json:"caddy_files_file_mode"`
}

func (p appPayload) intoApp(target *config.App) {
	target.Name = strings.TrimSpace(p.Name)
	target.RepoPath = strings.TrimSpace(p.RepoPath)
	target.Branch = strings.TrimSpace(p.Branch)
	target.Remote = strings.TrimSpace(p.Remote)
	target.PostUpdateCommand = strings.TrimSpace(p.PostUpdateCommand)
	target.PostUpdateArgs = trimAll(p.PostUpdateArgs)
	target.GitEnv = trimAll(p.GitEnv)
	target.AutoUpdateEnabled = p.AutoUpdateEnabled
	target.AutoUpdateMinutes = p.AutoUpdateMinutes
	target.AutoBuildEnabled = p.AutoBuildEnabled
	target.BuildCommand = strings.TrimSpace(p.BuildCommand)
	target.BuildArgs = trimAll(p.BuildArgs)
	target.AppPort = p.AppPort
	target.PortFlag = strings.TrimSpace(p.PortFlag)
	target.ServiceManageEnabled = p.ServiceManageEnabled
	target.ServiceName = strings.TrimSpace(p.ServiceName)
	target.ServiceScope = strings.TrimSpace(p.ServiceScope)
	target.ServiceDescription = p.ServiceDescription
	target.ServiceExecStart = strings.TrimSpace(p.ServiceExecStart)
	target.ServiceExecArgs = trimAll(p.ServiceExecArgs)
	target.ServiceWorkingDir = strings.TrimSpace(p.ServiceWorkingDir)
	target.ServiceUser = strings.TrimSpace(p.ServiceUser)
	target.ServiceEnv = trimAll(p.ServiceEnv)
	target.ServiceRestart = strings.TrimSpace(p.ServiceRestart)
	target.CaddyEnabled = p.CaddyEnabled
	target.CaddyAutoApply = p.CaddyAutoApply
	target.CaddyMode = strings.TrimSpace(p.CaddyMode)
	target.CaddyDomain = strings.TrimSpace(p.CaddyDomain)
	target.CaddyUpstream = strings.TrimSpace(p.CaddyUpstream)
	target.CaddyRoot = strings.TrimSpace(p.CaddyRoot)
	target.CaddyBrowse = p.CaddyBrowse
	target.CaddyTryFiles = strings.TrimSpace(p.CaddyTryFiles)
	target.CaddyExtra = p.CaddyExtra
	target.CaddySnippetDir = strings.TrimSpace(p.CaddySnippetDir)
	target.CaddySnippetName = strings.TrimSpace(p.CaddySnippetName)
	target.CaddyReloadCommand = trimAll(p.CaddyReloadCommand)
	target.CaddyFilesUser = strings.TrimSpace(p.CaddyFilesUser)
	target.CaddyFilesGroup = strings.TrimSpace(p.CaddyFilesGroup)
	target.CaddyFilesDirMode = strings.TrimSpace(p.CaddyFilesDirMode)
	target.CaddyFilesFileMode = strings.TrimSpace(p.CaddyFilesFileMode)
}

func appView(a config.App) appPayload {
	return appPayload{
		Name:                 a.Name,
		RepoPath:             a.RepoPath,
		Branch:               a.Branch,
		Remote:               a.Remote,
		PostUpdateCommand:    a.PostUpdateCommand,
		PostUpdateArgs:       a.PostUpdateArgs,
		GitEnv:               a.GitEnv,
		AutoUpdateEnabled:    a.AutoUpdateEnabled,
		AutoUpdateMinutes:    a.AutoUpdateMinutes,
		AutoBuildEnabled:     a.AutoBuildEnabled,
		BuildCommand:         a.BuildCommand,
		BuildArgs:            a.BuildArgs,
		AppPort:              a.AppPort,
		PortFlag:             a.PortFlag,
		ServiceManageEnabled: a.ServiceManageEnabled,
		ServiceName:          a.ServiceName,
		ServiceScope:         a.ServiceScope,
		ServiceDescription:   a.ServiceDescription,
		ServiceExecStart:     a.ServiceExecStart,
		ServiceExecArgs:      a.ServiceExecArgs,
		ServiceWorkingDir:    a.ServiceWorkingDir,
		ServiceUser:          a.ServiceUser,
		ServiceEnv:           a.ServiceEnv,
		ServiceRestart:       a.ServiceRestart,
		CaddyEnabled:         a.CaddyEnabled,
		CaddyAutoApply:       a.CaddyAutoApply,
		CaddyMode:            a.CaddyMode,
		CaddyDomain:          a.CaddyDomain,
		CaddyUpstream:        a.CaddyUpstream,
		CaddyRoot:            a.CaddyRoot,
		CaddyBrowse:          a.CaddyBrowse,
		CaddyTryFiles:        a.CaddyTryFiles,
		CaddyExtra:           a.CaddyExtra,
		CaddySnippetDir:      a.CaddySnippetDir,
		CaddySnippetName:     a.CaddySnippetName,
		CaddyReloadCommand:   a.CaddyReloadCommand,
		CaddyFilesUser:       a.CaddyFilesUser,
		CaddyFilesGroup:      a.CaddyFilesGroup,
		CaddyFilesDirMode:    a.CaddyFilesDirMode,
		CaddyFilesFileMode:   a.CaddyFilesFileMode,
	}
}

// appSummary is the lightweight payload used by the landing list.
type appSummary struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	RepoPath          string    `json:"repo_path"`
	ResolvedRepoPath  string    `json:"resolved_repo_path,omitempty"`
	Branch            string    `json:"branch"`
	AppPort           int       `json:"app_port,omitempty"`
	AutoUpdateEnabled bool      `json:"auto_update_enabled"`
	AutoUpdateMinutes int       `json:"auto_update_minutes"`
	AutoBuildEnabled  bool      `json:"auto_build_enabled"`
	ServiceEnabled    bool      `json:"service_enabled"`
	ServiceName       string    `json:"service_name,omitempty"`
	CaddyEnabled      bool      `json:"caddy_enabled"`
	CaddyDomain       string    `json:"caddy_domain,omitempty"`
	UpdatedAt         time.Time `json:"updated_at"`
	LastRun           time.Time `json:"last_run,omitempty"`
}

// loadAppOr404 returns the app as persisted (no path resolution).
// Use loadResolvedAppOr404 for handlers that pass the path to git, build,
// systemctl or Caddy and therefore need the absolute path.
func (s *Server) loadAppOr404(w http.ResponseWriter, r *http.Request) (config.App, bool) {
	id := r.PathValue("id")
	if !config.IsValidID(id) {
		writeJSONError(w, http.StatusBadRequest, "invalid app id")
		return config.App{}, false
	}
	snap := s.cfg.Snapshot()
	app, ok := snap.FindApp(id)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "app not found")
		return config.App{}, false
	}
	return app, true
}

// loadResolvedAppOr404 is loadAppOr404 plus repo_path resolution.
// The mutation is local to the returned copy; the persisted config is
// unchanged so editing the relative path round-trips cleanly.
func (s *Server) loadResolvedAppOr404(w http.ResponseWriter, r *http.Request) (config.App, bool) {
	app, ok := s.loadAppOr404(w, r)
	if !ok {
		return config.App{}, false
	}
	app.RepoPath = app.ResolvedRepoPath(s.cfg.Snapshot().ReposDir)
	return app, true
}

// ---------- CRUD ----------

func (s *Server) handleListApps(w http.ResponseWriter, r *http.Request) {
	snap := s.cfg.Snapshot()
	apps := snap.SortedApps()
	out := make([]appSummary, 0, len(apps))
	for _, a := range apps {
		resolved := a.ResolvedRepoPath(snap.ReposDir)
		if resolved == a.RepoPath {
			resolved = ""
		}
		out = append(out, appSummary{
			ID:                a.ID,
			Name:              a.Name,
			RepoPath:          a.RepoPath,
			ResolvedRepoPath:  resolved,
			Branch:            a.Branch,
			AppPort:           a.AppPort,
			AutoUpdateEnabled: a.AutoUpdateEnabled,
			AutoUpdateMinutes: a.AutoUpdateMinutes,
			AutoBuildEnabled:  a.AutoBuildEnabled,
			ServiceEnabled:    a.ServiceManageEnabled,
			ServiceName:       a.ServiceName,
			CaddyEnabled:      a.CaddyEnabled,
			CaddyDomain:       a.CaddyDomain,
			UpdatedAt:         a.UpdatedAt,
			LastRun:           s.updater.LastRun(a.ID),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"apps": out})
}

func (s *Server) handleCreateApp(w http.ResponseWriter, r *http.Request) {
	var p appPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad request")
		return
	}
	a := config.AppDefaults()
	p.intoApp(&a)
	created, err := s.cfg.AddApp(s.configPath, a)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"app": appWithID(created)})
}

func (s *Server) handleGetApp(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadAppOr404(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":         a.ID,
		"name":       a.Name,
		"created_at": a.CreatedAt,
		"updated_at": a.UpdatedAt,
		"app":        appView(a),
	})
}

func (s *Server) handleUpdateApp(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !config.IsValidID(id) {
		writeJSONError(w, http.StatusBadRequest, "invalid app id")
		return
	}
	var p appPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad request")
		return
	}
	updated, err := s.cfg.UpdateApp(s.configPath, id, func(a *config.App) {
		p.intoApp(a)
	})
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "app": appWithID(updated)})
}

func (s *Server) handleDeleteApp(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !config.IsValidID(id) {
		writeJSONError(w, http.StatusBadRequest, "invalid app id")
		return
	}
	if err := s.cfg.DeleteApp(s.configPath, id); err != nil {
		writeJSONError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func appWithID(a config.App) map[string]any {
	v := appView(a)
	return map[string]any{"id": a.ID, "name": a.Name, "created_at": a.CreatedAt, "updated_at": a.UpdatedAt, "app": v}
}

// ---------- per-app pipeline ----------

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadResolvedAppOr404(w, r)
	if !ok {
		return
	}
	st, err := s.updater.Status(r.Context(), a.ID)
	resp := map[string]any{
		"id":                  a.ID,
		"name":                a.Name,
		"repo_path":           a.RepoPath,
		"branch":              a.Branch,
		"remote":              a.Remote,
		"app_port":            a.AppPort,
		"auto_update_enabled": a.AutoUpdateEnabled,
		"auto_update_minutes": a.AutoUpdateMinutes,
		"git":                 st,
		"last_run":            s.updater.LastRun(a.ID),
	}
	if err != nil {
		resp["git_error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleUpdateNow(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadResolvedAppOr404(w, r)
	if !ok {
		return
	}
	entry, err := s.updater.RunUpdate(r.Context(), a.ID, "manual")
	status := http.StatusOK
	if err != nil {
		status = http.StatusInternalServerError
	}
	writeJSON(w, status, map[string]any{"ok": err == nil, "entry": entry})
}

func (s *Server) handleBuildDetect(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadResolvedAppOr404(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, build.Detect(a.RepoPath))
}

func (s *Server) handleBuildRun(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadResolvedAppOr404(w, r)
	if !ok {
		return
	}
	vars := tmpl.Vars{Port: a.AppPort, RepoPath: a.RepoPath, Name: a.ServiceName}
	out, err := build.Run(r.Context(), a.RepoPath, a.BuildCommand, a.BuildArgs, a.GitEnv, a.AutoBuildEnabled, vars)
	resp := map[string]any{"ok": err == nil, "output": out}
	if err != nil {
		resp["error"] = err.Error()
	}
	st := http.StatusOK
	if err != nil {
		st = http.StatusInternalServerError
	}
	writeJSON(w, st, resp)
}

// ---------- service ----------

func (s *Server) buildSpec(a config.App) service.Spec {
	scope := service.Scope(a.ServiceScope)
	if scope == "" {
		scope = service.ScopeSystem
	}
	exec := a.ServiceExecStart
	if exec == "" {
		d := build.Detect(a.RepoPath)
		if d.SuggestedOutput != "" {
			exec = d.SuggestedOutput
		}
	}
	wd := a.ServiceWorkingDir
	if wd == "" {
		wd = a.RepoPath
	}
	desc := a.ServiceDescription
	if desc == "" && a.ServiceName != "" {
		desc = "serverGitUpdater-managed: " + a.ServiceName
	}
	vars := tmpl.Vars{Port: a.AppPort, RepoPath: a.RepoPath, Name: a.ServiceName}
	exec = vars.Apply(exec)
	args := vars.ApplyAll(a.ServiceExecArgs)
	if a.AppPort > 0 && !tmpl.HasPort(a.ServiceExecArgs) {
		flag := a.PortFlag
		if flag == "" {
			flag = "-port"
		}
		args = append(args, flag, strconv.Itoa(a.AppPort))
	}
	return service.Spec{
		Name:        a.ServiceName,
		Scope:       scope,
		Description: desc,
		ExecStart:   exec,
		ExecArgs:    args,
		WorkingDir:  wd,
		User:        a.ServiceUser,
		Env:         a.ServiceEnv,
		Restart:     a.ServiceRestart,
	}
}

func (s *Server) handleServiceStatus(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadResolvedAppOr404(w, r)
	if !ok {
		return
	}
	if a.ServiceName == "" {
		writeJSON(w, http.StatusOK, map[string]any{"configured": false})
		return
	}
	scope := service.Scope(a.ServiceScope)
	if scope == "" {
		scope = service.ScopeSystem
	}
	st := service.ReadStatus(r.Context(), scope, a.ServiceName)
	writeJSON(w, http.StatusOK, map[string]any{"configured": true, "status": st})
}

func (s *Server) handleServicePreview(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadResolvedAppOr404(w, r)
	if !ok {
		return
	}
	spec := s.buildSpec(a)
	content, err := service.Render(spec)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	path, _ := service.UnitPath(spec.Scope, spec.Name)
	writeJSON(w, http.StatusOK, map[string]any{"unit_path": path, "unit": content})
}

func (s *Server) handleServiceInstall(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadResolvedAppOr404(w, r)
	if !ok {
		return
	}
	out, err := service.Install(r.Context(), s.buildSpec(a))
	resp := map[string]any{"ok": err == nil, "output": out}
	if err != nil {
		resp["error"] = err.Error()
	}
	st := http.StatusOK
	if err != nil {
		st = http.StatusInternalServerError
	}
	writeJSON(w, st, resp)
}

func (s *Server) handleServiceUninstall(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadResolvedAppOr404(w, r)
	if !ok {
		return
	}
	if a.ServiceName == "" {
		writeJSONError(w, http.StatusBadRequest, "service_name not set")
		return
	}
	scope := service.Scope(a.ServiceScope)
	if scope == "" {
		scope = service.ScopeSystem
	}
	out, err := service.Uninstall(r.Context(), scope, a.ServiceName)
	resp := map[string]any{"ok": err == nil, "output": out}
	if err != nil {
		resp["error"] = err.Error()
	}
	st := http.StatusOK
	if err != nil {
		st = http.StatusInternalServerError
	}
	writeJSON(w, st, resp)
}

func (s *Server) handleServiceRestart(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadResolvedAppOr404(w, r)
	if !ok {
		return
	}
	if a.ServiceName == "" {
		writeJSONError(w, http.StatusBadRequest, "service_name not set")
		return
	}
	scope := service.Scope(a.ServiceScope)
	if scope == "" {
		scope = service.ScopeSystem
	}
	out, err := service.Restart(r.Context(), scope, a.ServiceName)
	resp := map[string]any{"ok": err == nil, "output": out}
	if err != nil {
		resp["error"] = err.Error()
	}
	st := http.StatusOK
	if err != nil {
		st = http.StatusInternalServerError
	}
	writeJSON(w, st, resp)
}

// ---------- caddy ----------

func (s *Server) caddyConfig(a config.App) caddy.Config {
	snippetName := a.CaddySnippetName
	if snippetName == "" {
		snippetName = a.ServiceName
		if snippetName == "" {
			snippetName = a.Name
		}
	}
	mode := caddy.Mode(a.CaddyMode)
	if mode == "" {
		mode = caddy.ModeProxy
	}
	return caddy.Config{
		Enabled:       a.CaddyEnabled,
		Mode:          mode,
		Domain:        a.CaddyDomain,
		Upstream:      a.CaddyUpstream,
		Root:          a.CaddyRoot,
		Browse:        a.CaddyBrowse,
		TryFiles:      a.CaddyTryFiles,
		Extra:         a.CaddyExtra,
		SnippetDir:    a.CaddySnippetDir,
		SnippetName:   snippetName,
		ReloadCommand: a.CaddyReloadCommand,
		Vars:          tmpl.Vars{Port: a.AppPort, RepoPath: a.RepoPath, Name: snippetName},
	}
}

func (s *Server) handleCaddyStatus(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadResolvedAppOr404(w, r)
	if !ok {
		return
	}
	if !a.CaddyEnabled || a.CaddyDomain == "" {
		writeJSON(w, http.StatusOK, map[string]any{"configured": false})
		return
	}
	st := caddy.ReadStatus(s.caddyConfig(a))
	writeJSON(w, http.StatusOK, map[string]any{"configured": true, "status": st})
}

func (s *Server) handleCaddyPreview(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadResolvedAppOr404(w, r)
	if !ok {
		return
	}
	c := s.caddyConfig(a)
	content, err := c.Render()
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	st := caddy.ReadStatus(c)
	writeJSON(w, http.StatusOK, map[string]any{"snippet_path": st.SnippetPath, "snippet": content})
}

func (s *Server) handleCaddyApply(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadResolvedAppOr404(w, r)
	if !ok {
		return
	}
	out, err := caddy.Apply(r.Context(), s.caddyConfig(a))
	resp := map[string]any{"ok": err == nil, "output": out}
	if err != nil {
		resp["error"] = err.Error()
	}
	st := http.StatusOK
	if err != nil {
		st = http.StatusInternalServerError
	}
	writeJSON(w, st, resp)
}

func (s *Server) handleCaddyRemove(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadResolvedAppOr404(w, r)
	if !ok {
		return
	}
	out, err := caddy.Remove(r.Context(), s.caddyConfig(a))
	resp := map[string]any{"ok": err == nil, "output": out}
	if err != nil {
		resp["error"] = err.Error()
	}
	st := http.StatusOK
	if err != nil {
		st = http.StatusInternalServerError
	}
	writeJSON(w, st, resp)
}

func (s *Server) handleCaddyFixPermissions(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadResolvedAppOr404(w, r)
	if !ok {
		return
	}
	c := s.caddyConfig(a)
	root := c.EffectiveRoot()
	out, err := caddy.FixPermissions(r.Context(), caddy.PermissionOptions{
		Root:     root,
		User:     a.CaddyFilesUser,
		Group:    a.CaddyFilesGroup,
		DirMode:  a.CaddyFilesDirMode,
		FileMode: a.CaddyFilesFileMode,
	})
	resp := map[string]any{"ok": err == nil, "output": out, "root": root}
	if err != nil {
		resp["error"] = err.Error()
	}
	st := http.StatusOK
	if err != nil {
		st = http.StatusInternalServerError
	}
	writeJSON(w, st, resp)
}

func (s *Server) handleCaddyReload(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadResolvedAppOr404(w, r)
	if !ok {
		return
	}
	out, err := caddy.Reload(r.Context(), s.caddyConfig(a))
	resp := map[string]any{"ok": err == nil, "output": out}
	if err != nil {
		resp["error"] = err.Error()
	}
	st := http.StatusOK
	if err != nil {
		st = http.StatusInternalServerError
	}
	writeJSON(w, st, resp)
}

// ---------- admin / logs ----------

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	snap := s.cfg.Snapshot()
	limit := snap.MaxLogRows
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	appID := r.URL.Query().Get("app_id")
	entries, err := s.updater.ReadLog(limit, appID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

type appStat struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Runs          int       `json:"runs"`
	Successes     int       `json:"successes"`
	Failures      int       `json:"failures"`
	LastRun       time.Time `json:"last_run,omitempty"`
	LastSuccess   time.Time `json:"last_success,omitempty"`
	LastFailure   time.Time `json:"last_failure,omitempty"`
	AvgDurationMS int64     `json:"avg_duration_ms"`
}

func (s *Server) handleAdminSummary(w http.ResponseWriter, r *http.Request) {
	snap := s.cfg.Snapshot()
	// pull a generous slice of recent log entries for stats
	entries, err := s.updater.ReadLog(10000, "")
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	type acc struct {
		runs       int
		successes  int
		failures   int
		last       time.Time
		lastOK     time.Time
		lastFail   time.Time
		durSumMS   int64
		durSamples int
	}
	stats := map[string]*acc{}
	for _, a := range snap.Apps {
		stats[a.ID] = &acc{}
	}
	now := time.Now()
	last24h := 0
	successes24h := 0
	for _, e := range entries {
		st, exists := stats[e.AppID]
		if !exists {
			st = &acc{}
			stats[e.AppID] = st
		}
		st.runs++
		if e.Success {
			st.successes++
			if e.Time.After(st.lastOK) {
				st.lastOK = e.Time
			}
		} else {
			st.failures++
			if e.Time.After(st.lastFail) {
				st.lastFail = e.Time
			}
		}
		if e.Time.After(st.last) {
			st.last = e.Time
		}
		if d, perr := time.ParseDuration(e.Duration); perr == nil {
			st.durSumMS += d.Milliseconds()
			st.durSamples++
		}
		if now.Sub(e.Time) <= 24*time.Hour {
			last24h++
			if e.Success {
				successes24h++
			}
		}
	}

	out := make([]appStat, 0, len(snap.Apps))
	totalRuns, totalOK, totalFail := 0, 0, 0
	for _, a := range snap.Apps {
		st := stats[a.ID]
		if st == nil {
			st = &acc{}
		}
		avg := int64(0)
		if st.durSamples > 0 {
			avg = st.durSumMS / int64(st.durSamples)
		}
		out = append(out, appStat{
			ID:            a.ID,
			Name:          a.Name,
			Runs:          st.runs,
			Successes:     st.successes,
			Failures:      st.failures,
			LastRun:       st.last,
			LastSuccess:   st.lastOK,
			LastFailure:   st.lastFail,
			AvgDurationMS: avg,
		})
		totalRuns += st.runs
		totalOK += st.successes
		totalFail += st.failures
	}
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"app_count":     len(snap.Apps),
		"total_runs":    totalRuns,
		"total_success": totalOK,
		"total_failure": totalFail,
		"runs_24h":      last24h,
		"success_24h":   successes24h,
		"per_app":       out,
	})
}
