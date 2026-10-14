//go:build integration

package giga

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.giga-web.jp/", 3)
}

func TestLiveScrapeActor(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.giga-web.jp/search/index.php?actor_id=4014", 3)
}

func TestLiveScrapeLabel(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.giga-web.jp/search/index.php?label_id=1", 3)
}

func TestLiveScrapeTag(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.giga-web.jp/search/index.php?tag_id=130", 3)
}
