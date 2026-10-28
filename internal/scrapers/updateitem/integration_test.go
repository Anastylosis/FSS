//go:build integration

package updateitem

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func live(t *testing.T, id string) {
	t.Helper()
	for _, cfg := range sites {
		if cfg.SiteID != id {
			continue
		}
		testutil.RunLiveScrape(t, newScraper(cfg), "https://"+cfg.Domain, 3)
		return
	}
	t.Fatalf("unknown site %q", id)
}

func TestLiveSheSeducedMe(t *testing.T)     { live(t, "sheseducedme") }
func TestLiveLesbianSexuality(t *testing.T) { live(t, "lesbiansexuality") }
