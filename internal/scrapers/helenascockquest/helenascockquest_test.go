package helenascockquest

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/latestupdateutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestRegistered(t *testing.T) {
	got, err := scraper.ForURL("https://experiencehelenaprice.com/")
	if err != nil {
		t.Fatalf("ForURL: %v", err)
	}
	if got.ID() != "helenascockquest" {
		t.Errorf("ForURL = %s", got.ID())
	}
}

func TestMatchesOwnDomainOnly(t *testing.T) {
	s := latestupdateutil.New(site)
	for _, u := range []string{"https://experiencehelenaprice.com/", "https://www.experiencehelenaprice.com/categories/movies.html"} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://helenaprice.com/", "https://notexperiencehelenaprice.com/", "https://example.com/experiencehelenaprice.com/"} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}
