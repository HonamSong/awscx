// Package config loads and persists user config from ~/.config/awscx/config.json
// and configures the app-wide slog logger.
package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	DefaultRegion  = "ap-northeast-2"
	DefaultCommand = "/bin/sh"
)

// Config mirrors ~/.config/awscx/config.json.
type Config struct {
	Region             string `json:"region"`
	Command            string `json:"command"`
	LogEnabled         bool   `json:"log_enabled"`
	LogLevel           string `json:"log_level"`
	LogPath            string `json:"log_path"`
	MonitorIntervalSec int    `json:"monitor_interval_sec"`
	TailIntervalSec    int    `json:"tail_interval_sec"`
	ListRows           int    `json:"list_rows"`
	TerminalColor      bool   `json:"terminal_color"`
	Mouse              bool   `json:"mouse"`
	// ProfilePrefix: 프로파일 픽커에 노출할 프로파일 이름 필터 (regex).
	// 예: "^sts-", "(dev|prod)". 빈 값이면 전체 노출.
	ProfilePrefix string `json:"profile_prefix"`
}

func Dir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".config", "awscx")
	}
	return filepath.Join(home, ".config", "awscx")
}

func Path() string          { return filepath.Join(Dir(), "config.json") }
func DefaultLogPath() string { return filepath.Join(Dir(), "awscx.log") }

func Default() Config {
	return Config{
		Region:             DefaultRegion,
		Command:            DefaultCommand,
		LogEnabled:         true,
		LogLevel:           "DEBUG",
		LogPath:            DefaultLogPath(),
		MonitorIntervalSec: 30,
		TailIntervalSec:    3,
		ListRows:           15,
		TerminalColor:      true,
		// 기본 OFF: 터미널 네이티브 드래그 선택/복사가 그대로 되게(k9s 유사).
		Mouse:         false,
		ProfilePrefix: "",
	}
}

// Load reads config.json. If missing, writes defaults. Malformed JSON falls
// back to defaults silently (matches Python behavior).
func Load() (Config, error) {
	cfg := Default()
	data, err := os.ReadFile(Path())
	if errors.Is(err, fs.ErrNotExist) {
		_ = Save(cfg)
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Default(), nil
	}
	return cfg, nil
}

func Save(cfg Config) error {
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(Path(), data, 0o644)
}