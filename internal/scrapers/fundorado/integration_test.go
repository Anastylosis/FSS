//go:build integration

package fundorado

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

func liveScrape(t *testing.T, url, id string) {
	t.Helper()
	s, err := scraper.ForURL(url)
	if err != nil {
		t.Fatalf("no scraper matched %s: %v", url, err)
	}
	if s.ID() != id {
		t.Fatalf("expected %s, got %s", id, s.ID())
	}
	testutil.RunLiveScrape(t, s, url, 3)
}

func TestLiveRubberEmpire(t *testing.T) {
	liveScrape(t, "https://rubber-empire.com", "rubberempire")
}

func TestLiveRubberEmpireLabel(t *testing.T) {
	liveScrape(t, "https://rubber-empire.com/videos/browse/labels/751", "rubberempire")
}

func TestLiveRubberEmpireGenre(t *testing.T) {
	liveScrape(t, "https://rubber-empire.com/videos/browse/genres/236", "rubberempire")
}
