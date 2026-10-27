//go:build integration

package vrproduce

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveVRProduce(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "http://www.vr-produce.co.jp/", 3)
}

func TestLiveVRProduceVRGenre(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "http://www.vr-produce.co.jp/genrecat/vr/", 3)
}
