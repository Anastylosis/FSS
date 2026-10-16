package empirestore

import (
	"testing"

	"github.com/Anastylosis/FSS/scraper"
)

func TestSitesTable(t *testing.T) {
	if len(sites) != 8 {
		t.Errorf("sites count = %d, want 8", len(sites))
	}
}

func TestMatchesURL(t *testing.T) {
	all := scraper.All()

	cases := []struct {
		url    string
		siteID string
	}{
		{"https://www.elegantangel.com/", "elegantangel"},
		{"https://elegantangel.com/watch-newest-elegant-angel-clips-and-scenes.html", "elegantangel"},
		{"https://www.elegantangel.com/93560/studio/club-59-elegant-angel-studios.html", "elegantangel"},
		{"https://vouyermedia.com/watch-newest-vouyer-media-clips-and-scenes.html", "vouyermedia"},
		{"https://www.vouyermedia.com/", "vouyermedia"},
		{"https://vouyermedia.com/157946/vouyer-media-test-streaming-scene-video.html", "vouyermedia"},
		{"https://www.reaganfoxx.com/", "reaganfoxx"},
		{"https://reaganfoxx.com/scenes/673608/reagan-foxx-streaming-pornstar-videos.html", "reaganfoxx"},
		{"https://jodiwest.com/", "jodiwest"},
		{"https://www.jodiwest.com/watch-newest-jodi-west-clips-and-scenes.html", "jodiwest"},
		{"https://jodiwest.com/1572568/jodi-west-an-angel-gets-into-the-vip-section-streaming-scene-video.html", "jodiwest"},
		{"https://bobsvideos.empirestores.co/shop-streaming-video-by-scene.html", "bobsvideos"},
		{"https://jsi.empirestores.co/", "justinslayer"},
		{"https://spungygunkfilms.empirestores.co/", "spungygunkfilms"},
		{"https://www.dvsx.com/", "dvsx"},
		{"https://www.dvsx.com/21558/studio/dvsx-studios.html", "dvsx"},
		{"https://www.dvsx.com/watch-streaming-video-by-scene.html?studio=21558", "dvsx"},
	}

	for _, c := range cases {
		found := false
		for _, s := range all {
			if s.ID() == c.siteID && s.MatchesURL(c.url) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no scraper with ID %q matched %q", c.siteID, c.url)
		}
	}
}
