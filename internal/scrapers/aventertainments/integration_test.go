//go:build integration

package aventertainments

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveAVEntertainments(t *testing.T) {
	for name, u := range map[string]string{
		"catwalk":  "https://www.aventertainments.com/dvd/studio?lang=1&cat=29&studio=768",
		"redhot":   "https://www.aventertainments.com/dvd/studio?lang=1&culture=en-US&cat=29&studio=493",
		"kamikaze": "https://www.aventertainments.com/29/565/1/studio_products",
	} {
		t.Run(name, func(t *testing.T) { testutil.RunLiveScrape(t, New(), u, 3) })
	}
}
