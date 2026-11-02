package store

import (
	"reflect"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
)

func stripJunctionFKs(t *testing.T, s *SQLite) {
	t.Helper()
	stmts := []string{
		`DROP TABLE scene_performers`, `DROP TABLE scene_tags`,
		`DROP TABLE scene_categories`, `DROP TABLE scene_external_ids`,
		`CREATE TABLE scene_performers (scene_id TEXT NOT NULL, site_id TEXT NOT NULL, studio_url TEXT NOT NULL,
			performer_id INTEGER NOT NULL, position INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (scene_id, site_id, studio_url, performer_id))`,
		`CREATE TABLE scene_tags (scene_id TEXT NOT NULL, site_id TEXT NOT NULL, studio_url TEXT NOT NULL,
			tag_id INTEGER NOT NULL, position INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (scene_id, site_id, studio_url, tag_id))`,
		`CREATE TABLE scene_categories (scene_id TEXT NOT NULL, site_id TEXT NOT NULL, studio_url TEXT NOT NULL,
			category_id INTEGER NOT NULL, position INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (scene_id, site_id, studio_url, category_id))`,
		`CREATE TABLE scene_external_ids (scene_id TEXT NOT NULL, site_id TEXT NOT NULL, studio_url TEXT NOT NULL,
			source TEXT NOT NULL, external_id TEXT NOT NULL,
			PRIMARY KEY (scene_id, site_id, studio_url, source))`,
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

func setVersion(t *testing.T, s *SQLite, v int) {
	t.Helper()
	if _, err := s.db.Exec(`DELETE FROM schema_version`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO schema_version (version) VALUES (?)`, v); err != nil {
		t.Fatal(err)
	}
}

func count(t *testing.T, s *SQLite, q string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(q).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestMigration10RepairsMigration9(t *testing.T) {
	s := newTestDB(t)
	url := "https://www.example.com"
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	sc := models.Scene{
		ID: "a", SiteID: "x", StudioURL: url, Title: "A", URL: "u", ScrapedAt: now,
		Tags: []string{"good"}, Performers: []string{"Alice"}, Categories: []string{"c"},
		ExternalIDs: map[string]string{"stashdb": "1"},
	}
	if err := s.Save(url, []models.Scene{sc}); err != nil {
		t.Fatal(err)
	}

	stripJunctionFKs(t, s)
	// Reproduce migration 9's merge: a stale tag and an orphan row for a scene
	// that no longer exists, and a stale content hash on the survivor.
	if _, err := s.db.Exec(`INSERT INTO tags (name) VALUES ('stale')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO scene_tags (scene_id, site_id, studio_url, tag_id, position)
		SELECT 'a', 'x', ?, id, 0 FROM tags WHERE name = 'stale'`, url); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO scene_tags (scene_id, site_id, studio_url, tag_id, position)
		SELECT 'ghost', 'x', ?, id, 0 FROM tags WHERE name = 'stale'`, url); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO scene_external_ids VALUES ('ghost','x',?,'stashdb','9')`, url); err != nil {
		t.Fatal(err)
	}
	setVersion(t, s, 9)

	missing, err := s.JunctionTablesMissingCascade()
	if err != nil || len(missing) != 4 {
		t.Fatalf("precondition: missing = %v, err %v", missing, err)
	}

	if err := s.migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if missing, err := s.JunctionTablesMissingCascade(); err != nil || len(missing) != 0 {
		t.Fatalf("after migration 10: missing = %v, err %v", missing, err)
	}
	for _, table := range []string{"scene_performers", "scene_tags", "scene_categories", "scene_external_ids"} {
		q := `SELECT COUNT(*) FROM ` + table + ` j WHERE NOT EXISTS (SELECT 1 FROM scenes s
			WHERE s.id = j.scene_id AND s.site_id = j.site_id AND s.studio_url = j.studio_url)`
		if n := count(t, s, q); n != 0 {
			t.Errorf("%s has %d orphans", table, n)
		}
	}
	if n := count(t, s, `SELECT COUNT(*) FROM scenes WHERE content_hash <> ''`); n != 0 {
		t.Errorf("%d scenes kept a content_hash", n)
	}
	if v := count(t, s, `SELECT MAX(version) FROM schema_version`); v != latestSchemaVersion {
		t.Errorf("version = %d", v)
	}

	// Re-saving the true content heals the stale union.
	if err := s.Save(url, []models.Scene{sc}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load(url)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !reflect.DeepEqual(got[0].Tags, []string{"good"}) {
		t.Errorf("tags after re-save = %v, want [good]", got[0].Tags)
	}

	// Hard delete cascades again.
	if err := s.Save(url, nil); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"scene_performers", "scene_tags", "scene_categories", "scene_external_ids"} {
		if n := count(t, s, `SELECT COUNT(*) FROM `+table); n != 0 {
			t.Errorf("%s kept %d rows after hard delete", table, n)
		}
	}
}

func TestMigration10Recanonicalises(t *testing.T) {
	s := newTestDB(t)
	// What the non-idempotent migration 9 stored for http://x.com/a// .
	old := "https://x.com/a/"
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := s.db.Exec(`INSERT INTO scenes (id, site_id, studio_url, title, url, scraped_at, first_seen_at)
		VALUES ('1','x',?,'T','u',?,?)`, old, timeStr(now), timeStr(now)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO studios (url, site_id, name, added_at) VALUES (?,?,?,?)`,
		old, "x", "X", timeStr(now)); err != nil {
		t.Fatal(err)
	}
	setVersion(t, s, 9)
	if err := s.migrate(); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load("https://x.com/a")
	if err != nil || len(got) != 1 {
		t.Fatalf("Load = %v, %v", got, err)
	}
	if missing, _ := s.JunctionTablesMissingCascade(); len(missing) != 0 {
		t.Errorf("missing cascade after combined pass: %v", missing)
	}
}

func TestSQLiteRefusesNewerSchema(t *testing.T) {
	s := newTestDB(t)
	setVersion(t, s, latestSchemaVersion+1)
	if err := s.migrate(); err == nil {
		t.Fatal("migrate accepted a newer schema version")
	}
}

// Every Scene field must either reach the content hash and survive a SQLite
// round trip, or be listed here with the reason it does not.
var sceneHashExcluded = map[string]string{
	"ScrapedAt":   "changes every scrape; Save updates it separately",
	"FirstSeenAt": "store-owned, never changes once set",
}

var sceneRoundTripExcluded = map[string]string{
	"ScrapedAt":   "second-precision, compared separately",
	"FirstSeenAt": "stamped by the store",
}

func fillScene(t *testing.T) models.Scene {
	t.Helper()
	ts := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	var sc models.Scene
	v := reflect.ValueOf(&sc).Elem()
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		name := v.Type().Field(i).Name
		switch f.Interface().(type) {
		case string:
			f.SetString("v-" + name)
		case int:
			f.SetInt(int64(i + 1))
		case float64:
			f.SetFloat(float64(i) + 0.5)
		case time.Time:
			f.Set(reflect.ValueOf(ts))
		case *time.Time:
			f.Set(reflect.ValueOf(&ts))
		case []string:
			f.Set(reflect.ValueOf([]string{"a-" + name, "b-" + name}))
		case map[string]string:
			f.Set(reflect.ValueOf(map[string]string{"stashdb": "id-" + name}))
		case []models.PriceSnapshot:
			f.Set(reflect.ValueOf([]models.PriceSnapshot{{Date: ts, Regular: 9.5, Discounted: 4.5, IsFree: true, IsOnSale: true, DiscountPercent: 10}}))
		default:
			t.Fatalf("fillScene: unhandled type %T for field %s; extend the helper", f.Interface(), name)
		}
	}
	sc.StudioURL = "https://www.example.com"
	return sc
}

func TestEverySceneFieldFeedsContentHash(t *testing.T) {
	base := fillScene(t)
	baseHash := sceneContentHash(base)
	typ := reflect.TypeOf(base)
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		if _, skip := sceneHashExcluded[name]; skip {
			continue
		}
		alt := base
		f := reflect.ValueOf(&alt).Elem().Field(i)
		switch name {
		case "DeletedAt":
			ts := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
			f.Set(reflect.ValueOf(&ts))
		case "LowestPriceDate":
			ts := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
			f.Set(reflect.ValueOf(&ts))
		case "PriceHistory":
			f.Set(reflect.ValueOf([]models.PriceSnapshot{{Regular: 1}}))
		default:
			switch f.Kind() {
			case reflect.String:
				f.SetString("changed")
			case reflect.Int:
				f.SetInt(f.Int() + 1000)
			case reflect.Float64:
				f.SetFloat(f.Float() + 1000)
			case reflect.Struct:
				f.Set(reflect.ValueOf(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)))
			case reflect.Slice:
				f.Set(reflect.ValueOf([]string{"changed"}))
			case reflect.Map:
				f.Set(reflect.ValueOf(map[string]string{"stashdb": "changed"}))
			default:
				t.Fatalf("unhandled kind %s for %s", f.Kind(), name)
			}
		}
		if sceneContentHash(alt) == baseHash {
			t.Errorf("changing %s does not change sceneContentHash: add it to the hash or to sceneHashExcluded", name)
		}
	}
}

func TestEverySceneFieldSurvivesSQLiteRoundTrip(t *testing.T) {
	s := newTestDB(t)
	sc := fillScene(t)
	sc.ScrapedAt = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	sc.LowestPrice = 4.5
	if err := s.Save(sc.StudioURL, []models.Scene{sc}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load(sc.StudioURL)
	if err != nil || len(got) != 1 {
		t.Fatalf("Load = %v, %v", got, err)
	}
	gv, wv := reflect.ValueOf(got[0]), reflect.ValueOf(sc)
	for i := 0; i < wv.NumField(); i++ {
		name := wv.Type().Field(i).Name
		if _, skip := sceneRoundTripExcluded[name]; skip {
			continue
		}
		if !reflect.DeepEqual(gv.Field(i).Interface(), wv.Field(i).Interface()) {
			t.Errorf("%s lost in SQLite round trip: got %v want %v", name, gv.Field(i).Interface(), wv.Field(i).Interface())
		}
	}
}

func TestMigration10CollisionKeepsWinnerChildrenOnly(t *testing.T) {
	s := newTestDB(t)
	older := timeStr(time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))
	newer := timeStr(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	oldURL, newURL := "https://x.com/a", "https://x.com/a/"
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO tags (name) VALUES ('stale'), ('good')`)
	exec(`INSERT INTO performers (name) VALUES ('Mangled'), ('Alice')`)
	for _, v := range []struct{ url, when, tag, perf, ext string }{
		{oldURL, older, "stale", "Mangled", "OLD"},
		{newURL, newer, "good", "Alice", "NEW"},
	} {
		exec(`INSERT INTO scenes (id, site_id, studio_url, title, url, scraped_at, first_seen_at)
			VALUES ('1','x',?,'T','u',?,?)`, v.url, v.when, v.when)
		exec(`INSERT INTO scene_tags (scene_id, site_id, studio_url, tag_id, position)
			SELECT '1','x',?,id,0 FROM tags WHERE name = ?`, v.url, v.tag)
		exec(`INSERT INTO scene_performers (scene_id, site_id, studio_url, performer_id, position)
			SELECT '1','x',?,id,0 FROM performers WHERE name = ?`, v.url, v.perf)
		exec(`INSERT INTO scene_external_ids VALUES ('1','x',?,'stashdb',?)`, v.url, v.ext)
	}
	setVersion(t, s, 9)
	if err := s.migrate(); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load("https://x.com/a")
	if err != nil || len(got) != 1 {
		t.Fatalf("Load = %v, %v", got, err)
	}
	if !reflect.DeepEqual(got[0].Tags, []string{"good"}) || !reflect.DeepEqual(got[0].Performers, []string{"Alice"}) {
		t.Errorf("survivor tags=%v performers=%v, want only the newer row's", got[0].Tags, got[0].Performers)
	}
	if got[0].ExternalIDs["stashdb"] != "NEW" {
		t.Errorf("stashdb = %q, want NEW", got[0].ExternalIDs["stashdb"])
	}
}

func TestEveryPriceSnapshotFieldFeedsContentHash(t *testing.T) {
	base := fillScene(t)
	want := sceneContentHash(base)
	typ := reflect.TypeOf(models.PriceSnapshot{})
	for i := 0; i < typ.NumField(); i++ {
		alt := base
		alt.PriceHistory = []models.PriceSnapshot{base.PriceHistory[0]}
		f := reflect.ValueOf(&alt.PriceHistory[0]).Elem().Field(i)
		switch f.Kind() {
		case reflect.Bool:
			f.SetBool(!f.Bool())
		case reflect.Int:
			f.SetInt(f.Int() + 1)
		case reflect.Float64:
			f.SetFloat(f.Float() + 1)
		default:
			f.Set(reflect.ValueOf(time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC)))
		}
		if sceneContentHash(alt) == want {
			t.Errorf("PriceSnapshot.%s does not feed sceneContentHash", typ.Field(i).Name)
		}
	}
}
