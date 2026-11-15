package pinko

import (
	"testing"
	"time"
)

func tgScraper() *Scraper {
	return New(SiteConfig{SiteID: "pinkotgirls", Domain: "pinkotgirls.com", Base: "https://www.pinkotgirls.com", StudioName: "Pinko TGirls", DetailPrefix: "/videotrans/"})
}

func pcScraper() *Scraper {
	return New(SiteConfig{SiteID: "pinkoclub", Domain: "pinkoclub.com", Base: "https://www.pinkoclub.com", StudioName: "Pinko Club", Modern: true})
}

// Real markup captured from https://www.pinkotgirls.com/new-video.php?next=1
const tgListingFixture = `
<div class="box-home">
<a class="link-photo-home" href="/videotrans/7818-amanda-si-fa-filmare-mentre-viene-trapanata-e-gode.php" title="Amanda Gets Filmed While Getting Pounded and Loving It"><img width="100%" src="https://img.pinkocdn.com/image/MHM001/16_9.jpg" alt="Amanda Gets Filmed While Getting Pounded and Loving It" class="photo-home"></a>
<h4><a href="/videotrans/7818-amanda-si-fa-filmare-mentre-viene-trapanata-e-gode.php" title="Amanda Gets Filmed While Getting Pounded and Loving It">Amanda Gets Filmed While Getting Pounded and Loving It</a></h4>
</div>
<div class="box-home">
<a class="link-photo-home" href="/videotrans/7812-nicole-e-thaysa.php" title="Nicole &amp; Thaysa: mutual anal"><img width="100%" src="https://img.pinkocdn.com/image/JAZZ050/899-16_9.jpg" alt="x" class="photo-home"></a>
<h4><a href="/videotrans/7812-nicole-e-thaysa.php" title="Nicole &amp; Thaysa: mutual anal">Nicole &amp; Thaysa</a></h4>
</div>
<div class="lazy"><img src="https://img.pinkocdn.com/image//" alt=""></div>
`

// Real markup captured from https://www.pinkoclub.com/new-video.php?page=1 (modern template).
// One card uses the /video-porno-italiani/ line, one uses /frameleaks/ — both
// must resolve to the same generic ID extraction.
const pcListingFixture = `
<div class="results-grid" id="results">
                <article class="card">
  <a href="/video-porno-italiani/7836-the-last-dance--inculata-nella-realta-virtuale.php">
    <div class="card__media">
              <span class="card__tag card__tag--icon"><img src="https://www.pinkoclub.com/image/icon-opera-left.png" alt="Opera" loading="lazy"></span>
            <img src="https://img.pinkocdn.com/image/SC570/903-16_9.jpg" alt="The Last Dance – Ass Fucked in Virtual Reality" loading="lazy">
      <span class="card__play" aria-hidden="true">
        <svg viewBox="0 0 24 24" fill="currentColor"><path d="M8 5v14l11-7L8 5Z"/></svg>
      </span>
    </div>
    <h3 class="card__title">The Last Dance – Ass Fucked in Virtual Reality</h3>
    <p class="card__meta"><b>HD</b> &middot; 42 min</p>
  </a>
</article>
<article class="card">
  <a href="/frameleaks/7819-che-inculata-per-silvia-soprano.php">
    <div class="card__media">
              <span class="card__tag card__tag--icon"><img src="https://www.pinkoclub.com/image/fl-icon.png" alt="Frameleaks" loading="lazy"></span>
            <img src="https://img.pinkocdn.com/image/SC577S/16_9.jpg" alt="What an Ass-Fucking for Silvia Soprano" loading="lazy">
      <span class="card__play" aria-hidden="true">
        <svg viewBox="0 0 24 24" fill="currentColor"><path d="M8 5v14l11-7L8 5Z"/></svg>
      </span>
    </div>
    <h3 class="card__title">What an Ass-Fucking for Silvia Soprano</h3>
    <p class="card__meta"><b>HD</b> &middot; 35 min</p>
  </a>
</article>
</div>
`

// Real markup captured from a /videotrans/ detail page.
const tgDetailFixture = `
<meta property="og:title" content="Amanda Gets Filmed While Getting Pounded and Loving It" />
<meta property="og:description" content="Stunning Amanda drives her partner wild. Intense finale." />
<meta property="og:image" content="https://img.pinkocdn.com/image/MHM001/16_9.jpg" />
<div class="titolo-video">
	<h2>Amanda Gets Filmed While Getting Pounded and Loving It</h2>
	<h4><a href="/trans-star/amanda-araujo.php">Amanda Araujo</a>, <a href="/trans-star/paulo-machy.php">Paulo Machy</a></h4>
	<div class="caption captionvideo">Some caption text.</div>
</div>
<script type="application/ld+json">{"@type":"VideoObject","name":"Summer Brook","author":"primalfetishnetwork"}</script>
`

// Real markup captured from https://www.pinkoclub.com/video-porno-italiani/7836-...php
// (modern template detail page). Trimmed to the fields the parser reads.
const pcDetailFixture = `
<meta property="og:title" content="The Last Dance – Ass Fucked in Virtual Reality | Pinko Club">
<meta property="og:description" content="Watch &quot;The Last Dance – Ass Fucked in Virtual Reality&quot; streaming on Pinko Club: 4K player, details, performers and related content.">
<meta property="og:image" content="https://img.pinkocdn.com/image/SC570/903-16_9.jpg">
<h1 class="video-title">The Last Dance – Ass Fucked in Virtual Reality</h1>
<div class="video-meta">
  <span>6.434 views</span>
  <span>09/07/2026</span>
  <span>42 min</span>
  <span>20 likes</span>
</div>
<p class="video-desc" id="video-desc">
  <span class="video-desc__clip">Deep in a secret underground military bunker…</span>
  <span class="video-desc__full">Deep in a secret underground military bunker, Christian Clay wakes up on a medical table for one final extreme experiment.</span>
</p>
<aside>
  <div class="side-card side-card--performer">
    <h3>Performers</h3>
    <div class="performer">
      <img src="/image/star/thumb/729.jpg" alt="Giulia Diamond" loading="lazy">
      <div>
        <strong>Giulia Diamond</strong>
        <a href="/pornostar/giulia-diamond.php">View profile &rarr;</a>
      </div>
    </div>
    <div class="performer">
      <img src="/pinkoclub/image/avatar.svg" alt="Christian Clay" loading="lazy">
      <div>
        <strong>Christian Clay</strong>
        <a href="/pornostar/christian-clay.php">View profile &rarr;</a>
      </div>
    </div>
  </div>
</aside>
`

func TestListURL(t *testing.T) {
	s := tgScraper()
	if got, want := s.listURL(1), "https://www.pinkotgirls.com/new-video.php?next=1"; got != want {
		t.Errorf("listURL(1) = %q, want %q", got, want)
	}
	if got, want := s.listURL(7), "https://www.pinkotgirls.com/new-video.php?next=7"; got != want {
		t.Errorf("listURL(7) = %q, want %q", got, want)
	}
	if got, want := pcScraper().listURL(2), "https://www.pinkoclub.com/new-video.php?page=2"; got != want {
		t.Errorf("pc listURL(2) = %q, want %q", got, want)
	}
}

func TestParseListingTGirls(t *testing.T) {
	cards := tgScraper().parseListing([]byte(tgListingFixture))
	if len(cards) != 2 {
		t.Fatalf("got %d cards, want 2: %+v", len(cards), cards)
	}
	c0 := cards[0]
	if c0.id != "7818" {
		t.Errorf("id = %q, want 7818", c0.id)
	}
	if c0.url != "https://www.pinkotgirls.com/videotrans/7818-amanda-si-fa-filmare-mentre-viene-trapanata-e-gode.php" {
		t.Errorf("url = %q", c0.url)
	}
	if c0.title != "Amanda Gets Filmed While Getting Pounded and Loving It" {
		t.Errorf("title = %q", c0.title)
	}
	if c0.thumb != "https://img.pinkocdn.com/image/MHM001/16_9.jpg" {
		t.Errorf("thumb = %q", c0.thumb)
	}
	// HTML entity in title attribute must be unescaped.
	if cards[1].id != "7812" || cards[1].title != "Nicole & Thaysa: mutual anal" {
		t.Errorf("card[1] = %+v", cards[1])
	}
}

func TestParseListingClub(t *testing.T) {
	cards := pcScraper().parseListing([]byte(pcListingFixture))
	if len(cards) != 2 {
		t.Fatalf("got %d cards, want 2: %+v", len(cards), cards)
	}
	// First card is on the /video-porno-italiani/ line.
	c0 := cards[0]
	if c0.id != "7836" {
		t.Errorf("id = %q, want 7836", c0.id)
	}
	if c0.url != "https://www.pinkoclub.com/video-porno-italiani/7836-the-last-dance--inculata-nella-realta-virtuale.php" {
		t.Errorf("url = %q", c0.url)
	}
	if c0.title != "The Last Dance – Ass Fucked in Virtual Reality" {
		t.Errorf("title = %q", c0.title)
	}
	if c0.thumb != "https://img.pinkocdn.com/image/SC570/903-16_9.jpg" {
		t.Errorf("thumb = %q", c0.thumb)
	}
	// Second card is on the /frameleaks/ line — the generic ID regex must
	// still match it.
	c1 := cards[1]
	if c1.id != "7819" {
		t.Errorf("id = %q, want 7819", c1.id)
	}
	if c1.url != "https://www.pinkoclub.com/frameleaks/7819-che-inculata-per-silvia-soprano.php" {
		t.Errorf("url = %q", c1.url)
	}
	if c1.title != "What an Ass-Fucking for Silvia Soprano" {
		t.Errorf("title = %q", c1.title)
	}
}

func TestParseListingClubLocalePrefix(t *testing.T) {
	cards := pcScraper().parseListing([]byte(`<article class="card"><a href="/en/video-porno-italiani/7914-the-best-man.php"><img src="https://img.pinkocdn.com/image/SC598/912-16_9.jpg"><h3 class="card__title">The Best Man</h3></a></article>`))
	if len(cards) != 1 || cards[0].id != "7914" {
		t.Fatalf("got %+v", cards)
	}
}

func TestParseDetailTGirls(t *testing.T) {
	d := parseDetailLegacy([]byte(tgDetailFixture))
	if d.title != "Amanda Gets Filmed While Getting Pounded and Loving It" {
		t.Errorf("title = %q", d.title)
	}
	if d.description != "Stunning Amanda drives her partner wild. Intense finale." {
		t.Errorf("description = %q", d.description)
	}
	if d.image != "https://img.pinkocdn.com/image/MHM001/16_9.jpg" {
		t.Errorf("image = %q", d.image)
	}
	if len(d.performers) != 2 || d.performers[0] != "Amanda Araujo" || d.performers[1] != "Paulo Machy" {
		t.Errorf("performers = %v", d.performers)
	}
	if !d.date.IsZero() {
		t.Errorf("date = %v, want zero (legacy template has no date)", d.date)
	}
	if d.duration != 0 {
		t.Errorf("duration = %v, want 0 (legacy template has no duration)", d.duration)
	}
}

func TestParseDetailClub(t *testing.T) {
	d := parseDetailModern([]byte(pcDetailFixture))
	if d.title != "The Last Dance – Ass Fucked in Virtual Reality" {
		t.Errorf("title = %q", d.title)
	}
	if d.description != "Deep in a secret underground military bunker, Christian Clay wakes up on a medical table for one final extreme experiment." {
		t.Errorf("description = %q", d.description)
	}
	if d.image != "https://img.pinkocdn.com/image/SC570/903-16_9.jpg" {
		t.Errorf("image = %q", d.image)
	}
	if len(d.performers) != 2 || d.performers[0] != "Giulia Diamond" || d.performers[1] != "Christian Clay" {
		t.Errorf("performers = %v", d.performers)
	}
	wantDate := time.Date(2026, time.July, 9, 0, 0, 0, 0, time.UTC)
	if !d.date.Equal(wantDate) {
		t.Errorf("date = %v, want %v", d.date, wantDate)
	}
	if d.duration != 42*60 {
		t.Errorf("duration = %v, want %v", d.duration, 42*60)
	}
}

func TestParseDetailClubTitleFallsBackToOGTitle(t *testing.T) {
	body := []byte(`
<meta property="og:title" content="Some Scene Title | Pinko Club">
<meta property="og:image" content="https://img.pinkocdn.com/image/x.jpg">
`)
	d := parseDetailModern(body)
	if d.title != "Some Scene Title" {
		t.Errorf("title = %q, want %q", d.title, "Some Scene Title")
	}
}

func TestMatchesURL(t *testing.T) {
	tg := tgScraper()
	pc := pcScraper()
	cases := []struct {
		s    *Scraper
		url  string
		want bool
	}{
		{tg, "https://www.pinkotgirls.com/new-video.php", true},
		{tg, "https://pinkotgirls.com/", true},
		{tg, "https://www.pinkoclub.com/new-video.php", false},
		{pc, "https://www.pinkoclub.com/new-video.php?page=1", true},
		{pc, "https://www.pinkotgirls.com/", false},
		{tg, "https://example.com/", false},
	}
	for _, c := range cases {
		if got := c.s.MatchesURL(c.url); got != c.want {
			t.Errorf("%s MatchesURL(%q) = %v, want %v", c.s.ID(), c.url, got, c.want)
		}
	}
}
