//go:build integration

package bellesa

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.bellesa.co/videos?providers=bellesa-films", 5)
}
