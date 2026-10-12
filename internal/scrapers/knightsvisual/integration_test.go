//go:build integration

package knightsvisual

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.knights-visual.com/", 3)
}

func TestLiveScrapeLabels(t *testing.T) {
	for _, label := range []string{"furasupi", "glum", "hakudakudou", "paraphilia", "trigger-j"} {
		t.Run(label, func(t *testing.T) {
			testutil.RunLiveScrape(t, New(), "https://www.knights-visual.com/works/"+label+"/", 3)
		})
	}
}

func TestLiveScrapeSeries(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.knights-visual.com/works/furasupi/nonstop/", 3)
}
