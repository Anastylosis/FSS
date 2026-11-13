package match

import (
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
)

func TestSubstringSameTitleDifferentScenesAmbiguous(t *testing.T) {
	d1 := time.Date(2022, 12, 1, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2023, 12, 1, 0, 0, 0, 0, time.UTC)
	idx := BuildIndex([]models.Scene{
		{ID: "1", SiteID: "a", Title: "Christmas Special", Date: d1, Duration: 600},
		{ID: "2", SiteID: "b", Title: "Christmas Special", Date: d2, Duration: 1800},
	})
	if r := idx.Match("Studio - Christmas Special 1080p.mp4", 0); r.Confidence != MatchAmbiguous {
		t.Fatalf("got %v", r.Confidence)
	}
}

func TestExternalIDsDecide(t *testing.T) {
	d1 := time.Date(2022, 12, 1, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2023, 12, 1, 0, 0, 0, 0, time.UTC)
	a := models.Scene{Date: d1, ExternalIDs: map[string]string{"stashdb": "x"}}
	b := models.Scene{Date: d2, ExternalIDs: map[string]string{"stashdb": "x"}}
	if !scenesCompatible(a, b) {
		t.Fatal("same external id should agree")
	}
	b.ExternalIDs["stashdb"] = "y"
	b.Date = d1
	if scenesCompatible(a, b) {
		t.Fatal("different external id should disagree")
	}
}

func TestMergePrefersLiveThumbnail(t *testing.T) {
	exp := "https://cdn.example.com/a.jpg?expires=1000000000&token=x"
	m := MergeScenes([]models.Scene{{SiteID: "a", Thumbnail: exp}, {SiteID: "b", Thumbnail: "https://cdn.example.com/b.jpg"}}, time.Time{})
	if m.Thumbnail != "https://cdn.example.com/b.jpg" {
		t.Fatalf("got %q", m.Thumbnail)
	}
}
