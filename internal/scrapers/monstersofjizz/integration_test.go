//go:build integration

package monstersofjizz

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/elxupdateutil"
	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	testutil.RunLiveScrape(t, elxupdateutil.New(site), "https://monstersofjizz.com/", 3)
}
