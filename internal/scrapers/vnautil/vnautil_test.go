package vnautil

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

// Template A (vid-block) — sarajay
const templateAFixture = `<html><body>
<div class="videos-block">
<div class="vid-block">
    <div class="block-title block-title--top  flex">
      <p class="member-block_content__title">
        420 Nurse
      </p>
      <p class="date">April 24th 2026</p>
    </div>
    <div class="wrap-video-thumb flex">
      <a href="videos/39082/420-nurse#video1"><img src="sd3.php?show=file&path=/videos/38882/thumb_1.jpg" alt="">
      <img src="sd3.php?show=file&path=/videos/38882/thumb_2.jpg" alt=""></a>
    </div>
    <div class="block-title flex">
      <div class="vid-data">
        <p>Section <a href="videos/39082/420-nurse#video1">Solo Videos</a></p>
        <p>Stars Appearing:<a href="join.html"> Sara Jay</a></p>
        <p>Hot solo video description text here.</p>
<p><span onclick='addtocart(38882,"v")'>Download this clip for $15.95 </span></p>
      </div>
    </div>
</div><div class="vid-block">
    <div class="block-title block-title--top  flex">
      <p class="member-block_content__title">
        Thick Rehearsal
      </p>
      <p class="date">March 27th 2026</p>
    </div>
    <div class="wrap-video-thumb flex">
      <a href="videos/39042/thick-rehearsal#video1"><img src="sd3.php?show=file&path=/videos/38842/thumb_1.jpg" alt=""></a>
    </div>
    <div class="block-title flex">
      <div class="vid-data">
        <p>Section <a href="videos/39042/thick-rehearsal#video1">Hardcore Videos</a></p>
        <p>Stars Appearing:<a href="join.html"> Stephanie Love, Dickdealer Don, Sara Jay</a></p>
        <p>Hot threeway scene description text here.</p>
<p><span onclick='addtocart(38842,"v")'>Download this clip for $15.95 </span></p>
      </div>
    </div>
</div>
</div>
<div class='pagenav'><span class='current'>1</span><a href='videos/page/2'>2</a><a href='videos/page/94'>&gt;&gt;</a></div>
</body></html>`

// Template B (videoarea) — fuckedfeet, charleechase, most sites
const templateBFixture = `<html><body>
<div class="videoarea clear">
    <h3><a href="join.html">Licking Summer Bella's Feet!</a></h3>
    <p class="date">May 14th 2026</p>
    <div class="videos clear">
        <div class="video_pic"><a href="videos/39122/licking-summer-bellas-feet"><img src="sd3.php?show=file&path=/videos/38922/thumb_1.jpg" alt=""></a></div>
    </div>
    <div class="video_details clear">
        <h5><a href="videos/39122/licking-summer-bellas-feet">Featuring: Summer Bella</a></h5>
        <p>Great foot scene with lots of fun and excitement.</p>
<span onclick='addtocart(38922,"v")'>Download this clip for $9.95 </span>
    </div>
</div> </div>

<div class="videoarea clear">
    <h3><a href="join.html">Feet in the Pose!</a></h3>
    <p class="date">April 30th 2026</p>
    <div class="videos clear">
        <div class="video_pic"><a href="videos/39089/feet-in-the-pose"><img src="sd3.php?show=file&path=/videos/38889/thumb_1.jpg" alt=""></a></div>
    </div>
    <div class="video_details clear">
        <h4><a href="videos/39089/feet-in-the-pose">Section: FootFetish Videos</a></h4>
        <h5>Featuring: Bella Rose, Another Star</h5>
        <p>Cool scene description with enough text to be captured here.</p>
<span onclick='addtocart(38889,"v")'>Download this clip for $9.95 </span>
    </div>
</div> </div>
<div class='pagenav'><span class='current'>1</span><a href='videos/page/2'>2</a><a href='videos/page/106'>&gt;&gt;</a></div>
</body></html>`

// Template C (img-new) — vickyathome (tags + duration inline, no detail page needed)
const templateCFixture = `<html><body>
<div class="img-new"><a href="milf-videos/39124/busty-shower-tease"><img src="sd3.php?show=file&path=/videos/38924/thumb_1.jpg" style="max-height:400px;" alt=""></a></div>
<div class="img-new"><a href="milf-videos/39124/busty-shower-tease"><img src="sd3.php?show=file&path=/videos/38924/thumb_2.jpg" style="max-height:400px;" alt=""></a></div>
</div>
<h3><a href="milf-videos/39124/busty-shower-tease">Mediterranean Milf</a></h3>
<div class="section">Section: <span style="color:red;">Solo</span>  l  Posted May 8th 2026 <br> Featuring: Vicky Vette | Duration: <strong>00:03:40</strong></div>
<p>Private video from vacation, great shower tease adventure!</p>
<p style="color:#ffaf00 !important;"><span onclick='addtocart(38924,"v")'>Download this clip for $9.95 </span></p>
<p>Tags: Amateur,Busty,Cougar,Mature,MILF,</p>

<div class="img-new"><a href="milf-videos/39071/back-to-school"><img src="sd3.php?show=file&path=/videos/38871/thumb_1.jpg" style="max-height:400px;" alt=""></a></div>
</div>
<h3><a href="milf-videos/39071/back-to-school">Back to School</a></h3>
<div class="section">Section: <span style="color:red;">Solo</span>  l  Posted April 10th 2026 <br> Featuring: Vicky Vette | Duration: <strong>00:21:53</strong></div>
<p>Back to school scene description with lots of fun!</p>
<p style="color:#ffaf00 !important;"><span onclick='addtocart(38871,"v")'>Download this clip for $11.95 </span></p>
<p>Tags: Anal,MILF,Dildo,Mature,</p>
<div class='pagenav'><span class='current'>1</span><a href='milf-videos/page/2'>2</a><a href='milf-videos/page/240'>&gt;&gt;</a></div>
</body></html>`

// Template D (videoPost) — angelinacastrolive
const templateDFixture = `<html><body>
<div class="videoPosts">
<div class="videoPost clear">
    <h3>My Massive Cuban Boobs!</h3>
    <div class="date">March 4th 2026</div>
    <div class="videoPics clear">
        <div class="videoPic"><a href="videos/38983/my-massive-cuban-boobs"><img style="width:100%;" src="sd3.php?show=file&path=/videos/38783/thumb_1.jpg" alt=""></a></div>
    </div>
    <div class="videoContent">
        <span>The Story:</span>
I just like jerking it so much. You like my Cuban tits?<br><span>Starring:  Angelina Castro</span>
<span onclick='addtocart(38783,"v")'>Download this clip for $9.95 </span>
    </div>
</div>
</div>
<div class='pagenav'><span class='current'>1</span><a href='videos/page/2'>2</a><a href='videos/page/122'>&gt;&gt;</a></div>
</body></html>`

// Template E (updatedVideo) — juliaannlive
const templateEFixture = `<html><body>
<div class="updatedVideos clear">
<div class="updatedVideo clear">
    <h3>Julia</h3>
    <div class="posted">Posted May 8th 2026</div>
    <div class="videoPics clear">
        <div class="videoPic"><a href="videos/39115/julia"><img src="sd3.php?show=file&path=/videos/38915/thumb_1.jpg" alt=""></a></div>
    </div>
    <div class="videoDetails">
        <p><span>Section:</span> Julia Ann Videos <span>Models:</span> Julia Ann <br>
<span onclick='addtocart(38915,"v")'>Download this clip for $9.95 </span></p>
    </div>
</div>
</div>
<div class='pagenav'><span class='current'>1</span><a href='videos/page/2'>2</a><a href='videos/page/95'>&gt;&gt;</a></div>
</body></html>`

// Template F (videos/videos_title) — sunnylanelive
const templateFFixture = `<html><body>
<div class="videos clear">
<div class="videos_title clear">
<h3><a href="videos/37256/red-heart-seduction">RED HEART SEDUCTION</a></h3>
<p>Date: <span>January 9th 2024</span></p></div>
<div class="videos_here clear">
<div class="video_framebox"><a href="videos/37256/red-heart-seduction"><img class="video_frameimg" src="sd3.php?show=file&path=/videos/37057/thumb_1.jpg" alt=""></a></div>
</div>
<div class="vids_details clear">
<p><span>Section: <a href="videos/37256/red-heart-seduction">Solo Videos</a></span>
NEW SOLO TEASE! Great description text for the seduction scene here.<br>
<span onclick='addtocart(37057,"v")'>Download this clip for $9.95 </span></p>
</div>
</div>
<div class='pagenav'><span class='current'>1</span><a href='videos/page/2'>2</a><a href='videos/page/30'>&gt;&gt;</a></div>
</body></html>`

// Template G (latest-video-grid/item) — itscleolive
const templateGFixture = `<html><body>
<ul class="latest-video-grid">
<li class="item">
    <a href="videos/39087/vna-pole-dancing">
        <h4 class="video-title">VNA Pole Dancing!</h4>
        <div class="video-thumb">
            <ul><a href="videos/39087/vna-pole-dancing">
                <li><img src="sd3.php?show=file&path=/videos/38887/thumb_1.jpg" alt="" /></li></a>
            </ul>
        </div>
    </a>
    <div class="video-details">
        <div class="video-left">
            <p class="added-date"> April 24th 2026 </p>
            <p>Featuring: Its Cleo</p>
            <p>Hot pole dancing scene with great moves and fun!</p>
<span onclick='addtocart(38887,"v")'>Download this clip for $9.95 </span>
        </div>
    </div>
</li>
</ul>
<div class='pagenav'><span class='current'>1</span><a href='videos/page/2'>2</a><a href='videos/page/50'>&gt;&gt;</a></div>
</body></html>`

const detailFixture = `<html><body>
<div class="customcontent">
<h1 class="customhcolor">420 Nurse</h1>
<h2 class="customhcolor2">Hot solo video description.</h2>
<h3 class="customhcolor">Sara Jay</h3>
<h4 class="customhcolor">Big Tits,Cougar,MILF,Solo Masturbation,Striptease</h4>
</div>
<div class="date-and-covers">
    <div class="">video duration <strong>00:04:54</strong></div>
</div>
</body></html>`

// Template B without performers or description on the listing — womenbyjuliaann
const templateBBareFixture = `<html><body>
<div class="updated_videos clear"><div class="videoarea clear">
            <h3><a href="videos/27059/julia-vicky-together">Julia Vicky Together!</a></h3>
                <p class="date">December 20th 2018</p>
       <div class="videos clear">
             <div class="video_pic"><a href="videos/27059/julia-vicky-together"><img src="sd3.php?show=file&path=/videos/26860/thumb_1.jpg" width="295" height="164" alt=""></a></div>
       </div>
                <div class="video_details clear">
                <h4><a href="videos/27059/julia-vicky-together">Section: Videos - Erotic Scenes</a></h4>
                    <p></p>
<div class="video_download"><a href="join.html"></a></div>
                </div>
            </div> </div>
<div class='pagenav'><span class='current' style='margin:5px;'>1</span></div>
</body></html>`

// Detail page with a nested-markup description and &nbsp-separated performers — womenbyjuliaann
const detailNestedFixture = `<html><head><style>
    .customhcolor2{
        color: black !important;
    }
</style></head><body>
<div class="customcontent">
<h1 style="margin-top:1em;font-size: 1.8em;" class="customhcolor">Julia Vicky Together! </h1>
<div style="margin-top:1em; font-size:1.6em;" class="customhcolor2"><div style="text-align: center;">
<span style="color: #828282;">Julia and Vicky Together! It&#39;s been a long time</span> hampered only by distance.</div> </div>
<h3 style="margin-top:1em; font-size:1.4em;" class="customhcolor">Vicky Vette,&nbspJulia Ann </h3>
<h4 style="margin-top:1em; font-size:1.4em;" class="customhcolor">Big Tits,Blonde,Busty,MILF </h4>
</div>
<div class="date-and-covers">
    <div class="">video duration <strong>00:22:17</strong></div>
</div>
</body></html>`

func TestParseTemplateA(t *testing.T) {
	items := ParseListing([]byte(templateAFixture), "videos")
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}

	item := items[0]
	if item.ID != "39082" {
		t.Errorf("ID = %q, want %q", item.ID, "39082")
	}
	if item.Title != "420 Nurse" {
		t.Errorf("Title = %q", item.Title)
	}
	wantDate := time.Date(2026, 4, 24, 0, 0, 0, 0, time.UTC)
	if !item.Date.Equal(wantDate) {
		t.Errorf("Date = %v, want %v", item.Date, wantDate)
	}
	if item.Category != "Solo Videos" {
		t.Errorf("Category = %q", item.Category)
	}
	if len(item.Performers) != 1 || item.Performers[0] != "Sara Jay" {
		t.Errorf("Performers = %v", item.Performers)
	}
	if item.Price != 15.95 {
		t.Errorf("Price = %f", item.Price)
	}
	if item.Thumbnail != "sd3.php?show=file&path=/videos/38882/thumb_1.jpg" {
		t.Errorf("Thumbnail = %q", item.Thumbnail)
	}

	item2 := items[1]
	if item2.ID != "39042" {
		t.Errorf("item2 ID = %q", item2.ID)
	}
	if len(item2.Performers) != 3 {
		t.Errorf("item2 Performers = %v, want 3", item2.Performers)
	}
}

func TestParseTemplateB(t *testing.T) {
	items := ParseListing([]byte(templateBFixture), "videos")
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}

	item := items[0]
	if item.ID != "39122" {
		t.Errorf("ID = %q, want %q", item.ID, "39122")
	}
	if item.Title != "Licking Summer Bella's Feet!" {
		t.Errorf("Title = %q", item.Title)
	}
	if len(item.Performers) != 1 || item.Performers[0] != "Summer Bella" {
		t.Errorf("Performers = %v", item.Performers)
	}
	if item.Price != 9.95 {
		t.Errorf("Price = %f", item.Price)
	}

	item2 := items[1]
	if item2.ID != "39089" {
		t.Errorf("item2 ID = %q", item2.ID)
	}
	if len(item2.Performers) != 2 {
		t.Errorf("item2 Performers = %v, want 2", item2.Performers)
	}
}

func TestParseTemplateC(t *testing.T) {
	items := ParseListing([]byte(templateCFixture), "milf-videos")
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}

	item := items[0]
	if item.ID != "39124" {
		t.Errorf("ID = %q, want %q", item.ID, "39124")
	}
	if item.Title != "Mediterranean Milf" {
		t.Errorf("Title = %q", item.Title)
	}
	if item.Duration != 220 {
		t.Errorf("Duration = %d, want 220", item.Duration)
	}
	wantTags := []string{"Amateur", "Busty", "Cougar", "Mature", "MILF"}
	if len(item.Tags) != len(wantTags) {
		t.Fatalf("Tags = %v, want %v", item.Tags, wantTags)
	}
	if item.Thumbnail != "sd3.php?show=file&path=/videos/38924/thumb_1.jpg" {
		t.Errorf("Thumbnail = %q", item.Thumbnail)
	}
}

func TestParseTemplateD(t *testing.T) {
	items := ParseListing([]byte(templateDFixture), "videos")
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}

	item := items[0]
	if item.ID != "38983" {
		t.Errorf("ID = %q, want %q", item.ID, "38983")
	}
	if item.Title != "My Massive Cuban Boobs!" {
		t.Errorf("Title = %q", item.Title)
	}
	wantDate := time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)
	if !item.Date.Equal(wantDate) {
		t.Errorf("Date = %v, want %v", item.Date, wantDate)
	}
	if len(item.Performers) != 1 || item.Performers[0] != "Angelina Castro" {
		t.Errorf("Performers = %v", item.Performers)
	}
	if item.Price != 9.95 {
		t.Errorf("Price = %f", item.Price)
	}
}

func TestParseTemplateE(t *testing.T) {
	items := ParseListing([]byte(templateEFixture), "videos")
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}

	item := items[0]
	if item.ID != "39115" {
		t.Errorf("ID = %q, want %q", item.ID, "39115")
	}
	if item.Title != "Julia" {
		t.Errorf("Title = %q", item.Title)
	}
	wantDate := time.Date(2026, 5, 8, 0, 0, 0, 0, time.UTC)
	if !item.Date.Equal(wantDate) {
		t.Errorf("Date = %v, want %v", item.Date, wantDate)
	}
	if len(item.Performers) != 1 || item.Performers[0] != "Julia Ann" {
		t.Errorf("Performers = %v", item.Performers)
	}
}

func TestParseTemplateF(t *testing.T) {
	items := ParseListing([]byte(templateFFixture), "videos")
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}

	item := items[0]
	if item.ID != "37256" {
		t.Errorf("ID = %q, want %q", item.ID, "37256")
	}
	if item.Title != "RED HEART SEDUCTION" {
		t.Errorf("Title = %q", item.Title)
	}
	wantDate := time.Date(2024, 1, 9, 0, 0, 0, 0, time.UTC)
	if !item.Date.Equal(wantDate) {
		t.Errorf("Date = %v, want %v", item.Date, wantDate)
	}
}

func TestParseTemplateG(t *testing.T) {
	items := ParseListing([]byte(templateGFixture), "videos")
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}

	item := items[0]
	if item.ID != "39087" {
		t.Errorf("ID = %q, want %q", item.ID, "39087")
	}
	if item.Title != "VNA Pole Dancing!" {
		t.Errorf("Title = %q", item.Title)
	}
	wantDate := time.Date(2026, 4, 24, 0, 0, 0, 0, time.UTC)
	if !item.Date.Equal(wantDate) {
		t.Errorf("Date = %v, want %v", item.Date, wantDate)
	}
	if len(item.Performers) != 1 || item.Performers[0] != "Its Cleo" {
		t.Errorf("Performers = %v", item.Performers)
	}
}

func TestParseDetail(t *testing.T) {
	d := ParseDetail([]byte(detailFixture))
	if d.Duration != 294 {
		t.Errorf("Duration = %d, want 294", d.Duration)
	}
	wantTags := []string{"Big Tits", "Cougar", "MILF", "Solo Masturbation", "Striptease"}
	if len(d.Tags) != len(wantTags) {
		t.Fatalf("Tags = %v, want %v", d.Tags, wantTags)
	}
	for i, tag := range wantTags {
		if d.Tags[i] != tag {
			t.Errorf("Tags[%d] = %q, want %q", i, d.Tags[i], tag)
		}
	}
}

func TestParseDetailPerformersAndDescription(t *testing.T) {
	d := ParseDetail([]byte(detailFixture))
	if len(d.Performers) != 1 || d.Performers[0] != "Sara Jay" {
		t.Errorf("Performers = %v, want [Sara Jay]", d.Performers)
	}
	if d.Description != "Hot solo video description." {
		t.Errorf("Description = %q", d.Description)
	}

	d = ParseDetail([]byte(detailNestedFixture))
	wantPerf := []string{"Vicky Vette", "Julia Ann"}
	if len(d.Performers) != len(wantPerf) || d.Performers[0] != wantPerf[0] || d.Performers[1] != wantPerf[1] {
		t.Errorf("Performers = %q, want %q", d.Performers, wantPerf)
	}
	wantDesc := "Julia and Vicky Together! It's been a long time hampered only by distance."
	if d.Description != wantDesc {
		t.Errorf("Description = %q, want %q", d.Description, wantDesc)
	}
	if d.Duration != 1337 {
		t.Errorf("Duration = %d, want 1337", d.Duration)
	}
}

func TestParseDetailEmptyPerformers(t *testing.T) {
	body := `<h3 style="margin-top:1em;" class="customhcolor"> </h3>`
	if d := ParseDetail([]byte(body)); len(d.Performers) != 0 {
		t.Errorf("Performers = %q, want none", d.Performers)
	}
}

func TestParseDate(t *testing.T) {
	cases := []struct {
		input string
		want  time.Time
	}{
		{"April 24th 2026", time.Date(2026, 4, 24, 0, 0, 0, 0, time.UTC)},
		{"May 14th 2026", time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC)},
		{"January 1st 2025", time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"December 22nd 2024", time.Date(2024, 12, 22, 0, 0, 0, 0, time.UTC)},
		{"", time.Time{}},
	}
	for _, c := range cases {
		got := ParseDate(c.input)
		if !got.Equal(c.want) {
			t.Errorf("ParseDate(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}

func TestParseDuration(t *testing.T) {
	cases := []struct {
		input string
		want  int
	}{
		{"00:04:54", 294},
		{"00:21:53", 1313},
		{"01:30:00", 5400},
		{"00:03:40", 220},
		{"", 0},
	}
	for _, c := range cases {
		got := ParseDuration(c.input)
		if got != c.want {
			t.Errorf("ParseDuration(%q) = %d, want %d", c.input, got, c.want)
		}
	}
}

func TestHasNextPage(t *testing.T) {
	if !HasNextPage([]byte(templateAFixture), "videos", 1) {
		t.Error("expected page 1 to have next page (template A)")
	}
	if HasNextPage([]byte(templateAFixture), "videos", 94) {
		t.Error("expected page 94 to be last (template A)")
	}
	if !HasNextPage([]byte(templateCFixture), "milf-videos", 1) {
		t.Error("expected page 1 to have next page (template C)")
	}
}

func TestEstimateTotal(t *testing.T) {
	got := EstimateTotal([]byte(templateAFixture), "videos", 5)
	if got != 94*5 {
		t.Errorf("template A total = %d, want %d", got, 94*5)
	}
}

func TestMatchesURL(t *testing.T) {
	s := New(SiteConfig{SiteID: "sarajay", Domain: "sarajay.com", Studio: "Sara Jay", VideoPrefix: "videos"})
	cases := []struct {
		url   string
		match bool
	}{
		{"https://sarajay.com/videos", true},
		{"https://sarajay.com/videos/", true},
		{"https://sarajay.com/videos/page/3", true},
		{"https://www.sarajay.com/videos", true},
		{"https://sarajay.com/sd3.php?show=recent_video_updates", true},
		{"https://sarajay.com/", true},
		{"https://sarajay.com", true},
		{"https://sarajay.com/videos/39082/420-nurse", false},
		{"https://example.com/videos", false},
	}
	for _, c := range cases {
		got := s.MatchesURL(c.url)
		if got != c.match {
			t.Errorf("MatchesURL(%q) = %v, want %v", c.url, got, c.match)
		}
	}
}

// TestMatchesURL_concurrentSafe pins AUDIT.md §Concurrency #2: the previous
// implementation lazily wrote to a package-level map[string]*regexp.Regexp
// from MatchesURL with no locking. Under -race, concurrent invocations would
// flag a data race (and could panic with "concurrent map writes" in prod).
// matchRe is now built once in New() and only read after, so any number of
// goroutines may call MatchesURL in parallel safely.
func TestMatchesURL_concurrentSafe(t *testing.T) {
	scrapers := []*Scraper{
		New(SiteConfig{SiteID: "sarajay", Domain: "sarajay.com", VideoPrefix: "videos"}),
		New(SiteConfig{SiteID: "vnagirls", Domain: "vnagirls.com", VideoPrefix: "videoset"}),
		New(SiteConfig{SiteID: "milfvideos", Domain: "milf.example.com", VideoPrefix: "milf-videos"}),
	}
	urls := []string{
		"https://sarajay.com/videos",
		"https://vnagirls.com/videoset/page/2",
		"https://milf.example.com/milf-videos",
		"https://example.com/unrelated",
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				for _, s := range scrapers {
					for _, u := range urls {
						_ = s.MatchesURL(u)
					}
				}
			}
		}()
	}
	wg.Wait()
}

func TestToScene(t *testing.T) {
	cfg := SiteConfig{SiteID: "sarajay", Domain: "sarajay.com", Studio: "Sara Jay", VideoPrefix: "videos"}
	item := ListItem{
		ID:          "39082",
		Href:        "videos/39082/420-nurse",
		Title:       "420 Nurse",
		Date:        time.Date(2026, 4, 24, 0, 0, 0, 0, time.UTC),
		Thumbnail:   "sd3.php?show=file&path=/videos/38882/thumb_1.jpg",
		Category:    "Solo Videos",
		Performers:  []string{"Sara Jay"},
		Description: "Hot solo video.",
		Price:       15.95,
		Tags:        []string{"Big Tits", "MILF"},
		Duration:    294,
	}
	now := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	sc := ToScene(cfg, "https://sarajay.com", "https://sarajay.com/videos", item, now)

	if sc.ID != "39082" {
		t.Errorf("ID = %q", sc.ID)
	}
	if sc.SiteID != "sarajay" {
		t.Errorf("SiteID = %q", sc.SiteID)
	}
	if sc.URL != "https://sarajay.com/videos/39082/420-nurse" {
		t.Errorf("URL = %q", sc.URL)
	}
	if sc.Studio != "Sara Jay" {
		t.Errorf("Studio = %q", sc.Studio)
	}
	if len(sc.PriceHistory) != 1 || sc.PriceHistory[0].Regular != 15.95 {
		t.Errorf("PriceHistory = %v", sc.PriceHistory)
	}
}

func TestListScenesWithDetail(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/videos/page/1":
			_, _ = fmt.Fprint(w, templateBFixture)
		case "/videos/39122/licking-summer-bellas-feet":
			_, _ = fmt.Fprint(w, detailFixture)
		case "/videos/39089/feet-in-the-pose":
			_, _ = fmt.Fprint(w, detailFixture)
		default:
			_, _ = fmt.Fprint(w, "<html></html>")
		}
	}))
	defer ts.Close()

	cfg := SiteConfig{SiteID: "fuckedfeet", Domain: "fuckedfeet.com", Studio: "Fucked Feet", VideoPrefix: "videos"}
	s := NewWithBase(cfg, ts.URL, ts.Client())
	ch, err := s.ListScenes(context.Background(), ts.URL+"/videos", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}

	var scenes []scraper.SceneResult
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r)
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}

	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}
	if scenes[0].Scene.Duration != 294 {
		t.Errorf("scene[0].Duration = %d, want 294 (from detail)", scenes[0].Scene.Duration)
	}
	if p := scenes[0].Scene.Performers; len(p) != 1 || p[0] != "Summer Bella" {
		t.Errorf("scene[0].Performers = %v, want listing's [Summer Bella] over detail's", p)
	}
	if d := scenes[0].Scene.Description; d != "Great foot scene with lots of fun and excitement." {
		t.Errorf("scene[0].Description = %q, want listing's", d)
	}
}

func TestListScenesDetailFillsMissingListingFields(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/videos/page/1":
			_, _ = fmt.Fprint(w, templateBBareFixture)
		case "/videos/27059/julia-vicky-together":
			_, _ = fmt.Fprint(w, detailNestedFixture)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	cfg := SiteConfig{SiteID: "womenbyjuliaann", Domain: "womenbyjuliaann.com", Studio: "Women By Julia Ann", VideoPrefix: "videos"}
	s := NewWithBase(cfg, ts.URL, ts.Client())
	ch, err := s.ListScenes(context.Background(), ts.URL+"/sd3.php?show=recent_video_updates", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}

	var scenes []scraper.SceneResult
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r)
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}

	if len(scenes) != 1 {
		t.Fatalf("got %d scenes, want 1", len(scenes))
	}
	sc := scenes[0].Scene
	if sc.Title != "Julia Vicky Together!" {
		t.Errorf("Title = %q", sc.Title)
	}
	if want := time.Date(2018, 12, 20, 0, 0, 0, 0, time.UTC); !sc.Date.Equal(want) {
		t.Errorf("Date = %v, want %v", sc.Date, want)
	}
	if len(sc.Performers) != 2 || sc.Performers[0] != "Vicky Vette" || sc.Performers[1] != "Julia Ann" {
		t.Errorf("Performers = %q", sc.Performers)
	}
	if !strings.HasPrefix(sc.Description, "Julia and Vicky Together!") {
		t.Errorf("Description = %q", sc.Description)
	}
	if sc.Duration != 1337 {
		t.Errorf("Duration = %d, want 1337", sc.Duration)
	}
	if len(sc.Tags) != 4 {
		t.Errorf("Tags = %v, want 4", sc.Tags)
	}
	if len(sc.Categories) != 1 || sc.Categories[0] != "Videos - Erotic Scenes" {
		t.Errorf("Categories = %v", sc.Categories)
	}
	if sc.Thumbnail != ts.URL+"/sd3.php?show=file&path=/videos/26860/thumb_1.jpg" {
		t.Errorf("Thumbnail = %q", sc.Thumbnail)
	}
	if sc.URL != ts.URL+"/videos/27059/julia-vicky-together" {
		t.Errorf("URL = %q", sc.URL)
	}
}

func TestListScenesTemplateCNoDetail(t *testing.T) {
	detailCalled := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/milf-videos/page/1":
			_, _ = fmt.Fprint(w, templateCFixture)
		default:
			if r.URL.Path != "/milf-videos/page/2" {
				detailCalled = true
			}
			_, _ = fmt.Fprint(w, "<html></html>")
		}
	}))
	defer ts.Close()

	cfg := SiteConfig{SiteID: "vickyathome", Domain: "vickyathome.com", Studio: "Vicky Vette", VideoPrefix: "milf-videos"}
	s := NewWithBase(cfg, ts.URL, ts.Client())
	ch, err := s.ListScenes(context.Background(), ts.URL+"/milf-videos", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}

	var scenes []scraper.SceneResult
	for r := range ch {
		switch r.Kind {
		case scraper.KindScene:
			scenes = append(scenes, r)
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}

	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}
	if detailCalled {
		t.Error("template C should not fetch detail pages")
	}
	if scenes[0].Scene.Duration != 220 {
		t.Errorf("scene[0].Duration = %d, want 220", scenes[0].Scene.Duration)
	}
}

func TestKnownIDsStopsEarly(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/videos/page/1":
			_, _ = fmt.Fprint(w, templateBFixture)
		default:
			_, _ = fmt.Fprint(w, "<html></html>")
		}
	}))
	defer ts.Close()

	cfg := SiteConfig{SiteID: "test", Domain: "test.com", Studio: "Test", VideoPrefix: "videos"}
	s := NewWithBase(cfg, ts.URL, ts.Client())
	ch, err := s.ListScenes(context.Background(), ts.URL+"/videos", scraper.ListOpts{
		KnownIDs: map[string]bool{"39089": true},
	})
	if err != nil {
		t.Fatalf("ListScenes error: %v", err)
	}

	var scenes []scraper.SceneResult
	sawStoppedEarly := false
	for r := range ch {
		switch r.Kind {
		case scraper.KindStoppedEarly:
			sawStoppedEarly = true
		case scraper.KindScene:
			scenes = append(scenes, r)
		case scraper.KindError:
			t.Errorf("unexpected error: %v", r.Err)
		}
	}

	if len(scenes) != 1 {
		t.Errorf("got %d scenes, want 1", len(scenes))
	}
	if !sawStoppedEarly {
		t.Error("expected StoppedEarly")
	}
}

func TestParseListingUnescapesTitleAndDescription(t *testing.T) {
	body := `<div class="videoarea clear">
            <h3><a href="videos/100/julia-ann-tanya-tate">Julia Ann &amp; Tanya Tate</a></h3>
                <p class="date">January 5th 2016</p>
                <div class="video_details clear">
                    <p>We show that romantic expression doesn&#39;t have to go beyond a massage. &nbsp;</p>
                </div>
            </div>`
	items := ParseListing([]byte(body), "videos")
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if items[0].Title != "Julia Ann & Tanya Tate" {
		t.Errorf("Title = %q", items[0].Title)
	}
	if want := "We show that romantic expression doesn't have to go beyond a massage."; items[0].Description != want {
		t.Errorf("Description = %q, want %q", items[0].Description, want)
	}
}

func TestDedup(t *testing.T) {
	duped := `<html><body>
<div class="videoarea clear">
    <h3><a href="join.html">Scene One</a></h3>
    <div class="video_pic"><a href="videos/100/scene-one"><img src="sd3.php?show=file&path=/videos/100/thumb_1.jpg" alt=""></a></div>
</div>
<div class="videoarea clear">
    <h3><a href="join.html">Scene One Again</a></h3>
    <div class="video_pic"><a href="videos/100/scene-one"><img src="sd3.php?show=file&path=/videos/100/thumb_1.jpg" alt=""></a></div>
</div>
</body></html>`
	items := ParseListing([]byte(duped), "videos")
	if len(items) != 1 {
		t.Errorf("got %d items after dedup, want 1", len(items))
	}
}
