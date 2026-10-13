//go:build integration

package riggsfilms

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveRiggsFilms(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://riggsfilms.com", 3)
}

func TestLiveRiggsFilmsCategory(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://riggsfilms.com/category/katija-cortez/", 3)
}

func TestLiveRiggsFilmsModel(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://riggsfilms.com/coco-bae/", 3)
}

func TestLiveRiggsFilmsTag(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://riggsfilms.com/tag/lesbian/", 3)
}
