package hushpass

import "testing"

func TestMatchesURL(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"https://hushpass.com/", true},
		{"https://www.hushpass.com/t1/categories/movies_2_d.html", true},
		{"http://hushpass.com", true},
		{"https://interracialpass.com/", false},
		{"https://hushpass.com.evil.org/", false},
	}
	for _, c := range cases {
		if got := matchRe.MatchString(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}
