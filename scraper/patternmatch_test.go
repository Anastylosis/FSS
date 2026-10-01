package scraper

import (
	"context"
	"testing"
)

func TestPatternMatchesURL(t *testing.T) {
	cases := []struct {
		pattern, url string
		want         bool
	}{
		// Host, ignoring www and scheme.
		{"cumlouder.com/girl/{slug}", "https://www.cumlouder.com/girl/aletta-ocean", true},
		{"cumlouder.com/girl/{slug}", "http://cumlouder.com/girl/aletta-ocean", true},
		{"cumlouder.com/girl/{slug}", "https://example.com/girl/aletta-ocean", false},

		// Segment count is part of the shape.
		{"cumlouder.com/girl/{slug}", "https://cumlouder.com/girl/aletta-ocean/2", false},
		{"cumlouder.com/site/{slug}", "https://cumlouder.com/girl/aletta-ocean", false},

		// A bare host pattern is the catalogue, not a filtered view.
		{"cumlouder.com", "https://cumlouder.com/", true},
		{"cumlouder.com", "https://cumlouder.com/girl/x", false},

		// Placeholders inside a segment keep their literal runs.
		{"adultdoorway.com/tour/categories/{slug}_{page}_d.html",
			"https://adultdoorway.com/tour/categories/movies_1_d.html", true},
		{"adultdoorway.com/tour/categories/{slug}_{page}_d.html",
			"https://adultdoorway.com/tour/categories/movies.html", false},

		// Query keys the pattern names must be present; a literal value must match.
		{"adultprime.com/studios/videos?website=Arousins",
			"https://adultprime.com/studios/videos?website=Arousins", true},
		{"adultprime.com/studios/videos?website=Arousins",
			"https://adultprime.com/studios/videos?website=Other", false},
		{"forbiddenfruitsfilms.com/shop-streaming-video-by-scene.html?studio={id}",
			"https://forbiddenfruitsfilms.com/shop-streaming-video-by-scene.html?studio=42", true},
		{"adultprime.com/studios/videos?website=Arousins",
			"https://adultprime.com/studios/videos", false},

		{"", "https://example.com/x", false},
		{"example.com/a", "::not a url::", false},
	}
	for _, c := range cases {
		if got := PatternMatchesURL(c.pattern, c.url); got != c.want {
			t.Errorf("PatternMatchesURL(%q, %q) = %v, want %v", c.pattern, c.url, got, c.want)
		}
	}
}

type patternScraper struct{ patterns []string }

func (p *patternScraper) ID() string         { return "pattern" }
func (p *patternScraper) Patterns() []string { return p.patterns }
func (p *patternScraper) MatchesURL(string) bool {
	return true
}
func (p *patternScraper) ListScenes(context.Context, string, ListOpts) (<-chan SceneResult, error) {
	ch := make(chan SceneResult)
	close(ch)
	return ch, nil
}

func TestURLLooksUnhandled(t *testing.T) {
	s := &patternScraper{patterns: []string{
		"cumlouder.com",
		"cumlouder.com/girl/{slug}",
		"cumlouder.com/site/{slug}",
	}}

	cases := []struct {
		url  string
		want bool
	}{
		// The catalogue itself is never a fallthrough.
		{"https://cumlouder.com/", false},
		{"https://www.cumlouder.com", false},
		// Advertised filtered views.
		{"https://cumlouder.com/girl/aletta-ocean", false},
		{"https://cumlouder.com/site/cumlouder", false},
		// A filtered view the scraper never claimed: this is the case that
		// silently scrapes the whole catalogue under the filter's store key.
		{"https://cumlouder.com/pornstar/aletta-ocean", true},
		{"https://cumlouder.com/girl/aletta-ocean/2", true},
	}
	for _, c := range cases {
		if got := URLLooksUnhandled(s, c.url); got != c.want {
			t.Errorf("URLLooksUnhandled(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

// A scraper that advertises no non-root pattern at all accepts only its
// catalogue, so any path is a fallthrough.
func TestURLLooksUnhandledWithNoFilteredPatterns(t *testing.T) {
	s := &patternScraper{patterns: []string{"example.com"}}
	if !URLLooksUnhandled(s, "https://example.com/models/jane") {
		t.Error("a path on a scraper advertising none must look unhandled")
	}
	if URLLooksUnhandled(s, "https://example.com/") {
		t.Error("the bare catalogue must not look unhandled")
	}
}

type recognizingScraper struct {
	patternScraper
	recognizes string
}

func (r *recognizingScraper) RecognizesURL(u string) bool { return u == r.recognizes }

// A scraper whose rows are defined by a URL form cannot express that as a
// display pattern, so it may vouch for the URL directly.
func TestURLLooksUnhandledHonoursTheRecognizer(t *testing.T) {
	const vouched = "https://example.com/en/studio/adult-time"
	s := &recognizingScraper{
		patternScraper: patternScraper{patterns: []string{"example.com"}},
		recognizes:     vouched,
	}
	if URLLooksUnhandled(s, vouched) {
		t.Error("a vouched URL must not read as a fallthrough")
	}
	if !URLLooksUnhandled(s, "https://example.com/en/channel/other") {
		t.Error("an unvouched path with no matching pattern must still warn")
	}
}
