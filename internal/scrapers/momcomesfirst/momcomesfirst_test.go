package momcomesfirst

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
		{"https://momcomesfirst.com", true},
		{"https://www.momcomesfirst.com/the-italian-friend/", true},
		{"https://momcomesfirst.com/secret-massages/", true},
		{"https://www.manyvids.com/Profile/123/foo", false},
		{"https://example.com", false},
	}
	for _, c := range cases {
		if got := s.MatchesURL(c.url); got != c.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

const fixtureVideoPage = `<!doctype html><html><head>
<title>The Italian Friend - alex adams, babe, cumshot - Mom Comes First</title>
<meta property="article:published_time" content="2026-04-19T05:56:08+00:00" />
<meta property="og:description" content="You&#039;re going to get yourself into trouble." />
<meta property="og:image" content="https://momcomesfirst.com/wp-content/uploads/2026/04/theitalianfriend.png" />
<meta property="article:tag" content="alex adams" />
<meta property="article:tag" content="babe" />
<meta property="article:tag" content="Raissa Bellina" />
<meta property="article:section" content="Brunette" />
<link rel='shortlink' href='https://momcomesfirst.com/?p=3322' />
<script type="application/ld+json">{"@type":"BlogPosting","articleSection":"Big Butts, Blowjob, MILF"}</script>
<script type="application/ld+json">{"@type":"VideoObject","width":"1920","height":"1080"}</script>
</head><body></body></html>`

func TestParsePage(t *testing.T) {
	scene, skip, err := parsePage(
		"https://momcomesfirst.com",
		"https://momcomesfirst.com/the-italian-friend/",
		[]byte(fixtureVideoPage),
		fixedTime(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if skip {
		t.Fatal("expected video page, got skip")
	}

	if scene.ID != "3322" {
		t.Errorf("ID = %q, want 3322", scene.ID)
	}
	if scene.Title != "The Italian Friend" {
		t.Errorf("Title = %q", scene.Title)
	}
	if scene.Date.Year() != 2026 || scene.Date.Month() != 4 || scene.Date.Day() != 19 {
		t.Errorf("Date = %v", scene.Date)
	}
	if scene.Description != "You're going to get yourself into trouble." {
		t.Errorf("Description = %q", scene.Description)
	}
	if scene.Thumbnail != "https://momcomesfirst.com/wp-content/uploads/2026/04/theitalianfriend.png" {
		t.Errorf("Thumbnail = %q", scene.Thumbnail)
	}
	if scene.Width != 1920 || scene.Height != 1080 {
		t.Errorf("Width=%d Height=%d", scene.Width, scene.Height)
	}
	if scene.Resolution != "1080p" {
		t.Errorf("Resolution = %q", scene.Resolution)
	}
	if scene.SiteID != "momcomesfirst" {
		t.Errorf("SiteID = %q", scene.SiteID)
	}
	if scene.Studio != "Mom Comes First" {
		t.Errorf("Studio = %q", scene.Studio)
	}
	// Tags should be: article:tag values + articleSection categories
	wantTags := map[string]bool{
		"alex adams": true, "babe": true, "Raissa Bellina": true,
		"Big Butts": true, "Blowjob": true, "MILF": true,
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

const fixtureNonVideoPage = `<!doctype html><html><head>
<title>Mom Comes First</title>
</head><body><p>Homepage content.</p></body></html>`

func TestParsePageSkipsNonVideo(t *testing.T) {
	_, skip, err := parsePage(
		"https://momcomesfirst.com",
		"https://momcomesfirst.com/",
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

func TestParsePageNoShortlink(t *testing.T) {
	page := `<!doctype html><html><head>
<title>Fallback Test - Mom Comes First</title>
<meta property="article:tag" content="test" />
</head><body></body></html>`

	scene, skip, err := parsePage(
		"https://momcomesfirst.com",
		"https://momcomesfirst.com/fallback-test/",
		[]byte(page),
		fixedTime(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if skip {
		t.Fatal("unexpected skip")
	}
	if scene.ID != "fallback-test" {
		t.Errorf("ID = %q, want fallback-test (slug)", scene.ID)
	}
}

func TestListScenes(t *testing.T) {
	sitemapXML := `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>%s/</loc></url>
  <url><loc>%s/video-one/</loc></url>
</urlset>`

	videoPage := `<html><head>
<title>Video One - test tag, step-mother - Mom Comes First</title>
<meta property="article:published_time" content="2026-01-15T10:00:00+00:00" />
<meta property="article:tag" content="test tag" />
<meta property="article:tag" content="step-mother" />
<link rel='shortlink' href='%s/?p=42' />
</head><body></body></html>`

	homepage := `<html><head><title>Mom Comes First</title></head>
<body><p>Welcome.</p></body></html>`

	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/sitemap.xml":
			_, _ = fmt.Fprintf(w, sitemapXML, ts.URL, ts.URL)
		case strings.Contains(r.URL.Path, "video-one"):
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
	if scenes[0].Title != "Video One" {
		t.Errorf("scene title = %q, want Video One", scenes[0].Title)
	}
}

func fixedTime() time.Time {
	return time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)
}

// The og:title is "Title - tag1, tag2 - Mom Comes First". Matching the tag
// segment on a bare hyphen truncated the title at a hyphenated tag's own
// hyphen ("step-mother"), leaving the tag list in the stored title.
func TestStripTitleSuffix(t *testing.T) {
	tags := []string{"alex adams", "Andie Anderson", "step mom", "step-mother"}
	tests := []struct {
		title string
		tags  []string
		want  string
	}{
		{"Jealous - alex adams, Andie Anderson, step mom, step-mother - Mom Comes First", tags, "Jealous"},
		{"Bully Negotiation Tactics - alex adams, step mom - Mom Comes First", tags, "Bully Negotiation Tactics"},
		// A title with its own spaced hyphen keeps it.
		{"Part One - Part Two - alex adams - Mom Comes First", tags, "Part One - Part Two"},
		// No tag segment at all.
		{"Jealous - Mom Comes First", tags, "Jealous"},
		// A trailing segment that is not a tag is part of the title.
		{"Jealous - The Reckoning - Mom Comes First", tags, "Jealous - The Reckoning"},
		// A page with no declared tags keeps its whole title.
		{"Jealous - alex adams - Mom Comes First", nil, "Jealous - alex adams"},
		{"Mom Comes First", tags, "Mom Comes First"},
	}
	for _, tt := range tests {
		if got := stripTitleSuffix(tt.title, tt.tags); got != tt.want {
			t.Errorf("stripTitleSuffix(%q) = %q, want %q", tt.title, got, tt.want)
		}
	}
}
