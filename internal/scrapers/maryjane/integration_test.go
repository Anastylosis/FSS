//go:build integration

package maryjane

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveMaryJane(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "http://mary-jane.biz/mysite1/shouhinlist.html", 3)
}
