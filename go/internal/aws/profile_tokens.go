package aws

import (
	"bufio"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ProfileToken summarizes a profile's authentication type and (for SSO /
// assume-role profiles) its cached credential expiration. Zero ExpiresAt
// means unknown (no cache found or unreadable).
type ProfileToken struct {
	Name      string
	Kind      string // "static", "sso", "assume-role"
	ExpiresAt time.Time
	HasCache  bool
}

// ProfileTokens returns per-profile token status by:
//  1. parsing ~/.aws/config + ~/.aws/credentials
//  2. classifying each profile (sso / assume-role / static)
//  3. looking up cache files in ~/.aws/sso/cache and ~/.aws/cli/cache
//
// Never touches the network.
func ProfileTokens() []ProfileToken {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	configs := loadProfileConfigs(home)
	stsByArn := loadSTSCacheByArn(home)

	names := make([]string, 0, len(configs))
	for n := range configs {
		names = append(names, n)
	}
	sort.Strings(names)

	out := make([]ProfileToken, 0, len(names))
	for _, name := range names {
		c := configs[name]
		pt := ProfileToken{Name: name}
		switch {
		case c.ssoSession != "" || c.ssoStartUrl != "":
			pt.Kind = "sso"
			key := c.ssoStartUrl
			if c.ssoSession != "" {
				key = c.ssoSession
			}
			if exp, ok := readSSOCache(home, key); ok {
				pt.ExpiresAt = exp
				pt.HasCache = true
			}
		case c.roleArn != "":
			pt.Kind = "assume-role"
			if exp, ok := stsByArn[c.roleArn]; ok {
				pt.ExpiresAt = exp
				pt.HasCache = true
			}
		case c.credentialProcess != "":
			pt.Kind = "external"
		case c.hasSessionToken || c.expiration != "":
			pt.Kind = "sts"
		case c.mfaSerial != "":
			pt.Kind = "mfa"
		default:
			pt.Kind = "static"
		}
		// 프로파일에 직접 적힌 expiration 이 있으면 그 값을 최우선으로 사용.
		if c.expiration != "" {
			if t := parseFlexTime(c.expiration); !t.IsZero() {
				pt.ExpiresAt = t
				pt.HasCache = true
			}
		}
		out = append(out, pt)
	}
	return out
}

type profileConfig struct {
	ssoSession        string
	ssoStartUrl       string
	roleArn           string
	sourceProfile     string
	mfaSerial         string
	credentialProcess string
	// hasSessionToken: aws_session_token 이 프로파일에 직접 기록됨 → 임시 자격.
	// expiration: 외부 도구가 프로파일에 남긴 만료 타임스탬프 (aws-vault 등).
	hasSessionToken bool
	expiration      string
}

func loadProfileConfigs(home string) map[string]profileConfig {
	m := map[string]profileConfig{}

	apply := func(name, key, val string) {
		c := m[name]
		switch key {
		case "sso_session":
			c.ssoSession = val
		case "sso_start_url":
			c.ssoStartUrl = val
		case "role_arn":
			c.roleArn = val
		case "source_profile":
			c.sourceProfile = val
		case "mfa_serial":
			c.mfaSerial = val
		case "credential_process":
			c.credentialProcess = val
		case "aws_session_token":
			c.hasSessionToken = true
		case "expiration", "x_security_token_expires":
			c.expiration = val
		}
		m[name] = c
	}

	parseINIKV(filepath.Join(home, ".aws", "config"), func(section, key, val string) {
		name := ""
		if section == "default" {
			name = "default"
		} else if p, ok := strings.CutPrefix(section, "profile "); ok {
			name = strings.TrimSpace(p)
		}
		if name == "" {
			return
		}
		apply(name, key, val)
	})

	parseINIKV(filepath.Join(home, ".aws", "credentials"), func(section, key, val string) {
		if _, ok := m[section]; !ok {
			m[section] = profileConfig{}
		}
		apply(section, key, val)
	})
	return m
}

// parseINIKV invokes fn(section, key, value) for every INI k=v assignment.
// Section headers switch context; empty section means before any header.
func parseINIKV(path string, fn func(section, key, value string)) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	section := ""
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") && len(line) > 2 {
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		if section == "" {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		fn(section, key, val)
	}
}

// readSSOCache returns the cached SSO access token's expiration.
// key is the sso_session name (aws-cli v2 session-based) or sso_start_url
// (legacy) — hashed with SHA1 to form the cache filename.
func readSSOCache(home, key string) (time.Time, bool) {
	h := sha1.Sum([]byte(key))
	path := filepath.Join(home, ".aws", "sso", "cache", hex.EncodeToString(h[:])+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}, false
	}
	var c struct {
		ExpiresAt string `json:"expiresAt"`
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return time.Time{}, false
	}
	t := parseFlexTime(c.ExpiresAt)
	return t, !t.IsZero()
}

// loadSTSCacheByArn scans ~/.aws/cli/cache/*.json and returns the latest
// expiration per role ARN. Cache filenames are opaque hashes, so we match
// entries back to profiles via AssumedRoleUser.Arn → role ARN.
func loadSTSCacheByArn(home string) map[string]time.Time {
	m := map[string]time.Time{}
	dir := filepath.Join(home, ".aws", "cli", "cache")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return m
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var c struct {
			Credentials struct {
				Expiration string `json:"Expiration"`
			} `json:"Credentials"`
			AssumedRoleUser struct {
				Arn string `json:"Arn"`
			} `json:"AssumedRoleUser"`
		}
		if err := json.Unmarshal(data, &c); err != nil {
			continue
		}
		exp := parseFlexTime(c.Credentials.Expiration)
		if exp.IsZero() {
			continue
		}
		roleArn := stsArnToRoleArn(c.AssumedRoleUser.Arn)
		if roleArn == "" {
			continue
		}
		if prev, ok := m[roleArn]; !ok || exp.After(prev) {
			m[roleArn] = exp
		}
	}
	return m
}

// stsArnToRoleArn converts an assumed-role ARN into its underlying role ARN.
// Example: arn:aws:sts::123:assumed-role/MyRole/session → arn:aws:iam::123:role/MyRole
func stsArnToRoleArn(stsArn string) string {
	if !strings.HasPrefix(stsArn, "arn:aws:sts::") {
		return ""
	}
	slash := strings.Split(stsArn, "/")
	if len(slash) < 2 {
		return ""
	}
	role := slash[1]
	colon := strings.Split(slash[0], ":")
	if len(colon) < 6 {
		return ""
	}
	account := colon[4]
	return fmt.Sprintf("arn:aws:iam::%s:role/%s", account, role)
}

// parseFlexTime handles the various timestamp formats AWS caches use
// ("2024-01-01T00:00:00UTC", RFC3339, RFC3339Nano, +00:00 offset, ...).
func parseFlexTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05UTC",
		"2006-01-02T15:04:05.999999999+00:00",
		"2006-01-02T15:04:05+00:00",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}