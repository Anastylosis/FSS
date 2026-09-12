// Package creators reads the creators.d directory: one YAML file per creator,
// binding the several storefronts one person sells the same catalogue on.
//
// It is deliberately a directory of single-creator files rather than a block in
// config.yaml. The files carry no secrets, so a set of them can live in a git
// repository and be shared; and because each creator is its own file, two people
// adding creators never touch the same lines.
package creators

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/adrg/xdg"
	"gopkg.in/yaml.v3"
)

// maxFileBytes caps a single creator file. These are hand-written lists of a
// handful of URLs; anything larger is a mistake, not a creator.
const maxFileBytes = 1 << 18 // 256 KB

// Store is one storefront belonging to a creator.
type Store struct {
	URL string `yaml:"url"`
	// Delay overrides the per-request delay in milliseconds for this store
	// only. Nil inherits the site or global delay.
	Delay *int `yaml:"delay,omitempty"`
	// Language requests a content language for this store, for a site that
	// publishes in several. Empty inherits site_languages / content_language.
	Language string `yaml:"language,omitempty"`
	// Enabled set to false skips the store on --all-creators and --creator
	// runs, for a storefront that is login-walled or otherwise not scrapeable
	// unattended. Nil means enabled.
	Enabled *bool `yaml:"enabled,omitempty"`
	// Note is a free-text reminder, shown by `fss creators`.
	Note string `yaml:"note,omitempty"`
}

// On reports whether the store participates in creator-driven scrapes.
func (s Store) On() bool { return s.Enabled == nil || *s.Enabled }

// Creator is one person's storefronts, as read from a single file.
type Creator struct {
	Name    string   `yaml:"name"`
	Aliases []string `yaml:"aliases,omitempty"`
	Stores  []Store  `yaml:"stores"`

	// Path is the file this came from, for error messages. Not serialised.
	Path string `yaml:"-"`
}

// EnabledStores returns only the stores a creator-driven scrape should visit.
func (c Creator) EnabledStores() []Store {
	out := make([]Store, 0, len(c.Stores))
	for _, s := range c.Stores {
		if s.On() {
			out = append(out, s)
		}
	}
	return out
}

// URLs returns every store URL, enabled or not.
func (c Creator) URLs() []string {
	out := make([]string, 0, len(c.Stores))
	for _, s := range c.Stores {
		out = append(out, s.URL)
	}
	return out
}

// Keys returns the lookup keys a --creator value may match: the name and every
// alias, canonicalised.
func (c Creator) Keys() []string {
	keys := make([]string, 0, len(c.Aliases)+1)
	if k := Key(c.Name); k != "" {
		keys = append(keys, k)
	}
	for _, a := range c.Aliases {
		if k := Key(a); k != "" {
			keys = append(keys, k)
		}
	}
	return keys
}

// Key canonicalises a creator name for comparison: lowercased, with everything
// that is not a letter or digit removed.
//
// It is deliberately tighter than match.NormalizeName, which keeps spaces. The
// same person is spelled "Vera Quill", "VeraQuill" and "Vera Quill Films" across
// three storefronts, and only a key that ignores spacing treats the first two as
// one name.
func Key(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Filename returns the conventional file name for a creator: the name as a
// lowercase hyphenated slug. Only a convention — Load reads the `name:` field,
// never the file name, so renaming a file changes nothing.
func Filename(name string) string {
	var b strings.Builder
	lastHyphen := true
	for _, r := range strings.ToLower(name) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastHyphen = false
		case !lastHyphen:
			b.WriteByte('-')
			lastHyphen = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "creator"
	}
	return out + ".yaml"
}

// DefaultDir returns the conventional creators.d location for this platform.
// The directory need not exist — an absent one simply means no creators.
func DefaultDir() string {
	return filepath.Join(xdg.ConfigHome, "fss", "creators.d")
}

// Load reads every *.yaml / *.yml file in dir, one creator per file.
//
// A missing directory is not an error: creators are opt-in, and every command
// that consults them must work for an operator who has never made one. Files
// that are not YAML are skipped, so a shared repository can carry a README, a
// LICENSE and a .git directory without special handling.
func Load(dir string) ([]Creator, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading creators directory %s: %w", dir, err)
	}

	var out []Creator
	byKey := map[string]string{} // creator key → file that claimed it
	byURL := map[string]string{} // store URL → creator that claimed it
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		switch strings.ToLower(filepath.Ext(e.Name())) {
		case ".yaml", ".yml":
		default:
			continue
		}

		path := filepath.Join(dir, e.Name())
		c, err := loadFile(path)
		if err != nil {
			return nil, err
		}

		for _, k := range c.Keys() {
			if prev, dup := byKey[k]; dup {
				return nil, fmt.Errorf("%s: creator name or alias %q is already claimed by %s", path, k, prev)
			}
			byKey[k] = path
		}
		// A URL under two creators is not fatal — a genuinely shared
		// storefront is possible — but it means --all-creators would scrape
		// it twice, so say so once at load.
		for _, u := range c.URLs() {
			if prev, dup := byURL[u]; dup {
				log.Printf("warning: %s is listed under both %q and %q", u, prev, c.Name)
				continue
			}
			byURL[u] = c.Name
		}
		out = append(out, c)
	}

	sort.Slice(out, func(i, j int) bool { return Key(out[i].Name) < Key(out[j].Name) })
	return out, nil
}

func loadFile(path string) (Creator, error) {
	f, err := os.Open(path)
	if err != nil {
		return Creator{}, fmt.Errorf("opening %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	raw, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil {
		return Creator{}, fmt.Errorf("reading %s: %w", path, err)
	}
	if len(raw) > maxFileBytes {
		return Creator{}, fmt.Errorf("%s exceeds %d bytes", path, maxFileBytes)
	}

	var c Creator
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return Creator{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	warnUnknownKeys(raw, path)

	c.Path = path
	if err := c.validate(); err != nil {
		return Creator{}, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// warnUnknownKeys reports keys the Creator struct does not recognise, mirroring
// the config loader. It warns rather than failing so a file written for a newer
// FSS still loads; the dangerous typo — writing `urls:` instead of `stores:` —
// is caught as a hard error by validate, which requires at least one store.
func warnUnknownKeys(raw []byte, path string) {
	var probe Creator
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
		if strings.Contains(err.Error(), "field") {
			log.Printf("warning: %s: %v (unknown settings are ignored)", path, err)
		}
		return
	}
}

func (c *Creator) validate() error {
	if strings.TrimSpace(c.Name) == "" {
		return errors.New("name is required")
	}
	if Key(c.Name) == "" {
		return fmt.Errorf("name %q has no letters or digits", c.Name)
	}
	if len(c.Stores) == 0 {
		return errors.New("at least one entry under `stores:` is required")
	}
	seen := map[string]bool{}
	for i := range c.Stores {
		s := &c.Stores[i]
		s.URL = strings.TrimSpace(s.URL)
		if s.URL == "" {
			return fmt.Errorf("stores[%d]: url is required", i)
		}
		if !strings.Contains(s.URL, "://") {
			return fmt.Errorf("stores[%d]: %q must be a full URL including the scheme", i, s.URL)
		}
		if seen[s.URL] {
			return fmt.Errorf("stores[%d]: %s is listed twice", i, s.URL)
		}
		seen[s.URL] = true
		if s.Delay != nil && *s.Delay < 0 {
			return fmt.Errorf("stores[%d]: delay must not be negative, got %d", i, *s.Delay)
		}
	}
	return nil
}

// Find resolves a --creator value against a loaded set.
//
// An exact key match wins outright. Failing that, a value that is a prefix of
// exactly one creator's key resolves to it, so `--creator mara` reaches "Mara
// Vance"; a prefix matching several is an error naming them, never a silent
// pick.
func Find(list []Creator, query string) (Creator, error) {
	q := Key(query)
	if q == "" {
		return Creator{}, fmt.Errorf("--creator %q has no letters or digits", query)
	}

	var prefix []Creator
	for _, c := range list {
		for _, k := range c.Keys() {
			if k == q {
				return c, nil
			}
			if strings.HasPrefix(k, q) {
				prefix = append(prefix, c)
				break
			}
		}
	}

	switch len(prefix) {
	case 1:
		return prefix[0], nil
	case 0:
		return Creator{}, fmt.Errorf("no creator matches %q%s", query, availableHint(list))
	default:
		names := make([]string, len(prefix))
		for i, c := range prefix {
			names[i] = c.Name
		}
		return Creator{}, fmt.Errorf("%q matches %d creators: %s — be more specific",
			query, len(prefix), strings.Join(names, ", "))
	}
}

func availableHint(list []Creator) string {
	if len(list) == 0 {
		return " (no creators defined — see `fss creators suggest`)"
	}
	names := make([]string, 0, len(list))
	for _, c := range list {
		names = append(names, c.Name)
	}
	const limit = 12
	if len(names) > limit {
		return fmt.Sprintf("\navailable: %s … and %d more", strings.Join(names[:limit], ", "), len(names)-limit)
	}
	return "\navailable: " + strings.Join(names, ", ")
}

// Marshal renders a creator as the contents of its file.
func (c Creator) Marshal() ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(c); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
