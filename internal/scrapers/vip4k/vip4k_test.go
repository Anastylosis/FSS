package vip4k

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

const testCard = `<div class="item">
            <a class="item__main"  href="/en/videos/1350?poster_alt_ids%%5B0%%5D=4475"  aria-label="Deep Green Field Of Experiments">
                <div class="item__image">
                    <picture class="item__inner ">
                        <source srcset="//cdn.black4k.com/content/sets/abc/thumb.webp" type="image/webp">
                        <source srcset="//cdn.black4k.com/content/sets/abc/thumb.jpg" type="image/jpeg">
                        <img loading="lazy" src="//cdn.black4k.com/content/sets/abc/thumb.jpg" alt="" />
                    </picture>
                    <div class="item__time">51:43</div>
                </div>
                <div class="item__preview-container" style="display: none;">
                    <video width="100%%" class="item__preview" preload="none" playsinline muted loop>
                        <source data-src="//cdn.black4k.com/content/sets/abc/preview.mp4" type="video/mp4">
                    </video>
                </div>
            </a>
            <div class="item__description">
                <div class="item__info">
                    <a class="item__site" href="https://vip4k.com/signup/">Hunt 4k</a>
                </div>
                <a class="item__title" href="/en/videos/1350?poster_alt_ids%%5B0%%5D=4475">Deep Green Field Of Experiments</a>
                <div class="item__about">
                    <div class="item__model">
                        <div class="item-model">
                            <a class="item-model__pic" aria-label="Wendy Marvell">
                                <picture><img src="model.jpg" /></picture>
                            </a>
                            <a class="item-model__text">Wendy Marvell</a>
                        </div>
                    </div>
                    <div class="item__date">2026-05-21</div>
                </div>
            </div>
        </div>`

const testDetail = `<html>
<div class="player-description__text">A chance encounter on a quiet country road.</div>
<div class="player-description__tags">
    <div class="tags">
        <a class="tags__item ph_register" href="#">Babe</a>
        <a class="tags__item ph_register" href="#">Blowjob</a>
        <a class="tags__item ph_register" href="#">Anal</a>
    </div>
</div>
<div class="player-description__models">
    <div class="model__name">Wendy Marvell</div>
    <div class="model__name">Stanley Johnson</div>
</div>
</html>`

func TestParseListingPage(t *testing.T) {
	items := parseListingPage([]byte(testCard))
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}

	it := items[0]
	if it.id != "1350" {
		t.Errorf("id = %q, want 1350", it.id)
	}
	if it.title != "Deep Green Field Of Experiments" {
		t.Errorf("title = %q", it.title)
	}
	if it.channel != "Hunt 4k" {
		t.Errorf("channel = %q, want Hunt 4k", it.channel)
	}
	if it.date.Format("2006-01-02") != "2026-05-21" {
		t.Errorf("date = %v", it.date)
	}
	if it.duration != 3103 {
		t.Errorf("duration = %d, want 3103", it.duration)
	}
	if it.thumbnail != "https://cdn.black4k.com/content/sets/abc/thumb.jpg" {
		t.Errorf("thumbnail = %q", it.thumbnail)
	}
	if it.preview != "https://cdn.black4k.com/content/sets/abc/preview.mp4" {
		t.Errorf("preview = %q", it.preview)
	}
	if it.performer != "Wendy Marvell" {
		t.Errorf("performer = %q", it.performer)
	}
}

func TestParseDetailPage(t *testing.T) {
	d := parseDetailPage([]byte(testDetail))

	if d.description != "A chance encounter on a quiet country road." {
		t.Errorf("description = %q", d.description)
	}
	if len(d.tags) != 3 || d.tags[0] != "Babe" || d.tags[1] != "Blowjob" || d.tags[2] != "Anal" {
		t.Errorf("tags = %v", d.tags)
	}
	if len(d.performers) != 2 || d.performers[0] != "Wendy Marvell" || d.performers[1] != "Stanley Johnson" {
		t.Errorf("performers = %v", d.performers)
	}
}

func TestMatchesURL(t *testing.T) {
	s := New()
	tests := []struct {
		url  string
		want bool
	}{
		{"https://vip4k.com", true},
		{"https://vip4k.com/", true},
		{"https://www.vip4k.com/en/videos/1350", true},
		{"https://hunt4k.com", false},
		{"https://black4k.com", false},
		{"https://example.com", false},
	}
	for _, tt := range tests {
		if got := s.MatchesURL(tt.url); got != tt.want {
			t.Errorf("MatchesURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestChannelToSiteID(t *testing.T) {
	tests := []struct {
		channel string
		want    string
	}{
		{"Hunt 4k", "hunt-4k"},
		{"Black 4k", "black-4k"},
		{"Daddy 4k", "daddy-4k"},
		{"BUFU.xxx", "bufu-xxx"},
		{"Sis.Porn", "sis-porn"},
		{"Pinhole.xxx", "pinhole-xxx"},
		{"", ""},
	}
	for _, tt := range tests {
		got := channelToSiteID(tt.channel)
		if got != tt.want {
			t.Errorf("channelToSiteID(%q) = %q, want %q", tt.channel, got, tt.want)
		}
	}
}

func TestToScene(t *testing.T) {
	it := listItem{
		id:        "1350",
		title:     "Deep Green Field Of Experiments",
		channel:   "Hunt 4k",
		date:      time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC),
		duration:  3103,
		thumbnail: "https://cdn.black4k.com/thumb.jpg",
		preview:   "https://cdn.black4k.com/preview.mp4",
		performer: "Wendy Marvell",
	}
	d := detailData{
		description: "A chance encounter",
		tags:        []string{"Babe", "Blowjob"},
		performers:  []string{"Wendy Marvell", "Stanley Johnson"},
	}
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	scene := toScene(it, d, "https://vip4k.com", now)

	if scene.ID != "1350" {
		t.Errorf("ID = %q", scene.ID)
	}
	if scene.SiteID != "hunt-4k" {
		t.Errorf("SiteID = %q, want hunt-4k", scene.SiteID)
	}
	if scene.Title != "Deep Green Field Of Experiments" {
		t.Errorf("Title = %q", scene.Title)
	}
	if scene.URL != "https://vip4k.com/en/videos/1350" {
		t.Errorf("URL = %q", scene.URL)
	}
	if scene.Duration != 3103 {
		t.Errorf("Duration = %d", scene.Duration)
	}
	if len(scene.Performers) != 2 {
		t.Errorf("Performers = %v (want 2 from detail)", scene.Performers)
	}
	if scene.Description != "A chance encounter" {
		t.Errorf("Description = %q", scene.Description)
	}
	if scene.Studio != "Hunt 4k" {
		t.Errorf("Studio = %q", scene.Studio)
	}
	if scene.Thumbnail == "" {
		t.Error("Thumbnail is empty")
	}
	if scene.Preview == "" {
		t.Error("Preview is empty")
	}
}

func TestToSceneFallbackPerformer(t *testing.T) {
	it := listItem{
		id:        "100",
		performer: "Alice",
	}
	d := detailData{}
	scene := toScene(it, d, "https://vip4k.com", time.Now())

	if len(scene.Performers) != 1 || scene.Performers[0] != "Alice" {
		t.Errorf("Performers = %v, want [Alice]", scene.Performers)
	}
}

func TestDeduplicateCards(t *testing.T) {
	body := []byte(testCard + testCard)
	items := parseListingPage(body)
	if len(items) != 1 {
		t.Errorf("got %d items, want 1 (should deduplicate)", len(items))
	}
}

func TestEnsureHTTPS(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"//cdn.black4k.com/img.jpg", "https://cdn.black4k.com/img.jpg"},
		{"https://cdn.black4k.com/img.jpg", "https://cdn.black4k.com/img.jpg"},
	}
	for _, tt := range tests {
		got := ensureHTTPS(tt.in)
		if got != tt.want {
			t.Errorf("ensureHTTPS(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestHasNextPage(t *testing.T) {
	with := []byte(`<a thumbs-more class="button" href="/en/publish/tag/all/all/all/2">Show more</a>`)
	without := []byte(`<div>no more</div>`)

	if !hasNextPage(with) {
		t.Error("expected hasNextPage=true")
	}
	if hasNextPage(without) {
		t.Error("expected hasNextPage=false")
	}
}

func testListingPage(cards string, nextPage bool) string {
	page := cards
	if nextPage {
		page += `<a thumbs-more class="button" href="/en/publish/tag/all/all/all/2">Show more</a>`
	}
	return page
}

func TestFullScrape(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/en/publish/tag/all/all/all/1":
			_, _ = fmt.Fprint(w, testListingPage(testCard, false))
		case "/en/videos/1350":
			_, _ = fmt.Fprint(w, testDetail)
		default:
			w.WriteHeader(404)
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	out := make(chan scraper.SceneResult)
	go func() {
		defer close(out)
		s.runWithBase(context.Background(), ts.URL, "https://vip4k.com", scraper.ListOpts{}, out)
	}()

	var scenes []scraper.SceneResult
	for r := range out {
		scenes = append(scenes, r)
	}

	sceneCount := 0
	for _, r := range scenes {
		switch r.Kind {
		case scraper.KindScene:
			sceneCount++
			if r.Scene.ID != "1350" {
				t.Errorf("unexpected scene ID %q", r.Scene.ID)
			}
			if r.Scene.Description != "A chance encounter on a quiet country road." {
				t.Errorf("Description = %q", r.Scene.Description)
			}
			if len(r.Scene.Tags) != 3 {
				t.Errorf("Tags = %v", r.Scene.Tags)
			}
			if len(r.Scene.Performers) != 2 {
				t.Errorf("Performers = %v", r.Scene.Performers)
			}
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
	if sceneCount != 1 {
		t.Errorf("got %d scenes, want 1", sceneCount)
	}
}

func TestEarlyStop(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/en/publish/tag/all/all/all/1":
			_, _ = fmt.Fprint(w, testListingPage(testCard, false))
		case "/en/videos/1350":
			_, _ = fmt.Fprint(w, testDetail)
		default:
			w.WriteHeader(404)
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	out := make(chan scraper.SceneResult)
	go func() {
		defer close(out)
		s.runWithBase(context.Background(), ts.URL, "https://vip4k.com", scraper.ListOpts{
			KnownIDs: map[string]bool{"1350": true},
		}, out)
	}()

	var gotEarlyStop bool
	for r := range out {
		if r.Kind == scraper.KindStoppedEarly {
			gotEarlyStop = true
		}
		if r.Kind == scraper.KindScene {
			t.Error("should not have received a scene")
		}
	}
	if !gotEarlyStop {
		t.Error("expected early stop signal")
	}
}

func TestPagination(t *testing.T) {
	card2 := `<div class="item">
            <a class="item__main" href="/en/videos/999" aria-label="Page 2 Scene">
                <div class="item__image">
                    <picture class="item__inner">
                        <source srcset="//cdn.black4k.com/p2.webp" type="image/webp">
                        <source srcset="//cdn.black4k.com/p2.jpg" type="image/jpeg">
                        <img src="//cdn.black4k.com/p2.jpg" />
                    </picture>
                    <div class="item__time">30:00</div>
                </div>
            </a>
            <div class="item__description">
                <div class="item__info">
                    <a class="item__site" href="#">Daddy 4k</a>
                </div>
                <a class="item__title" href="/en/videos/999">Page 2 Scene</a>
                <div class="item__about">
                    <div class="item__date">2026-01-01</div>
                </div>
            </div>
        </div>`

	page2Called := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/en/publish/tag/all/all/all/1":
			_, _ = fmt.Fprint(w, testListingPage(testCard, true))
		case "/en/publish/tag/all/all/all/2":
			page2Called = true
			_, _ = fmt.Fprint(w, testListingPage(card2, false))
		case "/en/videos/1350":
			_, _ = fmt.Fprint(w, testDetail)
		case "/en/videos/999":
			_, _ = fmt.Fprint(w, `<html><div class="player-description__text">P2 desc</div></html>`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	out := make(chan scraper.SceneResult)
	go func() {
		defer close(out)
		s.runWithBase(context.Background(), ts.URL, "https://vip4k.com", scraper.ListOpts{}, out)
	}()

	count := 0
	for r := range out {
		if r.Kind == scraper.KindScene {
			count++
		}
	}

	if !page2Called {
		t.Error("page 2 was never fetched")
	}
	if count != 2 {
		t.Errorf("got %d scenes, want 2", count)
	}
}

func TestScraperInterface(t *testing.T) {
	var _ scraper.StudioScraper = (*Scraper)(nil)
}

// A detail page that will not load costs the description, tags and cast — the
// listing already carries the rest. Returning an error from inside the
// Paginate callback aborted the whole walk, and --full's authoritative Save
// would then delete every scene the run never reached.
func TestDetailFailureKeepsTheSceneAndContinues(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/en/publish/tag/all/all/all/1" {
			_, _ = fmt.Fprint(w, testListingPage(testCard, false))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	s := &Scraper{client: ts.Client(), base: ts.URL}
	out := make(chan scraper.SceneResult)
	go func() {
		defer close(out)
		s.runWithBase(context.Background(), ts.URL, "https://vip4k.com", scraper.ListOpts{}, out)
	}()

	scenes, errs := 0, 0
	for r := range out {
		switch r.Kind {
		case scraper.KindScene:
			scenes++
			if r.Scene.ID != "1350" || r.Scene.Title == "" {
				t.Errorf("the listing fields must survive: %+v", r.Scene)
			}
			if r.Scene.Description != "" {
				t.Errorf("Description = %q, want empty without a detail page", r.Scene.Description)
			}
		case scraper.KindError:
			errs++
		}
	}
	if scenes != 1 {
		t.Errorf("scenes = %d, want 1", scenes)
	}
	if errs == 0 {
		t.Error("the detail failure must still be reported")
	}
}
