package cmd

import (
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
)

func TestReimportDoesNotDuplicatePriceHistory(t *testing.T) {
	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := t1.AddDate(0, 0, 7)
	sc := importScene("1", "One", t1)
	sc.AddPrice(models.PriceSnapshot{Date: t1, Regular: 10})
	sc.AddPrice(models.PriceSnapshot{Date: t2, Regular: 12})
	path := writeImportStudioFile(t, t.TempDir(), "studio.json", models.StudioFile{
		StudioURL: importURL, Scenes: []models.Scene{sc},
	})

	db := newImportDB(t)
	for i := 0; i < 3; i++ {
		if _, _, err := importFile(db, path, false, false); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := db.Load(importURL)
	if len(got) != 1 || len(got[0].PriceHistory) != 2 {
		t.Fatalf("history after 3 imports = %+v, want 2 snapshots", got)
	}
	if !got[0].PriceHistory[0].Date.Before(got[0].PriceHistory[1].Date) {
		t.Errorf("history out of order: %+v", got[0].PriceHistory)
	}
}

func TestMergePriceHistoriesInterleavesByDate(t *testing.T) {
	d := func(n int) time.Time { return time.Date(2026, 1, n, 0, 0, 0, 0, time.UTC) }
	var stored, file models.Scene
	stored.AddPrice(models.PriceSnapshot{Date: d(1), Regular: 10})
	stored.AddPrice(models.PriceSnapshot{Date: d(5), Regular: 20})
	file.AddPrice(models.PriceSnapshot{Date: d(1).Add(500 * time.Millisecond), Regular: 10})
	file.AddPrice(models.PriceSnapshot{Date: d(3), Regular: 15})

	got := mergePriceHistories(file, stored)
	want := []float64{10, 15, 20}
	if len(got.PriceHistory) != len(want) {
		t.Fatalf("history = %+v", got.PriceHistory)
	}
	for i, w := range want {
		if got.PriceHistory[i].Regular != w {
			t.Errorf("history[%d] = %v, want %v", i, got.PriceHistory[i].Regular, w)
		}
	}
	if got.LowestPrice != 10 {
		t.Errorf("LowestPrice = %v", got.LowestPrice)
	}
}
