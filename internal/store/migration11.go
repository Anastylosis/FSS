package store

import "fmt"

// migration11 adds price_history.currency. Existing rows read back as "", which
// the models treat as "the scraper did not say". See docs/storage.md.
const migration11 = `ALTER TABLE price_history ADD COLUMN currency TEXT NOT NULL DEFAULT ''`

func (s *SQLite) applyMigration11() error {
	var n int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('price_history') WHERE name = 'currency'`,
	).Scan(&n); err != nil {
		return fmt.Errorf("inspecting price_history: %w", err)
	}
	if n > 0 {
		return s.applyMigration("", 11)
	}
	return s.applyMigration(migration11, 11)
}
