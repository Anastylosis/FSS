package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/adrg/xdg"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Workers int    `yaml:"workers"`
	Output  string `yaml:"output"`
	OutDir  string `yaml:"out_dir"`
	// DB is the store selector: absent (nil) means "not configured", an
	// explicit empty string means "deliberately the flat store". They must
	// stay distinguishable — see DBSetting.
	DB        *string `yaml:"db"`
	Delay     int     `yaml:"delay"`
	UserAgent string  `yaml:"user_agent"`
	// Notices controls advisory messages, such as a heads-up that a default
	// is going to change in a future release. Absent means enabled; set
	// `notices: false` to silence them. A pointer so "absent" and "explicitly
	// false" are distinguishable.
	Notices *bool `yaml:"notices"`
	// SiteDelays overrides Delay per scraper ID (e.g. "manyvids", "pornhub").
	// Sites without an entry fall back to Delay.
	SiteDelays map[string]int `yaml:"site_delays"`
	// SiteCookies supplies a Cookie header per scraper ID, for hosts that will
	// not answer an anonymous request. See scraper.ListOpts.Cookie. Values are
	// credentials of a sort — a config holding them wants the same care as one
	// holding an api_key.
	SiteCookies map[string]string `yaml:"site_cookies"`
	// CreatorsDir is the directory of one-creator-per-file YAML definitions.
	// Empty means the conventional location beside this config. Point it at a
	// clone to use a shared set.
	CreatorsDir string           `yaml:"creators_dir"`
	Stash       StashConfig      `yaml:"stash"`
	Stashbox    []StashboxConfig `yaml:"stashbox"`
	// Language selects the help-text language, e.g. "ko". Empty means English.
	// Overridden by FSS_LANG and --lang. An unrecognised value falls back to
	// English rather than failing, so a config written for a newer fss still
	// works on an older binary.
	Language string `yaml:"language"`
}

type StashboxConfig struct {
	URL    string `yaml:"url"`
	APIKey string `yaml:"api_key"`
}

type StashConfig struct {
	URL            string `yaml:"url"`
	APIKey         string `yaml:"api_key"`
	Tag            string `yaml:"tag"`
	StashboxTag    string `yaml:"stashbox_tag"`
	ResolutionTags bool   `yaml:"resolution_tags"`
}

func defaults() *Config {
	return &Config{
		Workers: 3,
		Output:  "json",
		OutDir:  ".",
		Delay:   500,
		Stash: StashConfig{
			URL:            "http://localhost:9999",
			Tag:            "fss_import",
			StashboxTag:    "fss_stashbox_override",
			ResolutionTags: true,
		},
	}
}

// DefaultPath returns the canonical config file path for the current platform.
// The file may not exist yet — this is where it should be created.
func DefaultPath() string {
	return filepath.Join(xdg.ConfigHome, "fss", "config.yaml")
}

// DefaultDBPath returns the canonical SQLite database path for the current
// platform, under the XDG data directory (e.g. ~/.local/share/fss/fss.db).
func DefaultDBPath() string {
	return filepath.Join(xdg.DataHome, "fss", "fss.db")
}

// CreatorsPath returns the configured creators directory, or "" to let the
// creators package use its conventional location. Safe on a nil Config.
func (c *Config) CreatorsPath() string {
	if c == nil {
		return ""
	}
	return c.CreatorsDir
}

// DBRef is a helper for building a Config with a literal `db:` value, since the
// field is a pointer.
func DBRef(v string) *string { return &v }

// DBSetting returns the configured `db:` value and whether the key was present
// at all.
//
// The distinction matters for a change already planned: SQLite is to become the
// default store, and `fss scrape` tells operators to set `db: ""` if they want
// to keep JSON files. If absent and explicitly-empty were the same value, that
// instruction would stop working the moment the default flipped — an explicit
// opt-out would be indistinguishable from never having chosen. A nil pointer
// keeps them apart.
func (c *Config) DBSetting() (value string, set bool) {
	if c == nil || c.DB == nil {
		return "", false
	}
	return *c.DB, true
}

// ResolveDBPath interprets the db config value: empty means no database,
// "default" or "true" means DefaultDBPath(), anything else is a literal path
// (absolute, or relative to the working directory).
func ResolveDBPath(raw string) string {
	switch raw {
	case "":
		return ""
	case "default", "true":
		return DefaultDBPath()
	default:
		return raw
	}
}

// windowsPathRe matches double-quoted YAML values that look like Windows
// absolute paths (drive letter followed by :\). YAML treats backslashes as
// escape characters inside double-quoted strings, so "C:\Users" fails
// because \U is parsed as a Unicode escape. We replace \ with / before
// decoding — Go's filepath functions accept forward slashes on all platforms.
var windowsPathRe = regexp.MustCompile(`"([A-Za-z]:\\[^"]*)"`)

func sanitizeWindowsPaths(data []byte) []byte {
	return windowsPathRe.ReplaceAllFunc(data, func(match []byte) []byte {
		out := make([]byte, len(match))
		copy(out, match)
		for i, b := range out {
			if b == '\\' {
				out[i] = '/'
			}
		}
		return out
	})
}

const maxConfigBytes = 1 << 20 // 1 MB

// Load reads the config file from the XDG config directory.
// If no file exists, defaults are returned without error.
func Load() (*Config, error) {
	cfg := defaults()

	path, err := xdg.SearchConfigFile("fss/config.yaml")
	if err != nil {
		// No config file found — use defaults.
		return cfg, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening config %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	if runtime.GOOS != "windows" {
		if info, err := f.Stat(); err == nil && info.Mode().Perm()&0o077 != 0 {
			log.Printf("warning: %s is readable by other users (mode %04o); consider chmod 600", path, info.Mode().Perm())
		}
	}

	raw, err := io.ReadAll(io.LimitReader(f, maxConfigBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}
	if len(raw) > maxConfigBytes {
		return nil, fmt.Errorf("config %s exceeds %d bytes", path, maxConfigBytes)
	}

	raw = sanitizeWindowsPaths(raw)

	if err := yaml.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}
	warnUnknownConfigKeys(raw, path)

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}

	return cfg, nil
}

// LanguagePref returns the `language:` setting, or "" if there is no config
// file, it cannot be read, or it does not parse. Best-effort by design: a
// broken config must still let `fss --help` work.
func LanguagePref() string {
	path, err := xdg.SearchConfigFile("fss/config.yaml")
	if err != nil {
		return ""
	}

	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()

	raw, err := io.ReadAll(io.LimitReader(f, maxConfigBytes+1))
	if err != nil || len(raw) > maxConfigBytes {
		return ""
	}

	raw = sanitizeWindowsPaths(raw)

	var probe struct {
		Language string `yaml:"language"`
	}
	if err := yaml.Unmarshal(raw, &probe); err != nil {
		return ""
	}
	return probe.Language
}

// warnUnknownConfigKeys reports keys the Config struct does not recognise.
//
// A typo like `dealy: 800` is otherwise completely silent: yaml.Unmarshal
// ignores unknown fields, so the setting appears to have been accepted while
// the default stays in force. This warns rather than failing, because a config
// that has worked for months should not suddenly become a hard error — an
// unknown key may also be a setting from a newer version.
func warnUnknownConfigKeys(raw []byte, path string) {
	var probe Config
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)

	for {
		err := dec.Decode(&probe)
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			return
		}
		// KnownFields turns unknown keys into a type error listing each one.
		// Any other parse failure was already reported by the real Unmarshal
		// above, so it is not worth repeating here.
		if strings.Contains(err.Error(), "field") {
			log.Printf("warning: %s: %v (unknown settings are ignored)", path, err)
		}
		return
	}
}

func (c *Config) Validate() error {
	if c.Workers < 0 {
		return fmt.Errorf("workers must be non-negative, got %d", c.Workers)
	}
	if c.Delay < 0 {
		return fmt.Errorf("delay must be non-negative, got %d", c.Delay)
	}
	for name, d := range c.SiteDelays {
		if d < 0 {
			return fmt.Errorf("site_delays[%s] must be non-negative, got %d", name, d)
		}
	}
	for name, v := range c.SiteCookies {
		if strings.ContainsAny(v, "\r\n") {
			return fmt.Errorf("site_cookies[%s] must not contain a newline", name)
		}
	}
	if c.Output != "" {
		for _, f := range strings.Split(c.Output, ",") {
			f = strings.TrimSpace(f)
			if f != "" && f != "json" && f != "csv" {
				return fmt.Errorf("unknown output format %q (valid: json, csv)", f)
			}
		}
	}
	return nil
}
