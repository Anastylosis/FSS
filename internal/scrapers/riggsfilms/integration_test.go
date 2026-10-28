//go:build integration

package riggsfilms

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveRiggsFilms(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://riggsfilms.com", 3)
}

func TestLiveRiggsFilmsPerformer(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://riggsfilms.com/pornstars/coco-bae/", 2)
}
