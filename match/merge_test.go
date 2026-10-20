package match

import (
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
)

func TestMergeScenesBasic(t *testing.T) {
	s1 := models.Scene{
		ID:          "1",
		SiteID:      "manyvids",
		Title:       "Fostering the Bully",
		URL:         "https://manyvids.com/Video/123/fostering-the-bully",
		Date:        time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
		Description: "Short desc",
		Performers:  []string{"Jane Eyre"},
		Tags:        []string{"JOI", "POV"},
		Studio:      "Jane Eyre",
		Duration:    600,
		Width:       1920,
		Height:      1080,
	}
	s2 := models.Scene{
		ID:          "456",
		SiteID:      "clips4sale",
		Title:       "Fostering the Bully",
		URL:         "https://clips4sale.com/studio/789/fostering-the-bully",
		Date:        time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
		Description: "A much longer and more detailed description of the scene",
		Performers:  []string{"Jane Eyre"},
		Tags:        []string{"JOI", "Taboo"},
		Studio:      "Jane Eyre",
		Duration:    610,
		Width:       3840,
		Height:      2160,
	}

	m := MergeScenes([]models.Scene{s1, s2}, time.Time{})

	if m.Title != "Fostering the Bully" {
		t.Errorf("Title = %q", m.Title)
	}

	// Earliest date wins.
	wantDate := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	if !m.Date.Equal(wantDate) {
		t.Errorf("Date = %v, want %v", m.Date, wantDate)
	}

	// Longest description wins.
	if m.Description != s2.Description {
		t.Errorf("Description = %q, want s2's longer one", m.Description)
	}

	// URLs are union.
	if len(m.URLs) != 2 {
		t.Errorf("URLs len = %d, want 2", len(m.URLs))
	}

	// Tags are union.
	wantTags := map[string]bool{"JOI": true, "POV": true, "Taboo": true}
	if len(m.Tags) != len(wantTags) {
		t.Errorf("Tags = %v, want %d unique", m.Tags, len(wantTags))
	}
	for _, tag := range m.Tags {
		if !wantTags[tag] {
			t.Errorf("unexpected tag %q", tag)
		}
	}

	// Performers deduplicated.
	if len(m.Performers) != 1 || m.Performers[0] != "Jane Eyre" {
		t.Errorf("Performers = %v", m.Performers)
	}

	// Max duration.
	if m.Duration != 610 {
		t.Errorf("Duration = %d, want 610", m.Duration)
	}

	// Highest resolution.
	if m.Width != 3840 {
		t.Errorf("Width = %d, want 3840", m.Width)
	}

	// Sites tracked.
	if len(m.Sites) != 2 {
		t.Errorf("Sites = %v, want 2 entries", m.Sites)
	}
}

func TestMergeScenesExistingDateEarlier(t *testing.T) {
	s := models.Scene{
		Title: "Test",
		Date:  time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
	}
	existingDate := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	m := MergeScenes([]models.Scene{s}, existingDate)
	if !m.Date.Equal(existingDate) {
		t.Errorf("Date = %v, want existing date %v", m.Date, existingDate)
	}
}

func TestMergeScenesExistingDateLater(t *testing.T) {
	s := models.Scene{
		Title: "Test",
		Date:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	existingDate := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	m := MergeScenes([]models.Scene{s}, existingDate)
	if !m.Date.Equal(s.Date) {
		t.Errorf("Date = %v, want FSS date %v", m.Date, s.Date)
	}
}

func TestMergeScenesSingle(t *testing.T) {
	s := models.Scene{
		ID:     "1",
		SiteID: "manyvids",
		Title:  "Solo Scene",
		URL:    "https://example.com/1",
		Tags:   []string{"A", "B"},
	}

	m := MergeScenes([]models.Scene{s}, time.Time{})
	if m.Title != "Solo Scene" {
		t.Errorf("Title = %q", m.Title)
	}
	if len(m.URLs) != 1 {
		t.Errorf("URLs len = %d", len(m.URLs))
	}
	if len(m.Tags) != 2 {
		t.Errorf("Tags len = %d", len(m.Tags))
	}
}

func TestMergeScenesEmpty(t *testing.T) {
	m := MergeScenes(nil, time.Time{})
	if m.Title != "" {
		t.Errorf("Title = %q, want empty", m.Title)
	}
	if !m.Date.IsZero() {
		t.Errorf("Date = %v, want zero", m.Date)
	}
	if len(m.URLs) != 0 {
		t.Errorf("URLs len = %d, want 0", len(m.URLs))
	}
	if len(m.Tags) != 0 {
		t.Errorf("Tags len = %d, want 0", len(m.Tags))
	}
	if len(m.Performers) != 0 {
		t.Errorf("Performers len = %d, want 0", len(m.Performers))
	}
	if len(m.Sites) != 0 {
		t.Errorf("Sites len = %d, want 0", len(m.Sites))
	}
}

func TestMergeScenesEmptyWithExistingDate(t *testing.T) {
	existing := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	m := MergeScenes(nil, existing)
	if !m.Date.Equal(existing) {
		t.Errorf("Date = %v, want %v", m.Date, existing)
	}
}

func TestCleanDescription(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"triple spaces become newline", "word   word", "word\nword"},
		{"triple blank lines collapse", "a\n\n\nb", "a\n\nb"},
		{"many blank lines collapse", "a\n\n\n\n\nb", "a\n\nb"},
		{"leading/trailing whitespace stripped", "  hello  ", "hello"},
		{"tabs count as space runs", "word\t\t\tword", "word\nword"},
		{"two spaces not enough", "word  word", "word  word"},
		{"normal text unchanged", "A short description.", "A short description."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := cleanDescription(c.input)
			if got != c.want {
				t.Errorf("cleanDescription(%q) = %q, want %q", c.input, got, c.want)
			}
		})
	}
}

func TestMergeScenesResolutionTie(t *testing.T) {
	s1 := models.Scene{
		ID:     "1",
		SiteID: "siteA",
		Title:  "Scene A",
		Width:  1920,
		Height: 1080,
	}
	s2 := models.Scene{
		ID:     "2",
		SiteID: "siteB",
		Title:  "Scene B",
		Width:  1920,
		Height: 800,
	}

	m := MergeScenes([]models.Scene{s1, s2}, time.Time{})

	// When widths tie, the first scene's values should be kept (> comparison, not >=).
	if m.Width != 1920 {
		t.Errorf("Width = %d, want 1920", m.Width)
	}
	if m.Height != 1080 {
		t.Errorf("Height = %d, want 1080 (first scene wins on tie)", m.Height)
	}
}

func TestResolutionTags(t *testing.T) {
	cases := []struct {
		width int
		want  string
	}{
		{3840, "4K Available"},
		{1920, "Full HD Available"},
		{1280, "HD Available"},
		{720, ""},
		{0, ""},
	}
	for _, c := range cases {
		tags := ResolutionTags(c.width)
		if c.want == "" {
			if len(tags) != 0 {
				t.Errorf("ResolutionTags(%d) = %v, want none", c.width, tags)
			}
		} else {
			if len(tags) != 1 || tags[0] != c.want {
				t.Errorf("ResolutionTags(%d) = %v, want [%q]", c.width, tags, c.want)
			}
		}
	}
}

func TestMergeStrings(t *testing.T) {
	cases := []struct {
		name             string
		existing, update []string
		want             []string
	}{
		{"both empty", nil, nil, []string{}},
		{"existing only", []string{"a", "b"}, nil, []string{"a", "b"}},
		{"new only", nil, []string{"a", "b"}, []string{"a", "b"}},
		{"disjoint appends in order", []string{"a"}, []string{"b", "c"}, []string{"a", "b", "c"}},
		{"dedups overlap, keeps existing order", []string{"a", "b"}, []string{"b", "a", "c"}, []string{"a", "b", "c"}},
		{"preserves duplicates already in existing", []string{"a", "a"}, []string{"a"}, []string{"a", "a"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := MergeStrings(c.existing, c.update)
			if len(got) != len(c.want) {
				t.Fatalf("MergeStrings(%v, %v) = %v, want %v", c.existing, c.update, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("MergeStrings(%v, %v) = %v, want %v", c.existing, c.update, got, c.want)
				}
			}
		})
	}
}

func TestMergeStrings_doesNotMutateInputs(t *testing.T) {
	existing := []string{"a", "b"}
	update := []string{"c"}
	_ = MergeStrings(existing, update)
	if len(existing) != 2 || existing[0] != "a" || existing[1] != "b" {
		t.Errorf("existing slice was mutated: %v", existing)
	}
	if len(update) != 1 || update[0] != "c" {
		t.Errorf("update slice was mutated: %v", update)
	}
}

// Aylo's API serves performer names with stray whitespace ("Irene Adler "), and
// every consumer of these strings compares them exactly. The cross-site case is
// the one that matters most: dedup is by exact string, so without trimming the
// same performer arrives twice from two sites and `fss stash import` then creates
// a duplicate performer in Stash rather than matching the existing one.
func TestMergeScenesTrimsNamesAndDedupesAcrossSites(t *testing.T) {
	scenes := []models.Scene{
		{
			ID: "1", SiteID: "babes", Title: "T", URL: "https://babes.com/1",
			Performers: []string{"Irene Adler ", "Mina Harker"},
			Tags:       []string{" Blowjob", "Big Tits "},
			Categories: []string{" Couples "},
			Studio:     " Babes ",
		},
		{
			ID: "1", SiteID: "other", Title: "T", URL: "https://other.com/1",
			Performers: []string{"Irene Adler", "  "},
			Tags:       []string{"Blowjob", ""},
			Categories: []string{"Couples"},
		},
	}

	m := MergeScenes(scenes, time.Time{})

	if len(m.Performers) != 2 {
		t.Errorf("Performers = %q, want 2 — the untrimmed and trimmed forms of the "+
			"same name must collapse, and a whitespace-only entry must be dropped", m.Performers)
	}
	for _, p := range m.Performers {
		if p != strings.TrimSpace(p) {
			t.Errorf("performer %q still carries surrounding whitespace", p)
		}
		if p == "" {
			t.Error("empty performer name retained")
		}
	}
	if len(m.Tags) != 2 {
		t.Errorf("Tags = %q, want 2 (Blowjob, Big Tits)", m.Tags)
	}
	for _, tag := range m.Tags {
		if tag != strings.TrimSpace(tag) || tag == "" {
			t.Errorf("tag %q is empty or untrimmed", tag)
		}
	}
	if len(m.Categories) != 1 || m.Categories[0] != "Couples" {
		t.Errorf("Categories = %q, want [Couples]", m.Categories)
	}
	if m.Studio != "Babes" {
		t.Errorf("Studio = %q, want %q", m.Studio, "Babes")
	}
}

// Dedup is by canonical key (case-folded, whitespace-collapsed) while the stored
// value keeps the first site's spelling.
//
// This reverses an earlier decision to keep both casings. Keeping both did not
// help: `fss stash import` looks entities up by exact name, so two spellings
// create two Stash performers for one person. Emitting one — the first
// contributing site's — is strictly better, and the value written is still a
// real site spelling rather than a folded one.
func TestMergeScenesFoldsCasingButKeepsSpelling(t *testing.T) {
	scenes := []models.Scene{
		{ID: "1", SiteID: "a", URL: "https://a/1",
			Performers: []string{"Irene Adler"}, Tags: []string{"Big Tits"}},
		{ID: "1", SiteID: "b", URL: "https://b/1",
			Performers: []string{"irene  adler"}, Tags: []string{"big tits"}},
	}
	m := MergeScenes(scenes, time.Time{})
	if len(m.Performers) != 1 || m.Performers[0] != "Irene Adler" {
		t.Errorf("Performers = %q, want [Irene Adler]", m.Performers)
	}
	if len(m.Tags) != 1 || m.Tags[0] != "Big Tits" {
		t.Errorf("Tags = %q, want [Big Tits]", m.Tags)
	}
}

// Internal whitespace runs are collapsed in the stored value, not just used for
// the dedup key — Stash lookups are by exact name.
func TestMergeScenesCollapsesInternalWhitespace(t *testing.T) {
	scenes := []models.Scene{
		{ID: "1", SiteID: "a", URL: "https://a/1",
			Performers: []string{"Irene  Adler"}, Studio: "  Babes   Network "},
	}
	m := MergeScenes(scenes, time.Time{})
	if len(m.Performers) != 1 || m.Performers[0] != "Irene Adler" {
		t.Errorf("Performers = %q, want [Irene Adler]", m.Performers)
	}
	if m.Studio != "Babes Network" {
		t.Errorf("Studio = %q, want %q", m.Studio, "Babes Network")
	}
}

func TestMergeScenesRecordsProvenance(t *testing.T) {
	jan1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	jan5 := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)

	scenes := []models.Scene{
		{ID: "1", SiteID: "sitea", URL: "https://a/1",
			Title: "The Scene", Date: jan5, Description: "short", Duration: 600},
		{ID: "1", SiteID: "siteb", URL: "https://b/1",
			Title: "The Scene (HD)", Date: jan1, Description: "a much longer description", Duration: 610},
	}
	m := MergeScenes(scenes, time.Time{})

	// Title: first non-empty wins, the other is recorded as dropped.
	title := m.Sources["title"]
	if title.Site != "sitea" || !title.Conflicted() {
		t.Errorf("title source = %+v, want sitea with a conflict", title)
	}
	if len(title.Discarded) != 1 || title.Discarded[0] != "siteb: The Scene (HD)" {
		t.Errorf("title discarded = %v", title.Discarded)
	}

	// Date: earliest wins, so site B.
	if got := m.Sources["date"]; got.Site != "siteb" || len(got.Discarded) != 1 {
		t.Errorf("date source = %+v, want siteb with one discard", got)
	}
	// Description: longest wins.
	if got := m.Sources["description"]; got.Site != "siteb" {
		t.Errorf("description source = %+v, want siteb", got)
	}
	// Duration: largest wins.
	if got := m.Sources["duration"]; got.Site != "siteb" {
		t.Errorf("duration source = %+v, want siteb", got)
	}
	// A field nobody supplied is absent entirely.
	if _, ok := m.Sources["thumbnail"]; ok {
		t.Error("thumbnail should have no source entry")
	}
}

// Agreement between sites is not a conflict.
func TestMergeScenesProvenanceNoConflictOnAgreement(t *testing.T) {
	scenes := []models.Scene{
		{ID: "1", SiteID: "sitea", URL: "https://a/1", Title: "Same", Studio: "Babes"},
		{ID: "1", SiteID: "siteb", URL: "https://b/1", Title: "Same", Studio: "Babes"},
	}
	m := MergeScenes(scenes, time.Time{})
	for _, field := range []string{"title", "studio"} {
		if got := m.Sources[field]; got.Conflicted() {
			t.Errorf("%s reported a conflict: %+v", field, got)
		} else if got.Site != "sitea" {
			t.Errorf("%s site = %q, want sitea", field, got.Site)
		}
	}
}

// The Stash date can beat every site's; nothing in the scene set claims it.
func TestMergeScenesProvenanceExistingDateWins(t *testing.T) {
	scenes := []models.Scene{
		{ID: "1", SiteID: "sitea", URL: "https://a/1",
			Date: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)},
	}
	existing := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	m := MergeScenes(scenes, existing)

	if !m.Date.Equal(existing) {
		t.Fatalf("Date = %v, want the earlier existing date", m.Date)
	}
	src := m.Sources["date"]
	if src.Site != "" {
		t.Errorf("date site = %q, want empty (value came from Stash)", src.Site)
	}
	if len(src.Discarded) != 1 {
		t.Errorf("date discarded = %v, want the site's losing value", src.Discarded)
	}
}
