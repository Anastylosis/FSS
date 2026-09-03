package familytherapy

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	cases := []struct {
		url  string
		want bool
	}{
		{"https://familytherapyxxx.com", true},
		{"https://www.familytherapyxxx.com/no-vacancy/", true},
		{"https://familytherapyxxx.com/some-scene/", true},
		{"https://momcomesfirst.com", false},
		{"https://example.com", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

const fixtureVideoPage = `<!doctype html><html><head>
<title>No Vacancy - Alex Adams, Onyx Reign - Family Therapy XXX</title>
<meta property="article:published_time" content="2026-04-19T05:00:00+00:00" />
<meta property="og:description" content="A wild scene." />
<meta property="og:image" content="https://familytherapyxxx.com/wp-content/uploads/2026/04/novacancy.png" />
<meta property="article:tag" content="Alex Adams" />
<meta property="article:tag" content="Onyx Reign" />
<meta property="article:tag" content="interracial" />
<link rel='shortlink' href='https://familytherapyxxx.com/?p=3689' />
<script type="application/ld+json">{"@type":"VideoObject","width":"1920","height":"1080"}</script>
<script type="application/ld+json">{"articleSection":"Big Butts, Interracial"}</script>
</head><body></body></html>`

func TestParsePage(t *testing.T) {
	scene, skip, err := parsePage(
		"https://familytherapyxxx.com",
		"https://familytherapyxxx.com/no-vacancy/",
		[]byte(fixtureVideoPage),
		fixedTime(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if skip {
		t.Fatal("expected video page, got skip")
	}

	if scene.ID != "3689" {
		t.Errorf("ID = %q, want 3689", scene.ID)
	}
	if scene.Title != "No Vacancy" {
		t.Errorf("Title = %q, want No Vacancy", scene.Title)
	}
	if len(scene.Performers) != 2 || scene.Performers[0] != "Alex Adams" || scene.Performers[1] != "Onyx Reign" {
		t.Errorf("Performers = %v", scene.Performers)
	}
	if scene.Date.Year() != 2026 || scene.Date.Month() != 4 || scene.Date.Day() != 19 {
		t.Errorf("Date = %v", scene.Date)
	}
	if scene.Description != "A wild scene." {
		t.Errorf("Description = %q", scene.Description)
	}
	if scene.Thumbnail != "https://familytherapyxxx.com/wp-content/uploads/2026/04/novacancy.png" {
		t.Errorf("Thumbnail = %q", scene.Thumbnail)
	}
	if scene.Width != 1920 || scene.Height != 1080 {
		t.Errorf("Width=%d Height=%d", scene.Width, scene.Height)
	}
	if scene.Resolution != "1080p" {
		t.Errorf("Resolution = %q", scene.Resolution)
	}
	if scene.SiteID != "familytherapy" {
		t.Errorf("SiteID = %q", scene.SiteID)
	}
	if scene.Studio != "Family Therapy" {
		t.Errorf("Studio = %q", scene.Studio)
	}

	wantTags := map[string]bool{
		"Alex Adams": true, "Onyx Reign": true, "interracial": true,
		"Big Butts": true, "Interracial": true,
	}
	for _, tag := range scene.Tags {
		if !wantTags[tag] {
			t.Errorf("unexpected tag %q", tag)
		}
		delete(wantTags, tag)
	}
	for tag := range wantTags {
		t.Errorf("missing tag %q", tag)
	}
}

func TestParsePageSimpleTitle(t *testing.T) {
	page := `<!doctype html><html><head>
<title>Solo Scene - Family Therapy XXX</title>
<meta property="article:tag" content="solo" />
<link rel='shortlink' href='https://familytherapyxxx.com/?p=100' />
</head><body></body></html>`

	scene, skip, err := parsePage(
		"https://familytherapyxxx.com",
		"https://familytherapyxxx.com/solo-scene/",
		[]byte(page),
		fixedTime(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if skip {
		t.Fatal("unexpected skip")
	}
	if scene.Title != "Solo Scene" {
		t.Errorf("Title = %q, want Solo Scene", scene.Title)
	}
	if len(scene.Performers) != 0 {
		t.Errorf("Performers = %v, want empty", scene.Performers)
	}
}

const fixtureNonVideoPage = `<!doctype html><html><head>
<title>Family Therapy XXX</title>
</head><body><p>Homepage.</p></body></html>`

func TestParsePageSkipsNonVideo(t *testing.T) {
	_, skip, err := parsePage(
		"https://familytherapyxxx.com",
		"https://familytherapyxxx.com/",
		[]byte(fixtureNonVideoPage),
		fixedTime(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !skip {
		t.Error("expected skip=true for non-video page")
	}
}

func TestListScenes(t *testing.T) {
	sitemapXML := `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>%s/</loc></url>
  <url><loc>%s/no-vacancy/</loc></url>
</urlset>`

	videoPage := `<html><head>
<title>No Vacancy - Alex Adams - Family Therapy XXX</title>
<meta property="article:published_time" content="2026-04-19T05:00:00+00:00" />
<meta property="article:tag" content="Alex Adams" />
<link rel='shortlink' href='%s/?p=3689' />
</head><body></body></html>`

	homepage := `<html><head><title>Family Therapy XXX</title></head>
<body><p>Welcome.</p></body></html>`

	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/sitemap.xml":
			_, _ = fmt.Fprintf(w, sitemapXML, ts.URL, ts.URL)
		case strings.Contains(r.URL.Path, "no-vacancy"):
			_, _ = fmt.Fprintf(w, videoPage, ts.URL)
		default:
			_, _ = w.Write([]byte(homepage))
		}
	}))
	defer ts.Close()

	s := &Scraper{
		client:   ts.Client(),
		siteBase: ts.URL,
		headers:  map[string]string{},
	}

	ch, err := s.ListScenes(context.Background(), ts.URL, scraper.ListOpts{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}

	scenes := testutil.CollectScenes(t, ch)

	if len(scenes) != 1 {
		t.Fatalf("got %d scenes, want 1 (homepage should be filtered)", len(scenes))
	}
	if scenes[0].Title != "No Vacancy" {
		t.Errorf("scene title = %q, want No Vacancy", scenes[0].Title)
	}
}

func fixedTime() time.Time {
	return time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)
}

// The middle segment of the title is usually the cast, but the site also uses
// it for a part marker, which is part of the title rather than a person.
func TestPartMarkerIsNotAPerformer(t *testing.T) {
	cases := []struct {
		title      string
		wantTitle  string
		wantPeople []string
	}{
		{"Stepmom's Lesson - Jane Doe, John Smith - Family Therapy XXX", "Stepmom's Lesson", []string{"Jane Doe", "John Smith"}},
		{"Stepmom's Lesson - Part 2 - Family Therapy XXX", "Stepmom's Lesson - Part 2", nil},
		{"Stepmom's Lesson - Pt. 3 - Family Therapy XXX", "Stepmom's Lesson - Pt. 3", nil},
		{"Stepmom's Lesson - Family Therapy XXX", "Stepmom's Lesson", nil},
	}
	for _, c := range cases {
		body := []byte(`<html><head><meta property="og:title" content="` + c.title + `"><meta name="post-id" content="1"></head><body></body></html>`)
		sc, skip, err := parsePage("https://familytherapyxxx.com/", "https://familytherapyxxx.com/scene/x/", body, time.Now().UTC())
		if err != nil {
			t.Fatalf("parsePage(%q): %v", c.title, err)
		}
		if skip {
			continue
		}
		if sc.Title != c.wantTitle {
			t.Errorf("Title for %q = %q, want %q", c.title, sc.Title, c.wantTitle)
		}
		if len(sc.Performers) != len(c.wantPeople) {
			t.Errorf("Performers for %q = %v, want %v", c.title, sc.Performers, c.wantPeople)
		}
	}
}

func TestPartMarkerRe(t *testing.T) {
	for _, s := range []string{"Part 2", "part 10", "Pt. 3", "Episode 4", "Vol. 1", "Chapter 12"} {
		if !partMarkerRe.MatchString(s) {
			t.Errorf("%q should be a part marker", s)
		}
	}
	for _, s := range []string{"Jane Doe", "Part Time Lover", "Partner 2 Partner", "Ep Jones"} {
		if partMarkerRe.MatchString(s) {
			t.Errorf("%q should not be a part marker", s)
		}
	}
}
