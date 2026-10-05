package darkkotv

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/latestupdateutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestRegistered(t *testing.T) {
	got, err := scraper.ForURL("https://darkkotv.com/")
	if err != nil {
		t.Fatalf("ForURL: %v", err)
	}
	if got.ID() != "darkkotv" {
		t.Errorf("ForURL = %s", got.ID())
	}
}

func TestMatchesURL(t *testing.T) {
	s := latestupdateutil.New(site)
	for _, u := range []string{
		"https://darkkotv.com/",
		"https://www.darkkotv.com/categories/movies.html",
		"https://darkkotv.com/models/x.html",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{
		"https://notdarkkotv.com/",
		"https://example.com/darkkotv.com/",
	} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}
