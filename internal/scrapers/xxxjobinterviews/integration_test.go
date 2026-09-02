//go:build integration

package xxxjobinterviews

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://xxxjobinterviews.com/", 4)
}
