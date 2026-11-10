//go:build integration

package hibino

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveHibino(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://av-hibino.com/web/bbs/board.php?bo_table=works", 3)
}
