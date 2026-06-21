package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/K-RED90/gidm/internal/config"
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
	log.Info("gidmd starting",
		"socket", cfg.Daemon.SocketPath,
		"db", cfg.Storage.DBPath,
		"max_concurrent", cfg.Download.MaxConcurrent,
	)
	log.Info("engine and API arrive in milestones M1/M2; exiting")
	return nil
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
