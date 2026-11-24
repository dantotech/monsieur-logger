// Command monsieur-logger is a configurable JSON log generator used to
// test log pipelines (e.g. Fluent Bit -> Azure Data Explorer) for log loss.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dantotech/monsieur-logger/internal/applog"
	"github.com/dantotech/monsieur-logger/internal/config"
	"github.com/dantotech/monsieur-logger/internal/generator"
	"github.com/dantotech/monsieur-logger/internal/logwriter"
	"github.com/dantotech/monsieur-logger/internal/server"
	"github.com/dantotech/monsieur-logger/internal/version"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "monsieur-logger:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	level, err := applog.ParseLevel(cfg.LogLevel)
	if err != nil {
		return err
	}

	out := logwriter.New(os.Stdout)
	var hostAttrs []any
	for k, v := range map[string]string{"pod": cfg.Host.Pod, "namespace": cfg.Host.Namespace, "node": cfg.Host.Node} {
		if v != "" {
			hostAttrs = append(hostAttrs, k, v)
		}
	}
	log := applog.New(out, level, hostAttrs...)

	mgr := generator.NewManager(out, cfg.Host, cfg.Limits, cfg.MaxConcurrentRuns, log)
	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           server.New(mgr, cfg.Host, log).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Info("Bonjour! monsieur-logger is listening", "addr", cfg.HTTPAddr,
			"version", version.Version, "swagger", "/swagger/")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	if cfg.AutoStart != nil {
		if st, err := mgr.Start(*cfg.AutoStart); err != nil {
			log.Error("autostart run failed", "error", err)
		} else {
			log.Info("autostart run started", "run_id", st.RunID)
		}
	}

	select {
	case <-ctx.Done():
	case err := <-errCh:
		return err
	}

	log.Info("Au revoir! shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	mgr.Shutdown(shutdownCtx)
	return nil
}
