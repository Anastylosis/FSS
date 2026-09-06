//go:build integration

package adultempirestore

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveForbiddenFruitsFilms(t *testing.T) {
	testutil.RunLiveScrape(t, New(sites[0]), "https://www.forbiddenfruitsfilms.com/", 3)
}

func TestLiveRodneyMoore(t *testing.T) {
	testutil.RunLiveScrape(t, New(sites[1]), "https://rodneymoorestore.com/", 3)
}
