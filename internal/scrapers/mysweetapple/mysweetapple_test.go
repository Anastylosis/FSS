package mysweetapple

import (
	"testing"

	"github.com/Anastylosis/FSS/scraper"
)

func TestRegistered(t *testing.T) {
	got, err := scraper.ForURL("https://mysweetapple.com/categories/movies.html")
	if err != nil {
		t.Fatalf("ForURL: %v", err)
	}
	if got.ID() != "mysweetapple" {
		t.Errorf("ForURL = %s", got.ID())
	}
}
