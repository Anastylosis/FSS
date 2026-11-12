package store

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/output"
)

// validateScenes rejects scenes that would be unaddressable downstream:
// the composite key `(id, site_id)` is used as a primary key in SQLite
// and as a map key in both stores for relation lookups, so an empty
// component would either fail at insert time, collide with other
// empty-keyed scenes, or silently lose its performers/tags/price
// history on Load. Catch it once at the store boundary so neither
// implementation has to.
func validateScenes(scenes []models.Scene) error {
	for i, sc := range scenes {
		if sc.ID == "" {
			return fmt.Errorf("scene[%d]: ID is required (siteID=%q, title=%q)", i, sc.SiteID, sc.Title)
		}
		if sc.SiteID == "" {
			return fmt.Errorf("scene[%d]: SiteID is required (id=%q, title=%q)", i, sc.ID, sc.Title)
		}
	}
	return nil
}

// firstSeenFor returns the FirstSeenAt to persist for a scene, given what the
// store already holds for the same key (nil when the scene is new).
//
// FirstSeenAt is the one field Save does not take verbatim: it records when a
// scene entered the catalogue, which a later scrape cannot re-derive. An
// existing non-zero value always wins.
func firstSeenFor(fresh models.Scene, prev *models.Scene) time.Time {
	if prev != nil {
		if !prev.FirstSeenAt.IsZero() {
			return prev.FirstSeenAt
		}
		// Stored before FirstSeenAt existed: its last scrape is the best
		// available upper bound.
		if !prev.ScrapedAt.IsZero() {
			return prev.ScrapedAt
		}
	}
	if !fresh.FirstSeenAt.IsZero() {
		return fresh.FirstSeenAt
	}
	if !fresh.ScrapedAt.IsZero() {
		return fresh.ScrapedAt
	}
	return time.Now().UTC()
}

// canonicalKey is the identity a studio is stored under. Callers pass whatever
// URL the operator typed; the store keys on the canonical form so that
// `http://x.com`, `https://x.com` and `https://x.com/` are one studio rather
// than three.
//
// Only the *key* is canonicalised. The URL handed to a scraper is never
// rewritten — see output.CanonicalStudioURL.
func canonicalKey(studioURL string) string {
	return output.CanonicalStudioURL(studioURL)
}

// withCanonicalStudioURL returns a copy of scenes whose StudioURL matches the
// key they are being stored under. Save canonicalises its parameter, and the
// scene rows carry their own copy of the URL — the child tables key on it — so
// the two must agree or Load finds nothing. Copying avoids mutating the
// caller's slice.
func withCanonicalStudioURL(scenes []models.Scene, studioURL string) []models.Scene {
	out := make([]models.Scene, len(scenes))
	copy(out, scenes)
	for i := range out {
		out[i].StudioURL = studioURL
	}
	return out
}

// Store is the persistence layer. The default implementation uses flat JSON/CSV files.
// An optional SQLite-backed implementation is selected with the --db flag.
type Store interface {
	// Lock acquires a process-level advisory lock for the given studio URL,
	// preventing concurrent scrapes of the same URL from racing on
	// Load+Save. The caller must defer Close on the returned value to
	// release the lock. On Unix the lock uses flock(2) on a sidecar
	// `.lock` file; on Windows it is a no-op.
	Lock(studioURL string) (io.Closer, error)

	// LockContext is Lock with a wait that ctx can cancel. A wait of a second
	// or more prints a notice naming the studio.
	LockContext(ctx context.Context, studioURL string) (io.Closer, error)

	// Load returns all scenes previously scraped for this studio URL.
	Load(studioURL string) ([]models.Scene, error)

	// Save persists the full scene list for a studio URL, replacing any
	// prior data: scenes whose (id, site_id) is absent from `scenes` are
	// hard-deleted from the store (including their join rows and
	// price_history). The cmd layer's `--full` path depends on this — it
	// passes only freshly-scraped scenes and expects everything else to
	// disappear. Incremental and `--refresh` modes pass the merged
	// existing+fresh set so nothing is dropped. Use MarkDeleted for
	// soft-delete semantics that preserve historical data.
	//
	// `FirstSeenAt` is the one exception to verbatim writing: a value
	// already stored for the same key is preserved, and a scene with no
	// value gets one stamped from its ScrapedAt. See firstSeenFor.
	//
	// Soft-delete state is NOT sticky across Save: each scene's
	// `DeletedAt` is written verbatim. A re-emitted scene with
	// `DeletedAt == nil` therefore auto-revives a prior soft-delete —
	// "the site brought the scene back, so the store shouldn't lie
	// about it being gone". `--refresh` mode is what stamps `DeletedAt`
	// on scenes the scraper no longer sees; callers wanting to preserve
	// an existing soft-delete must include the existing scene (with its
	// `DeletedAt` intact) in the `scenes` slice.
	Save(studioURL string, scenes []models.Scene) error

	// MarkDeleted soft-deletes scenes by ID — sets DeletedAt, does not remove records.
	MarkDeleted(studioURL, siteID string, ids []string) error

	// Export writes scenes for a studio URL to a file in the given format ("json" or "csv").
	// Used when SQLite is the source of truth and flat files are requested as output.
	Export(format, path, studioURL string) error

	// UpsertStudio records or updates a studio entry. No-op for the flat store.
	UpsertStudio(studio models.Studio) error

	// ListStudios returns all known studios. Empty for the flat store.
	ListStudios() ([]models.Studio, error)
}
