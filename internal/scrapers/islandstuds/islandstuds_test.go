package islandstuds

import "testing"

func TestIDAndPatterns(t *testing.T) {
	s := New()
	if s.ID() != "islandstuds" {
		t.Errorf("ID() = %q", s.ID())
	}
	want := map[string]bool{
		"islandstuds.com/tour/":                                true,
		"islandstuds.com/tour/categories/movies_{page}_d.html": true,
		"islandstuds.com/tour/categories/{slug}.html":          true,
		"islandstuds.com/tour/models/{slug}.html":              true,
	}
	for _, p := range s.Patterns() {
		delete(want, p)
	}
	if len(want) != 0 {
		t.Errorf("missing patterns: %v", want)
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := map[string]bool{
		"https://www.islandstuds.com/tour/":                                    true,
		"https://islandstuds.com/tour/categories/movies_2_d.html":              true,
		"https://www.islandstuds.com/tour/categories/HairyGuys.html":           true,
		"https://www.islandstuds.com/tour/models/Lennon.html":                  true,
		"https://www.islandstuds.com/tour/scenes/Lennon-Daddy-Lover_vids.html": true,
		"https://example.com/islandstuds.com":                                  false,
		"https://www.julesjordan.com/trial/":                                   false,
	}
	for u, want := range cases {
		if got := s.MatchesURL(u); got != want {
			t.Errorf("MatchesURL(%q) = %v, want %v", u, got, want)
		}
	}
}
