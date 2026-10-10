package erosarts

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestBuildBaseURL(t *testing.T) {
	cases := []struct {
		studioURL string
		want      string
	}{
		{"https://sexpov.com/", "https://sexpov.com/tour.php"},
		{"https://sexpov.com", "https://sexpov.com/tour.php"},
		{
			"https://sexpov.com/tour.php?task=search_videos&model_id=1&video_keyword_id=&keyword=Search+by+Keywords&video_sort=",
			"https://sexpov.com/tour.php?keyword=Search+by+Keywords&model_id=1&task=search_videos&video_keyword_id=&video_sort=",
		},
		{
			"https://taboohandjobs.com/tour.php?task=search_videos&model_id=5&p=2",
			"https://taboohandjobs.com/tour.php?model_id=5&task=search_videos",
		},
	}
	for _, c := range cases {
		got := buildBaseURL("https://"+extractDomain(c.studioURL), c.studioURL)
		if got != c.want {
			t.Errorf("buildBaseURL(%q)\n  got  %q\n  want %q", c.studioURL, got, c.want)
		}
	}
}

func extractDomain(u string) string {
	if idx := strings.Index(u, "://"); idx >= 0 {
		u = u[idx+3:]
	}
	if idx := strings.Index(u, "/"); idx >= 0 {
		u = u[:idx]
	}
	return u
}

func TestMatchesURL(t *testing.T) {
	for _, cfg := range sites {
		s := New(cfg)
		url := "https://www." + cfg.Patterns[0] + "/"
		if !s.MatchesURL(url) {
			t.Errorf("%s: MatchesURL(%q) = false", cfg.ID, url)
		}
		if s.MatchesURL("https://example.com/") {
			t.Errorf("%s: MatchesURL(example.com) should be false", cfg.ID)
		}
	}
}

const fixtureSingle = `
<div class="curvy">
<span id="ctl"><b>&bull;</b></span>
<span id="cbl"><b>&bull;</b></span>
<span id="ctr"><b>&bull;</b></span>
<span id="cbr"><b>&bull;</b></span>
<table border="0" cellspacing="0" cellpadding="5" class="form" style="margin:0px;" width="100%">
<tr>
<td colspan="2">
<div class="title">1500 Video(s) Found for: <span style="font-style:italic;">All Videos</span></div>
<hr style="margin:10px 0px 35px 0px;">
</td>
</tr>
<tr>
<td valign="top" width="300">
<a href="/videos/796/"><img src="/cover_images/JOI1_Simone_Sonay_720mp4.gif" class="photo" width="285" height="171"></a>
<div class="keywords">
<a href="/keywords/20">Big Tits</a>, <a href="/keywords/11">Blondes</a>, <a href="/keywords/96">Friend&#039;s Mom</a>, <a href="/keywords/39">Milf</a></div>
</td>
<td valign="top">
<div class="title">Jacking off for horny widow Simone Sonay</div>
<p>You are staying with your friend for a little while and your friend's step-mom is happy to have you.&nbsp;<a href="/videos/796/">more</a></p>
Date Added: 04/23/2026<br>
Starring: <a href="/models/281">Simone Sonay</a><br>
Company: Liberated Eye Inc<br>
Running Time: 11  mins<br><br>
</td>
</tr>
</table>
</div>
`

const fixtureMultiPerf = `
<div class="curvy">
<span id="ctl"><b>&bull;</b></span>
<span id="cbl"><b>&bull;</b></span>
<span id="ctr"><b>&bull;</b></span>
<span id="cbr"><b>&bull;</b></span>
<table border="0" cellspacing="0" cellpadding="5" class="form" style="margin:0px;" width="100%">
<tr>
<td valign="top" width="300">
<a href="/videos/500/"><img src="/cover_images/scene500.gif" class="photo" width="285" height="171"></a>
<div class="keywords">
<a href="/keywords/39">Milf</a></div>
</td>
<td valign="top">
<div class="title">Double trouble</div>
<p>Two hotties team up.&nbsp;<a href="/videos/500/">more</a></p>
Date Added: 06/15/2020<br>
Starring: <a href="/models/255">Ariella Ferrera</a> and <a href="/models/149">Zoey Holloway</a><br>
Company: Liberated Eye Inc<br>
Running Time: 15  mins<br><br>
</td>
</tr>
</table>
</div>
`

func TestParseListingPage(t *testing.T) {
	cards, total := parseListingPage([]byte(fixtureSingle), "https://test.local")
	if total != 1500 {
		t.Errorf("total = %d, want 1500", total)
	}
	if len(cards) != 1 {
		t.Fatalf("got %d cards, want 1", len(cards))
	}

	c := cards[0]
	if c.id != "796" {
		t.Errorf("id = %q", c.id)
	}
	if c.url != "https://test.local/videos/796/" {
		t.Errorf("url = %q", c.url)
	}
	if c.title != "Jacking off for horny widow Simone Sonay" {
		t.Errorf("title = %q", c.title)
	}
	if c.thumbnail != "https://test.local/cover_images/JOI1_Simone_Sonay_720mp4.gif" {
		t.Errorf("thumbnail = %q", c.thumbnail)
	}
	if c.date.Year() != 2026 || c.date.Month() != 4 || c.date.Day() != 23 {
		t.Errorf("date = %v", c.date)
	}
	if len(c.performers) != 1 || c.performers[0] != "Simone Sonay" {
		t.Errorf("performers = %v", c.performers)
	}
	if c.duration != 11*60 {
		t.Errorf("duration = %d", c.duration)
	}
	if len(c.tags) != 4 || c.tags[2] != "Friend's Mom" {
		t.Errorf("tags = %v", c.tags)
	}
}

func TestParseMultiPerformer(t *testing.T) {
	cards, _ := parseListingPage([]byte(fixtureMultiPerf), "https://test.local")
	if len(cards) != 1 {
		t.Fatalf("got %d cards", len(cards))
	}
	if len(cards[0].performers) != 2 || cards[0].performers[0] != "Ariella Ferrera" {
		t.Errorf("performers = %v", cards[0].performers)
	}
}

func TestToScene(t *testing.T) {
	cfg := SiteConfig{
		ID:       "testsite",
		SiteBase: "https://test.local",
		SiteName: "Test Site",
		MatchRe:  regexp.MustCompile(`test\.local`),
	}
	c := listingCard{
		id:         "796",
		url:        "https://test.local/videos/796/",
		title:      "Test Scene",
		duration:   660,
		performers: []string{"Model A"},
		tags:       []string{"Tag1"},
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	scene := c.toScene(cfg, "https://test.local/tour.php?cat=3", now)

	if scene.StudioURL != "https://test.local/tour.php?cat=3" {
		t.Errorf("StudioURL = %q", scene.StudioURL)
	}

	if scene.ID != "796" {
		t.Errorf("ID = %q", scene.ID)
	}
	if scene.SiteID != "testsite" {
		t.Errorf("SiteID = %q", scene.SiteID)
	}
	if scene.Studio != "Test Site" {
		t.Errorf("Studio = %q", scene.Studio)
	}
}

// Config-only wrapper: parsing tests live in the *util it delegates to. What is
// not covered there is this table's own integrity — see testutil.CheckSiteTable.
// Every row here is its own domain, so the domain-consistency checks apply too.
func TestSiteTableIntegrity(t *testing.T) {
	rows := make([]testutil.SiteRow, 0, len(sites))
	for _, c := range sites {
		rows = append(rows, testutil.SiteRow{
			ID: c.ID, Base: c.SiteBase, Studio: c.SiteName,
			Patterns: c.Patterns, MatchRe: c.MatchRe,
		})
	}
	testutil.CheckSiteTable(t, rows)
	testutil.CheckSiteTableDomains(t, rows)
}
