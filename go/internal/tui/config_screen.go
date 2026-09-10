package tui

import (
	"strconv"
	"strings"

	"github.com/HonamSong/awscx/go/internal/config"
)

type settingType int

const (
	settingStr settingType = iota
	settingInt
	settingBool
)

// setting describes one editable row in the config screen. Getter/setter
// closures avoid reflection while keeping the definitions declarative.
type setting struct {
	key    string
	label  string
	typ    settingType
	getStr func(config.Config) string
	getVal func(config.Config) any
	setStr func(*config.Config, string) error
	toggle func(*config.Config) // bool only
}

func strSetting(key, label string, get func(config.Config) string, set func(*config.Config, string)) setting {
	return setting{
		key: key, label: label, typ: settingStr,
		getStr: get,
		getVal: func(c config.Config) any { return get(c) },
		setStr: func(c *config.Config, s string) error { set(c, s); return nil },
	}
}

func intSetting(key, label string, get func(config.Config) int, set func(*config.Config, int)) setting {
	return setting{
		key: key, label: label, typ: settingInt,
		getStr: func(c config.Config) string { return strconv.Itoa(get(c)) },
		getVal: func(c config.Config) any { return get(c) },
		setStr: func(c *config.Config, s string) error {
			n, err := strconv.Atoi(strings.TrimSpace(s))
			if err != nil {
				return err
			}
			set(c, n)
			return nil
		},
	}
}

func boolSetting(key, label string, get func(config.Config) bool, toggle func(*config.Config)) setting {
	return setting{
		key: key, label: label, typ: settingBool,
		getStr: func(c config.Config) string {
			if get(c) {
				return "ON"
			}
			return "OFF"
		},
		getVal: func(c config.Config) any { return get(c) },
		toggle: toggle,
	}
}

// settings mirrors python SETTINGS in awscx/app.py (order + labels preserved).
var settings = []setting{
	strSetting("region", "AWS region",
		func(c config.Config) string { return c.Region },
		func(c *config.Config, s string) { c.Region = strings.TrimSpace(s) }),
	strSetting("command", "shell command",
		func(c config.Config) string { return c.Command },
		func(c *config.Config, s string) { c.Command = strings.TrimSpace(s) }),
	boolSetting("log_enabled", "로그 기록",
		func(c config.Config) bool { return c.LogEnabled },
		func(c *config.Config) { c.LogEnabled = !c.LogEnabled }),
	strSetting("log_level", "로그 레벨(DEBUG/INFO/WARNING/ERROR)",
		func(c config.Config) string { return c.LogLevel },
		func(c *config.Config, s string) { c.LogLevel = strings.TrimSpace(s) }),
	strSetting("log_path", "로그 파일 경로",
		func(c config.Config) string { return c.LogPath },
		func(c *config.Config, s string) { c.LogPath = strings.TrimSpace(s) }),
	intSetting("monitor_interval_sec", "모니터 갱신주기(초)",
		func(c config.Config) int { return c.MonitorIntervalSec },
		func(c *config.Config, n int) { c.MonitorIntervalSec = n }),
	intSetting("tail_interval_sec", "로그 tail 주기(초)",
		func(c config.Config) int { return c.TailIntervalSec },
		func(c *config.Config, n int) { c.TailIntervalSec = n }),
	intSetting("list_rows", "목록에 보이는 줄 수",
		func(c config.Config) int { return c.ListRows },
		func(c *config.Config, n int) { c.ListRows = n }),
	boolSetting("terminal_color", "터미널 컬러 렌더링",
		func(c config.Config) bool { return c.TerminalColor },
		func(c *config.Config) { c.TerminalColor = !c.TerminalColor }),
	boolSetting("mouse",
		"마우스 사용 (재시작 필요)",
		func(c config.Config) bool { return c.Mouse },
		func(c *config.Config) { c.Mouse = !c.Mouse }),
	strSetting("profile_prefix",
		"프로파일 필터 (regex, 예: ^sts- 또는 (dev|prod))",
		func(c config.Config) string { return c.ProfilePrefix },
		func(c *config.Config, s string) { c.ProfilePrefix = strings.TrimSpace(s) }),
}

// defaultCfg is a snapshot of the built-in defaults, used for the DEFAULT
// column in the config table.
var defaultCfg = config.Default()