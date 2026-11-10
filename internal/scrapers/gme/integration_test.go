//go:build integration

package gme

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveLabel(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "http://www.gme-inc.net/product_list.html?label_id=629969", 3)
}

func TestLiveAnnexLabel(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "http://www.gme-inc.net/product_list.html?label_id=603302", 3)
}
