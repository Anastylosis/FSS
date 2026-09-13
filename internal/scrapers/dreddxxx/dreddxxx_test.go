package dreddxxx

import (
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{
		"https://officialdreddxxx.com",
		"https://www.officialdreddxxx.com/scene/casey-calvert/",
		"http://officialdreddxxx.com/",
	} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://dreddxxx.com/", "https://example.com/officialdreddxxx.com", ""} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

const scenePage = `<html><head>
<meta property="og:description" content="This scene features Casey Calvert &amp; Dredd." />
<meta property="og:image" content="https://officialdreddxxx.com/wp-content/uploads/2025/03/shot.jpg" />
<script type="application/ld+json">{"@context":"https://schema.org","@graph":[
 {"@type":"ImageObject","url":"https://x/y.jpg"},
 {"@type":"WebPage","name":"Casey Calvert","datePublished":"2025-03-27T22:13:17+00:00","dateModified":"2025-04-16T20:02:10+00:00"}]}</script>
</head><body>
<h1 class="entry-title">Casey Calvert &amp; Dredd</h1>
<a href="https://officialdreddxxx.com/pornstar/casey-calvert/">Casey Calvert</a>
<a href="https://officialdreddxxx.com/pornstar/casey-calvert/">Casey Calvert</a>
<a href="https://officialdreddxxx.com/scene-category/anal/">Anal</a>
<a href="https://officialdreddxxx.com/scene-category/rimming/">Rimming</a>
</body></html>`

func TestParseScene(t *testing.T) {
	now := time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)
	scene, err := parseScene("https://officialdreddxxx.com", "https://officialdreddxxx.com/scene/casey-calvert/", []byte(scenePage), now)
	if err != nil {
		t.Fatalf("parseScene: %v", err)
	}
	if scene.ID != "casey-calvert" || scene.SiteID != siteID || scene.Studio != studioName {
		t.Errorf("ID/SiteID/Studio = %q/%q/%q", scene.ID, scene.SiteID, scene.Studio)
	}
	if scene.Title != "Casey Calvert & Dredd" {
		t.Errorf("Title = %q", scene.Title)
	}
	if scene.Description != "This scene features Casey Calvert & Dredd." {
		t.Errorf("Description = %q", scene.Description)
	}
	// datePublished, not dateModified: the latter moves on every edit.
	if got := scene.Date.Format("2006-01-02"); got != "2025-03-27" {
		t.Errorf("Date = %s, want 2025-03-27", got)
	}
	if strings.Join(scene.Performers, "|") != "Casey Calvert" {
		t.Errorf("Performers = %v, want one de-duplicated name", scene.Performers)
	}
	if strings.Join(scene.Categories, "|") != "Anal|Rimming" {
		t.Errorf("Categories = %v", scene.Categories)
	}
	if scene.Thumbnail == "" {
		t.Error("Thumbnail is empty")
	}
}

// A page whose markup changed enough to lose the title is a parse failure, not
// a scene with an empty name — the difference decides whether an authoritative
// save may delete.
func TestParseSceneWithoutTitleIsAParseError(t *testing.T) {
	_, err := parseScene("https://officialdreddxxx.com", "https://officialdreddxxx.com/scene/x/", []byte(`<html><body></body></html>`), time.Now())
	if err == nil {
		t.Fatal("want an error")
	}
	if kind := scraper.Classify(err); kind != scraper.FailureParse {
		t.Errorf("failure kind = %v, want FailureParse", kind)
	}
}

func TestPublishedAtIgnoresAGraphWithoutDates(t *testing.T) {
	if got := publishedAt(`<script type="application/ld+json">{"@graph":[{"@type":"WebSite"}]}</script>`); !got.IsZero() {
		t.Errorf("publishedAt = %v, want the zero time", got)
	}
	if got := publishedAt(`no json-ld here`); !got.IsZero() {
		t.Errorf("publishedAt = %v, want the zero time", got)
	}
}

func TestCleanTextStripsMarkupAndEntities(t *testing.T) {
	if got := cleanText("  <span>Tom &amp;  Jerry</span>\n  "); got != "Tom & Jerry" {
		t.Errorf("cleanText = %q", got)
	}
}
