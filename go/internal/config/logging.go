package config

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// Logger is the app-wide slog logger. Discards until SetupLogging is called.
var Logger = slog.New(slog.NewTextHandler(io.Discard, nil))

// SetupLogging configures Logger based on cfg. Safe to call multiple times.
// version is stamped into the startup line for correlation.
func SetupLogging(cfg Config, version string) {
	if !cfg.LogEnabled {
		Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
		return
	}
	path := cfg.LogPath
	if path == "" {
		path = DefaultLogPath()
	}
	if parent := filepath.Dir(path); parent != "" {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	h := slog.NewTextHandler(f, &slog.HandlerOptions{Level: parseLevel(cfg.LogLevel)})
	Logger = slog.New(h)
	Logger.Debug("awscx started", "version", version, "log", path)
}

func parseLevel(s string) slog.Level {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "INFO":
		return slog.LevelInfo
	case "WARN", "WARNING":
		return slog.LevelWarn
	case "ERROR", "CRITICAL", "FATAL":
		return slog.LevelError
	default:
		return slog.LevelDebug
	}
}