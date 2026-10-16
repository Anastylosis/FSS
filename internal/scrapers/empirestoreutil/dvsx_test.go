package empirestoreutil

import "testing"

func TestParseListingPageImgs4Thumbnail(t *testing.T) {
	body := []byte(`
<article class="scene-widget store-view"
	data-scene-id="109967"
	data-master-id="1449742"><div class="scene-preview-container"><a class="scene-img"
               href="/109967/dvsx-lesbian-enjoying-sex-in-indoor-streaming-scene-video.html"><img class="screenshot img-full-fluid"
                                        src="https://imgs4cdn.adultempire.com/frame/1449742?w=10&tc=3640"
                                        data-src="https://imgs4cdn.adultempire.com/frame/1449742?w=200&tc=3640"
                                        alt="x"/></a></div><div class="scene-info-container"><div class="scene-primary-details"><a class="scene-title"
                    href="/109967/dvsx-frisky-lesbian-kitties-2-streaming-scene-video.html"><h6>
                              Lesbian Enjoying Sex in Indoor
                    </h6></a></div><div class="scene-secondary-details"><p class="scene-performer-names">
							Leigh Livingston,
							Ryan Hunter
					</p><p class="scene-length">
				26 min
			</p></div></div></article>`)
	scenes := ParseListingPage(body, "https://www.dvsx.com")
	if len(scenes) != 1 {
		t.Fatalf("got %d scenes", len(scenes))
	}
	s := scenes[0]
	if s.Thumb != "https://imgs4cdn.adultempire.com/frame/1449742?w=200&tc=3640" {
		t.Errorf("Thumb = %q", s.Thumb)
	}
	if s.Title != "Lesbian Enjoying Sex in Indoor" || s.Duration != 1560 {
		t.Errorf("title=%q duration=%d", s.Title, s.Duration)
	}
	if len(s.Performers) != 2 || s.Performers[1] != "Ryan Hunter" {
		t.Errorf("performers = %v", s.Performers)
	}
	if s.URL != "https://www.dvsx.com/109967/dvsx-frisky-lesbian-kitties-2-streaming-scene-video.html" {
		t.Errorf("URL = %q", s.URL)
	}
}

func TestParseDetailPageStudioLink(t *testing.T) {
	body := []byte(`<div class="release-date"><span class="font-weight-bold mr-2">Released:</span>Jan 05, 2009
</div><!-- Studio --><div class="studio"><span class="font-weight-bold mr-2">Studio:</span><a href="/watch-streaming-video-by-scene.html?studio=21558"
		data-Category="Store Only Scene" data-Label="Studio">DVSX
	</a></div><div class="series"><span class="font-weight-bold mr-2">Series:</span><a href="/streaming-video-by-scene.html?series=37604">Frisky Lesbian Kitties</a></div>`)
	d := ParseDetailPage(body)
	if d.Studio != "DVSX" {
		t.Errorf("Studio = %q", d.Studio)
	}
	if d.Series != "Frisky Lesbian Kitties" {
		t.Errorf("Series = %q", d.Series)
	}
	if d.Date.Year() != 2009 || d.Date.Month() != 1 || d.Date.Day() != 5 {
		t.Errorf("Date = %v", d.Date)
	}
}

func TestHasNextPageEscapedAmpersand(t *testing.T) {
	body := []byte(`<a href="?sort=released&amp;page=2&amp;studio=21558">2</a><a href="?page=48&amp;studio=21558">48</a>`)
	if !HasNextPage(body, 1) {
		t.Error("page 2 link behind &amp; not seen")
	}
	if HasNextPage(body, 48) {
		t.Error("no page beyond 48 expected")
	}
}

func TestResolveListingURLStudioID(t *testing.T) {
	got := resolveListingURL("https://www.dvsx.com/21558/studio/dvsx-studios.html", "https://www.dvsx.com", "/x")
	if want := "https://www.dvsx.com/watch-streaming-video-by-scene.html?studio=21558&sort=released"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
