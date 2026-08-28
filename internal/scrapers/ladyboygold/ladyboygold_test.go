package ladyboygold

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/natscmsutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url   string
		match bool
	}{
		{"https://ladyboygold.com", true},
		{"https://www.ladyboygold.com/", true},
		{"http://ladyboygold.com/tour/trailer/x/", true},
		{"https://ladyboygold.net/", false},
		{"", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.match {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.match)
		}
	}
}

func TestID(t *testing.T) {
	if got := New().ID(); got != "ladyboygold" {
		t.Errorf("ID() = %q", got)
	}
}

// The set_list block mixes photo sets in with videos and offers no type field,
// so the content path is the only way to tell them apart. Getting this wrong
// silently files ~1800 photo sets as scenes.
func TestSkipPathRe(t *testing.T) {
	photoPaths := []string{
		"lbg/00lfb/photos/some_set/",
		"lbg/00lfb/4kphotos/snack_miniskirt4k/",
		"lbg/00lfb/photos4k/x/",
		"lbg/00lfb/candid_photos/y/",
	}
	for _, p := range photoPaths {
		if !skipPathRe.MatchString(p) {
			t.Errorf("photo path %q was not skipped", p)
		}
	}

	videoPaths := []string{
		"lbg/00lfb/videos/x/",
		"lbg/00lfb/4kvideos/snack_skintightminiskirt4k/",
		"lbg/00lfb/videos4k/x/",
		"lbg/00lfb/remastered4k/x/",
		"lbg/00lfb/ladyboys/om/",
		"lbg/00lfb/gogoladyboys/x/",
		"lbg/00lfb/globalshemales/x/",
		"lbg/00lfb/hotspots/hot-tuna/",
		"lbg/00lfb/candid_videos/x/",
	}
	for _, p := range videoPaths {
		if skipPathRe.MatchString(p) {
			t.Errorf("video path %q was wrongly skipped", p)
		}
	}
}

func TestSiblingsAreDistinctAndRegistered(t *testing.T) {
	ids := map[string]bool{"ladyboygold": true}
	for _, cfg := range siblings {
		if ids[cfg.ID] {
			t.Errorf("duplicate site id %q", cfg.ID)
		}
		ids[cfg.ID] = true
		if cfg.CMSAreaID == "" || cfg.SiteBase == "" || cfg.SiteName == "" {
			t.Errorf("%s: incomplete config %+v", cfg.ID, cfg)
		}
		got, err := scraper.ForURL(cfg.SiteBase + "/")
		if err != nil {
			t.Errorf("ForURL(%s): %v", cfg.SiteBase, err)
			continue
		}
		if got.ID() != cfg.ID {
			t.Errorf("ForURL(%s) = %s, want %s", cfg.SiteBase, got.ID(), cfg.ID)
		}
	}
	if len(siblings) != 5 {
		t.Errorf("expected 5 sibling sites, got %d", len(siblings))
	}
}

// The siblings share ladyboygold.com's mixed photo/video set list, so they
// must inherit the same filter — and their studio name must be their own, not
// the network's.
func TestWithDefaults(t *testing.T) {
	cfg := withDefaults(siblings[0])
	if cfg.NatsAPIBase != natsAPIBase {
		t.Errorf("NatsAPIBase = %q", cfg.NatsAPIBase)
	}
	if cfg.SkipPathRe == nil || !cfg.SkipPathRe.MatchString("lbg/00lfb/4kphotos/x/") {
		t.Error("SkipPathRe not inherited")
	}
	if cfg.StudioName != cfg.SiteName {
		t.Errorf("StudioName = %q, want %q", cfg.StudioName, cfg.SiteName)
	}
	if len(cfg.Patterns) != 1 || cfg.Patterns[0] != "www.tsraw.com" {
		t.Errorf("Patterns = %v", cfg.Patterns)
	}
}

func TestSiblingMatchIsHostAnchored(t *testing.T) {
	s := natscmsutil.New(withDefaults(siblings[0]))
	for _, u := range []string{"https://tsraw.com/", "https://www.tsraw.com", "http://tsraw.com/tour/"} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://tsraw.net/", "https://nottsraw.com/", "https://example.com/tsraw.com/"} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}
