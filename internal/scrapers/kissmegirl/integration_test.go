//go:build integration

package kissmegirl

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveKissMeGirl(t *testing.T) {
	cases := map[string]string{
		"kissmegirl":         "https://www.kissmegirl.com/welcome.html",
		"kissmegirlexplicit": "https://www.kissmegirlexplicit.com/welcome.html",
		"sloppygirl":         "https://www.sloppygirl.com/welcome.html",
	}
	for _, cfg := range sites {
		t.Run(cfg.ID, func(t *testing.T) {
			testutil.RunLiveScrape(t, New(cfg), cases[cfg.ID], 3)
		})
	}
}
