package primalfetish

import (
	"testing"
	"time"
)

var tabooCfg = SiteConfig{
	SiteID:     "primalstaboofamily",
	PaysiteID:  14,
	Slug:       "primals-taboo-relations",
	StudioName: "Primal's Taboo Family Relations",
}

// fixtureCard is a listing card trimmed from a real /paysite/primals-taboo-relations/ page.
const fixtureCard = `
<article class="main__videoElement item-col is-nsfw-locked" data-video="">
    <a href="https://primalfetishnetwork.com/videos/isabella-nice-cant-contain-her-crush-on-step-dad-part-one/" class="main__videoWrapper">
        <span class="image-wrapp">
            <img width="896" height="534" src="https://primalfetishnetwork.com/wp-content/uploads/2026/07/isabella-7.jpg" class="main__video wp-post-image" alt="" />
        </span>
    </a>
    <a href="https://primalfetishnetwork.com/videos/isabella-nice-cant-contain-her-crush-on-step-dad-part-one/" class="main__videoTitle">
        Isabella Nice &#8211; Can&#8217;t Contain her Crush on Step-Dad PART ONE    </a>
    <div class="main__info">
        <div class="main__data">
                <div class="main__models">
                    <a class="video__listModel model" title="Michael Masters" href="https://primalfetishnetwork.com/performers/michael-masters/"><span class="sub-label">Michael Masters</span></a>
                    <a class="video__listModel model" title="Isabella Nice" href="https://primalfetishnetwork.com/performers/isabella-nice/"><span class="sub-label">Isabella Nice</span></a>
                </div>
            <div class="main__timedate">
                <div class="date">Date: July 17, 2026</div>
                <div class="time">Time: 12:00 am</div>
            </div>
        </div>
    </div>
    <form class="video-add-to-cart-form" method="post">
        <input type="hidden" name="video-id" value="7590">
    </form>
</article>

<article class="main__videoElement item-col">
    <a href="#" class="main__videoTitle">placeholder</a>
</article>
`

func TestParseListing(t *testing.T) {
	s := New(tabooCfg)
	scenes := s.parseListing([]byte(fixtureCard), "https://primalfetishnetwork.com/paysite/primals-taboo-relations/")

	if len(scenes) != 1 {
		t.Fatalf("got %d scenes, want 1 (placeholder card must be skipped)", len(scenes))
	}
	sc := scenes[0]

	if sc.ID != "7590" {
		t.Errorf("ID = %q, want 7590", sc.ID)
	}
	if sc.SiteID != "primalstaboofamily" || sc.Studio != "Primal's Taboo Family Relations" {
		t.Errorf("SiteID/Studio = %q/%q", sc.SiteID, sc.Studio)
	}
	if sc.Title != "Isabella Nice – Can’t Contain her Crush on Step-Dad PART ONE" {
		t.Errorf("Title = %q", sc.Title)
	}
	if sc.URL != "https://primalfetishnetwork.com/videos/isabella-nice-cant-contain-her-crush-on-step-dad-part-one/" {
		t.Errorf("URL = %q", sc.URL)
	}
	want := time.Date(2026, time.July, 17, 0, 0, 0, 0, time.UTC)
	if !sc.Date.Equal(want) {
		t.Errorf("Date = %v, want %v", sc.Date, want)
	}
	if len(sc.Performers) != 2 || sc.Performers[0] != "Michael Masters" || sc.Performers[1] != "Isabella Nice" {
		t.Errorf("Performers = %v", sc.Performers)
	}
	if sc.Thumbnail != "https://primalfetishnetwork.com/wp-content/uploads/2026/07/isabella-7.jpg" {
		t.Errorf("Thumbnail = %q", sc.Thumbnail)
	}
	if sc.ScrapedAt.IsZero() {
		t.Error("ScrapedAt is zero")
	}
}

func TestPageURL(t *testing.T) {
	s := New(tabooCfg)
	tests := []struct {
		page int
		want string
	}{
		{1, "https://primalfetishnetwork.com/paysite/primals-taboo-relations/"},
		{2, "https://primalfetishnetwork.com/paysite/primals-taboo-relations/page/2/"},
		{5, "https://primalfetishnetwork.com/paysite/primals-taboo-relations/page/5/"},
	}
	for _, tt := range tests {
		if got := s.pageURL(tt.page); got != tt.want {
			t.Errorf("pageURL(%d) = %q, want %q", tt.page, got, tt.want)
		}
	}
}

func TestMatchesURL(t *testing.T) {
	taboo := New(tabooCfg)
	cosplay := New(SiteConfig{SiteID: "primalscosplay", PaysiteID: 32, Slug: "cosplay", StudioName: "Primal's Cosplay"})

	for _, u := range []string{
		"https://primalfetishnetwork.com/paysite/primals-taboo-relations/",
		"https://primalfetishnetwork.com/paysite/primals-taboo-relations/page/2/",
		"https://primalfetishnetwork.com/paysites/14/primals-taboo-relations/videos/",
	} {
		if !taboo.MatchesURL(u) {
			t.Errorf("taboo should match %s", u)
		}
		if cosplay.MatchesURL(u) {
			t.Errorf("cosplay must not match %s", u)
		}
	}
	if !cosplay.MatchesURL("https://primalfetishnetwork.com/paysite/cosplay/") {
		t.Error("cosplay should match its own URL")
	}
	if cosplay.MatchesURL("https://example.com/paysite/cosplay/") {
		t.Error("must not match non-primalfetish hosts")
	}
}
