package server

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/s95f/servergitupdater/internal/config"
	"github.com/s95f/servergitupdater/internal/repofile"
)

// ---------- repo-file import ----------

func (s *Server) handlePreviewConfig(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadResolvedAppOr404(w, r)
	if !ok {
		return
	}
	f, full, err := repofile.Read(a.RepoPath, a.RepoFilePath)
	if errors.Is(err, repofile.ErrNotFound) {
		writeJSON(w, http.StatusOK, map[string]any{
			"exists": false,
			"path":   full,
			"error":  "file not found",
		})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"exists": false,
			"path":   full,
			"error":  err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"exists":   true,
		"path":     full,
		"imported": f,
	})
}

func (s *Server) handleImportConfig(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadResolvedAppOr404(w, r)
	if !ok {
		return
	}
	f, full, err := repofile.Read(a.RepoPath, a.RepoFilePath)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":    false,
			"path":  full,
			"error": err.Error(),
		})
		return
	}
	updated, uerr := s.cfg.UpdateApp(s.configPath, a.ID, func(app *config.App) {
		repofile.Apply(app, f)
	})
	if uerr != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":    false,
			"path":  full,
			"error": uerr.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"path": full,
		"app":  appWithID(updated),
	})
}

// ---------- webhook ----------

// handleWebhookRegen creates a fresh webhook secret for an app.
func (s *Server) handleWebhookRegen(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadAppOr404(w, r)
	if !ok {
		return
	}
	secret, err := newRandomHex(32)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	updated, uerr := s.cfg.UpdateApp(s.configPath, a.ID, func(app *config.App) {
		app.WebhookSecret = secret
	})
	if uerr != nil {
		writeJSONError(w, http.StatusInternalServerError, uerr.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "webhook_secret": updated.WebhookSecret})
}

// handleWebhook receives an inbound HTTP webhook (e.g. from GitHub) and
// triggers a manual-style update if HMAC verifies. NOT under requireAuth:
// public endpoint, secured by the per-app shared secret.
func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !config.IsValidID(id) {
		http.NotFound(w, r)
		return
	}
	snap := s.cfg.Snapshot()
	app, ok := snap.FindApp(id)
	if !ok || !app.WebhookEnabled || app.WebhookSecret == "" {
		// Don't leak which apps exist; same response either way.
		http.NotFound(w, r)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MiB
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	sig := firstNonEmpty(r.Header.Get("X-Hub-Signature-256"), r.Header.Get("X-Webhook-Signature-256"))
	if !verifyHMAC256(app.WebhookSecret, body, sig) {
		s.log.Warn("webhook signature mismatch", "app", app.Name, "ip", clientIP(r))
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return
	}

	// GitHub-style ping event support.
	if r.Header.Get("X-GitHub-Event") == "ping" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "pong": true})
		return
	}

	// Optional branch filter: only act on pushes that match app.Branch.
	if app.WebhookBranchOnly && app.Branch != "" {
		var p struct {
			Ref string `json:"ref"`
		}
		_ = json.Unmarshal(body, &p)
		expected := "refs/heads/" + app.Branch
		if p.Ref != "" && p.Ref != expected {
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":      true,
				"message": "ignored: ref " + p.Ref + " does not match " + expected,
			})
			return
		}
	}

	go func(appID, name string) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if _, err := s.updater.RunUpdate(ctx, appID, "webhook"); err != nil {
			s.log.Warn("webhook update failed", "app", name, "err", err)
		}
	}(app.ID, app.Name)

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "queued": true})
}

func verifyHMAC256(secret string, body []byte, signature string) bool {
	if secret == "" {
		return false
	}
	signature = strings.TrimSpace(signature)
	const prefix = "sha256="
	if !strings.HasPrefix(signature, prefix) {
		return false
	}
	want := strings.TrimPrefix(signature, prefix)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	got := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(got), []byte(want))
}

func newRandomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.Index(xff, ","); i > 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	return r.RemoteAddr
}
