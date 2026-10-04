//go:build integration

package islandstuds

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveIslandStuds(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.islandstuds.com/tour/categories/movies_1_d.html", 3)
}

func TestLiveIslandStudsCategory(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.islandstuds.com/tour/categories/NakedSurfers.html", 3)
}

func TestLiveIslandStudsModel(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.islandstuds.com/tour/models/Lennon.html", 1)
}
