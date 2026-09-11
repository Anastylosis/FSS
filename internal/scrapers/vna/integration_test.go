//go:build integration

package vna

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
	"github.com/Anastylosis/FSS/internal/scrapers/vnautil"
)

func TestLiveSaraJay(t *testing.T) {
	s := vnautil.New(vnautil.SiteConfig{SiteID: "sarajay", Domain: "sarajay.com", Studio: "Sara Jay", VideoPrefix: "videos"})
	testutil.RunLiveScrape(t, s, "https://sarajay.com/videos/", 2)
}

func TestLiveFuckedFeet(t *testing.T) {
	s := vnautil.New(vnautil.SiteConfig{SiteID: "fuckedfeet", Domain: "fuckedfeet.com", Studio: "Fucked Feet", VideoPrefix: "videos"})
	testutil.RunLiveScrape(t, s, "https://fuckedfeet.com/videos/", 2)
}

func TestLiveVickyAtHome(t *testing.T) {
	s := vnautil.New(vnautil.SiteConfig{SiteID: "vickyathome", Domain: "vickyathome.com", Studio: "Vicky at Home", VideoPrefix: "milf-videos"})
	testutil.RunLiveScrape(t, s, "https://vickyathome.com/milf-videos/", 2)
}

func TestLiveAngelinaCastro(t *testing.T) {
	s := vnautil.New(vnautil.SiteConfig{SiteID: "angelinacastro", Domain: "angelinacastrolive.com", Studio: "Angelina Castro", VideoPrefix: "videos"})
	testutil.RunLiveScrape(t, s, "https://angelinacastrolive.com/videos/", 2)
}

func TestLiveJuliaAnn(t *testing.T) {
	s := vnautil.New(vnautil.SiteConfig{SiteID: "juliaann", Domain: "juliaannlive.com", Studio: "Julia Ann Live", VideoPrefix: "videos"})
	testutil.RunLiveScrape(t, s, "https://juliaannlive.com/videos/", 2)
}

func TestLiveSunnyLane(t *testing.T) {
	s := vnautil.New(vnautil.SiteConfig{SiteID: "sunnylane", Domain: "sunnylanelive.com", Studio: "Sunny Lane", VideoPrefix: "videos"})
	testutil.RunLiveScrape(t, s, "https://sunnylanelive.com/videos/", 2)
}

func TestLiveItsCleo(t *testing.T) {
	s := vnautil.New(vnautil.SiteConfig{SiteID: "itscleo", Domain: "itscleolive.com", Studio: "It's Cleo Live", VideoPrefix: "videos", NeedsWWW: true})
	testutil.RunLiveScrape(t, s, "https://www.itscleolive.com/videos/", 2)
}

func TestLiveWomenByJuliaAnn(t *testing.T) {
	s := vnautil.New(vnautil.SiteConfig{SiteID: "womenbyjuliaann", Domain: "womenbyjuliaann.com", Studio: "Women By Julia Ann", VideoPrefix: "videos"})
	testutil.RunLiveScrape(t, s, "https://www.womenbyjuliaann.com/sd3.php?show=recent_video_updates", 2)
}
