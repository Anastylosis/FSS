package store

import (
	"database/sql"
	"fmt"
)

// latestSchemaVersion is the newest migration this binary knows. NewSQLite
// refuses a database stamped higher.
const latestSchemaVersion = 11

// applyMigration10 repairs what migration 9 left behind. See docs/storage.md.
func (s *SQLite) applyMigration10() error {
	mapping, err := s.studioURLMapping()
	if err != nil {
		return err
	}

	if _, err := s.db.Exec("PRAGMA foreign_keys = OFF"); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		_, _ = s.db.Exec("PRAGMA foreign_keys = ON")
		return err
	}
	err = migration10Tx(tx, mapping)
	if err != nil {
		_ = tx.Rollback()
	} else {
		err = tx.Commit()
	}
	if _, ferr := s.db.Exec("PRAGMA foreign_keys = ON"); ferr != nil && err == nil {
		err = fmt.Errorf("re-enabling foreign keys: %w", ferr)
	}
	return err
}

func migration10Tx(tx *sql.Tx, mapping map[string]string) error {
	if len(mapping) > 0 {
		if _, err := tx.Exec(`CREATE TEMP TABLE url_map (
			raw TEXT PRIMARY KEY, canonical TEXT NOT NULL)`); err != nil {
			return fmt.Errorf("creating url map: %w", err)
		}
		for raw, canonical := range mapping {
			if _, err := tx.Exec(`INSERT INTO url_map (raw, canonical) VALUES (?, ?)`, raw, canonical); err != nil {
				return fmt.Errorf("mapping %s: %w", raw, err)
			}
		}
		for _, stmt := range migration10WinnerOnly {
			if _, err := tx.Exec(stmt); err != nil {
				return fmt.Errorf("re-canonicalising: %w", err)
			}
		}
		for _, stmt := range migration9Statements {
			if _, err := tx.Exec(stmt); err != nil {
				return fmt.Errorf("re-canonicalising: %w", err)
			}
		}
		if _, err := tx.Exec(`DROP TABLE win`); err != nil {
			return err
		}
	}
	for _, stmt := range migration10Statements {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("migration 10 step: %w", err)
		}
	}
	if _, err := tx.Exec(`DELETE FROM schema_version`); err != nil {
		return err
	}
	_, err := tx.Exec(`INSERT INTO schema_version (version) VALUES (10)`)
	return err
}

// junctionRebuild rebuilds one junction table with its foreign keys, copying
// only rows whose scene and entity still exist.
func junctionRebuild(table, col, entityTable, extraCols, extraDDL string) []string {
	return []string{
		fmt.Sprintf(`CREATE TABLE %[1]s_new (
		    scene_id TEXT NOT NULL, site_id TEXT NOT NULL, studio_url TEXT NOT NULL,
		    %[2]s,
		    PRIMARY KEY (scene_id, site_id, studio_url, %[3]s),
		    FOREIGN KEY (scene_id, site_id, studio_url) REFERENCES scenes(id, site_id, studio_url) ON DELETE CASCADE%[4]s)`,
			table, extraDDL, col, entityFK(col, entityTable)),
		fmt.Sprintf(`INSERT OR IGNORE INTO %[1]s_new (scene_id, site_id, studio_url, %[2]s)
		    SELECT j.scene_id, j.site_id, j.studio_url, %[3]s FROM %[1]s j
		    WHERE EXISTS (SELECT 1 FROM scenes s
		                  WHERE s.id = j.scene_id AND s.site_id = j.site_id AND s.studio_url = j.studio_url)%[4]s`,
			table, extraCols, prefixed("j.", extraCols), entityExists(col, entityTable)),
		fmt.Sprintf(`DROP TABLE %s`, table),
		fmt.Sprintf(`ALTER TABLE %s_new RENAME TO %s`, table, table),
	}
}

func entityFK(col, entity string) string {
	if entity == "" {
		return ""
	}
	return fmt.Sprintf(",\n\t\t    FOREIGN KEY (%s) REFERENCES %s(id)", col, entity)
}

func entityExists(col, entity string) string {
	if entity == "" {
		return ""
	}
	return fmt.Sprintf("\n\t\t    AND EXISTS (SELECT 1 FROM %s e WHERE e.id = j.%s)", entity, col)
}

func prefixed(prefix, cols string) string {
	out := ""
	start := 0
	for i := 0; i <= len(cols); i++ {
		if i == len(cols) || cols[i] == ',' {
			part := cols[start:i]
			for len(part) > 0 && part[0] == ' ' {
				part = part[1:]
			}
			if out != "" {
				out += ", "
			}
			out += prefix + part
			start = i + 1
		}
	}
	return out
}

var migration10Statements = func() []string {
	var out []string
	out = append(out, junctionRebuild("scene_performers", "performer_id", "performers",
		"performer_id, position", "performer_id INTEGER NOT NULL, position INTEGER NOT NULL DEFAULT 0")...)
	out = append(out, junctionRebuild("scene_tags", "tag_id", "tags",
		"tag_id, position", "tag_id INTEGER NOT NULL, position INTEGER NOT NULL DEFAULT 0")...)
	out = append(out, junctionRebuild("scene_categories", "category_id", "categories",
		"category_id, position", "category_id INTEGER NOT NULL, position INTEGER NOT NULL DEFAULT 0")...)
	out = append(out, junctionRebuild("scene_external_ids", "source", "",
		"source, external_id", "source TEXT NOT NULL, external_id TEXT NOT NULL")...)
	return append(out,
		`DELETE FROM price_history WHERE NOT EXISTS (
		   SELECT 1 FROM scenes s WHERE s.id = price_history.scene_id
		     AND s.site_id = price_history.site_id AND s.studio_url = price_history.studio_url)`,
		`CREATE INDEX IF NOT EXISTS idx_scene_performers_performer ON scene_performers(performer_id)`,
		`CREATE INDEX IF NOT EXISTS idx_scene_tags_tag ON scene_tags(tag_id)`,
		`CREATE INDEX IF NOT EXISTS idx_scene_categories_category ON scene_categories(category_id)`,
		`CREATE INDEX IF NOT EXISTS idx_scene_external_ids_lookup ON scene_external_ids(source, external_id)`,
		`CREATE INDEX IF NOT EXISTS idx_scene_external_ids_studio ON scene_external_ids(studio_url)`,
		`CREATE INDEX IF NOT EXISTS idx_scene_performers_studio ON scene_performers(studio_url, scene_id, site_id, position, performer_id)`,
		`CREATE INDEX IF NOT EXISTS idx_scene_tags_studio ON scene_tags(studio_url, scene_id, site_id, position, tag_id)`,
		`CREATE INDEX IF NOT EXISTS idx_scene_categories_studio ON scene_categories(studio_url, scene_id, site_id, position, category_id)`,
		`UPDATE scenes SET content_hash = ''`,
	)
}()

// migration10WinnerOnly runs before migration 9's rebuild statements and
// deletes the children of every colliding scene row that will lose, so the
// survivor keeps only its own relations rather than the union.
var migration10WinnerOnly = func() []string {
	out := []string{`CREATE TEMP TABLE win AS
	 SELECT s.id, s.site_id, m.canonical, s.studio_url AS raw
	 FROM scenes s JOIN url_map m ON m.raw = s.studio_url
	 WHERE NOT EXISTS (
	   SELECT 1 FROM scenes s2 JOIN url_map m2 ON m2.raw = s2.studio_url
	   WHERE s2.id = s.id AND s2.site_id = s.site_id AND m2.canonical = m.canonical
	     AND (s2.scraped_at > s.scraped_at
	          OR (s2.scraped_at = s.scraped_at AND s2.rowid > s.rowid)))`}
	for _, table := range []string{"scene_performers", "scene_tags", "scene_categories", "scene_external_ids"} {
		out = append(out, fmt.Sprintf(`DELETE FROM %[1]s WHERE EXISTS (
		  SELECT 1 FROM url_map m JOIN win w
		    ON w.id = %[1]s.scene_id AND w.site_id = %[1]s.site_id AND w.canonical = m.canonical
		  WHERE m.raw = %[1]s.studio_url AND w.raw <> %[1]s.studio_url)`, table))
	}
	return out
}()
