package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrg/xdg"
	"gopkg.in/yaml.v3"
)

func isolateXDG(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("XDG_CONFIG_DIRS", dir)
	old := xdg.ConfigHome
	oldDirs := xdg.ConfigDirs
	xdg.Reload()
	t.Cleanup(func() {
		xdg.ConfigHome = old
		xdg.ConfigDirs = oldDirs
	})
	return dir
}

func TestSanitizeWindowsPaths(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "windows absolute path",
			in:   `db: "C:\Users\testuser\bin\fss.db"`,
			want: `db: "C:/Users/testuser/bin/fss.db"`,
		},
		{
			name: "multiple path fields",
			in:   "out_dir: \"D:\\data\\fss\"\ndb: \"C:\\Users\\testuser\\fss.db\"",
			want: "out_dir: \"D:/data/fss\"\ndb: \"C:/Users/testuser/fss.db\"",
		},
		{
			name: "single-quoted path unchanged",
			in:   `db: 'C:\Users\testuser\fss.db'`,
			want: `db: 'C:\Users\testuser\fss.db'`,
		},
		{
			name: "unquoted path unchanged",
			in:   `db: C:\Users\testuser\fss.db`,
			want: `db: C:\Users\testuser\fss.db`,
		},
		{
			name: "unix path unchanged",
			in:   `db: "/home/user/fss.db"`,
			want: `db: "/home/user/fss.db"`,
		},
		{
			name: "url unchanged",
			in:   `url: "http://localhost:9999"`,
			want: `url: "http://localhost:9999"`,
		},
		{
			name: "empty string unchanged",
			in:   `db: ""`,
			want: `db: ""`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(sanitizeWindowsPaths([]byte(tt.in)))
			if got != tt.want {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestWindowsPathsRoundTrip(t *testing.T) {
	raw := []byte(`workers: 5
output: csv
out_dir: "D:\data\fss\output"
db: "C:\Users\testuser\bin\fss.db"
delay: 500

site_delays:
  manyvids: 100

stash:
  url: "http://192.168.1.50:9999"
  api_key: "secret"
  tag: "my_import"
  stashbox_tag: "my_override"
  resolution_tags: false
`)

	// Without sanitization, yaml.Unmarshal fails on \U in "C:\Users\..."
	var discard Config
	if err := yaml.Unmarshal(raw, &discard); err == nil {
		t.Fatal("expected raw YAML with Windows paths to fail, but it didn't")
	}

	// With sanitization, it should decode correctly.
	sanitized := sanitizeWindowsPaths(raw)
	var cfg Config
	if err := yaml.Unmarshal(sanitized, &cfg); err != nil {
		t.Fatalf("Unmarshal after sanitization: %v", err)
	}

	if cfg.Workers != 5 {
		t.Errorf("Workers = %d, want 5", cfg.Workers)
	}
	if cfg.Output != "csv" {
		t.Errorf("Output = %q, want csv", cfg.Output)
	}
	if cfg.OutDir != "D:/data/fss/output" {
		t.Errorf("OutDir = %q, want D:/data/fss/output", cfg.OutDir)
	}
	if db, set := cfg.DBSetting(); !set || db != "C:/Users/testuser/bin/fss.db" {
		t.Errorf("DB = %q (set=%v), want C:/Users/testuser/bin/fss.db", db, set)
	}
	if cfg.Delay != 500 {
		t.Errorf("Delay = %d, want 500", cfg.Delay)
	}
	if cfg.SiteDelays["manyvids"] != 100 {
		t.Errorf("SiteDelays[manyvids] = %d, want 100", cfg.SiteDelays["manyvids"])
	}
	if cfg.Stash.URL != "http://192.168.1.50:9999" {
		t.Errorf("Stash.URL = %q, want http://192.168.1.50:9999", cfg.Stash.URL)
	}
	if cfg.Stash.APIKey != "secret" {
		t.Errorf("Stash.APIKey = %q, want secret", cfg.Stash.APIKey)
	}
	if cfg.Stash.Tag != "my_import" {
		t.Errorf("Stash.Tag = %q, want my_import", cfg.Stash.Tag)
	}
	if cfg.Stash.StashboxTag != "my_override" {
		t.Errorf("Stash.StashboxTag = %q, want my_override", cfg.Stash.StashboxTag)
	}
	if cfg.Stash.ResolutionTags {
		t.Error("Stash.ResolutionTags = true, want false")
	}
}

func TestLoadDefaults(t *testing.T) {
	isolateXDG(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Workers != 3 {
		t.Errorf("Workers = %d, want 3", cfg.Workers)
	}
	if cfg.Output != "json" {
		t.Errorf("Output = %q, want json", cfg.Output)
	}
	if cfg.OutDir != "." {
		t.Errorf("OutDir = %q, want .", cfg.OutDir)
	}
	if cfg.Delay != 500 {
		t.Errorf("Delay = %d, want 500", cfg.Delay)
	}
	if cfg.Stash.URL != "http://localhost:9999" {
		t.Errorf("Stash.URL = %q", cfg.Stash.URL)
	}
	if cfg.Stash.Tag != "fss_import" {
		t.Errorf("Stash.Tag = %q", cfg.Stash.Tag)
	}
	if !cfg.Stash.ResolutionTags {
		t.Error("Stash.ResolutionTags should default to true")
	}
}

func TestLoadFromFile(t *testing.T) {
	dir := isolateXDG(t)

	cfgDir := filepath.Join(dir, "fss")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := []byte(`workers: 8
output: csv
out_dir: /tmp/fss
delay: 1000
site_delays:
  manyvids: 200
stash:
  url: "http://10.0.0.5:9999"
  api_key: "testkey"
  tag: "custom_tag"
  stashbox_tag: "custom_stashbox"
  resolution_tags: false
stashbox:
  - url: "https://stashdb.org/graphql"
    api_key: "sdb_key"
`)
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), content, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Workers != 8 {
		t.Errorf("Workers = %d, want 8", cfg.Workers)
	}
	if cfg.Output != "csv" {
		t.Errorf("Output = %q, want csv", cfg.Output)
	}
	if cfg.OutDir != "/tmp/fss" {
		t.Errorf("OutDir = %q", cfg.OutDir)
	}
	if cfg.Delay != 1000 {
		t.Errorf("Delay = %d, want 1000", cfg.Delay)
	}
	if cfg.SiteDelays["manyvids"] != 200 {
		t.Errorf("SiteDelays[manyvids] = %d", cfg.SiteDelays["manyvids"])
	}
	if cfg.Stash.URL != "http://10.0.0.5:9999" {
		t.Errorf("Stash.URL = %q", cfg.Stash.URL)
	}
	if cfg.Stash.APIKey != "testkey" {
		t.Errorf("Stash.APIKey = %q", cfg.Stash.APIKey)
	}
	if cfg.Stash.Tag != "custom_tag" {
		t.Errorf("Stash.Tag = %q", cfg.Stash.Tag)
	}
	if cfg.Stash.ResolutionTags {
		t.Error("Stash.ResolutionTags = true, want false")
	}
	if len(cfg.Stashbox) != 1 {
		t.Fatalf("Stashbox len = %d, want 1", len(cfg.Stashbox))
	}
	if cfg.Stashbox[0].URL != "https://stashdb.org/graphql" {
		t.Errorf("Stashbox[0].URL = %q", cfg.Stashbox[0].URL)
	}
	if cfg.Stashbox[0].APIKey != "sdb_key" {
		t.Errorf("Stashbox[0].APIKey = %q", cfg.Stashbox[0].APIKey)
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	dir := isolateXDG(t)

	cfgDir := filepath.Join(dir, "fss")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte("workers: [invalid"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestLoadPartialConfig(t *testing.T) {
	dir := isolateXDG(t)

	cfgDir := filepath.Join(dir, "fss")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte("workers: 16\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Workers != 16 {
		t.Errorf("Workers = %d, want 16", cfg.Workers)
	}
	if cfg.Output != "json" {
		t.Errorf("Output = %q, want json (default)", cfg.Output)
	}
	if cfg.Stash.Tag != "fss_import" {
		t.Errorf("Stash.Tag = %q, want fss_import (default)", cfg.Stash.Tag)
	}
}

func TestValidate_negativeDelay(t *testing.T) {
	cfg := defaults()
	cfg.Delay = -1
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for negative delay")
	}
	if !strings.Contains(err.Error(), "delay must be non-negative") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_negativeSiteDelay(t *testing.T) {
	cfg := defaults()
	cfg.SiteDelays = map[string]int{"manyvids": -5}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for negative site delay")
	}
	if !strings.Contains(err.Error(), "site_delays[manyvids]") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_invalidOutputFormat(t *testing.T) {
	cfg := defaults()
	cfg.Output = "xml"
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for invalid output format")
	}
	if !strings.Contains(err.Error(), `unknown output format "xml"`) {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_multipleFormats(t *testing.T) {
	cfg := defaults()
	cfg.Output = "json,csv"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected error for valid multi-format output: %v", err)
	}
}

func TestLoad_oversizedConfig(t *testing.T) {
	dir := isolateXDG(t)

	cfgDir := filepath.Join(dir, "fss")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	big := make([]byte, 1<<20+1)
	for i := range big {
		big[i] = '#'
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), big, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for oversized config")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDefaultPath(t *testing.T) {
	p := DefaultPath()
	if !strings.HasSuffix(p, filepath.Join("fss", "config.yaml")) {
		t.Errorf("DefaultPath() = %q, want suffix %q", p, filepath.Join("fss", "config.yaml"))
	}
}

func TestDefaultDBPath(t *testing.T) {
	p := DefaultDBPath()
	if !strings.HasSuffix(p, filepath.Join("fss", "fss.db")) {
		t.Errorf("DefaultDBPath() = %q, want suffix %q", p, filepath.Join("fss", "fss.db"))
	}
}

func TestResolveDBPath(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"default keyword", "default", DefaultDBPath()},
		{"true keyword", "true", DefaultDBPath()},
		{"explicit path", "/tmp/my.db", "/tmp/my.db"},
		{"relative path", "./data/fss.db", "./data/fss.db"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveDBPath(tt.in)
			if got != tt.want {
				t.Errorf("ResolveDBPath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestDBSettingDistinguishesAbsentFromEmpty pins the reason `db:` is a pointer.
//
// SQLite is planned to become the default store, and `fss scrape` tells users to
// set `db: ""` to keep JSON files. If absent and explicitly-empty decoded to the
// same value, that instruction would stop working the moment the default flipped
// — an explicit opt-out would be indistinguishable from never having chosen.
func TestDBSettingDistinguishesAbsentFromEmpty(t *testing.T) {
	cases := []struct {
		name      string
		yaml      string
		wantValue string
		wantSet   bool
	}{
		{"absent", "workers: 3\n", "", false},
		{"explicitly empty", "db: \"\"\n", "", true},
		{"default keyword", "db: \"default\"\n", "default", true},
		{"explicit path", "db: \"./local.db\"\n", "./local.db", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := isolateXDG(t)
			cfgDir := filepath.Join(dir, "fss")
			if err := os.MkdirAll(cfgDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(c.yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			got, set := cfg.DBSetting()
			if got != c.wantValue || set != c.wantSet {
				t.Errorf("DBSetting() = (%q, %v), want (%q, %v)", got, set, c.wantValue, c.wantSet)
			}
		})
	}
}

func TestLanguagePref(t *testing.T) {
	t.Run("no config file", func(t *testing.T) {
		isolateXDG(t)
		if got := LanguagePref(); got != "" {
			t.Errorf("LanguagePref() = %q, want \"\"", got)
		}
	})

	t.Run("language set", func(t *testing.T) {
		dir := isolateXDG(t)
		cfgDir := filepath.Join(dir, "fss")
		if err := os.MkdirAll(cfgDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte("language: ko\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := LanguagePref(); got != "ko" {
			t.Errorf("LanguagePref() = %q, want ko", got)
		}
	})

	t.Run("malformed YAML", func(t *testing.T) {
		dir := isolateXDG(t)
		cfgDir := filepath.Join(dir, "fss")
		if err := os.MkdirAll(cfgDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte("language: [unclosed\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := LanguagePref(); got != "" {
			t.Errorf("LanguagePref() = %q, want \"\"", got)
		}
	})
}

func TestValidate_rejectsCookieWithNewline(t *testing.T) {
	cfg := &Config{SiteCookies: map[string]string{"mydirtyhobby": "KEY=1\nX-Evil: 1"}}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "site_cookies[mydirtyhobby]") {
		t.Errorf("error = %v, want it to name the offending key", err)
	}
}
