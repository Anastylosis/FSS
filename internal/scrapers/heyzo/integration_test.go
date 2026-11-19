//go:build integration

package heyzo

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveHeyzo(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.heyzo.com/", 2)
}

// A performer listing is the same walk over a different stem.
func TestLiveHeyzoActor(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.heyzo.com/listpages/actor_293_1", 2)
}

func TestLiveHeyzoCategory(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.heyzo.com/listpages/category_22_1", 2)
}
