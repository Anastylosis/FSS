package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/models"

	// Registers the cgo-free "sqlite" driver with database/sql.
	_ "modernc.org/sqlite"
)

// SQLite is the optional store backed by a SQLite database.
// Enabled with the --db flag. JSON/CSV are exported from it on request.
type SQLite struct {
	db      *sql.DB
	lockDir string
}

func NewSQLite(path string) (*SQLite, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening sqlite %s: %w", path, err)
	}
	db.SetMaxOpenConns(1) // SQLite does not support concurrent writes
	// Issue each PRAGMA in its own Exec. Multi-statement PRAGMA in a single
	// Exec is unreliable across SQLite drivers, and silently dropping
	// foreign_keys=ON would leave every ON DELETE CASCADE clause dead.
	pragmas := []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("%s: %w", p, err)
		}
	}
	s := &SQLite{db: db, lockDir: filepath.Dir(path)}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrating schema: %w", err)
	}
	return s, nil
}

func (s *SQLite) Close() error { return s.db.Close() }

func (s *SQLite) Lock(studioURL string) (io.Closer, error) {
	studioURL = canonicalKey(studioURL)
	if err := os.MkdirAll(s.lockDir, 0o700); err != nil {
		return nil, fmt.Errorf("creating lock dir: %w", err)
	}
	return lockFile(filepath.Join(s.lockDir, Slugify(studioURL)+".lock"))
}

// ---- schema ----

// baseSchema creates the core tables present since v0. The performers/tags/categories
// TEXT columns are kept for backwards compatibility but are no longer read or written.
const baseSchema = `
CREATE TABLE IF NOT EXISTS schema_version (
    version INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS scenes (
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
    PRIMARY KEY (id, site_id)
);

CREATE TABLE IF NOT EXISTS price_history (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    scene_id         TEXT NOT NULL,
    site_id          TEXT NOT NULL,
    date             TEXT NOT NULL,
    regular          REAL NOT NULL DEFAULT 0,
    discounted       REAL DEFAULT 0,
    is_free          INTEGER NOT NULL DEFAULT 0,
    is_on_sale       INTEGER NOT NULL DEFAULT 0,
    discount_percent INTEGER DEFAULT 0
);

CREATE TABLE IF NOT EXISTS studios (
    url              TEXT PRIMARY KEY,
    site_id          TEXT NOT NULL,
    name             TEXT DEFAULT '',
    added_at         TEXT NOT NULL,
    last_scraped_at  TEXT
);

CREATE INDEX IF NOT EXISTS idx_scenes_studio_url ON scenes(studio_url);
CREATE INDEX IF NOT EXISTS idx_price_history_scene ON price_history(scene_id, site_id);
`

// migration1 adds normalised junction tables for performers, tags, and categories.
const migration1 = `
CREATE TABLE IF NOT EXISTS performers (
    id   INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE
);

CREATE TABLE IF NOT EXISTS tags (
    id   INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE
);

CREATE TABLE IF NOT EXISTS categories (
    id   INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE
);

CREATE TABLE IF NOT EXISTS scene_performers (
    scene_id     TEXT NOT NULL,
    site_id      TEXT NOT NULL,
    performer_id INTEGER NOT NULL,
    position     INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (scene_id, site_id, performer_id),
    FOREIGN KEY (scene_id, site_id) REFERENCES scenes(id, site_id) ON DELETE CASCADE,
    FOREIGN KEY (performer_id) REFERENCES performers(id)
);

CREATE TABLE IF NOT EXISTS scene_tags (
    scene_id TEXT NOT NULL,
    site_id  TEXT NOT NULL,
    tag_id   INTEGER NOT NULL,
    PRIMARY KEY (scene_id, site_id, tag_id),
    FOREIGN KEY (scene_id, site_id) REFERENCES scenes(id, site_id) ON DELETE CASCADE,
    FOREIGN KEY (tag_id) REFERENCES tags(id)
);

CREATE TABLE IF NOT EXISTS scene_categories (
    scene_id    TEXT NOT NULL,
    site_id     TEXT NOT NULL,
    category_id INTEGER NOT NULL,
    PRIMARY KEY (scene_id, site_id, category_id),
    FOREIGN KEY (scene_id, site_id) REFERENCES scenes(id, site_id) ON DELETE CASCADE,
    FOREIGN KEY (category_id) REFERENCES categories(id)
);

CREATE INDEX IF NOT EXISTS idx_scene_performers_performer ON scene_performers(performer_id);
CREATE INDEX IF NOT EXISTS idx_scene_tags_tag ON scene_tags(tag_id);
CREATE INDEX IF NOT EXISTS idx_scene_categories_category ON scene_categories(category_id);
`

// migration2 qualifies every scene key with studio_url so two studio URLs on
// the same site can no longer share — and steal — a scene row. It rebuilds the
// scenes primary key to (id, site_id, studio_url) and adds studio_url to the
// junction tables and price_history, deriving each child row's studio_url from
// its parent scene. Run with foreign keys OFF (see applyMigration2). Because
// the pre-migration scenes PK was (id, site_id), every child joins to exactly
// one scene, so no rows are duplicated.
const migration2 = `
CREATE TABLE scenes_new (
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
    PRIMARY KEY (id, site_id, studio_url)
);
INSERT INTO scenes_new SELECT * FROM scenes;
DROP TABLE scenes;
ALTER TABLE scenes_new RENAME TO scenes;
CREATE INDEX IF NOT EXISTS idx_scenes_studio_url ON scenes(studio_url);

CREATE TABLE scene_performers_new (
    scene_id     TEXT NOT NULL,
    site_id      TEXT NOT NULL,
    studio_url   TEXT NOT NULL,
    performer_id INTEGER NOT NULL,
    position     INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (scene_id, site_id, studio_url, performer_id),
    FOREIGN KEY (scene_id, site_id, studio_url) REFERENCES scenes(id, site_id, studio_url) ON DELETE CASCADE,
    FOREIGN KEY (performer_id) REFERENCES performers(id)
);
INSERT INTO scene_performers_new (scene_id, site_id, studio_url, performer_id, position)
    SELECT j.scene_id, j.site_id, s.studio_url, j.performer_id, j.position
    FROM scene_performers j JOIN scenes s ON j.scene_id = s.id AND j.site_id = s.site_id;
DROP TABLE scene_performers;
ALTER TABLE scene_performers_new RENAME TO scene_performers;
CREATE INDEX IF NOT EXISTS idx_scene_performers_performer ON scene_performers(performer_id);

CREATE TABLE scene_tags_new (
    scene_id   TEXT NOT NULL,
    site_id    TEXT NOT NULL,
    studio_url TEXT NOT NULL,
    tag_id     INTEGER NOT NULL,
    PRIMARY KEY (scene_id, site_id, studio_url, tag_id),
    FOREIGN KEY (scene_id, site_id, studio_url) REFERENCES scenes(id, site_id, studio_url) ON DELETE CASCADE,
    FOREIGN KEY (tag_id) REFERENCES tags(id)
);
INSERT INTO scene_tags_new (scene_id, site_id, studio_url, tag_id)
    SELECT j.scene_id, j.site_id, s.studio_url, j.tag_id
    FROM scene_tags j JOIN scenes s ON j.scene_id = s.id AND j.site_id = s.site_id;
DROP TABLE scene_tags;
ALTER TABLE scene_tags_new RENAME TO scene_tags;
CREATE INDEX IF NOT EXISTS idx_scene_tags_tag ON scene_tags(tag_id);

CREATE TABLE scene_categories_new (
    scene_id    TEXT NOT NULL,
    site_id     TEXT NOT NULL,
    studio_url  TEXT NOT NULL,
    category_id INTEGER NOT NULL,
    PRIMARY KEY (scene_id, site_id, studio_url, category_id),
    FOREIGN KEY (scene_id, site_id, studio_url) REFERENCES scenes(id, site_id, studio_url) ON DELETE CASCADE,
    FOREIGN KEY (category_id) REFERENCES categories(id)
);
INSERT INTO scene_categories_new (scene_id, site_id, studio_url, category_id)
    SELECT j.scene_id, j.site_id, s.studio_url, j.category_id
    FROM scene_categories j JOIN scenes s ON j.scene_id = s.id AND j.site_id = s.site_id;
DROP TABLE scene_categories;
ALTER TABLE scene_categories_new RENAME TO scene_categories;
CREATE INDEX IF NOT EXISTS idx_scene_categories_category ON scene_categories(category_id);

CREATE TABLE price_history_new (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    scene_id         TEXT NOT NULL,
    site_id          TEXT NOT NULL,
    studio_url       TEXT NOT NULL,
    date             TEXT NOT NULL,
    regular          REAL NOT NULL DEFAULT 0,
    discounted       REAL DEFAULT 0,
    is_free          INTEGER NOT NULL DEFAULT 0,
    is_on_sale       INTEGER NOT NULL DEFAULT 0,
    discount_percent INTEGER DEFAULT 0
);
INSERT INTO price_history_new (id, scene_id, site_id, studio_url, date, regular, discounted, is_free, is_on_sale, discount_percent)
    SELECT ph.id, ph.scene_id, ph.site_id, s.studio_url, ph.date, ph.regular, ph.discounted, ph.is_free, ph.is_on_sale, ph.discount_percent
    FROM price_history ph JOIN scenes s ON ph.scene_id = s.id AND ph.site_id = s.site_id;
DROP TABLE price_history;
ALTER TABLE price_history_new RENAME TO price_history;
CREATE INDEX IF NOT EXISTS idx_price_history_scene ON price_history(scene_id, site_id, studio_url);
`

func (s *SQLite) migrate() error {
	if _, err := s.db.Exec(baseSchema); err != nil {
		return err
	}

	var version int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&version); err != nil {
		return fmt.Errorf("reading schema version: %w", err)
	}

	if version < 1 {
		if err := s.applyMigration1(); err != nil {
			return fmt.Errorf("migration 1: %w", err)
		}
	}
	if version < 2 {
		if err := s.applyMigration2(); err != nil {
			return fmt.Errorf("migration 2: %w", err)
		}
	}
	if version < 3 {
		if err := s.applyMigration3(); err != nil {
			return fmt.Errorf("migration 3: %w", err)
		}
	}
	if version < 4 {
		if err := s.applyMigration4(); err != nil {
			return fmt.Errorf("migration 4: %w", err)
		}
	}
	if version < 5 {
		if err := s.applyMigration5(); err != nil {
			return fmt.Errorf("migration 5: %w", err)
		}
	}
	if version < 6 {
		if err := s.applyMigration6(); err != nil {
			return fmt.Errorf("migration 6: %w", err)
		}
	}
	if version < 7 {
		if err := s.applyMigration7(); err != nil {
			return fmt.Errorf("migration 7: %w", err)
		}
	}
	if version < 8 {
		if err := s.applyMigration8(); err != nil {
			return fmt.Errorf("migration 8: %w", err)
		}
	}
	if version < 9 {
		if err := s.applyMigration9(); err != nil {
			return fmt.Errorf("migration 9: %w", err)
		}
	}
	return nil
}

// migration5 adds cross-site scene identity. The (source, external_id) index is
// the point: it answers "which stored scenes are this StashDB UUID" across every
// site and studio.
const migration5 = `
CREATE TABLE IF NOT EXISTS scene_external_ids (
    scene_id    TEXT NOT NULL,
    site_id     TEXT NOT NULL,
    studio_url  TEXT NOT NULL,
    source      TEXT NOT NULL,
    external_id TEXT NOT NULL,
    PRIMARY KEY (scene_id, site_id, studio_url, source),
    FOREIGN KEY (scene_id, site_id, studio_url) REFERENCES scenes(id, site_id, studio_url) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_scene_external_ids_lookup ON scene_external_ids(source, external_id);
`

func (s *SQLite) applyMigration5() error {
	return s.applyMigration(migration5, 5)
}

// migration6 adds content_hash, which lets Save skip scenes that would be
// rewritten identically. Existing rows get ”, which never matches a computed
// hash, so they are rewritten once and stamped on the next save.
const migration6 = `
ALTER TABLE scenes ADD COLUMN content_hash TEXT NOT NULL DEFAULT '';
`

func (s *SQLite) applyMigration6() error {
	return s.applyMigration(migration6, 6)
}

// migration7 indexes the child tables by studio_url, which Load filters on.
//
// Their primary keys start with scene_id, so a studio_url predicate could not
// use them and every Load scanned the entire table — the whole database's
// junction rows to read one studio's. Invisible with a single studio, O(all
// studios) with many.
//
// Deliberately narrow. Covering variants carrying scene_id/site_id/position too
// were measured at 47ms vs 50ms for these — 3ms, for 31 MB more on a 300 MB
// database. Not worth it.
const migration7 = `
CREATE INDEX IF NOT EXISTS idx_scene_performers_studio ON scene_performers(studio_url);
CREATE INDEX IF NOT EXISTS idx_scene_tags_studio ON scene_tags(studio_url);
CREATE INDEX IF NOT EXISTS idx_scene_categories_studio ON scene_categories(studio_url);
CREATE INDEX IF NOT EXISTS idx_scene_external_ids_studio ON scene_external_ids(studio_url);
`

func (s *SQLite) applyMigration7() error {
	return s.applyMigration(migration7, 7)
}

// migration8 widens the junction studio indexes to cover the columns Load
// aggregates on.
//
// Load groups relation rows per scene in SQL rather than shipping one driver
// row per name — a 59k-scene studio had 1.4M scene_tags rows, and crossing
// database/sql that many times cost more than the query itself. Grouping needs
// the rows to arrive already ordered by (scene_id, site_id) or SQLite builds a
// temp B-tree, which costs more than it saves. These indexes make the whole
// query index-only and let the GROUP BY stream.
//
// This supersedes migration 7's narrow indexes, which are dropped. Narrow was
// the right call when the query returned flat rows; it is not once it groups.
const migration8 = `
DROP INDEX IF EXISTS idx_scene_performers_studio;
DROP INDEX IF EXISTS idx_scene_tags_studio;
DROP INDEX IF EXISTS idx_scene_categories_studio;

CREATE INDEX IF NOT EXISTS idx_scene_performers_studio
    ON scene_performers(studio_url, scene_id, site_id, position, performer_id);
CREATE INDEX IF NOT EXISTS idx_scene_tags_studio
    ON scene_tags(studio_url, scene_id, site_id, position, tag_id);
CREATE INDEX IF NOT EXISTS idx_scene_categories_studio
    ON scene_categories(studio_url, scene_id, site_id, position, category_id);
`

func (s *SQLite) applyMigration8() error {
	return s.applyMigration(migration8, 8)
}

// migration4 adds first_seen_at, backfilled from scraped_at. That is an upper
// bound, not the true first sighting — it was never recorded — but it beats
// stamping existing scenes with the date of some future scrape.
const migration4 = `
ALTER TABLE scenes ADD COLUMN first_seen_at TEXT NOT NULL DEFAULT '';
UPDATE scenes SET first_seen_at = scraped_at WHERE first_seen_at = '';
`

func (s *SQLite) applyMigration4() error {
	return s.applyMigration(migration4, 4)
}

// applyMigration runs a schema-only migration and records its version.
func (s *SQLite) applyMigration(ddl string, version int) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if ddl != "" {
		if _, err := tx.Exec(ddl); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM schema_version`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO schema_version (version) VALUES (?)`, version); err != nil {
		return err
	}
	return tx.Commit()
}

// migration3 gives the tag and category junction tables the `position` column
// that scene_performers already had.
//
// Without it, Load's tag/category order was whatever order the join happened to
// return — the query had no ORDER BY at all. That is unspecified in SQLite and
// can change when the query planner does: an ANALYZE, a new index, or a library
// upgrade is enough to reorder them. Scrapers emit tags in a meaningful order
// (the site's own), and it reaches Stash and NFO output, so a silent reshuffle
// is a metadata regression nobody would connect to a SQLite version bump.
//
// Existing rows are backfilled to position 0. Their original order is not
// recoverable — it was never recorded — so they keep the join order they have
// now until the next scrape of that studio rewrites them with real positions.
const migration3 = `
ALTER TABLE scene_tags ADD COLUMN position INTEGER NOT NULL DEFAULT 0;
ALTER TABLE scene_categories ADD COLUMN position INTEGER NOT NULL DEFAULT 0;
`

func (s *SQLite) applyMigration3() error {
	return s.applyMigration(migration3, 3)
}

// applyMigration2 rebuilds the scene tables with studio_url-qualified keys.
// Foreign keys must be OFF during the rebuild (their toggle cannot live inside
// a transaction), so we disable them on the single shared connection, run the
// rebuild in one transaction, then restore them.
func (s *SQLite) applyMigration2() error {
	if _, err := s.db.Exec("PRAGMA foreign_keys = OFF"); err != nil {
		return err
	}
	defer func() { _, _ = s.db.Exec("PRAGMA foreign_keys = ON") }()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(migration2); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM schema_version`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO schema_version (version) VALUES (2)`); err != nil {
		return err
	}
	return tx.Commit()
}

// applyMigration1 creates the junction tables and migrates existing JSON data.
func (s *SQLite) applyMigration1() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(migration1); err != nil {
		return err
	}

	// Migrate existing JSON data from the scenes table into junction tables.
	rows, err := tx.Query(`SELECT id, site_id, performers, tags, categories FROM scenes`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	type sceneStrings struct {
		id, siteID                   string
		performers, tags, categories string
	}
	var all []sceneStrings
	for rows.Next() {
		var r sceneStrings
		if err := rows.Scan(&r.id, &r.siteID, &r.performers, &r.tags, &r.categories); err != nil {
			return err
		}
		all = append(all, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, r := range all {
		performers, err := unmarshalStrings(r.performers)
		if err != nil {
			return fmt.Errorf("parsing performers for scene %s: %w", r.id, err)
		}
		if err := syncRelationLegacy(tx, "performers", "scene_performers", "performer_id", r.id, r.siteID, performers, true); err != nil {
			return err
		}

		tags, err := unmarshalStrings(r.tags)
		if err != nil {
			return fmt.Errorf("parsing tags for scene %s: %w", r.id, err)
		}
		if err := syncRelationLegacy(tx, "tags", "scene_tags", "tag_id", r.id, r.siteID, tags, false); err != nil {
			return err
		}

		cats, err := unmarshalStrings(r.categories)
		if err != nil {
			return fmt.Errorf("parsing categories for scene %s: %w", r.id, err)
		}
		if err := syncRelationLegacy(tx, "categories", "scene_categories", "category_id", r.id, r.siteID, cats, false); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(`DELETE FROM schema_version`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO schema_version (version) VALUES (1)`); err != nil {
		return err
	}
	return tx.Commit()
}

// ---- Store interface ----

func (s *SQLite) Load(studioURL string) ([]models.Scene, error) {
	studioURL = canonicalKey(studioURL)
	// Every column below carrying a DEFAULT is still nullable — a DEFAULT only
	// applies when the column is omitted on insert, not when NULL is written
	// explicitly. A single NULL would otherwise fail the row scan and take the
	// whole studio's Load down with it, so they are coalesced here rather than
	// scanned into a sql.Null* per field. `date` is the one column with no
	// default at all, and is the likeliest to be NULL in a hand-edited or
	// externally-written database.
	rows, err := s.db.Query(`
		SELECT id, site_id, studio_url,
		       COALESCE(title, ''), COALESCE(url, ''),
		       COALESCE(date, ''), COALESCE(description, ''),
		       COALESCE(thumbnail, ''), COALESCE(preview, ''),
		       COALESCE(director, ''), COALESCE(studio, ''),
		       COALESCE(series, ''), COALESCE(series_part, 0),
		       COALESCE(duration, 0), COALESCE(resolution, ''),
		       COALESCE(width, 0), COALESCE(height, 0), COALESCE(format, ''),
		       COALESCE(views, 0), COALESCE(likes, 0), COALESCE(comments, 0),
		       COALESCE(lowest_price, 0), lowest_price_date,
		       COALESCE(first_seen_at, ''), scraped_at, deleted_at
		FROM scenes WHERE studio_url = ?`, studioURL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var scenes []models.Scene
	for rows.Next() {
		sc, err := scanScene(rows)
		if err != nil {
			return nil, err
		}
		scenes = append(scenes, sc)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Ordering happens here rather than in SQL. `ORDER BY scraped_at DESC, id`
	// made SQLite build a temp B-tree over every row in the studio; sorting the
	// already-materialised slice is far cheaper and gives the identical result.
	sort.SliceStable(scenes, func(i, j int) bool {
		if !scenes[i].ScrapedAt.Equal(scenes[j].ScrapedAt) {
			return scenes[i].ScrapedAt.After(scenes[j].ScrapedAt)
		}
		return scenes[i].ID < scenes[j].ID
	})

	if err := s.loadRelation(studioURL, "scene_performers", "performers", "performer_id", scenes); err != nil {
		return nil, err
	}
	if err := s.loadRelation(studioURL, "scene_tags", "tags", "tag_id", scenes); err != nil {
		return nil, err
	}
	if err := s.loadRelation(studioURL, "scene_categories", "categories", "category_id", scenes); err != nil {
		return nil, err
	}
	if err := s.loadPriceHistory(studioURL, scenes); err != nil {
		return nil, err
	}
	if err := s.loadExternalIDs(studioURL, scenes); err != nil {
		return nil, err
	}
	return scenes, nil
}

// Save replaces the stored scene set for studioURL with the passed
// slice. Scenes whose `(id, site_id)` is absent from `scenes` are hard-
// deleted from the `scenes` table (the `scene_performers`,
// `scene_tags`, `scene_categories` join rows cascade away via the
// schema's `ON DELETE CASCADE`; `price_history` rows have no FK
// CASCADE so they are deleted explicitly inside the same transaction).
//
// This matches the Flat store's behaviour, which rewrites the whole
// `<studio>.json` file. The cmd layer's `--full` path relies on this
// contract: it builds `result` from freshly-scraped scenes only and
// expects everything not present to disappear. Incremental and
// `--refresh` modes pass the merged set so nothing is dropped.
func (s *SQLite) Save(studioURL string, scenes []models.Scene) error {
	studioURL = canonicalKey(studioURL)
	if err := validateScenes(scenes); err != nil {
		return err
	}
	scenes = withCanonicalStudioURL(scenes, studioURL)

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	keep := make(map[sceneKey]struct{}, len(scenes))
	for _, sc := range scenes {
		keep[sceneKey{id: sc.ID, siteID: sc.SiteID}] = struct{}{}
	}

	// One pass over the studio's rows serves two purposes: finding what to
	// delete, and capturing each row's content hash so unchanged scenes can be
	// skipped below. Deliberately no joins — this must stay cheap.
	rows, err := tx.Query(
		`SELECT id, site_id, COALESCE(content_hash, ''), scraped_at FROM scenes WHERE studio_url = ?`,
		studioURL)
	if err != nil {
		return fmt.Errorf("selecting existing scene keys: %w", err)
	}
	var toDelete []sceneKey
	stored := make(map[sceneKey]storedScene, len(scenes))
	for rows.Next() {
		var k sceneKey
		var st storedScene
		if err := rows.Scan(&k.id, &k.siteID, &st.hash, &st.scrapedAt); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scanning scene key: %w", err)
		}
		if _, kept := keep[k]; !kept {
			toDelete = append(toDelete, k)
			continue
		}
		stored[k] = st
	}
	// A truncated iteration (driver error mid-scan) would yield an incomplete
	// delete-candidate list; abort rather than commit a partial authoritative
	// Save. The deferred Rollback unwinds the transaction.
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterating existing scene keys: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("closing scene-keys cursor: %w", err)
	}

	for _, k := range toDelete {
		// Delete price_history first — no FK CASCADE on that table.
		if _, err := tx.Exec(
			`DELETE FROM price_history WHERE scene_id = ? AND site_id = ? AND studio_url = ?`,
			k.id, k.siteID, studioURL,
		); err != nil {
			return fmt.Errorf("deleting price_history for %s/%s: %w", k.siteID, k.id, err)
		}
		if _, err := tx.Exec(
			`DELETE FROM scenes WHERE id = ? AND site_id = ? AND studio_url = ?`,
			k.id, k.siteID, studioURL,
		); err != nil {
			return fmt.Errorf("deleting scene %s/%s: %w", k.siteID, k.id, err)
		}
	}

	// Only scenes whose stored fingerprint differs need the full write path
	// (row upsert plus three relation syncs plus price history). For the rest,
	// at most `scraped_at` moved, which is one narrow UPDATE.
	//
	// This is what makes an incremental scrape cheap: without it, recording a
	// single new scene in a 59k-scene studio issued ~296,000 statements.
	// One session per Save: it caches prepared statements and entity-name
	// lookups, which is what keeps a first ingest of tens of thousands of
	// scenes from re-parsing its SQL and re-resolving the same names.
	sess := newSaveSession(tx)
	defer sess.close()

	const touchSQL = `UPDATE scenes SET scraped_at = ? WHERE id = ? AND site_id = ? AND studio_url = ?`

	for _, sc := range scenes {
		hash := sceneContentHash(sc)
		if prev, ok := stored[sceneKey{id: sc.ID, siteID: sc.SiteID}]; ok && prev.hash == hash {
			if now := timeStr(sc.ScrapedAt); now != prev.scrapedAt {
				if _, err := sess.exec(touchSQL, now, sc.ID, sc.SiteID, sc.StudioURL); err != nil {
					return fmt.Errorf("updating scraped_at for %s: %w", sc.ID, err)
				}
			}
			continue
		}
		if err := upsertScene(sess, sc, hash); err != nil {
			return err
		}
	}

	// Prepared statements are bound to the transaction, so release them before
	// committing rather than on the deferred path.
	sess.close()
	return tx.Commit()
}

// sceneKey is the composite primary key on the `scenes` table.
type sceneKey struct {
	id     string
	siteID string
}

// storedScene is the cheap per-row state Save reads up front to decide whether
// a scene needs rewriting.
type storedScene struct {
	hash      string
	scrapedAt string
}

func (s *SQLite) MarkDeleted(studioURL, siteID string, ids []string) error {
	studioURL = canonicalKey(studioURL)
	now := timeStr(time.Now().UTC())
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// content_hash is cleared alongside deleted_at. Save skips any scene whose
	// hash still matches, so a row mutated outside upsertScene must invalidate
	// its fingerprint — otherwise re-saving the scene with DeletedAt == nil
	// would be skipped and the soft-delete would never lift.
	//
	// Any future writer that touches scene state outside upsertScene owes the
	// same invalidation.
	for _, id := range ids {
		if _, err := tx.Exec(
			`UPDATE scenes SET deleted_at = ?, content_hash = ''
			 WHERE id = ? AND site_id = ? AND studio_url = ? AND deleted_at IS NULL`,
			now, id, siteID, studioURL,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLite) UpsertStudio(studio models.Studio) error {
	// Key on the canonical URL like every other entry point, or a studio row
	// lands under a spelling its scenes are not stored under and is orphaned.
	studio.URL = canonicalKey(studio.URL)
	_, err := s.db.Exec(`
		INSERT INTO studios (url, site_id, name, added_at, last_scraped_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(url) DO UPDATE SET
		    name            = CASE WHEN excluded.name != '' THEN excluded.name ELSE studios.name END,
		    last_scraped_at = excluded.last_scraped_at`,
		studio.URL, studio.SiteID, studio.Name,
		timeStr(studio.AddedAt), timePtrStr(studio.LastScrapedAt),
	)
	return err
}

func (s *SQLite) ListStudios() ([]models.Studio, error) {
	rows, err := s.db.Query(`
		SELECT url, site_id, name, added_at, last_scraped_at
		FROM studios ORDER BY added_at ASC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var studios []models.Studio
	for rows.Next() {
		var st models.Studio
		var addedAt string
		var lastScrapedAt sql.NullString
		if err := rows.Scan(&st.URL, &st.SiteID, &st.Name, &addedAt, &lastScrapedAt); err != nil {
			return nil, err
		}
		if st.AddedAt, err = parseStr(addedAt); err != nil {
			return nil, fmt.Errorf("parsing added_at for %s: %w", st.URL, err)
		}
		if st.LastScrapedAt, err = parseStrPtr(lastScrapedAt); err != nil {
			return nil, fmt.Errorf("parsing last_scraped_at for %s: %w", st.URL, err)
		}
		studios = append(studios, st)
	}
	return studios, rows.Err()
}

func (s *SQLite) Export(format, path, studioURL string) error {
	studioURL = canonicalKey(studioURL)
	scenes, err := s.Load(studioURL)
	if err != nil {
		return err
	}
	switch format {
	case "json":
		sf := models.StudioFile{
			SchemaVersion: models.StoreSchemaVersion,
			StudioURL:     studioURL,
			ScrapedAt:     time.Now().UTC(),
			SceneCount:    len(scenes),
			Scenes:        scenes,
		}
		return WriteJSON(sf, path)
	case "csv":
		return WriteCSV(scenes, path)
	default:
		return fmt.Errorf("unknown export format %q", format)
	}
}

// ---- upsert helpers ----

func upsertScene(sess *saveSession, sc models.Scene, contentHash string) error {
	_, err := sess.exec(`
		INSERT INTO scenes (
		    id, site_id, studio_url, title, url, date, description,
		    thumbnail, preview, performers, director, studio,
		    tags, categories, series, series_part,
		    duration, resolution, width, height, format,
		    views, likes, comments,
		    lowest_price, lowest_price_date, first_seen_at, scraped_at, deleted_at,
		    content_hash
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id, site_id, studio_url) DO UPDATE SET
		    title             = excluded.title,
		    url               = excluded.url,
		    date              = excluded.date,
		    description       = excluded.description,
		    thumbnail         = excluded.thumbnail,
		    preview           = excluded.preview,
		    performers        = excluded.performers,
		    director          = excluded.director,
		    studio            = excluded.studio,
		    tags              = excluded.tags,
		    categories        = excluded.categories,
		    series            = excluded.series,
		    series_part       = excluded.series_part,
		    duration          = excluded.duration,
		    resolution        = excluded.resolution,
		    width             = excluded.width,
		    height            = excluded.height,
		    format            = excluded.format,
		    views             = excluded.views,
		    likes             = excluded.likes,
		    comments          = excluded.comments,
		    lowest_price      = excluded.lowest_price,
		    lowest_price_date = excluded.lowest_price_date,
		    -- sticky: a first sighting already on record is never overwritten
		    first_seen_at     = COALESCE(NULLIF(scenes.first_seen_at, ''), excluded.first_seen_at),
		    scraped_at        = excluded.scraped_at,
		    deleted_at        = excluded.deleted_at,
		    content_hash      = excluded.content_hash`,
		sc.ID, sc.SiteID, sc.StudioURL, sc.Title, sc.URL,
		timeStr(sc.Date), sc.Description, sc.Thumbnail, sc.Preview,
		"[]", sc.Director, sc.Studio,
		"[]", "[]",
		sc.Series, sc.SeriesPart,
		sc.Duration, sc.Resolution, sc.Width, sc.Height, sc.Format,
		sc.Views, sc.Likes, sc.Comments,
		sc.LowestPrice, timePtrStr(sc.LowestPriceDate),
		timeStr(firstSeenFor(sc, nil)), timeStr(sc.ScrapedAt), timePtrStr(sc.DeletedAt),
		contentHash,
	)
	if err != nil {
		return fmt.Errorf("upserting scene %s: %w", sc.ID, err)
	}

	if err := syncRelation(sess, "performers", "scene_performers", "performer_id", sc.ID, sc.SiteID, sc.StudioURL, sc.Performers, true); err != nil {
		return fmt.Errorf("upserting performers for %s: %w", sc.ID, err)
	}
	if err := syncRelation(sess, "tags", "scene_tags", "tag_id", sc.ID, sc.SiteID, sc.StudioURL, sc.Tags, true); err != nil {
		return fmt.Errorf("upserting tags for %s: %w", sc.ID, err)
	}
	if err := syncRelation(sess, "categories", "scene_categories", "category_id", sc.ID, sc.SiteID, sc.StudioURL, sc.Categories, true); err != nil {
		return fmt.Errorf("upserting categories for %s: %w", sc.ID, err)
	}

	if err := syncExternalIDs(sess, sc); err != nil {
		return fmt.Errorf("upserting external IDs for %s: %w", sc.ID, err)
	}

	return syncPriceHistory(sess, sc)
}

// syncExternalIDs reconciles a scene's external-ID rows: drop sources no longer
// claimed, upsert the rest. Blank sources and blank IDs are skipped.
func syncExternalIDs(sess *saveSession, sc models.Scene) error {
	sources := make([]string, 0, len(sc.ExternalIDs))
	for source, id := range sc.ExternalIDs {
		if source == "" || id == "" {
			continue
		}
		sources = append(sources, source)
	}
	sort.Strings(sources) // deterministic statement text and insert order

	del := `DELETE FROM scene_external_ids WHERE scene_id = ? AND site_id = ? AND studio_url = ?`
	args := []any{sc.ID, sc.SiteID, sc.StudioURL}
	if len(sources) > 0 {
		del += ` AND source NOT IN (?` + strings.Repeat(",?", len(sources)-1) + `)`
		for _, source := range sources {
			args = append(args, source)
		}
	}
	if _, err := sess.exec(del, args...); err != nil {
		return err
	}

	for _, source := range sources {
		if _, err := sess.exec(`
			INSERT INTO scene_external_ids (scene_id, site_id, studio_url, source, external_id)
			VALUES (?,?,?,?,?)
			ON CONFLICT(scene_id, site_id, studio_url, source)
			DO UPDATE SET external_id = excluded.external_id`,
			sc.ID, sc.SiteID, sc.StudioURL, source, sc.ExternalIDs[source],
		); err != nil {
			return err
		}
	}
	return nil
}

var allowedRelationIDs = map[string]bool{
	"performers":       true,
	"tags":             true,
	"categories":       true,
	"scene_performers": true,
	"scene_tags":       true,
	"scene_categories": true,
	"performer_id":     true,
	"tag_id":           true,
	"category_id":      true,
	"position":         true,
}

func validateRelationIDs(names ...string) error {
	for _, n := range names {
		if !allowedRelationIDs[n] {
			return fmt.Errorf("disallowed SQL identifier: %q", n)
		}
	}
	return nil
}

// syncRelationLegacy is the pre-studio_url junction sync, FROZEN for use by
// applyMigration1 only (it populates the v1 two-column junction tables before
// migration2 rebuilds them). Runtime code uses syncRelation.
func syncRelationLegacy(tx *sql.Tx, entityTable, junctionTable, fkCol, sceneID, siteID string, names []string, withPosition bool) error {
	if err := validateRelationIDs(entityTable, junctionTable, fkCol); err != nil {
		return err
	}
	type want struct {
		name     string
		position int
	}
	seen := make(map[string]struct{}, len(names))
	desired := make([]want, 0, len(names))
	for i, name := range names {
		if name == "" {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		desired = append(desired, want{name: name, position: i})
	}
	desiredSet := make(map[string]int, len(desired))
	for _, w := range desired {
		desiredSet[w.name] = w.position
	}

	type linked struct {
		id       int64
		position int
	}
	posCol := "0"
	if withPosition {
		posCol = "j.position"
	}
	rows, err := tx.Query(
		`SELECT e.name, j.`+fkCol+`, `+posCol+`
		 FROM `+junctionTable+` j
		 JOIN `+entityTable+` e ON j.`+fkCol+` = e.id
		 WHERE j.scene_id = ? AND j.site_id = ?`,
		sceneID, siteID,
	)
	if err != nil {
		return err
	}
	existing := map[string]linked{}
	for rows.Next() {
		var name string
		var l linked
		if err := rows.Scan(&name, &l.id, &l.position); err != nil {
			_ = rows.Close()
			return err
		}
		existing[name] = l
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	// Drop junction rows whose entity is no longer in the desired set.
	for name, l := range existing {
		if _, keep := desiredSet[name]; keep {
			continue
		}
		if _, err := tx.Exec(
			`DELETE FROM `+junctionTable+
				` WHERE scene_id = ? AND site_id = ? AND `+fkCol+` = ?`,
			sceneID, siteID, l.id,
		); err != nil {
			return err
		}
		delete(existing, name)
	}

	// Walk desired in input order so newly inserted rowids reflect that order
	// (loadRelation has no ORDER BY for tags/categories and relies on this).
	for _, w := range desired {
		if l, ok := existing[w.name]; ok {
			if withPosition && l.position != w.position {
				if _, err := tx.Exec(
					`UPDATE `+junctionTable+
						` SET position = ? WHERE scene_id = ? AND site_id = ? AND `+fkCol+` = ?`,
					w.position, sceneID, siteID, l.id,
				); err != nil {
					return err
				}
			}
			continue
		}

		var id int64
		if err := tx.QueryRow(
			`INSERT INTO `+entityTable+` (name) VALUES (?)
			 ON CONFLICT(name) DO UPDATE SET name = excluded.name
			 RETURNING id`,
			w.name,
		).Scan(&id); err != nil {
			return err
		}
		if withPosition {
			_, err = tx.Exec(
				`INSERT INTO `+junctionTable+` (scene_id, site_id, `+fkCol+`, position) VALUES (?,?,?,?)`,
				sceneID, siteID, id, w.position,
			)
		} else {
			_, err = tx.Exec(
				`INSERT INTO `+junctionTable+` (scene_id, site_id, `+fkCol+`) VALUES (?,?,?)`,
				sceneID, siteID, id,
			)
		}
		if err != nil {
			return err
		}
	}

	return nil
}

// syncRelation reconciles a scene's junction rows (performers/tags/categories)
// against the desired name list. Rows are keyed by (scene_id, site_id,
// studio_url) so two studio URLs on the same site keep separate relations.
func syncRelation(sess *saveSession, entityTable, junctionTable, fkCol, sceneID, siteID, studioURL string, names []string, withPosition bool) error {
	if err := validateRelationIDs(entityTable, junctionTable, fkCol); err != nil {
		return err
	}
	type want struct {
		name     string
		position int
	}
	seen := make(map[string]struct{}, len(names))
	desired := make([]want, 0, len(names))
	for i, name := range names {
		if name == "" {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		desired = append(desired, want{name: name, position: i})
	}
	desiredSet := make(map[string]int, len(desired))
	for _, w := range desired {
		desiredSet[w.name] = w.position
	}

	type linked struct {
		id       int64
		position int
	}
	posCol := "0"
	if withPosition {
		posCol = "j.position"
	}
	rows, err := sess.query(
		`SELECT e.name, j.`+fkCol+`, `+posCol+`
		 FROM `+junctionTable+` j
		 JOIN `+entityTable+` e ON j.`+fkCol+` = e.id
		 WHERE j.scene_id = ? AND j.site_id = ? AND j.studio_url = ?`,
		sceneID, siteID, studioURL,
	)
	if err != nil {
		return err
	}
	existing := map[string]linked{}
	for rows.Next() {
		var name string
		var l linked
		if err := rows.Scan(&name, &l.id, &l.position); err != nil {
			_ = rows.Close()
			return err
		}
		existing[name] = l
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	// Drop junction rows whose entity is no longer in the desired set.
	for name, l := range existing {
		if _, keep := desiredSet[name]; keep {
			continue
		}
		if _, err := sess.exec(
			`DELETE FROM `+junctionTable+
				` WHERE scene_id = ? AND site_id = ? AND studio_url = ? AND `+fkCol+` = ?`,
			sceneID, siteID, studioURL, l.id,
		); err != nil {
			return err
		}
		delete(existing, name)
	}

	// Walk desired in input order so newly inserted rowids reflect that order
	// (loadRelation has no ORDER BY for tags/categories and relies on this).
	for _, w := range desired {
		if l, ok := existing[w.name]; ok {
			if withPosition && l.position != w.position {
				if _, err := sess.exec(
					`UPDATE `+junctionTable+
						` SET position = ? WHERE scene_id = ? AND site_id = ? AND studio_url = ? AND `+fkCol+` = ?`,
					w.position, sceneID, siteID, studioURL, l.id,
				); err != nil {
					return err
				}
			}
			continue
		}

		// Resolved through the session cache: names are globally unique per
		// table, so the first lookup in this transaction serves every later
		// scene that uses the same name.
		id, err := sess.entityID(entityTable, w.name)
		if err != nil {
			return err
		}
		if withPosition {
			_, err = sess.exec(
				`INSERT INTO `+junctionTable+` (scene_id, site_id, studio_url, `+fkCol+`, position) VALUES (?,?,?,?,?)`,
				sceneID, siteID, studioURL, id, w.position,
			)
		} else {
			_, err = sess.exec(
				`INSERT INTO `+junctionTable+` (scene_id, site_id, studio_url, `+fkCol+`) VALUES (?,?,?,?)`,
				sceneID, siteID, studioURL, id,
			)
		}
		if err != nil {
			return err
		}
	}

	return nil
}

// syncPriceHistory diffs the desired snapshot list against price_history rows
// already stored for this scene, deleting and inserting only the differences.
// Identity is the full snapshot tuple (date + price fields), so re-saves with
// unchanged history are a single SELECT, and adding one snapshot is a single
// INSERT — instead of the old DELETE-all + reinsert-all pattern.
func syncPriceHistory(sess *saveSession, sc models.Scene) error {
	type key struct {
		date            string
		regular         float64
		discounted      float64
		isFree          int
		isOnSale        int
		discountPercent int
	}
	keyOf := func(p models.PriceSnapshot) key {
		return key{
			date:            timeStr(p.Date),
			regular:         p.Regular,
			discounted:      p.Discounted,
			isFree:          boolInt(p.IsFree),
			isOnSale:        boolInt(p.IsOnSale),
			discountPercent: p.DiscountPercent,
		}
	}

	desired := make(map[key]int, len(sc.PriceHistory))
	for _, p := range sc.PriceHistory {
		desired[keyOf(p)]++
	}

	rows, err := sess.query(`
		SELECT id, date, regular, discounted, is_free, is_on_sale, discount_percent
		FROM price_history WHERE scene_id = ? AND site_id = ? AND studio_url = ?`,
		sc.ID, sc.SiteID, sc.StudioURL,
	)
	if err != nil {
		return err
	}
	type existingRow struct {
		id  int64
		key key
	}
	var existing []existingRow
	for rows.Next() {
		var r existingRow
		if err := rows.Scan(
			&r.id, &r.key.date, &r.key.regular, &r.key.discounted,
			&r.key.isFree, &r.key.isOnSale, &r.key.discountPercent,
		); err != nil {
			_ = rows.Close()
			return err
		}
		existing = append(existing, r)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	// For each existing row: if the desired set still wants one, consume it;
	// otherwise the row is no longer needed and is deleted.
	for _, r := range existing {
		if desired[r.key] > 0 {
			desired[r.key]--
			continue
		}
		if _, err := sess.exec(
			`DELETE FROM price_history WHERE id = ?`, r.id,
		); err != nil {
			return err
		}
	}

	// Anything still owed in `desired` represents new snapshots to insert.
	for _, p := range sc.PriceHistory {
		k := keyOf(p)
		if desired[k] <= 0 {
			continue
		}
		desired[k]--
		if _, err := sess.exec(`
			INSERT INTO price_history (scene_id, site_id, studio_url, date, regular, discounted, is_free, is_on_sale, discount_percent)
			VALUES (?,?,?,?,?,?,?,?,?)`,
			sc.ID, sc.SiteID, sc.StudioURL, k.date,
			k.regular, k.discounted, k.isFree, k.isOnSale, k.discountPercent,
		); err != nil {
			return fmt.Errorf("inserting price history for %s: %w", sc.ID, err)
		}
	}

	return nil
}

// ---- load helpers ----

// loadRelation batch-loads a string relation (performers, tags, or categories)
// for every scene in studioURL and attaches the results.
//
// The names are grouped into JSON arrays in SQL rather than returned one row
// per name. A 59k-scene studio has 1.4M scene_tags rows, and crossing the
// database/sql boundary that many times cost more than the query itself —
// profiling put ~70% of Load in Rows.Next. Grouping returns one row per scene
// instead, and the covering indexes from migration 8 let SQLite stream the
// GROUP BY rather than build a temp B-tree.
//
// Two parallel aggregates are used instead of one array of objects: both are
// fed the same row sequence, so index i in each refers to the same junction
// row, and []string / []int decode far more cheaply than a slice of structs.
// Order is taken from the position column, never from the order rows happen to
// arrive in.
func (s *SQLite) loadRelation(studioURL, junctionTable, entityTable, fkCol string, scenes []models.Scene) error {
	if err := validateRelationIDs(junctionTable, entityTable, fkCol); err != nil {
		return err
	}
	if len(scenes) == 0 {
		return nil
	}
	// Index by the composite primary key `(id, site_id)`. Using a struct key
	// instead of a `siteID+":"+id` concatenation avoids collisions if a scraper
	// ever stores IDs containing a colon.
	idx := make(map[sceneKey]int, len(scenes))
	for i, sc := range scenes {
		idx[sceneKey{id: sc.ID, siteID: sc.SiteID}] = i
	}

	rows, err := s.db.Query(`SELECT j.scene_id, j.site_id,
			json_group_array(e.name), json_group_array(j.position)
		FROM `+junctionTable+` j
		JOIN `+entityTable+` e ON j.`+fkCol+` = e.id
		WHERE j.studio_url = ?
		GROUP BY j.scene_id, j.site_id`, studioURL)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var sceneID, siteID, namesJSON, positionsJSON string
		if err := rows.Scan(&sceneID, &siteID, &namesJSON, &positionsJSON); err != nil {
			return err
		}
		i, ok := idx[sceneKey{id: sceneID, siteID: siteID}]
		if !ok {
			continue
		}
		names, err := orderedNames(namesJSON, positionsJSON)
		if err != nil {
			return fmt.Errorf("decoding %s for scene %s: %w", junctionTable, sceneID, err)
		}
		switch junctionTable {
		case "scene_performers":
			scenes[i].Performers = names
		case "scene_tags":
			scenes[i].Tags = names
		case "scene_categories":
			scenes[i].Categories = names
		}
	}
	return rows.Err()
}

// orderedNames decodes the two parallel JSON aggregates and returns the names
// sorted by their stored position.
func orderedNames(namesJSON, positionsJSON string) ([]string, error) {
	var names []string
	if err := json.Unmarshal([]byte(namesJSON), &names); err != nil {
		return nil, err
	}
	var positions []int
	if err := json.Unmarshal([]byte(positionsJSON), &positions); err != nil {
		return nil, err
	}
	if len(positions) != len(names) {
		return nil, fmt.Errorf("aggregate length mismatch: %d names, %d positions", len(names), len(positions))
	}
	// Already ordered in the common case — the covering index feeds rows in
	// position order — so check before paying for a sort.
	if sort.IntsAreSorted(positions) {
		return names, nil
	}
	order := make([]int, len(names))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return positions[order[a]] < positions[order[b]] })
	out := make([]string, len(names))
	for i, o := range order {
		out[i] = names[o]
	}
	return out, nil
}

func (s *SQLite) loadExternalIDs(studioURL string, scenes []models.Scene) error {
	if len(scenes) == 0 {
		return nil
	}
	idx := make(map[sceneKey]int, len(scenes))
	for i, sc := range scenes {
		idx[sceneKey{id: sc.ID, siteID: sc.SiteID}] = i
	}

	rows, err := s.db.Query(`
		SELECT scene_id, site_id, source, external_id
		FROM scene_external_ids WHERE studio_url = ?`, studioURL)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var sceneID, siteID, source, externalID string
		if err := rows.Scan(&sceneID, &siteID, &source, &externalID); err != nil {
			return err
		}
		i, ok := idx[sceneKey{id: sceneID, siteID: siteID}]
		if !ok {
			continue
		}
		if scenes[i].ExternalIDs == nil {
			scenes[i].ExternalIDs = map[string]string{}
		}
		scenes[i].ExternalIDs[source] = externalID
	}
	return rows.Err()
}

func (s *SQLite) loadPriceHistory(studioURL string, scenes []models.Scene) error {
	if len(scenes) == 0 {
		return nil
	}
	idx := make(map[sceneKey]int, len(scenes))
	for i, sc := range scenes {
		idx[sceneKey{id: sc.ID, siteID: sc.SiteID}] = i
	}

	rows, err := s.db.Query(`
		SELECT ph.scene_id, ph.site_id, ph.date, ph.regular, ph.discounted,
		       ph.is_free, ph.is_on_sale, ph.discount_percent
		FROM price_history ph
		JOIN scenes sc ON ph.scene_id = sc.id AND ph.site_id = sc.site_id AND ph.studio_url = sc.studio_url
		WHERE sc.studio_url = ?
		ORDER BY ph.date ASC`, studioURL)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var sceneID, siteID, dateStr string
		var p models.PriceSnapshot
		var isFree, isOnSale int
		if err := rows.Scan(&sceneID, &siteID, &dateStr,
			&p.Regular, &p.Discounted, &isFree, &isOnSale, &p.DiscountPercent); err != nil {
			return err
		}
		var parseErr error
		p.Date, parseErr = parseStr(dateStr)
		if parseErr != nil {
			return fmt.Errorf("parsing price_history date for %s: %w", sceneID, parseErr)
		}
		p.IsFree = isFree != 0
		p.IsOnSale = isOnSale != 0
		if i, ok := idx[sceneKey{id: sceneID, siteID: siteID}]; ok {
			scenes[i].PriceHistory = append(scenes[i].PriceHistory, p)
		}
	}
	return rows.Err()
}

func scanScene(rows *sql.Rows) (models.Scene, error) {
	var sc models.Scene
	var (
		dateStr         string
		lowestPriceDate sql.NullString
		firstSeenAt     string
		scrapedAt       string
		deletedAt       sql.NullString
	)
	err := rows.Scan(
		&sc.ID, &sc.SiteID, &sc.StudioURL, &sc.Title, &sc.URL,
		&dateStr, &sc.Description, &sc.Thumbnail, &sc.Preview,
		&sc.Director, &sc.Studio,
		&sc.Series, &sc.SeriesPart,
		&sc.Duration, &sc.Resolution, &sc.Width, &sc.Height, &sc.Format,
		&sc.Views, &sc.Likes, &sc.Comments,
		&sc.LowestPrice, &lowestPriceDate, &firstSeenAt, &scrapedAt, &deletedAt,
	)
	if err != nil {
		return sc, err
	}
	if sc.Date, err = parseStr(dateStr); err != nil {
		return sc, fmt.Errorf("parsing date for %s: %w", sc.ID, err)
	}
	if sc.FirstSeenAt, err = parseStr(firstSeenAt); err != nil {
		return sc, fmt.Errorf("parsing first_seen_at for %s: %w", sc.ID, err)
	}
	if sc.ScrapedAt, err = parseStr(scrapedAt); err != nil {
		return sc, fmt.Errorf("parsing scraped_at for %s: %w", sc.ID, err)
	}
	if sc.LowestPriceDate, err = parseStrPtr(lowestPriceDate); err != nil {
		return sc, fmt.Errorf("parsing lowest_price_date for %s: %w", sc.ID, err)
	}
	if sc.DeletedAt, err = parseStrPtr(deletedAt); err != nil {
		return sc, fmt.Errorf("parsing deleted_at for %s: %w", sc.ID, err)
	}
	return sc, nil
}

// ---- time / encoding helpers ----

func timeStr(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func timePtrStr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}

func parseStr(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, s)
}

func parseStrPtr(s sql.NullString) (*time.Time, error) {
	if !s.Valid || s.String == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, s.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// unmarshalStrings decodes a JSON array of strings, returning nil for empty/null arrays.
func unmarshalStrings(s string) ([]string, error) {
	if s == "" || s == "[]" || s == "null" {
		return nil, nil
	}
	var result []string
	if err := json.Unmarshal([]byte(s), &result); err != nil {
		return nil, err
	}
	return result, nil
}

// UnreferencedVocabulary counts entity rows no scene links to any more, per
// table.
//
// Save deletes a scene's junction rows but never the `performers` / `tags` /
// `categories` rows they pointed at, because the entity may still be shared
// with another studio and proving otherwise costs a scan. Over years of
// re-scrapes those accumulate: a parser that briefly truncated tag names leaves
// its mistakes in the vocabulary long after the junction rows are corrected.
//
// Deliberately not folded into Save. Each query walks the whole junction table,
// and Save is the hot path an incremental scrape runs per studio — the whole
// point of its content-hash short-circuit is not to touch 1.4M junction rows to
// record one new scene. This is maintenance, so it runs when asked.
// SceneCounts returns the number of live scenes per studio URL, keyed the same
// way Load and Save key: canonical URLs, as stored.
//
// Soft-deleted scenes are excluded. They are still rows, but they represent
// scenes the site no longer publishes, and counting them would make a database
// look ahead of a JSON file that never recorded them.
func (s *SQLite) SceneCounts() (map[string]int, error) {
	rows, err := s.db.Query(`SELECT studio_url, COUNT(*) FROM scenes WHERE deleted_at IS NULL GROUP BY studio_url`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	counts := map[string]int{}
	for rows.Next() {
		var url string
		var n int
		if err := rows.Scan(&url, &n); err != nil {
			return nil, err
		}
		counts[url] = n
	}
	return counts, rows.Err()
}

func (s *SQLite) UnreferencedVocabulary() (map[string]int, error) {
	out := make(map[string]int, len(vocabularyTables))
	for _, t := range vocabularyTables {
		var n int
		q := fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE id NOT IN (SELECT %s FROM %s)`,
			t.entity, t.fk, t.junction)
		if err := s.db.QueryRow(q).Scan(&n); err != nil {
			return nil, fmt.Errorf("counting unreferenced %s: %w", t.entity, err)
		}
		out[t.entity] = n
	}
	return out, nil
}

// PruneVocabulary deletes the rows UnreferencedVocabulary counts, returning how
// many went per table. It is safe to run at any time: a row with no junction
// row is unreachable from every scene, so nothing observable changes.
func (s *SQLite) PruneVocabulary() (map[string]int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	out := make(map[string]int, len(vocabularyTables))
	for _, t := range vocabularyTables {
		q := fmt.Sprintf(`DELETE FROM %s WHERE id NOT IN (SELECT %s FROM %s)`,
			t.entity, t.fk, t.junction)
		res, err := tx.Exec(q)
		if err != nil {
			return nil, fmt.Errorf("pruning %s: %w", t.entity, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("counting pruned %s: %w", t.entity, err)
		}
		out[t.entity] = int(n)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

// vocabularyTables lists the shared entity tables and the junction column that
// references each.
var vocabularyTables = []struct{ entity, junction, fk string }{
	{"performers", "scene_performers", "performer_id"},
	{"tags", "scene_tags", "tag_id"},
	{"categories", "scene_categories", "category_id"},
}
