package output

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
)

const testURL = "https://www.manyvids.com/Profile/1000001/jane-eyre/Store/Videos"

func TestWriteJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.json")
	now := time.Now().UTC().Truncate(time.Second)

	sf := models.StudioFile{
		StudioURL:  testURL,
		ScrapedAt:  now,
		SceneCount: 1,
		Scenes: []models.Scene{{
			ID: "1", SiteID: "test", Title: "Test Scene",
			URL: "https://example.com/1", ScrapedAt: now,
		}},
	}

	if err := WriteJSON(sf, path); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got models.StudioFile
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if got.StudioURL != testURL {
		t.Errorf("studioUrl = %q", got.StudioURL)
	}
	if got.SceneCount != 1 {
		t.Errorf("sceneCount = %d", got.SceneCount)
	}
	if got.Scenes[0].Title != "Test Scene" {
		t.Errorf("title = %q", got.Scenes[0].Title)
	}
}

func TestWriteJSONSpecialChars(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "special.json")
	now := time.Now().UTC().Truncate(time.Second)

	sf := models.StudioFile{
		StudioURL:  testURL,
		ScrapedAt:  now,
		SceneCount: 1,
		Scenes: []models.Scene{{
			ID: "1", SiteID: "test", ScrapedAt: now,
			Title:       `Title with "quotes" & <brackets>`,
			Description: "Line 1\nLine 2\tTabbed",
			Performers:  []string{"Performer O'Neil"},
		}},
	}

	if err := WriteJSON(sf, path); err != nil {
		t.Fatal(err)
	}

	var got models.StudioFile
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("invalid JSON after special chars: %v", err)
	}
	if got.Scenes[0].Title != `Title with "quotes" & <brackets>` {
		t.Errorf("title = %q", got.Scenes[0].Title)
	}
}

func TestWriteJSONAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "atomic.json")
	now := time.Now().UTC().Truncate(time.Second)

	sf := models.StudioFile{
		StudioURL: testURL, ScrapedAt: now, SceneCount: 1,
		Scenes: []models.Scene{{
			ID: "1", SiteID: "test", Title: "First", ScrapedAt: now,
		}},
	}
	if err := WriteJSON(sf, path); err != nil {
		t.Fatal(err)
	}

	sf.Scenes[0].Title = "Second"
	if err := WriteJSON(sf, path); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	var got models.StudioFile
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Scenes[0].Title != "Second" {
		t.Errorf("atomic overwrite failed, got %q", got.Scenes[0].Title)
	}

	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".fss-tmp-") {
			t.Errorf("temp file not cleaned up: %s", e.Name())
		}
	}
}

func TestWriteCSV(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.csv")
	now := time.Now().UTC().Truncate(time.Second)

	scenes := []models.Scene{
		{
			ID:         "1",
			SiteID:     "test",
			StudioURL:  testURL,
			Title:      "Test Scene",
			URL:        "https://example.com/1",
			Date:       now,
			Performers: []string{"Alice", "Bob"},
			Tags:       []string{"tag1", "tag2"},
			Duration:   3600,
			ScrapedAt:  now,
		},
	}

	if err := WriteCSV(scenes, path); err != nil {
		t.Fatalf("WriteCSV: %v", err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	r := csv.NewReader(f)
	records, err := r.ReadAll()
	if err != nil {
		t.Fatalf("CSV parse: %v", err)
	}

	if len(records) != 2 {
		t.Fatalf("got %d rows (including header), want 2", len(records))
	}

	header := records[0]
	if header[0] != "id" {
		t.Errorf("header[0] = %q, want 'id'", header[0])
	}
	if len(header) != len(CSVHeaders) {
		t.Errorf("got %d columns, want %d", len(header), len(CSVHeaders))
	}

	row := records[1]
	if row[0] != "1" {
		t.Errorf("id = %q", row[0])
	}
	if row[3] != "Test Scene" {
		t.Errorf("title = %q", row[3])
	}

	perfIdx := indexOf(header, "performers")
	if perfIdx < 0 {
		t.Fatal("performers column not found")
	}
	if row[perfIdx] != "Alice|Bob" {
		t.Errorf("performers = %q, want 'Alice|Bob'", row[perfIdx])
	}

	tagIdx := indexOf(header, "tags")
	if tagIdx < 0 {
		t.Fatal("tags column not found")
	}
	if row[tagIdx] != "tag1|tag2" {
		t.Errorf("tags = %q", row[tagIdx])
	}

	durIdx := indexOf(header, "duration")
	if durIdx < 0 {
		t.Fatal("duration column not found")
	}
	if row[durIdx] != "3600" {
		t.Errorf("duration = %q", row[durIdx])
	}
}

func TestWriteCSVEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.csv")

	if err := WriteCSV(nil, path); err != nil {
		t.Fatal(err)
	}

	f, _ := os.Open(path)
	defer func() { _ = f.Close() }()
	r := csv.NewReader(f)
	records, _ := r.ReadAll()
	if len(records) != 1 {
		t.Errorf("expected header only, got %d rows", len(records))
	}
}

func TestWriteCSVSpecialChars(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "special.csv")
	now := time.Now().UTC().Truncate(time.Second)

	scenes := []models.Scene{{
		ID: "1", SiteID: "test", StudioURL: testURL,
		Title:     `Scene with "quotes", commas, and newlines` + "\n" + "second line",
		ScrapedAt: now,
	}}

	if err := WriteCSV(scenes, path); err != nil {
		t.Fatal(err)
	}

	f, _ := os.Open(path)
	defer func() { _ = f.Close() }()
	r := csv.NewReader(f)
	records, err := r.ReadAll()
	if err != nil {
		t.Fatalf("CSV should handle special chars: %v", err)
	}
	if records[1][3] != scenes[0].Title {
		t.Errorf("title roundtrip failed: got %q", records[1][3])
	}
}

func TestFormatTime(t *testing.T) {
	if got := formatTime(time.Time{}); got != "" {
		t.Errorf("zero time = %q, want empty", got)
	}
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	if got := formatTime(now); got != "2026-05-06T12:00:00Z" {
		t.Errorf("formatTime = %q", got)
	}
}

func TestFormatTimePtr(t *testing.T) {
	if got := formatTimePtr(nil); got != "" {
		t.Errorf("nil = %q, want empty", got)
	}
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	if got := formatTimePtr(&now); got != "2026-05-06T12:00:00Z" {
		t.Errorf("formatTimePtr = %q", got)
	}
}

func TestSceneToRow(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	scene := models.Scene{
		ID: "42", SiteID: "test", StudioURL: testURL,
		Title: "Test", URL: "https://example.com/42",
		Performers: []string{"A", "B"}, Tags: []string{"t1"},
		Duration: 120, LowestPrice: 9.99,
		ScrapedAt: now,
	}
	row, err := sceneToRow(scene)
	if err != nil {
		t.Fatal(err)
	}
	if len(row) != len(CSVHeaders) {
		t.Fatalf("row has %d fields, want %d", len(row), len(CSVHeaders))
	}
	if row[0] != "42" {
		t.Errorf("id = %q", row[0])
	}
}

var hashSuffixRe = regexp.MustCompile(`^[0-9a-f]{8}$`)

// assertSlug checks Slugify(input) == wantBase + "-" + <8 hex> (or just the
// hash when wantBase is empty).
func assertSlug(t *testing.T, input, wantBase string) {
	t.Helper()
	got := Slugify(input)
	if wantBase == "" {
		if !hashSuffixRe.MatchString(got) {
			t.Errorf("Slugify(%q) = %q, want bare 8-hex hash", input, got)
		}
		return
	}
	prefix := wantBase + "-"
	if !strings.HasPrefix(got, prefix) {
		t.Errorf("Slugify(%q) = %q, want prefix %q", input, got, prefix)
		return
	}
	if suf := got[len(prefix):]; !hashSuffixRe.MatchString(suf) {
		t.Errorf("Slugify(%q) = %q, suffix %q is not an 8-hex hash", input, got, suf)
	}
}

func TestSlugify(t *testing.T) {
	assertSlug(t, testURL, "www-manyvids-com-profile-1000001-jane-eyre-store-videos")
	assertSlug(t, "https://www.clips4sale.com/studio/12345/some-store", "www-clips4sale-com-studio-12345-some-store")
}

func TestSlugifySpecialChars(t *testing.T) {
	assertSlug(t, "https://example.com/path/with spaces/", "example-com-path-with-spaces")
	assertSlug(t, "https://example.com/UPPER/Case", "example-com-upper-case")
	assertSlug(t, "https://example.com/path///multiple///slashes", "example-com-path-multiple-slashes")
	assertSlug(t, "https://example.com/special!@#$%chars", "https-example-com-special-chars")
}

func TestSlugifyEmpty(t *testing.T) {
	// Empty input has an empty base, so the slug is the bare hash — non-empty
	// and stable.
	assertSlug(t, "", "")
}

// TestSlugifyNoCollision is the A4 regression: distinct URLs whose sanitized
// base is identical must produce different slugs.
func TestSlugifyNoCollision(t *testing.T) {
	a := Slugify("https://example.com/anna_b")
	b := Slugify("https://example.com/anna-b")
	if a == b {
		t.Errorf("Slugify collision: anna_b and anna-b both → %q", a)
	}
	// Non-ASCII paths sanitize to an empty base but must stay distinct.
	c := Slugify("https://example.com/日本")
	d := Slugify("https://example.com/한국")
	if c == d || c == "" || d == "" {
		t.Errorf("non-ASCII slugs collided or empty: %q vs %q", c, d)
	}
}

func TestSlugifyPathTraversal(t *testing.T) {
	got := Slugify("https://evil.com/../../etc/passwd")
	if strings.Contains(got, "..") || strings.Contains(got, "/") {
		t.Errorf("Slugify path traversal = %q, want no dots/slashes", got)
	}
}

func indexOf(header []string, name string) int {
	for i, h := range header {
		if h == name {
			return i
		}
	}
	return -1
}

// --- SweepStaleTempFiles ---

func TestSweepStaleTempFiles(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	mustWrite := func(name string, mtime time.Time) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
		return path
	}

	stale := mustWrite(".fss-tmp-old1", now.Add(-1*time.Hour))
	stale2 := mustWrite(".fss-tmp-old2", now.Add(-20*time.Minute))
	fresh := mustWrite(".fss-tmp-fresh", now.Add(-1*time.Minute))
	unrelated := mustWrite("studio.json", now.Add(-1*time.Hour))

	removed := SweepStaleTempFiles(dir, 10*time.Minute)
	if removed != 2 {
		t.Errorf("removed = %d, want 2", removed)
	}
	for _, p := range []string{stale, stale2} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("expected %s removed, stat err=%v", p, err)
		}
	}
	for _, p := range []string{fresh, unrelated} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s kept, stat err=%v", p, err)
		}
	}
}

func TestSweepStaleTempFiles_missingDir(t *testing.T) {
	// Non-existent directory must not be an error.
	if n := SweepStaleTempFiles(filepath.Join(t.TempDir(), "nope"), time.Minute); n != 0 {
		t.Errorf("missing dir should yield 0 removals, got %d", n)
	}
}

func TestSweepStaleTempFiles_emptyDir(t *testing.T) {
	if n := SweepStaleTempFiles(t.TempDir(), time.Minute); n != 0 {
		t.Errorf("empty dir should yield 0 removals, got %d", n)
	}
}

func TestSweepStaleTempFiles_skipsSubdir(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, ".fss-tmp-subdir")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(sub, old, old); err != nil {
		t.Fatal(err)
	}
	if n := SweepStaleTempFiles(dir, 10*time.Minute); n != 0 {
		t.Errorf("directories must not count, got %d removed", n)
	}
	// Subdirectory must still exist.
	if _, err := os.Stat(sub); err != nil {
		t.Errorf("subdir was wrongly removed: %v", err)
	}
}

func TestAtomicWriteFileCleanupOnWriteError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fail.json")

	wantErr := "write boom"
	err := atomicWriteFile(path, func(w io.Writer) error {
		return fmt.Errorf("%s", wantErr)
	})
	if err == nil || err.Error() != wantErr {
		t.Fatalf("expected error %q, got %v", wantErr, err)
	}

	// Temp file should be cleaned up.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".fss-tmp-") {
			t.Errorf("temp file not cleaned up after write error: %s", e.Name())
		}
	}

	// Target file should not exist.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("target file should not exist after write error")
	}
}

// The age guard is the only thing separating cleanup from deleting a temp file
// an in-flight write still owns, so a caller cannot opt out of it.
func TestSweepStaleTempFilesClampsMaxAge(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	write := func(name string, age time.Duration) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		mt := now.Add(-age)
		if err := os.Chtimes(path, mt, mt); err != nil {
			t.Fatal(err)
		}
		return path
	}

	inFlight := write(".fss-tmp-inflight", 2*time.Minute)
	stale := write(".fss-tmp-stale", time.Hour)

	// Zero would mean "everything is stale" without the floor.
	if removed := SweepStaleTempFiles(dir, 0); removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
	if _, err := os.Stat(inFlight); err != nil {
		t.Errorf("a 2-minute-old temp file was swept: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("the hour-old temp file survived: %v", err)
	}
}
