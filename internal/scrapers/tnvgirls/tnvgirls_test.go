package tnvgirls

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/elxupdateutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestRegistered(t *testing.T) {
	got, err := scraper.ForURL("https://www.tnvgirls.com/tour/")
	if err != nil {
		t.Fatalf("ForURL: %v", err)
	}
	if got.ID() != "tnvgirls" {
		t.Errorf("ForURL = %s", got.ID())
	}
}

func TestMatchesURL(t *testing.T) {
	s := elxupdateutil.New(site)
	if !s.MatchesURL("https://tnvgirls.com/tour/categories/movies.html") {
		t.Error("did not match own URL")
	}
	if s.MatchesURL("https://nottnvgirls.com/") {
		t.Error("matched foreign host")
	}
}
