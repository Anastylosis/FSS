package output

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
)

var csvExcludedFields = map[string]string{}

func TestEverySceneFieldReachesCSV(t *testing.T) {
	ts := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	base := models.Scene{}
	baseRow, err := sceneToRow(base)
	if err != nil {
		t.Fatal(err)
	}
	typ := reflect.TypeOf(base)
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		if _, skip := csvExcludedFields[name]; skip {
			continue
		}
		alt := base
		f := reflect.ValueOf(&alt).Elem().Field(i)
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
			t.Fatalf("unhandled type %T for %s; extend the test", f.Interface(), name)
		}
		row, err := sceneToRow(alt)
		if err != nil {
			t.Fatal(err)
		}
		if slices.Equal(row, baseRow) {
			t.Errorf("Scene.%s does not appear in the CSV: add a column or list it in csvExcludedFields", name)
		}
	}
}
