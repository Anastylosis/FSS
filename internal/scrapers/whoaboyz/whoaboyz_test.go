package whoaboyz

import "testing"

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url  string
		want bool
	}{
		{"https://whoaboyz.com/", true},
		{"https://www.whoaboyz.com/", true},
		{"http://whoaboyz.com/tour/trailer/some-slug/", true},
		{"https://whoaboyzfake.com/", false},
		{"https://example.com/", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestID(t *testing.T) {
	if got := New().ID(); got != "whoaboyz" {
		t.Errorf("ID() = %q, want whoaboyz", got)
	}
}
