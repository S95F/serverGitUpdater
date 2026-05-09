package server

import (
	"crypto/subtle"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/s95f/servergitupdater/internal/auth"
	"github.com/s95f/servergitupdater/internal/config"
	"github.com/s95f/servergitupdater/internal/git"
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
	// no-store on /static/* so a binary upgrade is reflected in the browser
	// immediately. Without this, http.FileServer never sets Cache-Control
	// and embed.FS has no useful Last-Modified, so browsers happily reuse
	// stale settings.html / nav.js / style.css across upgrades.
	mux.Handle("GET /static/", http.StripPrefix("/static/", noStore(http.FileServer(http.FS(staticFS)))))
	mux.HandleFunc("GET /favicon.ico", s.handleFavicon)
	mux.HandleFunc("GET /favicon.svg", s.handleFavicon)

	// pages
	mux.HandleFunc("GET /", s.handleIndex)
	mux.HandleFunc("GET /apps/{id}", s.handleAppPage)
	mux.HandleFunc("GET /settings", s.handleSettingsPage)
	mux.HandleFunc("GET /admin", s.handleAdminPage)
	mux.HandleFunc("GET /login", s.handleLoginPage)
	mux.HandleFunc("POST /login", s.handleLoginSubmit)
	mux.HandleFunc("POST /logout", s.handleLogout)

	// account
	mux.HandleFunc("GET /api/me", s.requireAuth(s.handleMe))
	mux.HandleFunc("POST /api/password", s.requireAuthCSRF(s.handleChangePassword))

	// server-wide settings
	mux.HandleFunc("GET /api/server", s.requireAuth(s.handleGetServer))
	mux.HandleFunc("POST /api/server", s.requireAuthCSRF(s.handleSetServer))
	mux.HandleFunc("POST /api/server/restart", s.requireAuthCSRF(s.handleServerRestart))

	// apps CRUD
	mux.HandleFunc("GET /api/apps", s.requireAuth(s.handleListApps))
	mux.HandleFunc("POST /api/apps", s.requireAuthCSRF(s.handleCreateApp))
	mux.HandleFunc("GET /api/apps/{id}", s.requireAuth(s.handleGetApp))
	mux.HandleFunc("POST /api/apps/{id}", s.requireAuthCSRF(s.handleUpdateApp))
	mux.HandleFunc("DELETE /api/apps/{id}", s.requireAuthCSRF(s.handleDeleteApp))

	// per-app pipeline actions
	mux.HandleFunc("POST /api/apps/{id}/update", s.requireAuthCSRF(s.handleUpdateNow))
	mux.HandleFunc("POST /api/apps/{id}/clone", s.requireAuthCSRF(s.handleCloneApp))
	mux.HandleFunc("GET /api/apps/{id}/status", s.requireAuth(s.handleStatus))
	mux.HandleFunc("GET /api/apps/{id}/build/detect", s.requireAuth(s.handleBuildDetect))
	mux.HandleFunc("POST /api/apps/{id}/build/run", s.requireAuthCSRF(s.handleBuildRun))
	mux.HandleFunc("GET /api/apps/{id}/service/status", s.requireAuth(s.handleServiceStatus))
	mux.HandleFunc("GET /api/apps/{id}/service/preview", s.requireAuth(s.handleServicePreview))
	mux.HandleFunc("POST /api/apps/{id}/service/install", s.requireAuthCSRF(s.handleServiceInstall))
	mux.HandleFunc("POST /api/apps/{id}/service/uninstall", s.requireAuthCSRF(s.handleServiceUninstall))
	mux.HandleFunc("POST /api/apps/{id}/service/restart", s.requireAuthCSRF(s.handleServiceRestart))
	mux.HandleFunc("GET /api/apps/{id}/caddy/status", s.requireAuth(s.handleCaddyStatus))
	mux.HandleFunc("GET /api/apps/{id}/caddy/preview", s.requireAuth(s.handleCaddyPreview))
	mux.HandleFunc("POST /api/apps/{id}/caddy/apply", s.requireAuthCSRF(s.handleCaddyApply))
	mux.HandleFunc("POST /api/apps/{id}/caddy/remove", s.requireAuthCSRF(s.handleCaddyRemove))
	mux.HandleFunc("POST /api/apps/{id}/caddy/reload", s.requireAuthCSRF(s.handleCaddyReload))
	mux.HandleFunc("POST /api/apps/{id}/caddy/fix-permissions", s.requireAuthCSRF(s.handleCaddyFixPermissions))

	// repo-file import (auth) and inbound webhook (public, HMAC-verified)
	mux.HandleFunc("GET /api/apps/{id}/repo-file", s.requireAuth(s.handlePreviewConfig))
	mux.HandleFunc("POST /api/apps/{id}/repo-file/import", s.requireAuthCSRF(s.handleImportConfig))
	mux.HandleFunc("POST /api/apps/{id}/webhook/regenerate", s.requireAuthCSRF(s.handleWebhookRegen))
	mux.HandleFunc("POST /api/apps/{id}/webhook/sync", s.requireAuthCSRF(s.handleWebhookSync))
	mux.HandleFunc("POST /api/apps/{id}/webhook", s.handleWebhook)

	// admin
	mux.HandleFunc("GET /api/admin/summary", s.requireAuth(s.handleAdminSummary))
	mux.HandleFunc("GET /api/logs", s.requireAuth(s.handleLogs))

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
	servePage(w, "index.html")
}

func (s *Server) handleAppPage(w http.ResponseWriter, r *http.Request) {
	if s.authedUser(r) == "" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	servePage(w, "app.html")
}

func (s *Server) handleSettingsPage(w http.ResponseWriter, r *http.Request) {
	if s.authedUser(r) == "" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	servePage(w, "settings.html")
}

func (s *Server) handleAdminPage(w http.ResponseWriter, r *http.Request) {
	if s.authedUser(r) == "" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	servePage(w, "admin.html")
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if s.authedUser(r) != "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	servePage(w, "login.html")
}

// noStore wraps an http.Handler and adds Cache-Control: no-store so
// browsers always re-fetch the underlying asset. Used for /static/*
// because the embedded files have a fixed (epoch) mod time, so
// conditional GETs would always 304 and never pick up new releases.
func noStore(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		h.ServeHTTP(w, r)
	})
}

func (s *Server) handleFavicon(w http.ResponseWriter, r *http.Request) {
	data, err := web.Files.ReadFile("static/favicon.svg")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(data)
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
		Name: auth.SessionCookie, Value: tok, Path: "/",
		HttpOnly: true, Secure: snap.CookieSecure, SameSite: http.SameSiteStrictMode,
		MaxAge: int(ttl.Seconds()),
	})
	http.SetCookie(w, &http.Cookie{
		Name: auth.CSRFCookie, Value: csrf, Path: "/",
		HttpOnly: false, Secure: snap.CookieSecure, SameSite: http.SameSiteStrictMode,
		MaxAge: int(ttl.Seconds()),
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	for _, name := range []string{auth.SessionCookie, auth.CSRFCookie} {
		http.SetCookie(w, &http.Cookie{
			Name: name, Value: "", Path: "/",
			HttpOnly: name == auth.SessionCookie, SameSite: http.SameSiteStrictMode,
			MaxAge: -1,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	snap := s.cfg.Snapshot()
	writeJSON(w, http.StatusOK, map[string]any{"username": snap.Username})
}

type passwordReq struct {
	Current string `json:"current"`
	New     string `json:"new"`
}

// serverPayload is the editable subset of Snapshot exposed via /api/server.
// session_secret, username and password_hash are intentionally omitted.
type serverPayload struct {
	ListenAddr       string `json:"listen_addr"`
	TLSCertFile      string `json:"tls_cert_file"`
	TLSKeyFile       string `json:"tls_key_file"`
	SessionTTLHours  int    `json:"session_ttl_hours"`
	CookieSecure     bool   `json:"cookie_secure"`
	ReposDir         string `json:"repos_dir"`
	PublicBaseURL    string `json:"public_base_url"`
	LogPath          string `json:"log_path"`
	MaxLogRows       int    `json:"max_log_rows"`
	GitHubToken      string `json:"github_token,omitempty"`       // write-only; never echoed back
	GitHubTokenIsSet bool   `json:"github_token_is_set,omitempty"` // read-only flag
}

func (s *Server) handleGetServer(w http.ResponseWriter, r *http.Request) {
	snap := s.cfg.Snapshot()
	writeJSON(w, http.StatusOK, serverPayload{
		ListenAddr:       snap.ListenAddr,
		TLSCertFile:      snap.TLSCertFile,
		TLSKeyFile:       snap.TLSKeyFile,
		SessionTTLHours:  snap.SessionTTLHours,
		CookieSecure:     snap.CookieSecure,
		ReposDir:         snap.ReposDir,
		PublicBaseURL:    snap.PublicBaseURL,
		LogPath:          snap.LogPath,
		MaxLogRows:       snap.MaxLogRows,
		GitHubTokenIsSet: snap.GitHubToken != "",
	})
}

func (s *Server) handleSetServer(w http.ResponseWriter, r *http.Request) {
	var p serverPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad request")
		return
	}
	if p.SessionTTLHours <= 0 {
		p.SessionTTLHours = 12
	}
	if p.MaxLogRows < 0 {
		writeJSONError(w, http.StatusBadRequest, "max_log_rows must be >= 0")
		return
	}
	if (p.TLSCertFile == "") != (p.TLSKeyFile == "") {
		writeJSONError(w, http.StatusBadRequest, "tls_cert_file and tls_key_file must both be set or both empty")
		return
	}
	if err := s.cfg.Update(s.configPath, func(c *config.Snapshot) {
		c.ListenAddr = strings.TrimSpace(p.ListenAddr)
		c.TLSCertFile = strings.TrimSpace(p.TLSCertFile)
		c.TLSKeyFile = strings.TrimSpace(p.TLSKeyFile)
		c.SessionTTLHours = p.SessionTTLHours
		c.CookieSecure = p.CookieSecure
		c.ReposDir = strings.TrimSpace(p.ReposDir)
		c.PublicBaseURL = strings.TrimSpace(p.PublicBaseURL)
		c.LogPath = strings.TrimSpace(p.LogPath)
		c.MaxLogRows = p.MaxLogRows
		// GitHub token: empty means "leave existing" so the UI doesn't
		// have to round-trip a sensitive secret through the form.
		if t := strings.TrimSpace(p.GitHubToken); t != "" {
			c.GitHubToken = t
		}
	}); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "save failed")
		return
	}
	// surface which fields require a restart so the UI can warn the user
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":               true,
		"requires_restart": []string{"listen_addr", "tls_cert_file", "tls_key_file"},
	})
}

// handleServerRestart triggers a graceful self-shutdown of the daemon.
// systemd (or any process supervisor configured with Restart=always)
// is expected to bring the binary back up. The response is flushed
// before the SIGTERM is sent so the caller always sees the 200.
func (s *Server) handleServerRestart(w http.ResponseWriter, r *http.Request) {
	managedBySystemd := os.Getenv("INVOCATION_ID") != ""
	resp := map[string]any{
		"ok":                  true,
		"managed_by_systemd":  managedBySystemd,
		"message":             "restart requested; sending SIGTERM in 200ms",
	}
	if !managedBySystemd {
		resp["warning"] = "no INVOCATION_ID env var; the process will exit but nothing will bring it back automatically. Run under systemd (Restart=always) or supervisord to get auto-restart."
	}
	writeJSON(w, http.StatusOK, resp)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		s.log.Info("self-restart requested via /api/server/restart", "managed_by_systemd", managedBySystemd)
		_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
	}()
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

// ---------- helpers ----------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

func trimAll(ss []string) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	return out
}
