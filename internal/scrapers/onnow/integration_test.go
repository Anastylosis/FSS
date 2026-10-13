//go:build integration

package onnow

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func site(t *testing.T, id string) *Scraper {
	t.Helper()
	for _, c := range sites {
		if c.ID == id {
			return New(c)
		}
	}
	t.Fatalf("unknown site %q", id)
	return nil
}

func TestLiveHardBrazil(t *testing.T) {
	testutil.RunLiveScrape(t, site(t, "hardbrazil"), "https://hardbrazil.com.br/videos", 3)
}

func TestLiveHardBrazilCategory(t *testing.T) {
	testutil.RunLiveScrape(t, site(t, "hardbrazil"), "https://hardbrazil.com.br/videos/anal", 3)
}

func TestLiveIrmaosDotados(t *testing.T) {
	testutil.RunLiveScrape(t, site(t, "irmaosdotados"), "https://irmaosdotados.com.br/videos", 3)
}

func TestLiveIrmaosDotadosPagedCategory(t *testing.T) {
	testutil.RunLiveScrape(t, site(t, "irmaosdotados"), "https://irmaosdotados.com.br/videos/movies", 25)
}

func TestLiveIrmaosDotadosCategory(t *testing.T) {
	testutil.RunLiveScrape(t, site(t, "irmaosdotados"), "https://irmaosdotados.com.br/videos/solo", 3)
}
