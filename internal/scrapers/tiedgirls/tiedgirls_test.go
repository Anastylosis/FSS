package tiedgirls

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/latestupdateutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestRegistered(t *testing.T) {
	got, err := scraper.ForURL("https://tiedgirls.com/")
	if err != nil {
		t.Fatalf("ForURL: %v", err)
	}
	if got.ID() != "tiedgirls" {
		t.Errorf("ForURL = %s", got.ID())
	}
}

func TestMatchesURL(t *testing.T) {
	s := latestupdateutil.New(site)
	for _, u := range []string{
		"https://tiedgirls.com/",
		"https://www.tiedgirls.com/categories/movies.html",
		"https://tiedgirls.com/models/x.html",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	if s.MatchesURL("https://nottiedgirls.com/") {
		t.Error("matched foreign host")
	}
}
