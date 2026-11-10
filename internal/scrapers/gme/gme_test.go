package gme

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/Anastylosis/FSS/scraper"
)

func read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{"http://www.gme-inc.net", "https://gme-inc.net/product_list.html?label_id=629969", "http://www.gme-inc.net/NEWM-121.html"} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://gme-inc.org/", "https://example.com/gme-inc.net", ""} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

func TestListingFor(t *testing.T) {
	l, all := listingFor("http://www.gme-inc.net/product_list.html?label_id=629969")
	if all || l.url("http://x", 1) != "http://x/product_list.html?label_id=629969" || l.url("http://x", 3) != "http://x/product_list.html?label_id=629969&page=3" {
		t.Errorf("label listing = %q (all=%v)", l.url("http://x", 3), all)
	}
	if l, all := listingFor("http://www.gme-inc.net/ac792760.html"); all || l.url("http://x", 2) != "http://x/ac792760.html?page=2" {
		t.Errorf("actor listing = %q (all=%v)", l.url("http://x", 2), all)
	}
	if _, all := listingFor("http://www.gme-inc.net/"); !all {
		t.Error("the bare site must be the whole catalogue")
	}
	if _, all := listingFor("http://www.gme-inc.net/product_list.html"); !all {
		t.Error("an unfiltered list must be the whole catalogue")
	}
}

func TestParseCards(t *testing.T) {
	cards := parseCards(read(t, "list.html"))
	if len(cards) != 2 {
		t.Fatalf("got %d cards, want 2", len(cards))
	}
	if cards[0].code != "SMOM-012" || !strings.HasPrefix(cards[0].title, "シン・異常性交") || cards[0].date.Format("2006-01-02") != "2026-11-24" {
		t.Errorf("card = %+v", cards[0])
	}
	if cards[0].thumb != "img/package1/SMOM-012.jpg" {
		t.Errorf("thumb = %q", cards[0].thumb)
	}
}

func TestApplyDetail(t *testing.T) {
	it := item{code: "NEWM-121"}
	it.applyDetail(read(t, "detail.html"))
	if it.detailTitle != "嫁の母と禁断性交 其の六拾七 妻よりもお義母さんの方がいいよ… 夢野まりあ" {
		t.Errorf("title = %q", it.detailTitle)
	}
	if len(it.performers) != 1 || it.performers[0] != "夢野まりあ" {
		t.Errorf("performers = %v", it.performers)
	}
	if it.label != "New MOM" || it.series != "嫁の母と禁断性交" || it.director != "中野弥生" {
		t.Errorf("label/series/director = %q/%q/%q", it.label, it.series, it.director)
	}
	if it.duration != 125*60 || it.detailDate.Format("2006-01-02") != "2025-12-23" {
		t.Errorf("duration/date = %d/%v", it.duration, it.detailDate)
	}
	if !strings.HasPrefix(it.description, "まりあの爆乳が気になって") || !strings.Contains(it.description, "\n\n娘夫婦と同居") {
		t.Errorf("description = %q", it.description)
	}
	if it.detailThumb != "img/package14/NEWM-121.jpg" {
		t.Errorf("thumb = %q", it.detailThumb)
	}
}

func serve(t *testing.T) (*httptest.Server, *sync.Map) {
	t.Helper()
	var hits sync.Map
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Store(r.URL.RequestURI(), true)
		switch {
		case r.URL.Path == "/product_list.html":
			_, _ = fmt.Fprint(w, read(t, "list.html"))
		case strings.HasSuffix(r.URL.Path, ".html"):
			_, _ = fmt.Fprint(w, read(t, "detail.html"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func collect(t *testing.T, s *Scraper, url string, opts scraper.ListOpts) map[string]string {
	t.Helper()
	ch, err := s.ListScenes(context.Background(), url, opts)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			got[r.Scene.ID] = r.Scene.Studio
		case scraper.KindError:
			t.Errorf("error: %v", r.Err)
		}
	}
	return got
}

func TestRunWalksEveryLabelAndDeduplicates(t *testing.T) {
	srv, hits := serve(t)
	s := New()
	s.base = srv.URL
	got := collect(t, s, "http://www.gme-inc.net", scraper.ListOpts{Workers: 1})
	if len(got) != 2 || got["SMOM-012"] != "New MOM" {
		t.Errorf("scenes = %v", got)
	}
	for _, want := range []string{"/product_list.html?label_id=603302", "/product_list.html?label_id=629969"} {
		if _, ok := hits.Load(want); !ok {
			t.Errorf("label listing %s was not walked", want)
		}
	}
}

func TestRunFilteredAndKnownIDsSkipDetails(t *testing.T) {
	srv, hits := serve(t)
	s := New()
	s.base = srv.URL
	got := collect(t, s, "http://www.gme-inc.net/product_list.html?label_id=629969", scraper.ListOpts{Workers: 1, KnownIDs: map[string]bool{"RAPD-016": true}})
	if len(got) != 1 || got["SMOM-012"] == "" {
		t.Errorf("scenes = %v, want only the unknown one", got)
	}
	if _, ok := hits.Load("/product_list.html?label_id=603302"); ok {
		t.Error("a label URL must not walk other labels")
	}
	if _, ok := hits.Load("/RAPD-016.html"); ok {
		t.Error("a known scene's detail page was fetched")
	}
}
