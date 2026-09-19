package avjiali

import (
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{"https://avjiali.com", "http://www.avjiali.com/", "https://avjiali.com/some-scene/"} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://avjiali.net/", "https://example.com/avjiali.com", ""} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

const scenePage = `<html><head>
<meta name="description" content="Well the pandemic is tough &amp; company.">
<meta property="og:image" content="https://avjiali.com/wp-content/uploads/a.jpg">
</head><body>
<h1>Naughty Huang Ying ends up fucking her co-worker</h1>
<ul>
 <li><div class="video-duration"><svg><use xlink:href="#clocksvg"></use></svg>19:47</div></li>
 <li><div class="video-date"><svg><use xlink:href="#calsvg"></use></svg>May 13th, 2022</div></li>
</ul>
<a href="https://avjiali.com/models/huang-yina/">Huang Yina</a>
<a href="https://avjiali.com/models/huang-yina/">Huang Yina</a>
</body></html>`

func TestParseScene(t *testing.T) {
	scene, err := parseScene(scenePage, "https://avjiali.com", "https://avjiali.com/huang-ying-quarantine/", time.Now().UTC())
	if err != nil {
		t.Fatalf("parseScene: %v", err)
	}
	if scene.ID != "huang-ying-quarantine" || scene.SiteID != siteID || scene.Studio != studioName {
		t.Errorf("ID/SiteID/Studio = %q/%q/%q", scene.ID, scene.SiteID, scene.Studio)
	}
	if scene.Title != "Naughty Huang Ying ends up fucking her co-worker" {
		t.Errorf("Title = %q", scene.Title)
	}
	if scene.Duration != 1187 {
		t.Errorf("Duration = %d, want 1187", scene.Duration)
	}
	// The theme writes an ordinal day, which Go's layouts cannot parse directly.
	if got := scene.Date.Format("2006-01-02"); got != "2022-05-13" {
		t.Errorf("Date = %s", got)
	}
	if strings.Join(scene.Performers, "|") != "Huang Yina" {
		t.Errorf("Performers = %v, want one de-duplicated name", scene.Performers)
	}
	if scene.Description != "Well the pandemic is tough & company." {
		t.Errorf("Description = %q", scene.Description)
	}
	if scene.Thumbnail == "" {
		t.Error("Thumbnail is empty")
	}
}

func TestParseSceneWithoutTitleIsAParseError(t *testing.T) {
	_, err := parseScene("<html><body></body></html>", "u", "https://avjiali.com/x/", time.Now())
	if err == nil {
		t.Fatal("want an error")
	}
	if kind := scraper.Classify(err); kind != scraper.FailureParse {
		t.Errorf("failure kind = %v, want FailureParse", kind)
	}
}
