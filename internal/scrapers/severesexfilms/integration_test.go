//go:build integration

package severesexfilms

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/ghostpro"
	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	testutil.RunLiveScrape(t, ghostpro.New(site), "https://www.severesexfilms.com/", 3)
}
