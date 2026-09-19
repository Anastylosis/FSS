package germanscout

import (
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

func TestMatchesURL(t *testing.T) {
	s := New()
	for _, u := range []string{"https://www.german-scout.com/", "http://german-scout.com", "https://www.german-scout.com/2019/03/16/gabi/"} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	for _, u := range []string{"https://germanscout.com/", "https://example.com/german-scout.com", ""} {
		if s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = true", u)
		}
	}
}

const scenePage = `<html><head>
<meta property="og:title" content="Stepbrother caught &amp; more">
<meta property="og:description" content="Ups oh nein.">
<meta property="og:image" content="https://www.german-scout.com/a.jpg">
<script type="application/ld+json">{"@graph":[
 {"@type":"WebPage","datePublished":"2019-03-18T00:00:00+00:00"},
 {"@type":"Article","datePublished":"2019-03-17T13:13:27+00:00"}]}</script>
</head><body></body></html>`

func TestParseScene(t *testing.T) {
	scene, err := parseScene(scenePage, "https://www.german-scout.com/", "https://www.german-scout.com/2019/03/17/stiefbruder-beim-wichsen-erwischt/", time.Now().UTC())
	if err != nil {
		t.Fatalf("parseScene: %v", err)
	}
	// The permalink date is not part of the id, so a re-dated post keeps it.
	if scene.ID != "stiefbruder-beim-wichsen-erwischt" {
		t.Errorf("ID = %q", scene.ID)
	}
	if scene.Title != "Stepbrother caught & more" {
		t.Errorf("Title = %q", scene.Title)
	}
	// The Article node wins over the WebPage node.
	if got := scene.Date.Format("2006-01-02"); got != "2019-03-17" {
		t.Errorf("Date = %s", got)
	}
	if scene.Description == "" || scene.Thumbnail == "" {
		t.Errorf("Description/Thumbnail = %q/%q", scene.Description, scene.Thumbnail)
	}
}

// Without JSON-LD the permalink still carries the publish date.
func TestPublishedAtFallsBackToThePermalink(t *testing.T) {
	got := publishedAt("<html></html>", "https://www.german-scout.com/2019/03/16/gabi/")
	if got.Format("2006-01-02") != "2019-03-16" {
		t.Errorf("publishedAt = %v", got)
	}
	if !publishedAt("<html></html>", "https://www.german-scout.com/about/").IsZero() {
		t.Error("a non-post URL must yield no date")
	}
}

// The sitemap lists every post twice, German and /en/.
func TestEnglishMirrorsAreSkipped(t *testing.T) {
	s := New()
	_, skip, err := s.parsePage("u", "https://www.german-scout.com/en/2019/03/17/stiefbruder/", []byte(scenePage), time.Now())
	if err != nil || !skip {
		t.Errorf("skip = %v, err = %v; want the mirror skipped", skip, err)
	}
	_, skip, err = s.parsePage("u", "https://www.german-scout.com/2019/03/17/stiefbruder/", []byte(scenePage), time.Now())
	if err != nil || skip {
		t.Errorf("skip = %v, err = %v; want the original kept", skip, err)
	}
}

func TestParseSceneWithoutTitleIsAParseError(t *testing.T) {
	_, err := parseScene("<html></html>", "u", "https://www.german-scout.com/2019/03/17/x/", time.Now())
	if err == nil {
		t.Fatal("want an error")
	}
	if kind := scraper.Classify(err); kind != scraper.FailureParse {
		t.Errorf("failure kind = %v, want FailureParse", kind)
	}
}
