//go:build integration

package aromaplanning

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.aroma-p.com/", 3)
}

func TestLiveScrapeLabel(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.aroma-p.com/works/list/label/2/", 3)
}

func TestLiveScrapeGenre(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.aroma-p.com/works/list/genre/23/", 3)
}

func TestLiveScrapeDate(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.aroma-p.com/works/list/date/20260922/", 3)
}
