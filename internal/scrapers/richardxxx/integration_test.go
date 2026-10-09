//go:build integration

package richardxxx

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.richard.xxx/new-porn-videos", 3)
}

func TestLiveScrapeBrands(t *testing.T) {
	for _, slug := range []string{
		"my-brothers-hot-friend", "hot-jocks-nice-cocks", "im-a-married-man", "men-hard-at-work",
		"straight-seduction", "flip-fuckers", "cocksuckers", "tonight-s-boyriend",
	} {
		t.Run(slug, func(t *testing.T) {
			testutil.RunLiveScrape(t, New(), "https://www.richard.xxx/site/"+slug, 3)
		})
	}
}

func TestLiveScrapePerformer(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.richard.xxx/pornstar/aston-springs", 1)
}
