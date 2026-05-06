package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/s95f/servergitupdater/internal/auth"
	"github.com/s95f/servergitupdater/internal/build"
	"github.com/s95f/servergitupdater/internal/caddy"
	"github.com/s95f/servergitupdater/internal/config"
	"github.com/s95f/servergitupdater/internal/git"
	"github.com/s95f/servergitupdater/internal/service"
	"github.com/s95f/servergitupdater/internal/tmpl"
	"github.com/s95f/servergitupdater/web"
)

type Server struct {
	cfg        *config.Config
	configPath string
	updater    *git.Updater
	log        *slog.Logger
	limiter    *auth.LoginLimiter
}

func New(cfg *config.Config, configPath string, updater *git.Updater, log *slog.Logger) *Server {
	return &Server{
		cfg:        cfg,
		configPath: configPath,
		updater:    updater,
		log:        log,
		limiter:    auth.NewLoginLimiter(8, 15*time.Minute),
	}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	staticFS, err := fs.Sub(web.Files, "static")
	if err != nil {
		s.log.Error("embed static", "err", err)
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	mux.HandleFunc("GET /", s.handleIndex)
	mux.HandleFunc("GET /login", s.handleLoginPage)
	mux.HandleFunc("POST /login", s.handleLoginSubmit)
	mux.HandleFunc("POST /logout", s.handleLogout)

	mux.HandleFunc("GET /api/status", s.requireAuth(s.handleStatus))
	mux.HandleFunc("POST /api/update", s.requireAuthCSRF(s.handleUpdate))
	mux.HandleFunc("GET /api/config", s.requireAuth(s.handleGetConfig))
	mux.HandleFunc("POST /api/config", s.requireAuthCSRF(s.handleSetConfig))
	mux.HandleFunc("POST /api/password", s.requireAuthCSRF(s.handleChangePassword))
	mux.HandleFunc("GET /api/logs", s.requireAuth(s.handleLogs))

	mux.HandleFunc("GET /api/build/detect", s.requireAuth(s.handleBuildDetect))
	mux.HandleFunc("POST /api/build/run", s.requireAuthCSRF(s.handleBuildRun))

	mux.HandleFunc("GET /api/service/status", s.requireAuth(s.handleServiceStatus))
	mux.HandleFunc("GET /api/service/preview", s.requireAuth(s.handleServicePreview))
	mux.HandleFunc("POST /api/service/install", s.requireAuthCSRF(s.handleServiceInstall))
	mux.HandleFunc("POST /api/service/uninstall", s.requireAuthCSRF(s.handleServiceUninstall))
	mux.HandleFunc("POST /api/service/restart", s.requireAuthCSRF(s.handleServiceRestart))

	mux.HandleFunc("GET /api/caddy/status", s.requireAuth(s.handleCaddyStatus))
	mux.HandleFunc("GET /api/caddy/preview", s.requireAuth(s.handleCaddyPreview))
	mux.HandleFunc("POST /api/caddy/apply", s.requireAuthCSRF(s.handleCaddyApply))
	mux.HandleFunc("POST /api/caddy/remove", s.requireAuthCSRF(s.handleCaddyRemove))
	mux.HandleFunc("POST /api/caddy/reload", s.requireAuthCSRF(s.handleCaddyReload))

	return s.securityHeaders(mux)
}

// ---------- middleware ----------

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy",
			"default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' data:; "+
				"connect-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) authedUser(r *http.Request) string {
	c, err := r.Cookie(auth.SessionCookie)
	if err != nil {
		return ""
	}
	user, err := auth.ParseSessionToken(s.cfg.Snapshot().SessionSecret, c.Value)
	if err != nil {
		return ""
	}
	return user
}

func (s *Server) requireAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.authedUser(r) == "" {
			writeJSONError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		h(w, r)
	}
}

func (s *Server) requireAuthCSRF(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.authedUser(r) == "" {
			writeJSONError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		csrfCookie, err := r.Cookie(auth.CSRFCookie)
		if err != nil {
			writeJSONError(w, http.StatusForbidden, "missing csrf cookie")
			return
		}
		header := r.Header.Get(auth.CSRFHeader)
		if header == "" || subtle.ConstantTimeCompare([]byte(header), []byte(csrfCookie.Value)) != 1 {
			writeJSONError(w, http.StatusForbidden, "csrf mismatch")
			return
		}
		h(w, r)
	}
}

// ---------- pages ----------

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if s.authedUser(r) == "" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	servePage(w, "dashboard.html")
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if s.authedUser(r) != "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	servePage(w, "login.html")
}

func servePage(w http.ResponseWriter, name string) {
	data, err := web.Files.ReadFile("static/" + name)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

// ---------- auth handlers ----------

type loginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	ip := auth.ClientIP(r)
	if !s.limiter.Allow(ip) {
		writeJSONError(w, http.StatusTooManyRequests, "too many attempts; try again later")
		return
	}

	var req loginReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad request")
		return
	}

	snap := s.cfg.Snapshot()
	userMatch := subtle.ConstantTimeCompare([]byte(req.Username), []byte(snap.Username)) == 1
	passMatch := snap.PasswordHash != "" && auth.VerifyPassword(snap.PasswordHash, req.Password)
	if !userMatch || !passMatch {
		s.limiter.Record(ip, false)
		writeJSONError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	s.limiter.Record(ip, true)

	ttl := time.Duration(snap.SessionTTLHours) * time.Hour
	tok, err := auth.MakeSessionToken(snap.SessionSecret, snap.Username, ttl)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "session error")
		return
	}
	csrf := auth.NewCSRFToken()

	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookie,
		Value:    tok,
		Path:     "/",
		HttpOnly: true,
		Secure:   snap.CookieSecure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(ttl.Seconds()),
	})
	http.SetCookie(w, &http.Cookie{
		Name:     auth.CSRFCookie,
		Value:    csrf,
		Path:     "/",
		HttpOnly: false, // JS reads it for the X-CSRF-Token header
		Secure:   snap.CookieSecure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(ttl.Seconds()),
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	for _, name := range []string{auth.SessionCookie, auth.CSRFCookie} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			HttpOnly: name == auth.SessionCookie,
			SameSite: http.SameSiteStrictMode,
			MaxAge:   -1,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------- API handlers ----------

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	snap := s.cfg.Snapshot()
	st, err := s.updater.Status(r.Context())
	resp := map[string]any{
		"username":            snap.Username,
		"repo_path":           snap.RepoPath,
		"branch":              snap.Branch,
		"remote":              snap.Remote,
		"auto_update_enabled": snap.AutoUpdateEnabled,
		"auto_update_minutes": snap.AutoUpdateMinutes,
		"git":                 st,
	}
	if err != nil {
		resp["git_error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	entry, err := s.updater.RunUpdate(r.Context(), "manual")
	status := http.StatusOK
	if err != nil {
		status = http.StatusInternalServerError
	}
	writeJSON(w, status, map[string]any{
		"ok":    err == nil,
		"entry": entry,
	})
}

type configPayload struct {
	RepoPath          string   `json:"repo_path"`
	Branch            string   `json:"branch"`
	Remote            string   `json:"remote"`
	PostUpdateCommand string   `json:"post_update_command"`
	PostUpdateArgs    []string `json:"post_update_args"`
	AutoUpdateEnabled bool     `json:"auto_update_enabled"`
	AutoUpdateMinutes int      `json:"auto_update_minutes"`

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
	CaddyDomain        string   `json:"caddy_domain"`
	CaddyUpstream      string   `json:"caddy_upstream"`
	CaddyExtra         string   `json:"caddy_extra"`
	CaddySnippetDir    string   `json:"caddy_snippet_dir"`
	CaddySnippetName   string   `json:"caddy_snippet_name"`
	CaddyReloadCommand []string `json:"caddy_reload_command"`
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	snap := s.cfg.Snapshot()
	writeJSON(w, http.StatusOK, configPayload{
		RepoPath:             snap.RepoPath,
		Branch:               snap.Branch,
		Remote:               snap.Remote,
		PostUpdateCommand:    snap.PostUpdateCommand,
		PostUpdateArgs:       snap.PostUpdateArgs,
		AutoUpdateEnabled:    snap.AutoUpdateEnabled,
		AutoUpdateMinutes:    snap.AutoUpdateMinutes,
		AutoBuildEnabled:     snap.AutoBuildEnabled,
		BuildCommand:         snap.BuildCommand,
		BuildArgs:            snap.BuildArgs,
		AppPort:              snap.AppPort,
		PortFlag:             snap.PortFlag,
		ServiceManageEnabled: snap.ServiceManageEnabled,
		ServiceName:          snap.ServiceName,
		ServiceScope:         snap.ServiceScope,
		ServiceDescription:   snap.ServiceDescription,
		ServiceExecStart:     snap.ServiceExecStart,
		ServiceExecArgs:      snap.ServiceExecArgs,
		ServiceWorkingDir:    snap.ServiceWorkingDir,
		ServiceUser:          snap.ServiceUser,
		ServiceEnv:           snap.ServiceEnv,
		ServiceRestart:       snap.ServiceRestart,
		CaddyEnabled:         snap.CaddyEnabled,
		CaddyAutoApply:       snap.CaddyAutoApply,
		CaddyDomain:          snap.CaddyDomain,
		CaddyUpstream:        snap.CaddyUpstream,
		CaddyExtra:           snap.CaddyExtra,
		CaddySnippetDir:      snap.CaddySnippetDir,
		CaddySnippetName:     snap.CaddySnippetName,
		CaddyReloadCommand:   snap.CaddyReloadCommand,
	})
}

func (s *Server) handleSetConfig(w http.ResponseWriter, r *http.Request) {
	var p configPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad request")
		return
	}
	p.Branch = strings.TrimSpace(p.Branch)
	p.Remote = strings.TrimSpace(p.Remote)
	p.RepoPath = strings.TrimSpace(p.RepoPath)
	if p.Branch == "" {
		p.Branch = "main"
	}
	if p.Remote == "" {
		p.Remote = "origin"
	}
	if p.AutoUpdateMinutes <= 0 {
		p.AutoUpdateMinutes = 15
	}

	scope := strings.TrimSpace(p.ServiceScope)
	if scope == "" {
		scope = "system"
	}
	if scope != "system" && scope != "user" {
		writeJSONError(w, http.StatusBadRequest, "service_scope must be 'system' or 'user'")
		return
	}
	restart := strings.TrimSpace(p.ServiceRestart)
	if restart == "" {
		restart = "on-failure"
	}

	portFlag := strings.TrimSpace(p.PortFlag)
	if portFlag == "" {
		portFlag = "-port"
	}
	if p.AppPort < 0 || p.AppPort > 65535 {
		writeJSONError(w, http.StatusBadRequest, "app_port must be between 0 and 65535")
		return
	}
	caddyReload := p.CaddyReloadCommand
	if len(caddyReload) == 0 {
		caddyReload = []string{"systemctl", "reload", "caddy"}
	}

	if err := s.cfg.Update(s.configPath, func(c *config.Snapshot) {
		c.RepoPath = p.RepoPath
		c.Branch = p.Branch
		c.Remote = p.Remote
		c.PostUpdateCommand = p.PostUpdateCommand
		c.PostUpdateArgs = p.PostUpdateArgs
		c.AutoUpdateEnabled = p.AutoUpdateEnabled
		c.AutoUpdateMinutes = p.AutoUpdateMinutes
		c.AutoBuildEnabled = p.AutoBuildEnabled
		c.BuildCommand = strings.TrimSpace(p.BuildCommand)
		c.BuildArgs = p.BuildArgs
		c.AppPort = p.AppPort
		c.PortFlag = portFlag
		c.ServiceManageEnabled = p.ServiceManageEnabled
		c.ServiceName = strings.TrimSpace(p.ServiceName)
		c.ServiceScope = scope
		c.ServiceDescription = p.ServiceDescription
		c.ServiceExecStart = strings.TrimSpace(p.ServiceExecStart)
		c.ServiceExecArgs = p.ServiceExecArgs
		c.ServiceWorkingDir = strings.TrimSpace(p.ServiceWorkingDir)
		c.ServiceUser = strings.TrimSpace(p.ServiceUser)
		c.ServiceEnv = p.ServiceEnv
		c.ServiceRestart = restart
		c.CaddyEnabled = p.CaddyEnabled
		c.CaddyAutoApply = p.CaddyAutoApply
		c.CaddyDomain = strings.TrimSpace(p.CaddyDomain)
		c.CaddyUpstream = strings.TrimSpace(p.CaddyUpstream)
		c.CaddyExtra = p.CaddyExtra
		c.CaddySnippetDir = strings.TrimSpace(p.CaddySnippetDir)
		c.CaddySnippetName = strings.TrimSpace(p.CaddySnippetName)
		c.CaddyReloadCommand = caddyReload
	}); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "save failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------- build endpoints ----------

func (s *Server) handleBuildDetect(w http.ResponseWriter, r *http.Request) {
	snap := s.cfg.Snapshot()
	d := build.Detect(snap.RepoPath)
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) handleBuildRun(w http.ResponseWriter, r *http.Request) {
	snap := s.cfg.Snapshot()
	vars := tmpl.Vars{Port: snap.AppPort, RepoPath: snap.RepoPath, Name: snap.ServiceName}
	out, err := build.Run(r.Context(), snap.RepoPath, snap.BuildCommand, snap.BuildArgs, snap.GitEnv, snap.AutoBuildEnabled, vars)
	resp := map[string]any{"ok": err == nil, "output": out}
	if err != nil {
		resp["error"] = err.Error()
	}
	status := http.StatusOK
	if err != nil {
		status = http.StatusInternalServerError
	}
	writeJSON(w, status, resp)
}

// ---------- service endpoints ----------

func (s *Server) buildSpec(snap config.Snapshot) (service.Spec, error) {
	scope := service.Scope(snap.ServiceScope)
	if scope == "" {
		scope = service.ScopeSystem
	}
	exec := snap.ServiceExecStart
	if exec == "" {
		// fall back to detected build output
		d := build.Detect(snap.RepoPath)
		if d.SuggestedOutput != "" {
			exec = d.SuggestedOutput
		}
	}
	wd := snap.ServiceWorkingDir
	if wd == "" {
		wd = snap.RepoPath
	}
	desc := snap.ServiceDescription
	if desc == "" && snap.ServiceName != "" {
		desc = "serverGitUpdater-managed: " + snap.ServiceName
	}

	vars := tmpl.Vars{Port: snap.AppPort, RepoPath: snap.RepoPath, Name: snap.ServiceName}
	exec = vars.Apply(exec)
	args := vars.ApplyAll(snap.ServiceExecArgs)
	if snap.AppPort > 0 && !tmpl.HasPort(snap.ServiceExecArgs) {
		flag := snap.PortFlag
		if flag == "" {
			flag = "-port"
		}
		args = append(args, flag, strconv.Itoa(snap.AppPort))
	}

	return service.Spec{
		Name:        snap.ServiceName,
		Scope:       scope,
		Description: desc,
		ExecStart:   exec,
		ExecArgs:    args,
		WorkingDir:  wd,
		User:        snap.ServiceUser,
		Env:         snap.ServiceEnv,
		Restart:     snap.ServiceRestart,
	}, nil
}

func (s *Server) handleServiceStatus(w http.ResponseWriter, r *http.Request) {
	snap := s.cfg.Snapshot()
	if snap.ServiceName == "" {
		writeJSON(w, http.StatusOK, map[string]any{"configured": false})
		return
	}
	scope := service.Scope(snap.ServiceScope)
	if scope == "" {
		scope = service.ScopeSystem
	}
	st := service.ReadStatus(r.Context(), scope, snap.ServiceName)
	writeJSON(w, http.StatusOK, map[string]any{
		"configured": true,
		"status":     st,
	})
}

func (s *Server) handleServicePreview(w http.ResponseWriter, r *http.Request) {
	snap := s.cfg.Snapshot()
	spec, _ := s.buildSpec(snap)
	content, err := service.Render(spec)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	path, _ := service.UnitPath(spec.Scope, spec.Name)
	writeJSON(w, http.StatusOK, map[string]any{
		"unit_path": path,
		"unit":      content,
	})
}

func (s *Server) handleServiceInstall(w http.ResponseWriter, r *http.Request) {
	snap := s.cfg.Snapshot()
	spec, err := s.buildSpec(snap)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	out, err := service.Install(r.Context(), spec)
	resp := map[string]any{"ok": err == nil, "output": out}
	if err != nil {
		resp["error"] = err.Error()
	}
	status := http.StatusOK
	if err != nil {
		status = http.StatusInternalServerError
	}
	writeJSON(w, status, resp)
}

func (s *Server) handleServiceUninstall(w http.ResponseWriter, r *http.Request) {
	snap := s.cfg.Snapshot()
	if snap.ServiceName == "" {
		writeJSONError(w, http.StatusBadRequest, "service_name not set")
		return
	}
	scope := service.Scope(snap.ServiceScope)
	if scope == "" {
		scope = service.ScopeSystem
	}
	out, err := service.Uninstall(r.Context(), scope, snap.ServiceName)
	resp := map[string]any{"ok": err == nil, "output": out}
	if err != nil {
		resp["error"] = err.Error()
	}
	status := http.StatusOK
	if err != nil {
		status = http.StatusInternalServerError
	}
	writeJSON(w, status, resp)
}

func (s *Server) handleServiceRestart(w http.ResponseWriter, r *http.Request) {
	snap := s.cfg.Snapshot()
	if snap.ServiceName == "" {
		writeJSONError(w, http.StatusBadRequest, "service_name not set")
		return
	}
	scope := service.Scope(snap.ServiceScope)
	if scope == "" {
		scope = service.ScopeSystem
	}
	out, err := service.Restart(r.Context(), scope, snap.ServiceName)
	resp := map[string]any{"ok": err == nil, "output": out}
	if err != nil {
		resp["error"] = err.Error()
	}
	status := http.StatusOK
	if err != nil {
		status = http.StatusInternalServerError
	}
	writeJSON(w, status, resp)
}

type passwordReq struct {
	Current string `json:"current"`
	New     string `json:"new"`
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var req passwordReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad request")
		return
	}
	snap := s.cfg.Snapshot()
	if !auth.VerifyPassword(snap.PasswordHash, req.Current) {
		writeJSONError(w, http.StatusUnauthorized, "current password incorrect")
		return
	}
	hash, err := auth.HashPassword(req.New)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.cfg.Update(s.configPath, func(c *config.Snapshot) {
		c.PasswordHash = hash
	}); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "save failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	snap := s.cfg.Snapshot()
	entries, err := s.updater.ReadLog(snap.MaxLogRows)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

// ---------- caddy endpoints ----------

func (s *Server) caddyConfig(snap config.Snapshot) caddy.Config {
	return caddy.Config{
		Enabled:       snap.CaddyEnabled,
		Domain:        snap.CaddyDomain,
		Upstream:      snap.CaddyUpstream,
		Extra:         snap.CaddyExtra,
		SnippetDir:    snap.CaddySnippetDir,
		SnippetName:   snap.CaddySnippetName,
		ReloadCommand: snap.CaddyReloadCommand,
		Vars:          tmpl.Vars{Port: snap.AppPort, RepoPath: snap.RepoPath, Name: snap.ServiceName},
	}
}

func (s *Server) handleCaddyStatus(w http.ResponseWriter, r *http.Request) {
	snap := s.cfg.Snapshot()
	if !snap.CaddyEnabled || snap.CaddyDomain == "" || (snap.CaddySnippetName == "" && snap.ServiceName == "") {
		writeJSON(w, http.StatusOK, map[string]any{"configured": false})
		return
	}
	st := caddy.ReadStatus(s.caddyConfig(snap))
	writeJSON(w, http.StatusOK, map[string]any{"configured": true, "status": st})
}

func (s *Server) handleCaddyPreview(w http.ResponseWriter, r *http.Request) {
	snap := s.cfg.Snapshot()
	c := s.caddyConfig(snap)
	content, err := c.Render()
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	st := caddy.ReadStatus(c)
	writeJSON(w, http.StatusOK, map[string]any{
		"snippet_path": st.SnippetPath,
		"snippet":      content,
	})
}

func (s *Server) handleCaddyApply(w http.ResponseWriter, r *http.Request) {
	snap := s.cfg.Snapshot()
	out, err := caddy.Apply(r.Context(), s.caddyConfig(snap))
	resp := map[string]any{"ok": err == nil, "output": out}
	if err != nil {
		resp["error"] = err.Error()
	}
	status := http.StatusOK
	if err != nil {
		status = http.StatusInternalServerError
	}
	writeJSON(w, status, resp)
}

func (s *Server) handleCaddyRemove(w http.ResponseWriter, r *http.Request) {
	snap := s.cfg.Snapshot()
	out, err := caddy.Remove(r.Context(), s.caddyConfig(snap))
	resp := map[string]any{"ok": err == nil, "output": out}
	if err != nil {
		resp["error"] = err.Error()
	}
	status := http.StatusOK
	if err != nil {
		status = http.StatusInternalServerError
	}
	writeJSON(w, status, resp)
}

func (s *Server) handleCaddyReload(w http.ResponseWriter, r *http.Request) {
	snap := s.cfg.Snapshot()
	out, err := caddy.Reload(r.Context(), s.caddyConfig(snap))
	resp := map[string]any{"ok": err == nil, "output": out}
	if err != nil {
		resp["error"] = err.Error()
	}
	status := http.StatusOK
	if err != nil {
		status = http.StatusInternalServerError
	}
	writeJSON(w, status, resp)
}

// ---------- helpers ----------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}
