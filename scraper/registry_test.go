package scraper

import (
	"context"
	"strings"
	"testing"
)

type fakeScraper struct {
	id       string
	patterns []string
	matchFn  func(string) bool
}

func (f *fakeScraper) ID() string               { return f.id }
func (f *fakeScraper) Patterns() []string       { return f.patterns }
func (f *fakeScraper) MatchesURL(u string) bool { return f.matchFn(u) }
func (f *fakeScraper) ListScenes(_ context.Context, _ string, _ ListOpts) (<-chan SceneResult, error) {
	return nil, nil
}

func withCleanRegistry(t *testing.T, scrapers ...StudioScraper) {
	t.Helper()
	old := registered
	registered = nil
	for _, s := range scrapers {
		Register(s)
	}
	t.Cleanup(func() { registered = old })
}

func TestForID(t *testing.T) {
	a := &fakeScraper{id: "alpha"}
	b := &fakeScraper{id: "beta"}
	withCleanRegistry(t, a, b)

	got, err := ForID("beta")
	if err != nil {
		t.Fatalf("ForID(beta): %v", err)
	}
	if got.ID() != "beta" {
		t.Errorf("got %q, want beta", got.ID())
	}
}

func TestForIDNotFound(t *testing.T) {
	withCleanRegistry(t)

	_, err := ForID("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown ID")
	}
}

func TestForURL(t *testing.T) {
	a := &fakeScraper{
		id:      "alpha",
		matchFn: func(u string) bool { return u == "https://alpha.com/videos" },
	}
	b := &fakeScraper{
		id:      "beta",
		matchFn: func(u string) bool { return u == "https://beta.com/videos" },
	}
	withCleanRegistry(t, a, b)

	got, err := ForURL("https://beta.com/videos")
	if err != nil {
		t.Fatalf("ForURL: %v", err)
	}
	if got.ID() != "beta" {
		t.Errorf("got %q, want beta", got.ID())
	}
}

func TestForURLNotFound(t *testing.T) {
	withCleanRegistry(t)

	_, err := ForURL("https://unknown.com")
	if err == nil {
		t.Fatal("expected error for unknown URL")
	}
}

func TestForURLReturnsFirst(t *testing.T) {
	a := &fakeScraper{id: "first", matchFn: func(string) bool { return true }}
	b := &fakeScraper{id: "second", matchFn: func(string) bool { return true }}
	withCleanRegistry(t, a, b)

	got, err := ForURL("https://anything.com")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID() != "first" {
		t.Errorf("ForURL should return first match, got %q", got.ID())
	}
}

func TestAll(t *testing.T) {
	a := &fakeScraper{id: "a"}
	b := &fakeScraper{id: "b"}
	c := &fakeScraper{id: "c"}
	withCleanRegistry(t, a, b, c)

	all := All()
	if len(all) != 3 {
		t.Fatalf("All() returned %d, want 3", len(all))
	}
	ids := make(map[string]bool)
	for _, s := range all {
		ids[s.ID()] = true
	}
	for _, want := range []string{"a", "b", "c"} {
		if !ids[want] {
			t.Errorf("All() missing %q", want)
		}
	}
}

func TestAllEmpty(t *testing.T) {
	withCleanRegistry(t)

	all := All()
	if len(all) != 0 {
		t.Errorf("All() on empty registry returned %d", len(all))
	}
}

func TestRegisterDuplicatePanics(t *testing.T) {
	old := registered
	registered = nil
	t.Cleanup(func() { registered = old })

	Register(&fakeScraper{id: "dup"})

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on duplicate ID")
		}
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, "dup") {
			t.Errorf("panic message = %v, want to contain 'dup'", r)
		}
	}()
	Register(&fakeScraper{id: "dup"})
}

func TestResultKindString(t *testing.T) {
	tests := []struct {
		kind ResultKind
		want string
	}{
		{KindScene, "Scene"},
		{KindError, "Error"},
		{KindTotal, "Total"},
		{KindStoppedEarly, "StoppedEarly"},
		{ResultKind(99), "ResultKind(99)"},
	}
	for _, tt := range tests {
		if got := tt.kind.String(); got != tt.want {
			t.Errorf("%d.String() = %q, want %q", tt.kind, got, tt.want)
		}
	}
}

// canonicalizingScraper is a fakeScraper that also implements
// StudioURLCanonicalizer.
type canonicalizingScraper struct {
	*fakeScraper
	preferFn func(string) string
}

func (c *canonicalizingScraper) PreferredStudioURL(u string) string { return c.preferFn(u) }

func TestPreferredStudioURL(t *testing.T) {
	alias := &canonicalizingScraper{
		fakeScraper: &fakeScraper{id: "alias", matchFn: func(u string) bool {
			return strings.HasPrefix(u, "https://alias.com/")
		}},
		preferFn: func(u string) string {
			if u == "https://alias.com/vanity" {
				return "https://alias.com/creators/vanity"
			}
			return ""
		},
	}
	plain := &fakeScraper{id: "plain", matchFn: func(u string) bool {
		return strings.HasPrefix(u, "https://plain.com/")
	}}
	withCleanRegistry(t, alias, plain)

	cases := []struct {
		name string
		url  string
		want string
	}{
		{"rewrites the alias", "https://alias.com/vanity", "https://alias.com/creators/vanity"},
		{"no preference expressed", "https://alias.com/creators/vanity", "https://alias.com/creators/vanity"},
		{"scraper does not implement it", "https://plain.com/studio", "https://plain.com/studio"},
		{"no scraper claims the URL", "https://unknown.com/studio", "https://unknown.com/studio"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := PreferredStudioURL(c.url); got != c.want {
				t.Errorf("PreferredStudioURL(%q) = %q, want %q", c.url, got, c.want)
			}
		})
	}
}
