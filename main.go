package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/s95f/servergitupdater/internal/auth"
	"github.com/s95f/servergitupdater/internal/config"
	"github.com/s95f/servergitupdater/internal/git"
	"github.com/s95f/servergitupdater/internal/server"
)

func main() {
	var (
		configPath = flag.String("config", "config.json", "path to config file")
		addr       = flag.String("addr", "", "override listen address (e.g. :8080)")
		initUser   = flag.String("init-user", "", "create or reset the admin user with this username and exit")
		initPass   = flag.String("init-pass", "", "password for --init-user (read from stdin if empty)")
	)
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("load config", "err", err)
		os.Exit(1)
	}

	if *initUser != "" {
		pw := *initPass
		if pw == "" {
			pw, err = auth.ReadPasswordStdin("New password: ")
			if err != nil {
				logger.Error("read password", "err", err)
				os.Exit(1)
			}
		}
		hash, err := auth.HashPassword(pw)
		if err != nil {
			logger.Error("hash password", "err", err)
			os.Exit(1)
		}
		if err := cfg.Update(*configPath, func(s *config.Snapshot) {
			s.Username = *initUser
			s.PasswordHash = hash
		}); err != nil {
			logger.Error("save config", "err", err)
			os.Exit(1)
		}
		logger.Info("admin user written to config", "user", *initUser, "config", *configPath)
		return
	}

	if *addr != "" {
		if err := cfg.Update(*configPath, func(s *config.Snapshot) { s.ListenAddr = *addr }); err != nil {
			logger.Error("save config", "err", err)
			os.Exit(1)
		}
	}

	snap := cfg.Snapshot()
	if snap.PasswordHash == "" {
		logger.Error("no admin user configured; run with --init-user <name> to create one")
		os.Exit(1)
	}

	updater := git.NewUpdater(cfg, logger)
	srv := server.New(cfg, *configPath, updater, logger)

	httpSrv := &http.Server{
		Addr:              snap.ListenAddr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	updater.Start(ctx)

	// Reconcile any per-app GitHub webhooks that opt into auto-registration.
	// Best effort, async — we don't want to block startup on api.github.com.
	srv.ReconcileWebhooks(ctx)

	go func() {
		logger.Info("listening", "addr", snap.ListenAddr, "tls", snap.TLSCertFile != "")
		var err error
		if snap.TLSCertFile != "" && snap.TLSKeyFile != "" {
			err = httpSrv.ListenAndServeTLS(snap.TLSCertFile, snap.TLSKeyFile)
		} else {
			err = httpSrv.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			logger.Error("server", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	updater.Stop()
}
