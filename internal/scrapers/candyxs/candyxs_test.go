package candyxs

import "testing"

func TestMatchesURL(t *testing.T) {
	s := New()
	tests := []struct {
		url  string
		want bool
	}{
		{"https://mymember.site/candyxs", true},
		{"https://mymember.site/candyxs/385-videos", true},
		{"https://www.candyxs.com/", true},
		{"https://candyxs.com", true},
		// Another instance on the same platform is a different studio.
		{"https://mymember.site/someoneelse", false},
		{"https://example.com/", false},
	}
	for _, tt := range tests {
		if got := s.MatchesURL(tt.url); got != tt.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

// The instance is mounted under a path, so every request the shared engine
// builds has to carry it.
func TestSiteBaseCarriesThePath(t *testing.T) {
	if got := mm.SiteBase; got != "https://mymember.site/candyxs" {
		t.Errorf("SiteBase = %q", got)
	}
}
