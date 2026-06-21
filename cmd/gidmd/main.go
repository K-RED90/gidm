package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/K-RED90/gidm/internal/apiserver"
	"github.com/K-RED90/gidm/internal/config"
	"github.com/K-RED90/gidm/internal/engine"
	"github.com/K-RED90/gidm/internal/httpfetch"
	"github.com/K-RED90/gidm/internal/httpx"
	"github.com/K-RED90/gidm/internal/store/sqlite"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "gidmd:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("gidmd", flag.ContinueOnError)
	configPath := fs.String("config", "", "config file path (overrides $GIDM_CONFIG)")
	logLevel := fs.String("log-level", "", "log level override: debug|info|warn|error")
	socket := fs.String("socket", "", "unix socket path override")
	if err := fs.Parse(args); err != nil {
		return err
	}

	var (
		cfg *config.Config
		err error
	)
	if *configPath != "" {
		cfg, err = config.LoadFrom(*configPath)
	} else {
		cfg, err = config.Load()
	}
	if err != nil {
		return err
	}

	// Flags are the highest-priority layer.
	if *logLevel != "" {
		cfg.Log.Level = *logLevel
	}
	if *socket != "" {
		cfg.Daemon.SocketPath = *socket
	}
	if err := cfg.Validate(); err != nil {
		return err
	}

	log := newLogger(cfg.Log)
	return serve(cfg, log)
}

// serve is the daemon composition root: it wires the dependency-injection chain
// (httpx -> httpfetch -> sqlite -> engine -> Manager -> apiserver), recovers
// persisted downloads, serves the control socket, and tears everything down
// cleanly on SIGINT/SIGTERM. Each layer is closed on the way out so no socket
// file, goroutine, or open database handle leaks.
func serve(cfg *config.Config, log *slog.Logger) error {
	log.Info("gidmd starting",
		"socket", cfg.Daemon.SocketPath,
		"db", cfg.Storage.DBPath,
		"max_concurrent", cfg.Download.MaxConcurrent,
	)

	client, err := httpx.New(cfg.Network, cfg.Download)
	if err != nil {
		return fmt.Errorf("gidmd: http client: %w", err)
	}
	fetcher := httpfetch.New(client)

	store, err := sqlite.New(cfg.Storage.DBPath)
	if err != nil {
		return fmt.Errorf("gidmd: open store %q: %w", cfg.Storage.DBPath, err)
	}
	defer func() { _ = store.Close() }()

	e := engine.New(*cfg, fetcher, store)
	mgr := engine.NewManager(e, store, cfg.Download.MaxConcurrent)
	mgr.SetLogger(log)

	srv := apiserver.New(mgr, log, cfg.Daemon)

	// Signal-derived context: the first SIGINT/SIGTERM cancels it, beginning the
	// graceful shutdown sequence below.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Start recovers and re-enqueues persisted downloads before we accept clients.
	if err := mgr.Start(ctx); err != nil {
		return fmt.Errorf("gidmd: manager start: %w", err)
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ctx) }()

	select {
	case err := <-serveErr:
		// Serve returned on its own (bind failure or single-instance refusal). Stop
		// listening for signals and drain the manager so nothing is left running.
		stop()
		_ = srv.Close()
		drainManager(mgr, cfg, log)
		if errors.Is(err, apiserver.ErrAlreadyRunning) {
			log.Error("gidmd: another gidmd is already listening on the socket")
			return err
		}
		if err != nil {
			return fmt.Errorf("gidmd: serve: %w", err)
		}
		return nil
	case <-ctx.Done():
		log.Info("gidmd: shutdown signal received, draining")
		stop()
		_ = srv.Close()
		drainManager(mgr, cfg, log)
		// Wait for Serve to unwind so its goroutine does not outlive serve().
		<-serveErr
		return nil
	}
}

// drainManager shuts the Manager down within ShutdownTimeout, cancelling and
// persisting in-flight transfers consistently. A background-rooted context is
// used so an already-cancelled signal context does not abort the drain before it
// can persist.
func drainManager(mgr *engine.Manager, cfg *config.Config, log *slog.Logger) {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Daemon.ShutdownTimeout.Duration())
	defer cancel()
	if err := mgr.Shutdown(shutdownCtx); err != nil {
		log.Warn("gidmd: manager shutdown", "err", err)
	}
}

func newLogger(c config.Log) *slog.Logger {
	level := slog.LevelInfo
	switch strings.ToLower(c.Level) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	opts := &slog.HandlerOptions{Level: level}
	if strings.ToLower(c.Format) == "json" {
		return slog.New(slog.NewJSONHandler(os.Stderr, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stderr, opts))
}
