package match

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
)

func TestNormalize(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"MILF JOI Countdown!!!", "milf joi countdown"},
		{"Fostering the Bully", "fostering the bully"},
		{"Scene - Title (4K)", "scene title"},
		{"  spaces  everywhere  ", "spaces everywhere"},
		{"already clean", "already clean"},
		{"", ""},
		{"123-numbers_and-stuff", "123 numbers and stuff"},
		{"SunnyDayAtTheBeach_1080p", "sunny day at the beach 1080p"},
		{"camelCaseWords", "camel case words"},
		{"ALLCAPSword", "allcap sword"},
		{"Scene4KVersion", "scene4k version"},
		{"Some Title (FULL HD)", "some title"},
		{"Some Title (mp4)", "some title"},
		{"Some Title (mov)", "some title"},
		{"Some Title (wmv)", "some title"},
		{"Title With (Real Parens)", "title with real parens"},
		// Single-pass contract: only the outermost suffix is stripped. Nested
		// suffixes don't occur in real FSS data; if they did, the inner one
		// bleeds into the normalized title as an extra word.
		{"Title (HD) (4K)", "title hd"},
		// Zero-width Unicode characters are stripped (they're non-alphanumeric).
		{"Hello\u200bWorld", "hello world"}, // U+200B zero-width space
		{"Test\u200cTitle", "test title"},   // U+200C zero-width non-joiner
		{"Scene\u200dName", "scene name"},   // U+200D zero-width joiner
		{"Video\ufeffClip", "video clip"},   // U+FEFF byte order mark
		// Accented Latin letters fold to their base form instead of being stripped.
		{"Café Scene", "cafe scene"},
		{"Ñoño Video", "nono video"},
		{"Renée Pornélé", "renee pornele"},
		// Non-Latin scripts are preserved (letters/digits of any script survive)
		// rather than normalizing to an empty string.
		{"日本語 Title", "日本語 title"},
		{"Привет Мир", "привет мир"},
	}
	for _, c := range cases {
		got := Normalize(c.input)
		if got != c.want {
			t.Errorf("Normalize(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestStripExtension(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"scene.mp4", "scene"},
		{"scene.name.mp4", "scene.name"},
		{"no-extension", "no-extension"},
		{"", ""},
	}
	for _, c := range cases {
		got := stripExtension(c.input)
		if got != c.want {
			t.Errorf("stripExtension(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func scene(id, siteID, title string) models.Scene {
	return models.Scene{
		ID:     id,
		SiteID: siteID,
		Title:  title,
		URL:    "https://example.com/" + id,
	}
}

func sceneWithDuration(id, siteID, title string, duration int) models.Scene {
	s := scene(id, siteID, title)
	s.Duration = duration
	return s
}

func TestMatchExact(t *testing.T) {
	idx := BuildIndex([]models.Scene{
		scene("1", "manyvids", "Fostering the Bully"),
		scene("2", "manyvids", "MILF JOI Countdown"),
	})

	r := idx.Match("Fostering the Bully.mp4", 0)
	if r.Confidence != MatchExact {
		t.Errorf("confidence = %v, want EXACT", r.Confidence)
	}
	if len(r.Scenes) != 1 || r.Scenes[0].ID != "1" {
		t.Errorf("scenes = %v, want [id=1]", r.Scenes)
	}
}

func TestMatchExactCaseInsensitive(t *testing.T) {
	idx := BuildIndex([]models.Scene{
		scene("1", "manyvids", "Fostering the Bully"),
	})

	r := idx.Match("fostering the bully.mp4", 0)
	if r.Confidence != MatchExact {
		t.Errorf("confidence = %v, want EXACT", r.Confidence)
	}
}

func TestMatchSubstring(t *testing.T) {
	idx := BuildIndex([]models.Scene{
		scene("1", "iwantclips", "JOI Countdown"),
	})

	r := idx.Match("Studio Name - JOI Countdown.mp4", 0)
	if r.Confidence != MatchSubstring {
		t.Errorf("confidence = %v, want SUBSTR", r.Confidence)
	}
	if len(r.Scenes) != 1 || r.Scenes[0].ID != "1" {
		t.Errorf("scenes = %v, want [id=1]", r.Scenes)
	}
}

func TestMatchSubstringPicksLongest(t *testing.T) {
	idx := BuildIndex([]models.Scene{
		scene("1", "manyvids", "JOI"),
		scene("2", "iwantclips", "JOI Countdown Special"),
	})

	r := idx.Match("Studio - JOI Countdown Special Edition.mp4", 0)
	if r.Confidence != MatchSubstring {
		t.Errorf("confidence = %v, want SUBSTR", r.Confidence)
	}
	if len(r.Scenes) != 1 || r.Scenes[0].ID != "2" {
		t.Errorf("scenes = %v, want [id=2]", r.Scenes)
	}
}

func TestMatchAmbiguous(t *testing.T) {
	idx := BuildIndex([]models.Scene{
		scene("1", "manyvids", "Hot Scene Alpha"),
		scene("2", "iwantclips", "Hot Scene Bravo"),
	})

	// Both titles are 3 words matching 3 of 4 filename words (75%) — tied length.
	r := idx.Match("Hot Scene Alpha Bravo.mp4", 0)
	if r.Confidence != MatchAmbiguous {
		t.Errorf("confidence = %v, want AMBIGUOUS", r.Confidence)
	}
}

func TestMatchNone(t *testing.T) {
	idx := BuildIndex([]models.Scene{
		scene("1", "manyvids", "Fostering the Bully"),
	})

	r := idx.Match("completely unrelated filename.mp4", 0)
	if r.Confidence != MatchNone {
		t.Errorf("confidence = %v, want SKIP", r.Confidence)
	}
}

func TestMatchReleaseStyleFilenameWithDate(t *testing.T) {
	// Dominant rip naming Site.YY.MM.DD.Performer.Title.1080p: the date's three
	// components used to inflate the word count and push the real title below
	// the 50% ratio. Stripping the date run from the denominator lets it match.
	idx := BuildIndex([]models.Scene{
		scene("1", "site", "Alice Gets Her Revenge"),
	})

	r := idx.Match("studio.23.06.01.alice.gets.her.revenge.1080p.mp4", 0)
	if r.Confidence != MatchSubstring {
		t.Errorf("confidence = %v, want SUBSTR (date tokens excluded from ratio)", r.Confidence)
	}
	if len(r.Scenes) != 1 || r.Scenes[0].ID != "1" {
		t.Errorf("scenes = %+v, want single scene id=1", r.Scenes)
	}
}

func TestTitleWordCount(t *testing.T) {
	cases := []struct {
		words []string
		want  int
	}{
		{strings.Fields("studio 23 06 01 alice gets her revenge 1080p mp4"), 7}, // 10 - 3 date
		{strings.Fields("studio 2023 06 01 alice scene"), 3},                    // 6 - 3 date (YYYY)
		{strings.Fields("01 06 2023 alice scene"), 2},                           // DD MM YYYY
		{strings.Fields("no date here just words"), 5},                          // no date run
		{strings.Fields("scene 99 bonus"), 3},                                   // 99 not a date component alone
	}
	for _, c := range cases {
		if got := titleWordCount(c.words); got != c.want {
			t.Errorf("titleWordCount(%v) = %d, want %d", c.words, got, c.want)
		}
	}
}

func TestIsDateRun(t *testing.T) {
	yes := [][3]string{
		{"23", "06", "01"},   // YY MM DD
		{"2023", "12", "24"}, // YYYY MM DD
		{"01", "06", "2023"}, // DD MM YYYY
	}
	for _, c := range yes {
		if !isDateRun(c[0], c[1], c[2]) {
			t.Errorf("isDateRun%v = false, want true", c)
		}
	}
	no := [][3]string{
		{"23", "13", "01"},    // month out of range
		{"23", "06", "40"},    // day out of range
		{"alice", "06", "01"}, // non-numeric
		{"99", "99", "99"},    // not a date
	}
	for _, c := range no {
		if isDateRun(c[0], c[1], c[2]) {
			t.Errorf("isDateRun%v = true, want false", c)
		}
	}
}

func TestMatchShortTitleRejectsLongFilename(t *testing.T) {
	idx := BuildIndex([]models.Scene{
		scene("1", "manyvids", "JOI Custom Video"),
	})

	// "joi custom video" = 3 words, filename has 7 words → 3/7 = 43% < 50%, rejected
	r := idx.Match("yogabella_joi_and_squirting_custom_video_hd.mp4", 0)
	if r.Confidence != MatchNone {
		t.Errorf("confidence = %v, want SKIP (title too short for filename)", r.Confidence)
	}
}

func TestMatchNoPartialWord(t *testing.T) {
	idx := BuildIndex([]models.Scene{
		scene("1", "manyvids", "Neighbor"),
	})

	r := idx.Match("Free Use Neighborhood Cumdump.mp4", 0)
	if r.Confidence != MatchNone {
		t.Errorf("confidence = %v, want SKIP (neighbor != neighborhood)", r.Confidence)
	}
}

func TestMatchCrossSiteSameTitle(t *testing.T) {
	idx := BuildIndex([]models.Scene{
		scene("1", "manyvids", "Fostering the Bully"),
		scene("2", "clips4sale", "Fostering the Bully"),
	})

	r := idx.Match("Fostering the Bully.mp4", 0)
	if r.Confidence != MatchExact {
		t.Errorf("confidence = %v, want EXACT", r.Confidence)
	}
	if len(r.Scenes) != 2 {
		t.Errorf("scenes len = %d, want 2 (cross-site)", len(r.Scenes))
	}
}

func sceneWithDate(id, siteID, title string, date time.Time) models.Scene {
	s := scene(id, siteID, title)
	s.Date = date
	return s
}

func TestMatchSameTitleDifferentDatesIsAmbiguous(t *testing.T) {
	// Two genuinely different scenes that share a title (a yearly special).
	// Without a file duration to disambiguate, merging them would union both
	// casts/URLs onto one Stash scene — so report ambiguous instead.
	idx := BuildIndex([]models.Scene{
		sceneWithDate("1", "manyvids", "Christmas Special", time.Date(2022, 12, 24, 0, 0, 0, 0, time.UTC)),
		sceneWithDate("2", "manyvids", "Christmas Special", time.Date(2023, 12, 24, 0, 0, 0, 0, time.UTC)),
	})

	r := idx.Match("Christmas Special.mp4", 0)
	if r.Confidence != MatchAmbiguous {
		t.Errorf("confidence = %v, want AMBIGUOUS (different-date same-title scenes)", r.Confidence)
	}
}

func TestMatchSameTitleDurationDisambiguates(t *testing.T) {
	// Same setup but the file duration matches exactly one — the duration filter
	// leaves a single scene, so it's a clean exact match.
	idx := BuildIndex([]models.Scene{
		mustScene(sceneWithDate("1", "manyvids", "Christmas Special", time.Date(2022, 12, 24, 0, 0, 0, 0, time.UTC)), 600),
		mustScene(sceneWithDate("2", "manyvids", "Christmas Special", time.Date(2023, 12, 24, 0, 0, 0, 0, time.UTC)), 1800),
	})

	r := idx.Match("Christmas Special.mp4", 600)
	if r.Confidence != MatchExact {
		t.Errorf("confidence = %v, want EXACT", r.Confidence)
	}
	if len(r.Scenes) != 1 || r.Scenes[0].ID != "1" {
		t.Errorf("scenes = %+v, want single scene id=1", r.Scenes)
	}
}

func TestMatchSameTitleAgreeingDatesStillMerges(t *testing.T) {
	// Cross-site same scene: same title, dates within tolerance — still merge.
	idx := BuildIndex([]models.Scene{
		sceneWithDate("1", "manyvids", "Fostering the Bully", time.Date(2023, 6, 1, 0, 0, 0, 0, time.UTC)),
		sceneWithDate("2", "clips4sale", "Fostering the Bully", time.Date(2023, 6, 1, 12, 0, 0, 0, time.UTC)),
	})

	r := idx.Match("Fostering the Bully.mp4", 0)
	if r.Confidence != MatchExact {
		t.Errorf("confidence = %v, want EXACT (agreeing dates)", r.Confidence)
	}
	if len(r.Scenes) != 2 {
		t.Errorf("scenes len = %d, want 2 (cross-site merge)", len(r.Scenes))
	}
}

func mustScene(s models.Scene, duration int) models.Scene {
	s.Duration = duration
	return s
}

func TestMatchNonASCIITitle(t *testing.T) {
	// A CJK title used to normalize to "" and be dropped from the index entirely.
	idx := BuildIndex([]models.Scene{
		scene("1", "site", "日本語 Scene"),
	})
	r := idx.Match("日本語 Scene.mp4", 0)
	if r.Confidence != MatchExact {
		t.Errorf("confidence = %v, want EXACT for CJK title", r.Confidence)
	}
}

func TestMatchAccentInsensitive(t *testing.T) {
	// Accented title, unaccented filename (common in rip naming) should match.
	idx := BuildIndex([]models.Scene{
		scene("1", "site", "Renée Café Visit"),
	})
	r := idx.Match("Renee Cafe Visit.mp4", 0)
	if r.Confidence != MatchExact {
		t.Errorf("confidence = %v, want EXACT (accent-folded)", r.Confidence)
	}
}

func TestMatchStepStripped(t *testing.T) {
	idx := BuildIndex([]models.Scene{
		scene("1", "manyvids", "Step-Sister Blackmail Custom Video"),
	})

	// Filename has "sister" without "step" — should match via sanitized index.
	r := idx.Match("yogabella_sister_blackmail_custom_video.mp4", 0)
	if r.Confidence == MatchNone {
		t.Errorf("confidence = %v, want a match (step stripped)", r.Confidence)
	}
	if len(r.Scenes) != 1 || r.Scenes[0].ID != "1" {
		t.Errorf("scenes = %v, want [id=1]", r.Scenes)
	}
}

func TestMatchStepInBothSides(t *testing.T) {
	idx := BuildIndex([]models.Scene{
		scene("1", "manyvids", "Step-Sister Blackmail"),
	})

	// Filename also has "step" — should match on first pass (exact).
	r := idx.Match("Step-Sister Blackmail.mp4", 0)
	if r.Confidence != MatchExact {
		t.Errorf("confidence = %v, want EXACT", r.Confidence)
	}
}

func TestMatchStepInTitleNotFilename(t *testing.T) {
	idx := BuildIndex([]models.Scene{
		scene("1", "manyvids", "We HAVE to STOP step-Son"),
	})

	r := idx.Match("yogabella_We-HAVE-to-STOP-Son.mp4", 0)
	if r.Confidence == MatchNone {
		t.Errorf("confidence = %v, want a match (step in title, not in filename)", r.Confidence)
	}
	if len(r.Scenes) != 1 || r.Scenes[0].ID != "1" {
		t.Errorf("scenes = %v, want [id=1]", r.Scenes)
	}
}

func TestMatchCamelCaseFilename(t *testing.T) {
	idx := BuildIndex([]models.Scene{scene("1", "tt", "Sunny Day at the Beach")})
	r := idx.Match("SunnyDayAtTheBeach_1080p.mp4", 0)
	if r.Confidence != MatchSubstring {
		t.Errorf("confidence = %v, want SUBSTR", r.Confidence)
	}
	if r.Scenes[0].Title != "Sunny Day at the Beach" {
		t.Errorf("title = %q", r.Scenes[0].Title)
	}
}

func TestMatchEmptyFilename(t *testing.T) {
	idx := BuildIndex([]models.Scene{scene("1", "mv", "Title")})
	r := idx.Match("", 0)
	if r.Confidence != MatchNone {
		t.Errorf("confidence = %v, want SKIP", r.Confidence)
	}
}

// ---- Duration filtering ----

func TestMatchExactDurationMatch(t *testing.T) {
	idx := BuildIndex([]models.Scene{
		sceneWithDuration("1", "manyvids", "Some Title", 600),
	})

	// File is 605s, FSS says 600s — within tolerance
	r := idx.Match("Some Title.mp4", 605)
	if r.Confidence != MatchExact {
		t.Errorf("confidence = %v, want EXACT", r.Confidence)
	}
}

func TestMatchExactDurationFailFallsThroughToSubstring(t *testing.T) {
	// The exact-title bucket matches the filename but its only scene has the
	// wrong duration. A shorter title that is a subset of the filename has the
	// right duration — the duration-failed exact bucket must not short-circuit
	// to NONE before that substring candidate is considered.
	idx := BuildIndex([]models.Scene{
		sceneWithDuration("1", "site", "Rachel Scene Special", 1800),
		sceneWithDuration("2", "site", "Rachel Scene", 600),
	})

	r := idx.Match("Rachel Scene Special.mp4", 600)
	if r.Confidence != MatchSubstring {
		t.Fatalf("confidence = %v, want SUBSTR (fell through to duration-matching substring)", r.Confidence)
	}
	if len(r.Scenes) != 1 || r.Scenes[0].ID != "2" {
		t.Errorf("scenes = %+v, want single scene id=2", r.Scenes)
	}
}

func TestMatchExactDurationMismatch(t *testing.T) {
	idx := BuildIndex([]models.Scene{
		sceneWithDuration("1", "manyvids", "Some Title", 600),
	})

	// File is 900s, FSS says 600s — way off, rejected
	r := idx.Match("Some Title.mp4", 900)
	if r.Confidence != MatchNone {
		t.Errorf("confidence = %v, want SKIP (duration mismatch)", r.Confidence)
	}
}

func TestMatchDurationUnknownPassesThrough(t *testing.T) {
	idx := BuildIndex([]models.Scene{
		sceneWithDuration("1", "manyvids", "Some Title", 600),
	})

	// File duration unknown (0) — should still match
	r := idx.Match("Some Title.mp4", 0)
	if r.Confidence != MatchExact {
		t.Errorf("confidence = %v, want EXACT", r.Confidence)
	}
}

func TestMatchTrailingNumberStripped(t *testing.T) {
	idx := BuildIndex([]models.Scene{
		sceneWithDuration("1", "spankingglamour", "Hester Prynne 1", 795),
		sceneWithDuration("2", "spankingglamour", "Hester Prynne 2", 715),
	})

	// Filename has a different trailing number (site-wide episode ID).
	// Pass 3 strips trailing numbers from both sides, duration disambiguates.
	r := idx.Match("[SpankingGlamour] - Hester Prynne - 044.mp4", 795)
	if r.Confidence != MatchSubstring {
		t.Errorf("confidence = %v, want SUBSTR", r.Confidence)
	}
	if len(r.Scenes) != 1 || r.Scenes[0].ID != "1" {
		t.Errorf("scenes = %+v, want single scene id=1", r.Scenes)
	}
}

func TestMatchTrailingNumberNoMatch(t *testing.T) {
	idx := BuildIndex([]models.Scene{
		scene("1", "site", "A Title Without Number"),
	})

	// Title has no trailing number — pass 3 should not engage.
	r := idx.Match("Something Else 99.mp4", 0)
	if r.Confidence != MatchNone {
		t.Errorf("confidence = %v, want NONE", r.Confidence)
	}
}

func TestMatchTrailingNumberSkippedWithoutDuration(t *testing.T) {
	idx := BuildIndex([]models.Scene{
		sceneWithDuration("1", "site", "Rachel 1", 267),
		sceneWithDuration("2", "site", "Rachel 2", 358),
		sceneWithDuration("3", "site", "Rachel 3", 345),
	})

	// Without duration info, pass 3 is not attempted — too risky without
	// duration to disambiguate.
	r := idx.Match("[Site] - Rachel - 108.mp4", 0)
	if r.Confidence != MatchNone {
		t.Errorf("confidence = %v, want NONE (pass 3 requires duration)", r.Confidence)
	}
}

func TestMatchTrailingNumberShortNameWithDuration(t *testing.T) {
	idx := BuildIndex([]models.Scene{
		sceneWithDuration("1", "site", "Marsha 1", 597),
		sceneWithDuration("2", "site", "Marsha 2", 506),
	})

	// Single-word performer name — would fail the 50% word-count check in
	// passes 1-2, but pass 3 relaxes it when duration is available.
	r := idx.Match("[SpankingGlamour] - Marsha - 042.mp4", 597)
	if r.Confidence == MatchNone || r.Confidence == MatchAmbiguous {
		t.Errorf("confidence = %v, want a definite match", r.Confidence)
	}
	if len(r.Scenes) != 1 || r.Scenes[0].ID != "1" {
		t.Errorf("scenes = %+v, want single scene id=1", r.Scenes)
	}
}

func TestMatchTrailingNumberDisambiguatedByDuration(t *testing.T) {
	idx := BuildIndex([]models.Scene{
		sceneWithDuration("1", "site", "Rachel 1", 267),
		sceneWithDuration("2", "site", "Rachel 2", 358),
		sceneWithDuration("3", "site", "Rachel 3", 345),
	})

	r := idx.Match("[Site] - Rachel - 108.mp4", 267)
	if r.Confidence == MatchNone || r.Confidence == MatchAmbiguous {
		t.Errorf("confidence = %v, want a definite match", r.Confidence)
	}
	if len(r.Scenes) != 1 || r.Scenes[0].ID != "1" {
		t.Errorf("scenes = %+v, want single scene id=1", r.Scenes)
	}
}

func TestDurationClose(t *testing.T) {
	// tolerance = max(fileDuration * 10%, 30s)
	cases := []struct {
		fss  int
		file float64
		want bool
	}{
		{600, 605, true},  // diff=5, tol=max(60.5,30)=60.5 → ok
		{600, 660, true},  // diff=60, tol=max(66,30)=66 → ok
		{600, 900, false}, // diff=300, tol=max(90,30)=90 → too far
		{100, 125, true},  // diff=25, tol=max(12.5,30)=30 → ok
		{100, 135, false}, // diff=35, tol=max(13.5,30)=30 → too far
		{60, 85, true},    // diff=25, tol=max(8.5,30)=30 → ok
		{60, 95, false},   // diff=35, tol=max(9.5,30)=30 → too far
		{0, 600, true},    // FSS unknown → pass
		{600, 0, true},    // file unknown → pass
	}
	for _, c := range cases {
		got := durationClose(c.fss, c.file)
		if got != c.want {
			t.Errorf("durationClose(%d, %.0f) = %v, want %v", c.fss, c.file, got, c.want)
		}
	}
}

// ---- File loading ----

func TestLoadJSONFiles(t *testing.T) {
	dir := t.TempDir()
	sf := models.StudioFile{
		StudioURL:  "https://example.com/studio/1",
		ScrapedAt:  time.Now().UTC(),
		SceneCount: 2,
		Scenes: []models.Scene{
			scene("1", "manyvids", "Scene One"),
			scene("2", "manyvids", "Scene Two"),
		},
	}
	data, _ := json.MarshalIndent(sf, "", "  ")
	path := filepath.Join(dir, "test.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	scenes, err := LoadJSONFiles([]string{path})
	if err != nil {
		t.Fatalf("LoadJSONFiles error: %v", err)
	}
	if len(scenes) != 2 {
		t.Errorf("got %d scenes, want 2", len(scenes))
	}
}

// BenchmarkMatchSubstringLargeIndex exercises the substring-match slow path
// against a 10k-title index so the inverted-index optimization can be
// quantified. The filename does not exact-match any title, forcing the
// substring path on every iteration.
func BenchmarkMatchSubstringLargeIndex(b *testing.B) {
	scenes := make([]models.Scene, 0, 10000)
	for i := 0; i < 10000; i++ {
		scenes = append(scenes, scene(
			fmt.Sprintf("%d", i), "site",
			fmt.Sprintf("Scene Title Number %d Special", i),
		))
	}
	idx := BuildIndex(scenes)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx.Match("Studio - Scene Title Number 4242 Special Edition.mp4", 0)
	}
}

// BenchmarkMatchSubstringFullScan replicates the pre-optimization full-scan
// algorithm so the speedup is measured against the same input as
// BenchmarkMatchSubstringLargeIndex. Kept in test code only — the production
// code no longer calls this path.
func BenchmarkMatchSubstringFullScan(b *testing.B) {
	scenes := make([]models.Scene, 0, 10000)
	for i := 0; i < 10000; i++ {
		scenes = append(scenes, scene(
			fmt.Sprintf("%d", i), "site",
			fmt.Sprintf("Scene Title Number %d Special", i),
		))
	}
	idx := BuildIndex(scenes)
	filename := "Studio - Scene Title Number 4242 Special Edition.mp4"

	oldIsSubstring := func(title, filename string) bool {
		titleWords := strings.Fields(title)
		if len(titleWords) == 0 {
			return false
		}
		filenameWords := strings.Fields(filename)
		wordSet := make(map[string]bool, len(filenameWords))
		for _, w := range filenameWords {
			wordSet[w] = true
		}
		for _, w := range titleWords {
			if !wordSet[w] {
				return false
			}
		}
		return float64(len(titleWords)) >= float64(len(filenameWords))*0.5
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		raw := stripExtension(filename)
		norm := Normalize(raw)
		if _, ok := idx.byTitle[norm]; ok {
			continue
		}
		var matches int
		for title := range idx.byTitle {
			if oldIsSubstring(title, norm) {
				matches++
			}
		}
		_ = matches
	}
}

func TestLoadJSONDir(t *testing.T) {
	dir := t.TempDir()

	for _, name := range []string{"studio-a.json", "studio-b.json"} {
		sf := models.StudioFile{
			Scenes: []models.Scene{scene(name, "site", "Title for "+name)},
		}
		data, _ := json.MarshalIndent(sf, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Non-JSON file should be ignored.
	if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("ignore me"), 0o644); err != nil {
		t.Fatal(err)
	}

	scenes, err := LoadJSONDir(dir)
	if err != nil {
		t.Fatalf("LoadJSONDir error: %v", err)
	}
	if len(scenes) != 2 {
		t.Errorf("got %d scenes, want 2", len(scenes))
	}
}
