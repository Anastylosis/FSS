//go:build integration

package transexhot

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveTransexHot(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://transexhot.com.br", 3)
}

func TestLiveTransexHotCategory(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://transexhot.com.br/categoria/travesti-loiras/", 3)
}

func TestLiveTransexHotPerformer(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://transexhot.com.br/travestis/anita-evelyn/", 1)
}
