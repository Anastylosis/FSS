//go:build integration

package modelcentro

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestLiveThejerkygirls(t *testing.T) {
	url := "https://thejerkygirls.com/videos"
	s, err := scraper.ForURL(url)
	if err != nil {
		t.Fatalf("no scraper matched %s: %v", url, err)
	}
	if s.ID() != "thejerkygirls" {
		t.Fatalf("expected thejerkygirls, got %s", s.ID())
	}
	testutil.RunLiveScrape(t, s, url, 2)
}

func TestLiveMugurporn(t *testing.T) {
	url := "https://mugurporn.com/videos"
	s, err := scraper.ForURL(url)
	if err != nil {
		t.Fatalf("no scraper matched %s: %v", url, err)
	}
	if s.ID() != "mugurporn" {
		t.Fatalf("expected mugurporn, got %s", s.ID())
	}
	testutil.RunLiveScrape(t, s, url, 2)
}

func TestLiveBrookelynnebriar(t *testing.T) {
	url := "https://brookelynnebriar.com/videos"
	s, err := scraper.ForURL(url)
	if err != nil {
		t.Fatalf("no scraper matched %s: %v", url, err)
	}
	if s.ID() != "brookelynnebriar" {
		t.Fatalf("expected brookelynnebriar, got %s", s.ID())
	}
	testutil.RunLiveScrape(t, s, url, 2)
}

func TestLiveKinkyrubberworld(t *testing.T) {
	url := "https://www.kinkyrubberworld.com/videos"
	s, err := scraper.ForURL(url)
	if err != nil {
		t.Fatalf("no scraper matched %s: %v", url, err)
	}
	if s.ID() != "kinkyrubberworld" {
		t.Fatalf("expected kinkyrubberworld, got %s", s.ID())
	}
	testutil.RunLiveScrape(t, s, url, 2)
}

func TestLiveNaughtylada(t *testing.T) {
	url := "https://naughty-lada.com/videos"
	s, err := scraper.ForURL(url)
	if err != nil {
		t.Fatalf("no scraper matched %s: %v", url, err)
	}
	if s.ID() != "naughtylada" {
		t.Fatalf("expected naughtylada, got %s", s.ID())
	}
	testutil.RunLiveScrape(t, s, url, 2)
}

func TestLiveGoddessdiane(t *testing.T) {
	url := "https://goddessdiane.com/videos"
	s, err := scraper.ForURL(url)
	if err != nil {
		t.Fatalf("no scraper matched %s: %v", url, err)
	}
	if s.ID() != "goddessdiane" {
		t.Fatalf("expected goddessdiane, got %s", s.ID())
	}
	testutil.RunLiveScrape(t, s, url, 2)
}

func TestLiveNewSites(t *testing.T) {
	for _, id := range []string{
		"aussiefellatioqueens", "arabellesplayground", "samantalily", "jenysmith", "rebeccalordproductions",
	} {
		id := id
		t.Run(id, func(t *testing.T) {
			var domain string
			for _, c := range sites {
				if c.SiteID == id {
					domain = c.Domain
				}
			}
			url := "https://" + domain + "/videos"
			s, err := scraper.ForURL(url)
			if err != nil {
				t.Fatalf("no scraper matched %s: %v", url, err)
			}
			testutil.RunLiveScrape(t, s, url, 2)
		})
	}
}
