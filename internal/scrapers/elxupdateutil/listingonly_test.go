package elxupdateutil

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

func TestListingOnlyScenes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "movies_1_d.html") {
			_, _ = w.Write(readFixture(t, "listing_only_page1.html"))
			return
		}
		if strings.Contains(r.URL.Path, "join.php") {
			t.Errorf("listing-only site fetched %s", r.URL.Path)
		}
		_, _ = w.Write([]byte("<html></html>"))
	}))
	defer srv.Close()

	s := New(SiteConfig{SiteID: "tam", Domain: "trueamateurmodels.com", StudioName: "True Amateur Models", TourPrefix: "/previewtour", BareHost: true, ListingOnly: true})
	s.base = srv.URL

	ch, err := s.ListScenes(context.Background(), "https://www.trueamateurmodels.com/previewtour/", scraper.ListOpts{})
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	var scenes []models.Scene
	for res := range ch {
		switch res.Kind {
		case scraper.KindScene:
			scenes = append(scenes, res.Scene)
		case scraper.KindError:
			t.Errorf("error result: %v", res.Err)
		}
	}
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3", len(scenes))
	}
	sc := scenes[0]
	if sc.ID != "chili_video_02" || sc.Title != "Chili Solo Masturbation Video" {
		t.Errorf("ID/Title = %q / %q", sc.ID, sc.Title)
	}
	if len(sc.Performers) != 1 || sc.Performers[0] != "Chili Model" {
		t.Errorf("Performers = %v", sc.Performers)
	}
	if sc.Date.Format("2006-01-02") != "2026-01-24" {
		t.Errorf("Date = %v", sc.Date)
	}
	if sc.StudioURL != "https://www.trueamateurmodels.com/previewtour/" {
		t.Errorf("StudioURL = %q", sc.StudioURL)
	}
	if !strings.HasPrefix(sc.URL, srv.URL+"/previewtour/categories/movies_1_d.html") {
		t.Errorf("URL = %q, want the listing page", sc.URL)
	}
	if sc.Thumbnail != srv.URL+"/previewtour/content/chili_video_02/1-4x.jpg" {
		t.Errorf("Thumbnail = %q", sc.Thumbnail)
	}
}
