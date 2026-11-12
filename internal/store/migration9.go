package store

import (
	"database/sql"
	"fmt"
)

// applyMigration9 rewrites every stored studio_url to its canonical form.
//
// Studio URLs used to be stored exactly as typed, so `http://x.com`,
// `https://x.com` and `https://x.com/` were three separate studios that never
// merged. Canonicalising the key fixes that going forward; this migration fixes
// what is already stored.
//
// The work cannot be expressed in SQL alone — canonicalisation is a Go function
// — so the mapping is materialised into a temporary table that the statements
// join against.
//
// The hard part is collisions. Two studio URLs collapsing to one canonical form
// can produce two rows with the same (id, site_id, studio_url), which the
// primary key forbids. Rather than fail, the migration merges: for each clashing
// scene it keeps the most recently scraped row, breaking ties on rowid so the
// choice is deterministic. Child rows follow their surviving parent and are
// deduplicated on their own keys.
//
// Foreign keys are off for the rebuild, matching applyMigration2.
func (s *SQLite) applyMigration9() (err error) {
	mapping, err := s.studioURLMapping()
	if err != nil {
		return err
	}
	if len(mapping) == 0 {
		return s.applyMigration("", 9) // nothing stored; just record the version
	}

	if _, err := s.db.Exec("PRAGMA foreign_keys = OFF"); err != nil {
		return err
	}
	defer func() {
		if ferr := s.enableForeignKeys(); ferr != nil && err == nil {
			err = ferr
		}
	}()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`CREATE TEMP TABLE url_map (
		raw TEXT PRIMARY KEY, canonical TEXT NOT NULL)`); err != nil {
		return fmt.Errorf("creating url map: %w", err)
	}
	ins, err := tx.Prepare(`INSERT INTO url_map (raw, canonical) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	for raw, canonical := range mapping {
		if _, err := ins.Exec(raw, canonical); err != nil {
			_ = ins.Close()
			return fmt.Errorf("mapping %s: %w", raw, err)
		}
	}
	if err := ins.Close(); err != nil {
		return err
	}

	for _, stmt := range migration9Statements {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("migration 9 step: %w", err)
		}
	}

	if _, err := tx.Exec(`DELETE FROM schema_version`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO schema_version (version) VALUES (9)`); err != nil {
		return err
	}
	return tx.Commit()
}

// studioURLMapping returns raw -> canonical for every studio URL stored, with
// entries that are already canonical omitted.
func (s *SQLite) studioURLMapping() (map[string]string, error) {
	rows, err := s.db.Query(`
		SELECT studio_url FROM scenes
		UNION SELECT url FROM studios`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := map[string]string{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		out[raw] = canonicalKey(raw)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Nothing to do if every stored URL is already canonical.
	changed := false
	for raw, canonical := range out {
		if raw != canonical {
			changed = true
			break
		}
	}
	if !changed {
		return nil, nil
	}
	return out, nil
}

// migration9Statements rebuild each table with canonical studio URLs. Order
// matters: scenes first, so the child inserts can require a surviving parent.
var migration9Statements = []string{
	// --- scenes: keep the freshest row of each colliding group ---
	`CREATE TABLE scenes_c AS
	 SELECT s.id, s.site_id, m.canonical AS studio_url, s.title, s.url, s.date,
	        s.description, s.thumbnail, s.preview, s.performers, s.director,
	        s.studio, s.tags, s.categories, s.series, s.series_part, s.duration,
	        s.resolution, s.width, s.height, s.format, s.views, s.likes,
	        s.comments, s.lowest_price, s.lowest_price_date, s.scraped_at,
	        s.deleted_at, s.first_seen_at, s.content_hash
	 FROM scenes s
	 JOIN url_map m ON m.raw = s.studio_url
	 WHERE NOT EXISTS (
	   SELECT 1 FROM scenes s2
	   JOIN url_map m2 ON m2.raw = s2.studio_url
	   WHERE s2.id = s.id AND s2.site_id = s.site_id
	     AND m2.canonical = m.canonical
	     AND (s2.scraped_at > s.scraped_at
	          OR (s2.scraped_at = s.scraped_at AND s2.rowid > s.rowid)))`,

	`CREATE TABLE scenes_new (
	    id                TEXT NOT NULL,
	    site_id           TEXT NOT NULL,
	    studio_url        TEXT NOT NULL,
	    title             TEXT NOT NULL DEFAULT '',
	    url               TEXT NOT NULL DEFAULT '',
	    date              TEXT,
	    description       TEXT DEFAULT '',
	    thumbnail         TEXT DEFAULT '',
	    preview           TEXT DEFAULT '',
	    performers        TEXT DEFAULT '[]',
	    director          TEXT DEFAULT '',
	    studio            TEXT DEFAULT '',
	    tags              TEXT DEFAULT '[]',
	    categories        TEXT DEFAULT '[]',
	    series            TEXT DEFAULT '',
	    series_part       INTEGER DEFAULT 0,
	    duration          INTEGER DEFAULT 0,
	    resolution        TEXT DEFAULT '',
	    width             INTEGER DEFAULT 0,
	    height            INTEGER DEFAULT 0,
	    format            TEXT DEFAULT '',
	    views             INTEGER DEFAULT 0,
	    likes             INTEGER DEFAULT 0,
	    comments          INTEGER DEFAULT 0,
	    lowest_price      REAL DEFAULT 0,
	    lowest_price_date TEXT,
	    scraped_at        TEXT NOT NULL,
	    deleted_at        TEXT,
	    first_seen_at     TEXT NOT NULL DEFAULT '',
	    content_hash      TEXT NOT NULL DEFAULT '',
	    PRIMARY KEY (id, site_id, studio_url)
	)`,
	`INSERT OR IGNORE INTO scenes_new SELECT * FROM scenes_c`,
	`DROP TABLE scenes_c`,
	`DROP TABLE scenes`,
	`ALTER TABLE scenes_new RENAME TO scenes`,
	`CREATE INDEX IF NOT EXISTS idx_scenes_studio_url ON scenes(studio_url)`,

	// --- junction tables: remap, drop orphans, dedupe on the composite key ---
	`CREATE TABLE sp_new (
	    scene_id TEXT NOT NULL, site_id TEXT NOT NULL, studio_url TEXT NOT NULL,
	    performer_id INTEGER NOT NULL, position INTEGER NOT NULL DEFAULT 0,
	    PRIMARY KEY (scene_id, site_id, studio_url, performer_id))`,
	`INSERT OR IGNORE INTO sp_new
	   SELECT j.scene_id, j.site_id, m.canonical, j.performer_id, j.position
	   FROM scene_performers j JOIN url_map m ON m.raw = j.studio_url
	   WHERE EXISTS (SELECT 1 FROM scenes s
	                 WHERE s.id = j.scene_id AND s.site_id = j.site_id
	                   AND s.studio_url = m.canonical)`,
	`DROP TABLE scene_performers`,
	`ALTER TABLE sp_new RENAME TO scene_performers`,

	`CREATE TABLE st_new (
	    scene_id TEXT NOT NULL, site_id TEXT NOT NULL, studio_url TEXT NOT NULL,
	    tag_id INTEGER NOT NULL, position INTEGER NOT NULL DEFAULT 0,
	    PRIMARY KEY (scene_id, site_id, studio_url, tag_id))`,
	`INSERT OR IGNORE INTO st_new
	   SELECT j.scene_id, j.site_id, m.canonical, j.tag_id, j.position
	   FROM scene_tags j JOIN url_map m ON m.raw = j.studio_url
	   WHERE EXISTS (SELECT 1 FROM scenes s
	                 WHERE s.id = j.scene_id AND s.site_id = j.site_id
	                   AND s.studio_url = m.canonical)`,
	`DROP TABLE scene_tags`,
	`ALTER TABLE st_new RENAME TO scene_tags`,

	`CREATE TABLE sc_new (
	    scene_id TEXT NOT NULL, site_id TEXT NOT NULL, studio_url TEXT NOT NULL,
	    category_id INTEGER NOT NULL, position INTEGER NOT NULL DEFAULT 0,
	    PRIMARY KEY (scene_id, site_id, studio_url, category_id))`,
	`INSERT OR IGNORE INTO sc_new
	   SELECT j.scene_id, j.site_id, m.canonical, j.category_id, j.position
	   FROM scene_categories j JOIN url_map m ON m.raw = j.studio_url
	   WHERE EXISTS (SELECT 1 FROM scenes s
	                 WHERE s.id = j.scene_id AND s.site_id = j.site_id
	                   AND s.studio_url = m.canonical)`,
	`DROP TABLE scene_categories`,
	`ALTER TABLE sc_new RENAME TO scene_categories`,

	`CREATE TABLE sei_new (
	    scene_id TEXT NOT NULL, site_id TEXT NOT NULL, studio_url TEXT NOT NULL,
	    source TEXT NOT NULL, external_id TEXT NOT NULL,
	    PRIMARY KEY (scene_id, site_id, studio_url, source))`,
	`INSERT OR IGNORE INTO sei_new
	   SELECT j.scene_id, j.site_id, m.canonical, j.source, j.external_id
	   FROM scene_external_ids j JOIN url_map m ON m.raw = j.studio_url
	   WHERE EXISTS (SELECT 1 FROM scenes s
	                 WHERE s.id = j.scene_id AND s.site_id = j.site_id
	                   AND s.studio_url = m.canonical)`,
	`DROP TABLE scene_external_ids`,
	`ALTER TABLE sei_new RENAME TO scene_external_ids`,

	// --- price history: remap and collapse snapshots the merge duplicated ---
	`CREATE TABLE ph_new (
	    id INTEGER PRIMARY KEY AUTOINCREMENT,
	    scene_id TEXT NOT NULL, site_id TEXT NOT NULL, studio_url TEXT NOT NULL,
	    date TEXT NOT NULL, regular REAL NOT NULL DEFAULT 0,
	    discounted REAL DEFAULT 0, is_free INTEGER NOT NULL DEFAULT 0,
	    is_on_sale INTEGER NOT NULL DEFAULT 0, discount_percent INTEGER DEFAULT 0)`,
	`INSERT INTO ph_new (scene_id, site_id, studio_url, date, regular, discounted, is_free, is_on_sale, discount_percent)
	   SELECT DISTINCT ph.scene_id, ph.site_id, m.canonical, ph.date, ph.regular,
	          ph.discounted, ph.is_free, ph.is_on_sale, ph.discount_percent
	   FROM price_history ph JOIN url_map m ON m.raw = ph.studio_url
	   WHERE EXISTS (SELECT 1 FROM scenes s
	                 WHERE s.id = ph.scene_id AND s.site_id = ph.site_id
	                   AND s.studio_url = m.canonical)`,
	`DROP TABLE price_history`,
	`ALTER TABLE ph_new RENAME TO price_history`,

	// --- studios: merge, keeping the earliest added_at and latest scrape ---
	`CREATE TABLE studios_new (
	    url TEXT PRIMARY KEY, site_id TEXT NOT NULL, name TEXT DEFAULT '',
	    added_at TEXT NOT NULL, last_scraped_at TEXT)`,
	`INSERT INTO studios_new (url, site_id, name, added_at, last_scraped_at)
	   SELECT m.canonical, MIN(st.site_id), MAX(st.name),
	          MIN(st.added_at), MAX(st.last_scraped_at)
	   FROM studios st JOIN url_map m ON m.raw = st.url
	   GROUP BY m.canonical`,
	`DROP TABLE studios`,
	`ALTER TABLE studios_new RENAME TO studios`,

	// --- indexes the rebuilt tables lost ---
	`CREATE INDEX IF NOT EXISTS idx_scene_performers_performer ON scene_performers(performer_id)`,
	`CREATE INDEX IF NOT EXISTS idx_scene_tags_tag ON scene_tags(tag_id)`,
	`CREATE INDEX IF NOT EXISTS idx_scene_categories_category ON scene_categories(category_id)`,
	`CREATE INDEX IF NOT EXISTS idx_scene_external_ids_lookup ON scene_external_ids(source, external_id)`,
	`CREATE INDEX IF NOT EXISTS idx_scene_external_ids_studio ON scene_external_ids(studio_url)`,
	`CREATE INDEX IF NOT EXISTS idx_price_history_scene ON price_history(scene_id, site_id, studio_url)`,
	`CREATE INDEX IF NOT EXISTS idx_scene_performers_studio ON scene_performers(studio_url, scene_id, site_id, position, performer_id)`,
	`CREATE INDEX IF NOT EXISTS idx_scene_tags_studio ON scene_tags(studio_url, scene_id, site_id, position, tag_id)`,
	`CREATE INDEX IF NOT EXISTS idx_scene_categories_studio ON scene_categories(studio_url, scene_id, site_id, position, category_id)`,
	`DROP TABLE url_map`,
}

// unusedTx keeps the sql import honest if the statement list ever empties.
var _ = (*sql.Tx)(nil)
