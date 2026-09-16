package muku

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{"https://muku.tv/", "http://muku.tv", "https://muku.tv/works/detail/MUCD360", "https://muku.tv/actress/detail/767101"} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://muku.tv.example/", "https://example.com/muku.tv", ""} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

const workPage = `<html><head>
<title>部活帰りの美少女 | 無垢</title>
<meta name="description" content="説明 &amp; more">
<meta property="og:image" content="https://muku.tv/img/a.jpg">
</head><body>
<div class="item"><div class="th">女優</div><div class="td">
	<a class="c-tag" href="https://muku.tv/actress/detail/1">さつき芽衣</a>
	<a class="c-tag" href="https://muku.tv/actress/detail/1">さつき芽衣</a>
</div></div>
<div class="item"><div class="th">発売日</div><div class="td">
	<a class="c-tag" href="https://muku.tv/works/list/date/2026-08-18">2026年8月18日</a>
</div></div>
<div class="item"><div class="th">収録時間</div><div class="td"><p><span class="c-tag02">DVD</span>240分</p></div></div>
<div class="item"><div class="th">品番</div><div class="td"><p><span class="c-tag02">DVD</span>MUCD360</p></div></div>
<div class="item"><div class="th">シリーズ</div><div class="td">
	<a class="c-tag" href="https://muku.tv/works/list/series/5991">部活帰りの汗だく美少女と</a>
</div></div>
<div class="item"><div class="th">ジャンル</div><div class="td">
	<a class="c-tag" href="https://muku.tv/works/list/genre/481">汗だく</a>
	<a class="c-tag" href="https://muku.tv/works/list/genre/482">制服</a>
</div></div>
</body></html>`

func TestParseWork(t *testing.T) {
	scene, err := parseWork(workPage, "MUCD360", "https://muku.tv/", time.Now().UTC())
	if err != nil {
		t.Fatalf("parseWork: %v", err)
	}
	if scene.ID != "MUCD360" || scene.SiteID != siteID || scene.Studio != studioName {
		t.Errorf("ID/SiteID/Studio = %q/%q/%q", scene.ID, scene.SiteID, scene.Studio)
	}
	if scene.Title != "部活帰りの美少女" {
		t.Errorf("Title = %q, want the site suffix trimmed", scene.Title)
	}
	if got := scene.Date.Format("2006-01-02"); got != "2026-08-18" {
		t.Errorf("Date = %s", got)
	}
	// The runtime is published in minutes.
	if scene.Duration != 240*60 {
		t.Errorf("Duration = %d, want %d", scene.Duration, 240*60)
	}
	if strings.Join(scene.Performers, "|") != "さつき芽衣" {
		t.Errorf("Performers = %v, want one de-duplicated name", scene.Performers)
	}
	if strings.Join(scene.Tags, "|") != "汗だく|制服" {
		t.Errorf("Tags = %v", scene.Tags)
	}
	if scene.Series != "部活帰りの汗だく美少女と" {
		t.Errorf("Series = %q", scene.Series)
	}
	if scene.Description != "説明 & more" {
		t.Errorf("Description = %q", scene.Description)
	}
}

// Every row must be read: closing each row with a regex that matches the next
// header consumes it, which silently drops every second row.
func TestRowsReadsEveryRow(t *testing.T) {
	got := rows(workPage)
	for _, label := range []string{"女優", "発売日", "収録時間", "品番", "シリーズ", "ジャンル"} {
		if _, ok := got[label]; !ok {
			t.Errorf("row %q missing; got %d rows", label, len(got))
		}
	}
}

func TestParseWorkWithoutTitleIsAnError(t *testing.T) {
	if _, err := parseWork(`<html><body></body></html>`, "X", "https://muku.tv/", time.Now()); err == nil {
		t.Error("want an error when the title is gone")
	}
}

func TestListScenesWalksTheKanaIndex(t *testing.T) {
	var actressPages, indexPages int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/works/detail/"):
			_, _ = fmt.Fprint(w, workPage)
		case r.URL.Path == "/works/list/release/":
			_, _ = fmt.Fprint(w, `<a href="/works/detail/MUCD360">x</a>`)
		case strings.HasPrefix(r.URL.Path, "/actress/detail/"):
			actressPages++
			_, _ = fmt.Fprint(w, `<a href="/works/detail/MUCD361">y</a><a href="/works/detail/MUCD360">dupe</a>`)
		case strings.HasPrefix(r.URL.Path, "/actress/"):
			indexPages++
			if r.URL.Path == "/actress/a" {
				_, _ = fmt.Fprint(w, `<a href="/actress/detail/42">A</a>`)
				return
			}
			_, _ = fmt.Fprint(w, `<html>no actresses</html>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	s := New()
	s.client = srv.Client()
	s.base = srv.URL

	ch, err := s.ListScenes(context.Background(), srv.URL, scraper.ListOpts{Workers: 2})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	ids := map[string]bool{}
	for r := range ch {
		switch r.Kind {
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		case scraper.KindScene:
			ids[r.Scene.ID] = true
			if !strings.HasPrefix(r.Scene.URL, siteBase+"/works/detail/") {
				t.Errorf("scene URL = %q, want a %s address", r.Scene.URL, siteBase)
			}
		}
	}
	if len(ids) != 2 {
		t.Fatalf("got %d scenes (%v), want 2 — the repeat is one work", len(ids), ids)
	}
	if indexPages != len(kanaIndexes) {
		t.Errorf("fetched %d kana index pages, want %d", indexPages, len(kanaIndexes))
	}
	if actressPages != 1 {
		t.Errorf("fetched %d actress pages, want 1", actressPages)
	}
}

// An actress URL scrapes only her works.
func TestActressURLScrapesOneActress(t *testing.T) {
	var kana int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/works/detail/"):
			_, _ = fmt.Fprint(w, workPage)
		case r.URL.Path == "/actress/detail/42":
			_, _ = fmt.Fprint(w, `<a href="/works/detail/MUCD361">y</a>`)
		default:
			kana++
			_, _ = fmt.Fprint(w, `<html></html>`)
		}
	}))
	defer srv.Close()

	s := New()
	s.client = srv.Client()
	s.base = srv.URL

	ch, _ := s.ListScenes(context.Background(), srv.URL+"/actress/detail/42", scraper.ListOpts{})
	var n int
	for r := range ch {
		if r.Kind == scraper.KindScene {
			n++
		}
	}
	if n != 1 {
		t.Errorf("got %d scenes, want 1", n)
	}
	if kana != 0 {
		t.Errorf("fetched %d other pages — an actress URL must not walk the index", kana)
	}
}
