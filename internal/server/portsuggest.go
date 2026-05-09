package server

import (
	"net"
	"net/http"
	"strconv"

	"github.com/s95f/servergitupdater/internal/portscan"
)

// handleSuggestPort returns a random TCP port that's free to bind right
// now and unlikely to collide with anything important: other managed
// apps' ports, the updater's own listen port, the kernel's ephemeral
// range, and a list of well-known service ports.
//
// Optional query string:
//   exclude_app=<id>   skip this app's currently-saved port when
//                      building the avoid list — handy when re-rolling
//                      a port for an app that already has one.
func (s *Server) handleSuggestPort(w http.ResponseWriter, r *http.Request) {
	excludeApp := r.URL.Query().Get("exclude_app")
	snap := s.cfg.Snapshot()

	avoid := make(map[int]string)
	for _, a := range snap.Apps {
		if a.AppPort <= 0 {
			continue
		}
		if a.ID == excludeApp {
			continue
		}
		avoid[a.AppPort] = "app:" + a.Name
	}
	if _, p, err := net.SplitHostPort(snap.ListenAddr); err == nil {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			avoid[n] = "serverGitUpdater"
		}
	}

	port, err := portscan.Suggest(portscan.Options{Avoid: avoid})
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"port": port,
	})
}
