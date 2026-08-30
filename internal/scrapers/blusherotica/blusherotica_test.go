package blusherotica

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/latestupdateutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestRegistered(t *testing.T) {
	got, err := scraper.ForURL("https://blusheroticavr.com/")
	if err != nil {
		t.Fatalf("ForURL: %v", err)
	}
	if got.ID() != "blusheroticavr" {
		t.Errorf("ForURL = %s", got.ID())
	}
}

func TestMatchesOwnDomainOnly(t *testing.T) {
	s := latestupdateutil.New(site)
	for _, u := range []string{"https://blusheroticavr.com/", "https://www.blusheroticavr.com/categories/movies.html"} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://blusherotica.com/", "https://notblusheroticavr.com/", "https://example.com/blusheroticavr.com/"} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}
