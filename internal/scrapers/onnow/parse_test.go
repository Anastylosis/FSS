package onnow

import "testing"

// Sites exposes the config table to the external test package.
func Sites() []SiteConfig { return sites }

func TestParseDuration(t *testing.T) {
	cases := map[string]int{
		"28m 24s":    28*60 + 24,
		"13m 1s":     13*60 + 1,
		"45s":        45,
		"1h 2m 3s":   3723,
		" 2h ":       7200,
		"":           0,
		"sem tempo":  0,
		"36m 8s    ": 36*60 + 8,
	}
	for in, want := range cases {
		if got := parseDuration(in); got != want {
			t.Errorf("parseDuration(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestCategory(t *testing.T) {
	cases := map[string]string{
		"https://irmaosdotados.com.br":                 "",
		"https://irmaosdotados.com.br/videos":          "",
		"https://irmaosdotados.com.br/videos/5":        "",
		"https://irmaosdotados.com.br/videos/solo":     "solo",
		"https://irmaosdotados.com.br/videos/solo/3":   "solo",
		"https://hardbrazil.com.br/videos/anal-brazil": "anal-brazil",
		"https://hardbrazil.com.br/videos/anal?page=2": "anal",
		"https://irmaosdotados.com.br/models":          "",
		"https://irmaosdotados.com.br/video/765":       "",
	}
	for in, want := range cases {
		if got := category(in); got != want {
			t.Errorf("category(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestListingURL(t *testing.T) {
	s := New(SiteConfig{BaseURL: "https://x.test"})
	cases := []struct {
		cat  string
		page int
		want string
	}{
		{"", 1, "https://x.test/videos"},
		{"", 3, "https://x.test/videos/3"},
		{"solo", 1, "https://x.test/videos/solo"},
		{"solo", 2, "https://x.test/videos/solo/2"},
	}
	for _, c := range cases {
		if got := s.listingURL(c.cat, c.page); got != c.want {
			t.Errorf("listingURL(%q,%d) = %q, want %q", c.cat, c.page, got, c.want)
		}
	}
}

func TestLastPage(t *testing.T) {
	cases := map[string]int{
		`<a href='/videos/2'  class="page-link">2</a><a href='/videos/36'  class="page-link">>></a>`:               36,
		`<a href='/videos/movies/2'  class="page-link">2</a><a href='/videos/movies/22'  class="page-link">>></a>`: 22,
		`<a href='https://hardbrazil.com.br/videos/anal-brazil/4'  class="page-link">4</a>`:                        4,
		`<li class="page-item active"><a href="#"  class="page-link">1</a></li>`:                                   0,
		``: 0,
	}
	for in, want := range cases {
		if got := lastPage(in); got != want {
			t.Errorf("lastPage(%q) = %d, want %d", in, got, want)
		}
	}
}
