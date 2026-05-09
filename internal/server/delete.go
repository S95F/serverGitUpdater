package server

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/s95f/servergitupdater/internal/caddy"
	"github.com/s95f/servergitupdater/internal/config"
	"github.com/s95f/servergitupdater/internal/githubhook"
	"github.com/s95f/servergitupdater/internal/service"
	"github.com/s95f/servergitupdater/internal/tmpl"
)

// cascadingDelete removes everything an app touched (Caddy snippet,
// systemd unit, GitHub webhook, working-copy directory) and finally
// drops the app from the config. Each step is best-effort and reported
// individually so the caller can see what worked and what didn't.
//
// Each piece can be opted out of via query string: keep_caddy,
// keep_service, keep_webhook, keep_repo (any value triggers "keep").
func (s *Server) cascadingDelete(ctx context.Context, app config.App, snap config.Snapshot, opts deleteOpts) map[string]any {
	cleanup := map[string]any{}

	// --- 1. Caddy snippet + reload --------------------------------
	if !opts.keepCaddy && app.CaddyEnabled && app.CaddyDomain != "" {
		// Use the same renderer as the per-app handler so name/dir
		// fall back to defaults consistently. We don't have an
		// http.Request here so loadResolvedAppOr404 isn't usable;
		// build the equivalent inline.
		resolved := app
		resolved.RepoPath = app.ResolvedRepoPath(snap.ReposDir)
		c := s.caddyConfig(resolved)
		_ = c.Vars // keep linter happy if Vars unused
		out, err := caddy.Remove(ctx, c)
		cleanup["caddy"] = stepResult(out, err)
	}

	// --- 2. systemd unit -----------------------------------------
	if !opts.keepService && app.ServiceName != "" {
		scope := service.Scope(app.ServiceScope)
		if scope == "" {
			scope = service.ScopeSystem
		}
		out, err := service.Uninstall(ctx, scope, app.ServiceName)
		cleanup["service"] = stepResult(out, err)
	}

	// --- 3. GitHub webhook ---------------------------------------
	if !opts.keepWebhook && app.WebhookAutoRegister && app.WebhookRemoteID != 0 && snap.GitHubToken != "" && app.CloneURL != "" {
		owner, repo, ok := githubhook.ParseURL(app.CloneURL)
		if ok {
			err := githubhook.NewClient().Delete(ctx, snap.GitHubToken, owner, repo, app.WebhookRemoteID)
			if errors.Is(err, githubhook.ErrNotFound) {
				err = nil
			}
			cleanup["github_webhook"] = stepResult("", err)
		} else {
			cleanup["github_webhook"] = map[string]any{
				"ok":    false,
				"error": "clone_url is not a recognised GitHub URL; skipping",
			}
		}
	}

	// --- 4. Repo working copy ------------------------------------
	if !opts.keepRepo {
		resolved := app.ResolvedRepoPath(snap.ReposDir)
		switch {
		case resolved == "":
			cleanup["repo"] = map[string]any{"ok": true, "skipped": "repo_path is empty"}
		case !safeToRemove(snap.ReposDir, resolved):
			cleanup["repo"] = map[string]any{
				"ok":    false,
				"error": "refusing to remove unsafe path: " + resolved,
			}
		default:
			info, err := os.Stat(resolved)
			if err != nil && os.IsNotExist(err) {
				cleanup["repo"] = map[string]any{"ok": true, "skipped": resolved + " does not exist"}
			} else if err != nil {
				cleanup["repo"] = map[string]any{"ok": false, "error": err.Error()}
			} else if !info.IsDir() {
				cleanup["repo"] = map[string]any{"ok": false, "error": resolved + " is not a directory"}
			} else if err := os.RemoveAll(resolved); err != nil {
				cleanup["repo"] = map[string]any{"ok": false, "error": err.Error()}
			} else {
				cleanup["repo"] = map[string]any{"ok": true, "removed": resolved}
			}
		}
	}

	// keep tmpl import live (Vars is ref'd via caddyConfig but we
	// also reference it here so a future inline-build won't surprise)
	_ = tmpl.Vars{}

	return cleanup
}

// safeToRemove is a defense-in-depth check before RemoveAll on a path
// derived from user-supplied config. The resolver already strips ".."
// and leading slashes so a malicious repo_path can't escape repos_dir,
// but we double-check here in case repos_dir itself is empty (or, in
// the future, the resolver changes).
func safeToRemove(reposDir, resolved string) bool {
	cleaned := filepath.Clean(resolved)
	if cleaned == "" || !filepath.IsAbs(cleaned) {
		return false
	}
	forbidden := map[string]struct{}{
		"/":     {},
		"/bin":  {},
		"/boot": {},
		"/dev":  {},
		"/etc":  {},
		"/home": {},
		"/lib":  {},
		"/lib64": {},
		"/media": {},
		"/mnt":  {},
		"/opt":  {},
		"/proc": {},
		"/root": {},
		"/run":  {},
		"/sbin": {},
		"/srv":  {},
		"/sys":  {},
		"/tmp":  {},
		"/usr":  {},
		"/var":  {},
	}
	if _, bad := forbidden[cleaned]; bad {
		return false
	}
	if reposDir != "" {
		rd := filepath.Clean(reposDir)
		if cleaned == rd {
			return false
		}
		return strings.HasPrefix(cleaned+string(os.PathSeparator), rd+string(os.PathSeparator))
	}
	// No repos_dir: require a 3-component path so we don't let a
	// stray /home/foo or /srv/foo through.
	parts := strings.Split(strings.TrimPrefix(cleaned, "/"), "/")
	return len(parts) >= 3
}

type deleteOpts struct {
	keepCaddy   bool
	keepService bool
	keepWebhook bool
	keepRepo    bool
}

func deleteOptsFromQuery(r *http.Request) deleteOpts {
	q := r.URL.Query()
	yes := func(s string) bool {
		v := strings.ToLower(q.Get(s))
		return v == "1" || v == "true" || v == "yes"
	}
	return deleteOpts{
		keepCaddy:   yes("keep_caddy"),
		keepService: yes("keep_service"),
		keepWebhook: yes("keep_webhook"),
		keepRepo:    yes("keep_repo"),
	}
}

func stepResult(output string, err error) map[string]any {
	r := map[string]any{"ok": err == nil}
	if output != "" {
		r["output"] = output
	}
	if err != nil {
		r["error"] = err.Error()
	}
	return r
}
