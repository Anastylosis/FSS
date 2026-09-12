package scraper

import (
	"context"
	"strings"
	"testing"
)

type langScraper struct {
	id    string
	langs []string
}

func (s *langScraper) ID() string             { return s.id }
func (s *langScraper) Patterns() []string     { return nil }
func (s *langScraper) MatchesURL(string) bool { return false }
func (s *langScraper) ListScenes(context.Context, string, ListOpts) (<-chan SceneResult, error) {
	return nil, nil
}
func (s *langScraper) Languages() []string { return s.langs }

type plainScraper struct{ id string }

func (s *plainScraper) ID() string             { return s.id }
func (s *plainScraper) Patterns() []string     { return nil }
func (s *plainScraper) MatchesURL(string) bool { return false }
func (s *plainScraper) ListScenes(context.Context, string, ListOpts) (<-chan SceneResult, error) {
	return nil, nil
}

func TestNormalizeLanguage(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", "", false},
		{"  ", "", false},
		{"DE", "de", false},
		{" pt-BR ", "pt-br", false},
		{"zh-Hant", "zh-hant", false},
		{"english", "", true},
		{"d", "", true},
		{"de_DE", "", true},
		{"de;drop", "", true},
	}
	for _, c := range cases {
		got, err := NormalizeLanguage(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("NormalizeLanguage(%q) error = %v, wantErr %v", c.in, err, c.wantErr)
			continue
		}
		if got != c.want {
			t.Errorf("NormalizeLanguage(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestResolveLanguageSupported(t *testing.T) {
	sc := &langScraper{id: "acme", langs: []string{"en", "de"}}
	got, err := ResolveLanguage(sc, "DE")
	if err != nil {
		t.Fatalf("ResolveLanguage: %v", err)
	}
	if got != "de" {
		t.Errorf("got %q, want %q", got, "de")
	}
}

func TestResolveLanguageUnsupportedNamesTheSet(t *testing.T) {
	sc := &langScraper{id: "acme", langs: []string{"en", "de"}}
	_, err := ResolveLanguage(sc, "ja")
	if err == nil {
		t.Fatal("want an error for a language the site does not serve")
	}
	if !strings.Contains(err.Error(), "en, de") {
		t.Errorf("error should name the supported set, got: %v", err)
	}
}

// A site with one language is not an error: an --all-creators run naming a
// language must not fail on every storefront that has no choice to make.
func TestResolveLanguageIgnoredByMonolingualScraper(t *testing.T) {
	got, err := ResolveLanguage(&plainScraper{id: "acme"}, "de")
	if err != nil {
		t.Fatalf("ResolveLanguage: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want the scraper default", got)
	}
}

func TestResolveLanguageEmptyMeansScraperDefault(t *testing.T) {
	got, err := ResolveLanguage(&langScraper{id: "acme", langs: []string{"en"}}, "")
	if err != nil || got != "" {
		t.Fatalf("got %q, %v; want \"\", nil", got, err)
	}
}

func TestResolveLanguageRejectsMalformedTag(t *testing.T) {
	if _, err := ResolveLanguage(&langScraper{id: "acme", langs: []string{"en"}}, "not a language"); err == nil {
		t.Fatal("want an error for a malformed tag")
	}
}

func TestLanguagesFor(t *testing.T) {
	if got := LanguagesFor(&plainScraper{id: "acme"}); got != nil {
		t.Errorf("got %v, want nil for a scraper with no language dimension", got)
	}
	if got := LanguagesFor(&langScraper{id: "acme", langs: []string{"en"}}); len(got) != 1 {
		t.Errorf("got %v, want [en]", got)
	}
}
