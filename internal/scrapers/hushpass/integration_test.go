//go:build integration

package hushpass

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/darkreachmodernutil"
	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLive(t *testing.T) {
	testutil.RunLiveScrape(t, darkreachmodernutil.New(darkreachmodernutil.SiteConfig{
		ID:         "hushpass",
		SiteBase:   "https://hushpass.com",
		Studio:     "Hush Pass",
		TourPrefix: "/t1",
		MatchRe:    matchRe,
	}), "https://hushpass.com/", 3)
}
