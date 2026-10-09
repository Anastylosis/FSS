package trueamateurmodels

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/elxupdateutil"
)

func TestMatchesURL(t *testing.T) {
	s := elxupdateutil.New(site)
	cases := map[string]bool{
		"https://www.trueamateurmodels.com/previewtour/":                       true,
		"https://trueamateurmodels.com/previewtour/categories/movies_2_d.html": true,
		"https://example.com/trueamateurmodels.com/":                           false,
		"https://nottrueamateurmodels.com/":                                    false,
	}
	for u, want := range cases {
		if got := s.MatchesURL(u); got != want {
			t.Errorf("MatchesURL(%q) = %v, want %v", u, got, want)
		}
	}
}
