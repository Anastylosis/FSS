package store

import (
	"database/sql"
	"fmt"
)

// Migrations 1-8 and syncRelationLegacy are frozen: they reproduce the schema
// history exactly as shipped. Do not "tidy" them to match the live write path.

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
func (s *SQLite) applyMigration2() (err error) {
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
