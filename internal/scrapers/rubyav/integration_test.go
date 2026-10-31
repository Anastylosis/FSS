//go:build integration

package rubyav

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveRubyAV(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.ruby-av.com/", 3)
}

func TestLiveRubyAVMaker(t *testing.T) {
	testutil.RunLiveScrape(t, New(), "https://www.ruby-av.com/search.php?search_flag=dvd&mkr=ILLUMINATI", 3)
}
