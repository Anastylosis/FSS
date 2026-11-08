package goonmuse

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/latestupdateutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestRegistered(t *testing.T) {
	got, err := scraper.ForURL("https://goonmuse.com/")
	if err != nil {
		t.Fatalf("ForURL: %v", err)
	}
	if got.ID() != "goonmuse" {
		t.Errorf("ForURL = %s", got.ID())
	}
}

func TestMatchesURL(t *testing.T) {
	s := latestupdateutil.New(site)
	for _, u := range []string{
		"https://goonmuse.com/",
		"https://www.goonmuse.com/categories/movies.html",
		"https://goonmuse.com/models/x.html",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	if s.MatchesURL("https://notgoonmuse.com/") {
		t.Error("matched foreign host")
	}
}
