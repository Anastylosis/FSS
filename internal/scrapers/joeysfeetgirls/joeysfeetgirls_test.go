package joeysfeetgirls

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/latestupdateutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestRegistered(t *testing.T) {
	got, err := scraper.ForURL("https://joeysfeetgirls.com/")
	if err != nil {
		t.Fatalf("ForURL: %v", err)
	}
	if got.ID() != "joeysfeetgirls" {
		t.Errorf("ForURL = %s", got.ID())
	}
}

func TestMatchesOwnDomainOnly(t *testing.T) {
	s := latestupdateutil.New(site)
	for _, u := range []string{"https://joeysfeetgirls.com/", "https://www.joeysfeetgirls.com/categories/movies.html"} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://other-joeysfeetgirls.com/", "https://notjoeysfeetgirls.com/", "https://example.com/joeysfeetgirls.com/"} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}
