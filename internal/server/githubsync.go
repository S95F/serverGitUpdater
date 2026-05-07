package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/s95f/servergitupdater/internal/config"
	"github.com/s95f/servergitupdater/internal/githubhook"
)

// SyncWebhook reconciles the GitHub webhook for one app to match its
// WebhookEnabled / WebhookAutoRegister / WebhookSecret state. Best
// effort: errors are returned and logged but never fail the request
// they were triggered from. Idempotent — calling it twice does nothing
// the second time.
func (s *Server) SyncWebhook(ctx context.Context, appID string) (string, error) {
	snap := s.cfg.Snapshot()
	app, ok := snap.FindApp(appID)
	if !ok {
		return "", fmt.Errorf("app not found: %s", appID)
	}

	// If auto-register is off and we still hold a remote ID, leave it
	// alone — the user opted out of management; they may manage the
	// hook by hand. To clear the remote we instead use Sync with
	// auto-register on + enabled off.
	if !app.WebhookAutoRegister {
		return "auto-register is off; nothing to do", nil
	}
	if snap.PublicBaseURL == "" {
		return "", errors.New("public_base_url is empty (set it in Settings → Server configuration)")
	}
	if snap.GitHubToken == "" {
		return "", errors.New("github_token is empty (set it in Settings → Server configuration)")
	}
	if app.CloneURL == "" {
		return "", errors.New("clone_url is empty; can't determine the repo")
	}
	owner, repo, ok := githubhook.ParseURL(app.CloneURL)
	if !ok {
		return "", fmt.Errorf("clone_url %q is not a recognised GitHub URL (only github.com is supported)", app.CloneURL)
	}
	if app.WebhookEnabled && app.WebhookSecret == "" {
		return "", errors.New("webhook_secret is empty; click Generate new secret first")
	}

	hookURL := strings.TrimRight(snap.PublicBaseURL, "/") + "/api/apps/" + app.ID + "/webhook"
	payload := githubhook.Payload{
		Name:   "web",
		Active: app.WebhookEnabled,
		Events: []string{"push"},
		Config: githubhook.Config{
			URL:         hookURL,
			ContentType: "json",
			Secret:      app.WebhookSecret,
		},
	}

	client := githubhook.NewClient()

	// Case A: webhook is disabled. Remove any existing remote registration
	// so the user doesn't see a dead hook on GitHub still pointing here.
	if !app.WebhookEnabled {
		if app.WebhookRemoteID == 0 {
			return "webhook disabled; no remote hook to remove", nil
		}
		err := client.Delete(ctx, snap.GitHubToken, owner, repo, app.WebhookRemoteID)
		// Even on 404 we proceed: the local id is stale, clear it.
		if err != nil && !errors.Is(err, githubhook.ErrNotFound) {
			return "", err
		}
		_, _ = s.cfg.UpdateApp(s.configPath, app.ID, func(a *config.App) {
			a.WebhookRemoteID = 0
		})
		return fmt.Sprintf("removed webhook from %s/%s", owner, repo), nil
	}

	// Case B: webhook is enabled and we already have an ID — update it.
	if app.WebhookRemoteID != 0 {
		_, err := client.Update(ctx, snap.GitHubToken, owner, repo, app.WebhookRemoteID, payload)
		if err == nil {
			return fmt.Sprintf("updated hook %d on %s/%s", app.WebhookRemoteID, owner, repo), nil
		}
		if !errors.Is(err, githubhook.ErrNotFound) {
			return "", err
		}
		// Stale id — fall through to create.
	}

	// Case C: enabled and no remote ID — create.
	h, err := client.Create(ctx, snap.GitHubToken, owner, repo, payload)
	if err != nil {
		return "", err
	}
	if _, uerr := s.cfg.UpdateApp(s.configPath, app.ID, func(a *config.App) {
		a.WebhookRemoteID = h.ID
	}); uerr != nil {
		return "", fmt.Errorf("created hook %d but couldn't persist id: %w", h.ID, uerr)
	}
	return fmt.Sprintf("created hook %d on %s/%s", h.ID, owner, repo), nil
}

// ReconcileWebhooks runs SyncWebhook for every app whose
// WebhookAutoRegister is on. Used at server startup. Best-effort,
// errors are logged.
func (s *Server) ReconcileWebhooks(ctx context.Context) {
	snap := s.cfg.Snapshot()
	if snap.GitHubToken == "" || snap.PublicBaseURL == "" {
		return
	}
	for _, app := range snap.Apps {
		if !app.WebhookAutoRegister {
			continue
		}
		a := app
		go func() {
			cctx, cancel := context.WithTimeout(ctx, 1*time.Minute)
			defer cancel()
			out, err := s.SyncWebhook(cctx, a.ID)
			if err != nil {
				s.log.Warn("webhook reconcile failed", "app", a.Name, "err", err)
				return
			}
			s.log.Info("webhook reconciled", "app", a.Name, "result", out)
		}()
	}
}

// HTTP handler for the manual "Sync now" button.
func (s *Server) handleWebhookSync(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadAppOr404(w, r)
	if !ok {
		return
	}
	out, err := s.SyncWebhook(r.Context(), a.ID)
	resp := map[string]any{"ok": err == nil, "message": out}
	if err != nil {
		resp["error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}
