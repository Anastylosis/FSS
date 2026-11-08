package store

import (
	"testing"

	"github.com/Anastylosis/FSS/models"
)

func TestCurrencyChangesContentHash(t *testing.T) {
	a := fillScene(t)
	b := a
	b.PriceHistory = []models.PriceSnapshot{a.PriceHistory[0]}
	b.PriceHistory[0].Currency = "USD"
	if sceneContentHash(a) == sceneContentHash(b) {
		t.Error("changing only a snapshot's currency leaves the content hash unchanged")
	}
}

func TestMigration11AddsCurrencyColumn(t *testing.T) {
	s := newTestDB(t)
	if _, err := s.db.Exec(`SELECT currency FROM price_history LIMIT 1`); err != nil {
		t.Fatalf("price_history.currency missing: %v", err)
	}
}
