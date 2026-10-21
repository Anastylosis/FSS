//go:build integration

package fundorado

import (
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

// The API allows 240 requests a minute per IP and a limit-3 scrape still
// fetches a page of details, so live runs are spaced out.
var lastLive time.Time

func liveScrape(t *testing.T, url, id string) {
	t.Helper()
	if wait := 15*time.Second - time.Since(lastLive); !lastLive.IsZero() && wait > 0 {
		time.Sleep(wait)
	}
	defer func() { lastLive = time.Now() }()
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

func TestLiveOrgasmAbuse(t *testing.T) {
	liveScrape(t, "https://orgasmabuse.com", "orgasmabuse")
}

func TestLiveOrgasmAbuseStudio(t *testing.T) {
	liveScrape(t, "https://orgasmabuse.com/videos/browse/studio/2354", "orgasmabuse")
}

func TestLiveTAWorship(t *testing.T) {
	liveScrape(t, "https://taworship.com", "taworship")
}

func TestLiveTAWorshipLabel(t *testing.T) {
	liveScrape(t, "https://taworship.com/videos/browse/labels/612", "taworship")
}

func TestLiveTickleAbuse(t *testing.T) {
	liveScrape(t, "https://tickleabuse.com", "tickleabuse")
}

func TestLiveRFMovies(t *testing.T) {
	liveScrape(t, "https://rfmovies.com", "rfmovies")
}

func TestLiveTexasBukkake(t *testing.T) {
	liveScrape(t, "https://texasbukkake.com", "texasbukkake")
}
