package xbrats

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/latestupdateutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestRegistered(t *testing.T) {
	got, err := scraper.ForURL("https://xbrats.com/")
	if err != nil {
		t.Fatalf("ForURL: %v", err)
	}
	if got.ID() != "xbrats" {
		t.Errorf("ForURL = %s", got.ID())
	}
}

func TestMatchesURL(t *testing.T) {
	s := latestupdateutil.New(site)
	for _, u := range []string{
		"https://xbrats.com/",
		"https://www.xbrats.com/categories/movies.html",
		"https://xbrats.com/models/AloraJaymes.html",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{
		"https://notxbrats.com/",
		"https://example.com/xbrats.com/",
	} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}
