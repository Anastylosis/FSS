package store

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/output"
)

// Flat is the default store backed by a per-studio JSON file on disk.
// JSON is always written — it is the backing format for incremental updates.
// CSV is written as an additional export when included in formats.
type Flat struct {
	dir     string
	formats []string

	// held tracks the studio locks this process currently owns, keyed by lock
	// path, so Lock can be re-entrant. flock(2) is per-open-file-description:
	// a second Lock on the same path from the same process opens a new fd and
	// blocks on itself forever. See Lock.
	mu   sync.Mutex
	held map[string]*reentrantLock
}

func NewFlat(dir string, formats []string) *Flat {
	return &Flat{dir: dir, formats: formats, held: map[string]*reentrantLock{}}
}

// reentrantLock wraps a held flock with a reference count. The flock is
// released when the last handle in this process lets go. ready closes once the
// flock is acquired (or has failed, with err set), so a caller needing the same
// lock waits on it without holding Flat.mu.
type reentrantLock struct {
	f     *Flat
	path  string
	inner io.Closer
	refs  int
	ready chan struct{}
	err   error
}

// lockHandle is one holder's Closer; closing it twice releases once.
type lockHandle struct {
	r    *reentrantLock
	once sync.Once
}

func (h *lockHandle) Close() error {
	var err error
	h.once.Do(func() { err = h.r.release() })
	return err
}

func (r *reentrantLock) release() error {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	r.refs--
	if r.refs > 0 {
		return nil
	}
	if r.f.held[r.path] == r {
		delete(r.f.held, r.path)
	}
	if r.inner == nil {
		return nil
	}
	return r.inner.Close()
}

func (f *Flat) jsonPath(studioURL string) string {
	return filepath.Join(f.dir, Slugify(studioURL)+".json")
}

// legacyJSONPath is the pre-hash filename for a studio URL, used to migrate
// files written before Slugify gained its hash suffix.
func (f *Flat) legacyJSONPath(studioURL string) string {
	legacy := output.LegacySlugify(studioURL)
	if legacy == "" {
		return ""
	}
	return filepath.Join(f.dir, legacy+".json")
}

func (f *Flat) csvPath(studioURL string) string {
	return filepath.Join(f.dir, Slugify(studioURL)+".csv")
}

// Lock acquires the studio's advisory lock, and is re-entrant within this
// process: taking it twice returns a second Closer over the same underlying
// flock, released when both are closed.
//
// Re-entrancy is not a convenience. flock(2) is per-open-file-description, so a
// second Lock on the same path from the same process opens a new fd and blocks
// against the lock this process already holds — a self-deadlock with no error
// and no timeout. MarkDeleted locks internally, so any caller that held the lock
// across a Load→modify→Save cycle (which scrapeOne does) and then called it
// hung forever. The SQLite store has no such constraint — its MarkDeleted runs
// in a transaction and is safe under a held lock — so without this the two
// implementations would not honour the same Store contract.
//
// Cross-process locking is unchanged: a different process still blocks, which is
// the point of the lock.
func (f *Flat) Lock(studioURL string) (io.Closer, error) {
	return f.LockContext(context.Background(), studioURL)
}

// LockContext is Lock with a cancellable wait. Flat.mu is never held while
// blocked on another process, so releasing one studio is not stalled by a wait
// on another.
func (f *Flat) LockContext(ctx context.Context, studioURL string) (io.Closer, error) {
	studioURL = canonicalKey(studioURL)
	if err := os.MkdirAll(f.dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating output dir for lock: %w", err)
	}
	path := filepath.Join(f.dir, Slugify(studioURL)+".lock")

	for {
		f.mu.Lock()
		r, ok := f.held[path]
		if !ok {
			return f.acquire(ctx, path, studioURL)
		}
		r.refs++
		f.mu.Unlock()
		h := &lockHandle{r: r}
		select {
		case <-r.ready:
		case <-ctx.Done():
			_ = h.Close()
			return nil, ctx.Err()
		}
		if r.err == nil {
			return h, nil
		}
		// The acquirer failed (often its own cancellation); that is not this
		// caller's failure, so try again as the acquirer.
		_ = h.Close()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
}

// acquire is called with f.mu held and releases it.
func (f *Flat) acquire(ctx context.Context, path, label string) (io.Closer, error) {
	r := &reentrantLock{f: f, path: path, refs: 1, ready: make(chan struct{})}
	f.held[path] = r
	f.mu.Unlock()

	inner, err := lockFileCtx(ctx, path, label)

	f.mu.Lock()
	if err != nil {
		r.err = err
		if f.held[path] == r {
			delete(f.held, path)
		}
	} else {
		r.inner = inner
	}
	f.mu.Unlock()
	close(r.ready)
	if err != nil {
		return nil, err
	}
	return &lockHandle{r: r}, nil
}

func (f *Flat) Load(studioURL string) ([]models.Scene, error) {
	studioURL = canonicalKey(studioURL)
	sf, err := f.loadStudioFile(studioURL)
	if err != nil || sf == nil {
		return nil, err
	}
	return sf.Scenes, nil
}

// loadStudioFile reads the slug-keyed JSON file, parses it, and verifies the
// stored StudioURL matches the requested one. Two distinct studio URLs can
// slug to the same filename (e.g. "/foo-bar" and "/foo/bar"), and silently
// overwriting one with the other was a documented data-loss bug. Returns
// (nil, nil) when no file exists yet.
func (f *Flat) loadStudioFile(studioURL string) (*models.StudioFile, error) {
	path := f.jsonPath(studioURL)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		// Migrate a pre-hash (legacy) file to the new hashed name, if one
		// exists and belongs to this studio, so existing incremental state
		// (price history, soft-deletes) survives the Slugify change.
		migrated, mErr := f.migrateLegacy(studioURL, path)
		if mErr != nil {
			return nil, mErr
		}
		if !migrated {
			// The studio may be stored under a non-canonical spelling of its
			// URL, whose slug is a different hash entirely. Slugify cannot be
			// reversed, so the only way to find it is to look at what each file
			// says it holds.
			if migrated, mErr = f.migrateURLVariant(studioURL, path); mErr != nil {
				return nil, mErr
			}
		}
		if !migrated {
			return nil, nil
		}
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, fmt.Errorf("reading store: %w", err)
	}
	var sf models.StudioFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return nil, fmt.Errorf("parsing store: %w", err)
	}
	// Refuse a file from a newer FSS: Save is authoritative, so loading a
	// partially-understood file would rewrite away whatever the newer layout added.
	if sf.SchemaVersion > models.StoreSchemaVersion {
		return nil, fmt.Errorf(
			"%s was written with store schema v%d but this build understands up to v%d — upgrade fss",
			path, sf.SchemaVersion, models.StoreSchemaVersion,
		)
	}
	// studioURL is already canonical here; compare like with like, or a file
	// written under a variant spelling of the same URL looks like a collision.
	if sf.StudioURL != "" && canonicalKey(sf.StudioURL) != studioURL {
		return nil, fmt.Errorf(
			"slug collision: %s stores data for %q but %q was requested — rename or move one of the studio files",
			path, sf.StudioURL, studioURL,
		)
	}
	return &sf, nil
}

// migrateLegacy renames a pre-hash studio file to the new hashed name when one
// exists and belongs to studioURL. Returns whether a migration happened. A
// legacy file recording a different StudioURL is left untouched (the studios
// no longer collide once both are hashed).
func (f *Flat) migrateLegacy(studioURL, newPath string) (bool, error) {
	legacy := f.legacyJSONPath(studioURL)
	if legacy == "" || legacy == newPath {
		return false, nil
	}
	data, err := os.ReadFile(legacy)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reading legacy store: %w", err)
	}
	var sf models.StudioFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return false, fmt.Errorf("parsing legacy store: %w", err)
	}
	if sf.StudioURL != "" && canonicalKey(sf.StudioURL) != studioURL {
		return false, nil
	}
	if err := os.Rename(legacy, newPath); err != nil {
		return false, fmt.Errorf("migrating legacy store %s: %w", legacy, err)
	}
	return true, nil
}

// migrateURLVariant renames a studio file stored under a non-canonical spelling
// of its URL onto the canonical slug, so `http://x.com`, `https://x.com` and
// `https://x.com/` resolve to one file instead of three.
//
// Slugify hashes the raw URL and is not reversible, so the variant cannot be
// computed — every studio file in the directory is read and asked which studio
// it holds. That only happens when the canonical file is absent, which is once
// per studio at most.
//
// When several variants exist the newest by ScrapedAt wins and the others are
// left in place rather than deleted; merging their scenes is `fss import`'s job,
// and silently discarding a file would be worse than leaving it.
func (f *Flat) migrateURLVariant(studioURL, newPath string) (bool, error) {
	entries, err := os.ReadDir(f.dir)
	if err != nil {
		return false, nil // no directory yet: nothing to migrate
	}

	var bestPath string
	var bestAt time.Time
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		candidate := filepath.Join(f.dir, e.Name())
		if candidate == newPath {
			continue
		}
		data, rErr := os.ReadFile(candidate)
		if rErr != nil {
			continue
		}
		var sf models.StudioFile
		if json.Unmarshal(data, &sf) != nil || sf.StudioURL == "" {
			continue
		}
		if canonicalKey(sf.StudioURL) != studioURL {
			continue
		}
		if bestPath == "" || sf.ScrapedAt.After(bestAt) {
			bestPath, bestAt = candidate, sf.ScrapedAt
		}
	}
	if bestPath == "" {
		return false, nil
	}
	if err := os.Rename(bestPath, newPath); err != nil {
		return false, fmt.Errorf("migrating studio file %s: %w", bestPath, err)
	}
	return true, nil
}

func (f *Flat) Save(studioURL string, scenes []models.Scene) error {
	studioURL = canonicalKey(studioURL)
	if err := validateScenes(scenes); err != nil {
		return err
	}
	if err := os.MkdirAll(f.dir, 0o755); err != nil {
		return fmt.Errorf("creating output dir: %w", err)
	}
	// Refuse to clobber a file that belongs to a different studio URL.
	prev, err := f.loadStudioFile(studioURL)
	if err != nil {
		return err
	}
	scenes = withFirstSeen(withCanonicalStudioURL(scenes, studioURL), prev)

	sf := models.StudioFile{
		SchemaVersion: models.StoreSchemaVersion,
		StudioURL:     studioURL,
		ScrapedAt:     time.Now().UTC(),
		SceneCount:    len(scenes),
		Scenes:        scenes,
	}
	if err := WriteJSON(sf, f.jsonPath(studioURL)); err != nil {
		return err
	}
	for _, format := range f.formats {
		if format == "csv" {
			if err := WriteCSV(scenes, f.csvPath(studioURL)); err != nil {
				return err
			}
		}
	}
	return nil
}

// withFirstSeen returns a copy of scenes with FirstSeenAt resolved against what
// the studio file already held. The copy keeps Save from mutating the caller's
// slice.
func withFirstSeen(scenes []models.Scene, prev *models.StudioFile) []models.Scene {
	var stored map[sceneKey]models.Scene
	if prev != nil {
		stored = make(map[sceneKey]models.Scene, len(prev.Scenes))
		for _, s := range prev.Scenes {
			stored[sceneKey{id: s.ID, siteID: s.SiteID}] = s
		}
	}

	out := make([]models.Scene, len(scenes))
	copy(out, scenes)
	for i := range out {
		var p *models.Scene
		if s, ok := stored[sceneKey{id: out[i].ID, siteID: out[i].SiteID}]; ok {
			p = &s
		}
		out[i].FirstSeenAt = firstSeenFor(out[i], p)
	}
	return out
}

func (f *Flat) MarkDeleted(studioURL, siteID string, ids []string) error {
	studioURL = canonicalKey(studioURL)
	unlock, err := f.Lock(studioURL)
	if err != nil {
		return fmt.Errorf("locking studio for MarkDeleted: %w", err)
	}
	defer func() { _ = unlock.Close() }()

	scenes, err := f.Load(studioURL)
	if err != nil {
		return err
	}
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	now := time.Now().UTC()
	for i := range scenes {
		// Match the SQLite store: a scene is soft-deleted only when both
		// its ID is in the set and its SiteID matches. Studio files that
		// mix scenes from multiple sites (e.g. cross-site stash merges)
		// can hold overlapping IDs across SiteIDs; without the SiteID
		// filter those collateral scenes would be wiped too.
		if set[scenes[i].ID] && scenes[i].SiteID == siteID && scenes[i].DeletedAt == nil {
			scenes[i].DeletedAt = &now
		}
	}
	return f.Save(studioURL, scenes)
}

// Export is a no-op for the flat store — files are written directly by Save.
func (f *Flat) Export(_, _, _ string) error { return nil }

// UpsertStudio is a no-op for the flat store — studio tracking requires SQLite.
func (f *Flat) UpsertStudio(_ models.Studio) error { return nil }

// ListStudios is a no-op for the flat store.
func (f *Flat) ListStudios() ([]models.Studio, error) { return nil, nil }

func Slugify(rawURL string) string {
	return output.Slugify(rawURL)
}
