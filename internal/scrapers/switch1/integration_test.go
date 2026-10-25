//go:build integration

package switch1

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://switch1.jp/", 3)
}

func TestLiveScrapeYear(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://switch1.jp/web/bbs/board.php?bo_table=works&sca=2023%E5%B9%B4", 3)
}
