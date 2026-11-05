package rippeddigital

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/latestupdateutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestRegistered(t *testing.T) {
	got, err := scraper.ForURL("https://ripped.digital/")
	if err != nil {
		t.Fatalf("ForURL: %v", err)
	}
	if got.ID() != "rippeddigital" {
		t.Errorf("ForURL = %s", got.ID())
	}
}

func TestMatchesOwnDomainOnly(t *testing.T) {
	s := latestupdateutil.New(site)
	for _, u := range []string{"https://ripped.digital/", "https://www.ripped.digital/categories/movies.html"} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://other-ripped.digital/", "https://notripped.digital/", "https://example.com/ripped.digital/"} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}
