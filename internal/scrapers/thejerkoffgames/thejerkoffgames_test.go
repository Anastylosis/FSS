package thejerkoffgames

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/latestupdateutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestRegistered(t *testing.T) {
	got, err := scraper.ForURL("https://thejerkoffmembers.com/")
	if err != nil {
		t.Fatalf("ForURL: %v", err)
	}
	if got.ID() != "thejerkoffgames" {
		t.Errorf("ForURL = %s", got.ID())
	}
}

func TestMatchesURL(t *testing.T) {
	s := latestupdateutil.New(site)
	for _, u := range []string{
		"https://thejerkoffmembers.com/",
		"https://www.thejerkoffmembers.com/categories/movies.html",
		"https://thejerkoffgames.com/",
		"https://thejoigames.com/",
		"https://thejerkoffmembers.com/models/x.html",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{
		"https://notthejerkoffmembers.com/",
		"https://example.com/thejerkoffmembers.com/",
	} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}
