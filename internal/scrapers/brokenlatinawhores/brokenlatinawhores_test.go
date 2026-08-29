package brokenlatinawhores

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/elxupdateutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestRegistered(t *testing.T) {
	got, err := scraper.ForURL("https://brokenlatinawhores.com/")
	if err != nil {
		t.Fatalf("ForURL: %v", err)
	}
	if got.ID() != "brokenlatinawhores" {
		t.Errorf("ForURL = %s", got.ID())
	}
}

func TestMatchesOwnDomainOnly(t *testing.T) {
	s := elxupdateutil.New(site)
	for _, u := range []string{
		"https://brokenlatinawhores.com/",
		"https://www.brokenlatinawhores.com/categories/updates_2_d.html",
		"http://brokenlatinawhores.com",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://brokenlatinawhores.net/", "https://notbrokenlatinawhores.com/", "https://example.com/brokenlatinawhores.com/"} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

// The site serves from the apex and mounts the tour at the root, so every
// derived URL must carry no www and no /tour.
func TestURLsHaveNoTourPrefix(t *testing.T) {
	s := elxupdateutil.New(site)
	if got := s.Patterns()[1]; got != "brokenlatinawhores.com/categories/{category}.html" {
		t.Errorf("Patterns()[1] = %q", got)
	}
}
