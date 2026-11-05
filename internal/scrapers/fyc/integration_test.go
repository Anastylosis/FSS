//go:build integration

package fyc

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/fycutil"
	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLivePassionHD(t *testing.T) {
	const url = "https://passion-hd.com"
	testutil.SkipIfPlaceholder(t, url)
	s := fycutil.New(sites[0])
	testutil.RunLiveScrape(t, s, url, 2)
}

func TestLiveTiny4K(t *testing.T) {
	const url = "https://tiny4k.com"
	testutil.SkipIfPlaceholder(t, url)
	s := fycutil.New(sites[1])
	testutil.RunLiveScrape(t, s, url, 2)
}

func TestLiveBaeb(t *testing.T) {
	s := fycutil.New(sites[len(sites)-1])
	testutil.RunLiveScrape(t, s, "https://baeb.com/", 3)
}
