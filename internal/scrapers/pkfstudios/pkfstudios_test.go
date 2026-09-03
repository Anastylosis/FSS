package pkfstudios

import (
	"testing"

	"github.com/Anastylosis/FSS/scraper"
)

func TestRegistered(t *testing.T) {
	got, err := scraper.ForURL("https://www.pkfstudios.com/updates/")
	if err != nil {
		t.Fatalf("ForURL: %v", err)
	}
	if got.ID() != "pkfstudios" {
		t.Errorf("ForURL = %s", got.ID())
	}
}

// The apex is an age-gate page; the catalogue lives under /updates/, and a
// bare-host URL must still resolve to this scraper.
func TestApexResolvesToo(t *testing.T) {
	got, err := scraper.ForURL("https://pkfstudios.com/")
	if err != nil {
		t.Fatalf("ForURL: %v", err)
	}
	if got.ID() != "pkfstudios" {
		t.Errorf("ForURL = %s", got.ID())
	}
}

func TestDoesNotClaimOtherHosts(t *testing.T) {
	s, err := scraper.ForID("pkfstudios")
	if err != nil {
		t.Fatalf("ForID: %v", err)
	}
	for _, u := range []string{"https://pkfstudios.net/", "https://notpkfstudios.com/", "https://example.com/pkfstudios.com/"} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}
