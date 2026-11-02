package cmd

import (
	"reflect"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
)

var preserveEnrichmentExcluded = map[string]string{
	"ID":              "identity, always set by the scraper",
	"SiteID":          "identity, always set by the scraper",
	"StudioURL":       "identity, always set by the scraper",
	"PriceHistory":    "merged by carryOverPriceHistory",
	"LowestPrice":     "derived by AddPrice in carryOverPriceHistory",
	"LowestPriceDate": "derived by AddPrice in carryOverPriceHistory",
	"FirstSeenAt":     "store-owned (firstSeenFor)",
	"ScrapedAt":       "always the fresh scrape's",
	"DeletedAt":       "soft-delete state is owned by the refresh path",
}

func TestPreserveEnrichmentCoversEverySceneField(t *testing.T) {
	ts := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	var existing models.Scene
	v := reflect.ValueOf(&existing).Elem()
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		switch f.Interface().(type) {
		case string:
			f.SetString("v")
		case int:
			f.SetInt(7)
		case float64:
			f.SetFloat(7.5)
		case time.Time:
			f.Set(reflect.ValueOf(ts))
		case *time.Time:
			f.Set(reflect.ValueOf(&ts))
		case []string:
			f.Set(reflect.ValueOf([]string{"a"}))
		case map[string]string:
			f.Set(reflect.ValueOf(map[string]string{"stashdb": "id"}))
		case []models.PriceSnapshot:
			f.Set(reflect.ValueOf([]models.PriceSnapshot{{Regular: 1}}))
		default:
			t.Fatalf("unhandled type %T for %s; extend the test", f.Interface(), v.Type().Field(i).Name)
		}
	}
	fresh := models.Scene{ID: existing.ID, SiteID: existing.SiteID, StudioURL: existing.StudioURL}
	got := reflect.ValueOf(preserveEnrichment(fresh, existing))
	for i := 0; i < v.NumField(); i++ {
		name := v.Type().Field(i).Name
		if _, skip := preserveEnrichmentExcluded[name]; skip {
			continue
		}
		if !reflect.DeepEqual(got.Field(i).Interface(), v.Field(i).Interface()) {
			t.Errorf("preserveEnrichment does not restore %s: handle it or add it to preserveEnrichmentExcluded", name)
		}
	}
}
