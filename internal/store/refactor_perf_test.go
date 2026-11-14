package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
)

func TestSQLitePriceHistorySameSecondKeepsInsertionOrder(t *testing.T) {
	s, err := NewSQLite(filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	at := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	sc := models.Scene{ID: "1", SiteID: "x", Title: "t", URL: "u", ScrapedAt: at}
	sc.PriceHistory = []models.PriceSnapshot{
		{Date: at, Regular: 20},
		{Date: at, Regular: 20, Discounted: 10, IsOnSale: true, DiscountPercent: 50},
		{Date: at.Add(-time.Hour), Regular: 25},
	}
	const url = "https://example.com/s"
	if err := s.Save(url, []models.Scene{sc}); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		got, err := s.Load(url)
		if err != nil || len(got) != 1 {
			t.Fatalf("load: %v %d", err, len(got))
		}
		ph := got[0].PriceHistory
		if len(ph) != 3 || ph[0].Regular != 25 || ph[1].IsOnSale || !ph[2].IsOnSale {
			t.Fatalf("order = %+v", ph)
		}
	}
}

func TestFlatVariantScanMemoClearedBySave(t *testing.T) {
	f := NewFlat(t.TempDir(), []string{"json"})
	const url = "https://example.com/m"
	if got, err := f.Load(url); err != nil || len(got) != 0 {
		t.Fatalf("first load: %v %v", got, err)
	}
	if !f.noVariant[url] {
		t.Fatal("scan result was not remembered")
	}
	if err := f.Save(url, []models.Scene{{ID: "1", SiteID: "x", Title: "t", URL: "u", ScrapedAt: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	if f.noVariant[url] {
		t.Fatal("Save left a stale memo")
	}
	if got, err := f.Load(url); err != nil || len(got) != 1 {
		t.Fatalf("load after save: %v %v", got, err)
	}
}

func TestContentHashIgnoresBlankAndDuplicateNames(t *testing.T) {
	base := models.Scene{ID: "1", SiteID: "x", Tags: []string{"a", "b"}}
	dirty := base
	dirty.Tags = []string{"a", "", "a", "b", "b"}
	if sceneContentHash(base) != sceneContentHash(dirty) {
		t.Fatal("blank/duplicate names changed the hash")
	}
	other := base
	other.Tags = []string{"b", "a"}
	if sceneContentHash(base) == sceneContentHash(other) {
		t.Fatal("order must still be significant")
	}
}
