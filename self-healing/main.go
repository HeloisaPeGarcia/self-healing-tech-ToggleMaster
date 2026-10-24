package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	log := newLogger()

	token := os.Getenv("WEBHOOK_TOKEN")
	if token == "" {
		log.Error("required_configuration_missing", "message", "WEBHOOK_TOKEN não configurado")
		os.Exit(1)
	}

	cfg, err := loadConfig(getenv("CONFIG_PATH", "/etc/self-healing/config.json"))
	if err != nil {
		log.Error("config_load_failed", "error", err.Error())
		os.Exit(1)
	}

	dryRun := os.Getenv("DRY_RUN") == "true"

	var restarter Restarter
	if dryRun {
		restarter = dryRunRestarter{log: log}
	} else {
		restarter, err = newInClusterRestarter()
		if err != nil {
			log.Error("kubernetes_client_failed", "error", err.Error())
			os.Exit(1)
		}
	}

	srv := &Server{
		cfg:       cfg,
		token:     token,
		restarter: restarter,
		cooldown:  newCooldown(time.Duration(cfg.CooldownSeconds) * time.Second),
		log:       log,
		dryRun:    dryRun,
	}

	httpServer := &http.Server{
		Addr:              ":" + getenv("PORT", "8090"),
		Handler:           srv.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      20 * time.Second,
	}

	log.Info("service_started",
		"addr", httpServer.Addr,
		"namespace", cfg.Namespace,
		"cooldown_seconds", cfg.CooldownSeconds,
		"rules", len(cfg.Rules),
		"dry_run", dryRun)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(ctx)
	}()

	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("http_server_failed", "error", err.Error())
		os.Exit(1)
	}
}
