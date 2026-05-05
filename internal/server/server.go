package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
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
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	snap := s.cfg.Snapshot()
	writeJSON(w, http.StatusOK, configPayload{
		RepoPath:          snap.RepoPath,
		Branch:            snap.Branch,
		Remote:            snap.Remote,
		PostUpdateCommand: snap.PostUpdateCommand,
		PostUpdateArgs:    snap.PostUpdateArgs,
		AutoUpdateEnabled: snap.AutoUpdateEnabled,
		AutoUpdateMinutes: snap.AutoUpdateMinutes,
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

	if err := s.cfg.Update(s.configPath, func(c *config.Snapshot) {
		c.RepoPath = p.RepoPath
		c.Branch = p.Branch
		c.Remote = p.Remote
		c.PostUpdateCommand = p.PostUpdateCommand
		c.PostUpdateArgs = p.PostUpdateArgs
		c.AutoUpdateEnabled = p.AutoUpdateEnabled
		c.AutoUpdateMinutes = p.AutoUpdateMinutes
	}); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "save failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
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

// ---------- helpers ----------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}
