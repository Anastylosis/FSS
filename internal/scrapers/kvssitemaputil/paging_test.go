package kvssitemaputil

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/Anastylosis/FSS/scraper"
)

func TestSitemapPaging(t *testing.T) {
	page := func(from, to int) string {
		var sb strings.Builder
		sb.WriteString(`<urlset xmlns:video="http://www.google.com/schemas/sitemap-video/1.1">`)
		for i := from; i <= to; i++ {
			sb.WriteString(`<url><loc>https://x.test/videos/` + strconv.Itoa(i) + `/t/</loc><video:video><video:title>T</video:title></video:video></url>`)
		}
		sb.WriteString(`</urlset>`)
		return sb.String()
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("from_links_videos") {
		case "1":
			_, _ = fmt.Fprint(w, page(1, sitemapPageSize))
		case "2":
			_, _ = fmt.Fprint(w, page(sitemapPageSize+1, sitemapPageSize+3))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cfg := testConfig(srv.URL)
	cfg.PerformerRe = regexp.MustCompile(`(?s)NOMATCH(.*?)NOMATCH`)
	cfg.TagRe = nil
	s := New(cfg)
	s.Client = srv.Client()
	want := sitemapPageSize + 3
	known := map[string]bool{}
	for i := 1; i <= want; i++ {
		known[strconv.Itoa(i)] = true
	}
	_, total := collect(t, s, srv.URL, scraper.ListOpts{KnownIDs: known})
	if total != want {
		t.Errorf("total = %d, want %d", total, want)
	}
}
