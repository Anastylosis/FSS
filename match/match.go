package match

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"

	"github.com/Anastylosis/FSS/models"
)

// SceneIndex is a precomputed index of scene titles for fast filename matching.
type SceneIndex struct {
	byTitle             map[string][]models.Scene
	byTitleSanitized    map[string][]models.Scene // titles with noise words stripped
	byTitleNoTrailingID map[string][]models.Scene // titles with trailing number stripped
	all                 []models.Scene

	titleSubstrIdx        *substringIndex
	sanitizedSubstrIdx    *substringIndex
	noTrailingIDSubstrIdx *substringIndex
}

// substringIndex narrows the substring-match search from O(N_titles) to roughly
// O(rarest-word-bucket-size). Each title is filed under its lowest-frequency
// word; only that bucket is consulted when its key word appears in the
// filename. The full subset check still runs on the (small) shortlist.
type substringIndex struct {
	byRarestWord map[string][]indexedTitle
}

type indexedTitle struct {
	title string
	words []string
}

func newSubstringIndex(titleMap map[string][]models.Scene) *substringIndex {
	freq := make(map[string]int)
	for title := range titleMap {
		for _, w := range strings.Fields(title) {
			freq[w]++
		}
	}
	si := &substringIndex{byRarestWord: make(map[string][]indexedTitle)}
	for title := range titleMap {
		words := strings.Fields(title)
		if len(words) == 0 {
			continue
		}
		rarest := words[0]
		for _, w := range words[1:] {
			if freq[w] < freq[rarest] {
				rarest = w
			}
		}
		si.byRarestWord[rarest] = append(si.byRarestWord[rarest], indexedTitle{
			title: title,
			words: words,
		})
	}
	return si
}

// findCandidateTitles returns titles whose word-set is a subset of the
// filename's word-set and whose word count is ≥ minWordRatio of the filename's
// word count. Reached via the rarest-word inversion rather than a full scan.
func (si *substringIndex) findCandidateTitles(filenameWords []string, minWordRatio float64) []string {
	if len(filenameWords) == 0 {
		return nil
	}
	filenameSet := make(map[string]bool, len(filenameWords))
	for _, w := range filenameWords {
		filenameSet[w] = true
	}
	// Count only title-bearing words in the denominator. The dominant rip
	// naming Site.YY.MM.DD.Performer.Title.1080p embeds a date whose components
	// each count as words, inflating the filename length enough to push real
	// (short) titles below minWordRatio. Date tokens stay in filenameSet for the
	// subset check; they're just excluded from the ratio.
	minLen := float64(titleWordCount(filenameWords)) * minWordRatio
	seen := make(map[string]bool)
	var titles []string
	for w := range filenameSet {
		for _, it := range si.byRarestWord[w] {
			if seen[it.title] {
				continue
			}
			seen[it.title] = true
			if float64(len(it.words)) < minLen {
				continue
			}
			ok := true
			for _, tw := range it.words {
				if !filenameSet[tw] {
					ok = false
					break
				}
			}
			if ok {
				titles = append(titles, it.title)
			}
		}
	}
	return titles
}

// titleWordCount returns the number of words that carry title signal, excluding
// the components of any embedded date run (YY/YYYY MM DD and DD MM YYYY). Used
// as the minWordRatio denominator so release-style filenames don't reject short
// titles purely because their date inflates the word count.
func titleWordCount(words []string) int {
	skip := make([]bool, len(words))
	for i := 0; i+2 < len(words); i++ {
		if isDateRun(words[i], words[i+1], words[i+2]) {
			skip[i], skip[i+1], skip[i+2] = true, true, true
		}
	}
	n := 0
	for i := range words {
		if !skip[i] {
			n++
		}
	}
	return n
}

// isDateRun reports whether three consecutive numeric tokens form a plausible
// date: YYYY MM DD, YY MM DD, or DD MM YYYY.
func isDateRun(a, b, c string) bool {
	ai, err1 := strconv.Atoi(a)
	bi, err2 := strconv.Atoi(b)
	ci, err3 := strconv.Atoi(c)
	if err1 != nil || err2 != nil || err3 != nil {
		return false
	}
	month := bi >= 1 && bi <= 12
	isYear := func(tok string, n int) bool { return len(tok) == 4 && n >= 1900 && n <= 2100 }
	// YYYY MM DD or YY MM DD
	if month && ci >= 1 && ci <= 31 && (isYear(a, ai) || len(a) == 2) {
		return true
	}
	// DD MM YYYY
	if ai >= 1 && ai <= 31 && month && isYear(c, ci) {
		return true
	}
	return false
}

// MatchConfidence indicates how strong a filename-to-title match is.
type MatchConfidence int

// Confidence levels, ordered from strongest to weakest. MatchNone means no
// candidate matched; MatchAmbiguous means several distinct titles did and the
// caller must disambiguate.
const (
	MatchExact MatchConfidence = iota
	MatchSubstring
	MatchNone
	MatchAmbiguous
)

func (c MatchConfidence) String() string {
	switch c {
	case MatchExact:
		return "EXACT"
	case MatchSubstring:
		return "SUBSTR"
	case MatchAmbiguous:
		return "AMBIGUOUS"
	default:
		return "SKIP"
	}
}

// MatchResult holds the matched scenes and confidence level for a single filename lookup.
type MatchResult struct {
	Confidence MatchConfidence
	Scenes     []models.Scene // all matching FSS scenes (possibly from multiple sites)
	Candidates int            // for ambiguous: how many distinct titles matched
}

var (
	camelLowerUpper  = regexp.MustCompile(`([a-z])([A-Z])`)
	camelUpperSeries = regexp.MustCompile(`([A-Z]+)([A-Z][a-z])`)
	formatSuffixRe   = regexp.MustCompile(`(?i)\s*\(\s*(?:full\s+hd|4k|hd|mp4|mov|wmv|avi|mkv|1080p|720p|480p|sd)\s*\)\s*$`)
)

// accentFold strips combining marks after NFD decomposition, folding accented
// Latin letters to their base form (é→e, ñ→n) so accented and unaccented
// spellings of the same title match.
var accentFold = transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)

// Normalize lowercases, folds accents, splits camelCase, and collapses runs of
// non-letter/non-digit characters into single spaces. Unlike a plain [a-z0-9]
// strip it preserves letters and digits of every script, so Cyrillic and CJK
// titles survive instead of normalizing to an empty string.
func Normalize(s string) string {
	s = stripFormatSuffix(s)
	// Drop invalid UTF-8 up front. Otherwise transform.String errors on the bad
	// bytes, the accentFold below is skipped, and accented letters survive
	// unfolded — making Normalize non-idempotent (the second pass, on now-valid
	// input, would fold them). collapseToWords discards these bytes anyway.
	s = strings.ToValidUTF8(s, "")
	if folded, _, err := transform.String(accentFold, s); err == nil {
		s = folded
	}
	s = camelLowerUpper.ReplaceAllString(s, "${1} ${2}")
	s = camelUpperSeries.ReplaceAllString(s, "${1} ${2}")
	return collapseToWords(strings.ToLower(s))
}

// collapseToWords keeps Unicode letters and digits, replacing every other run
// of characters with a single space and trimming the ends.
func collapseToWords(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	pendingSpace := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if pendingSpace && b.Len() > 0 {
				b.WriteByte(' ')
			}
			pendingSpace = false
			b.WriteRune(r)
		} else {
			pendingSpace = true
		}
	}
	return b.String()
}

// stripFormatSuffix removes a single trailing format-tag suffix like " (4K)"
// or " (mp4)". One pass only — nested suffixes like "Title (HD) (4K)" are
// not produced by any FSS scraper in practice (Clips4Sale emits one row per
// format, each with a single suffix), so handling that case isn't worth the
// loop. If a nested case ever shows up, the inner suffix bleeds into the
// normalized title as an extra word, which is acceptable for matching.
func stripFormatSuffix(s string) string {
	return formatSuffixRe.ReplaceAllString(s, "")
}

func stripExtension(filename string) string {
	ext := filepath.Ext(filename)
	if ext == "" {
		return filename
	}
	return filename[:len(filename)-len(ext)]
}

var noiseWords = map[string]bool{
	"step": true,
}

func stripNoise(s string) string {
	words := strings.Fields(s)
	var out []string
	for _, w := range words {
		if !noiseWords[w] {
			out = append(out, w)
		}
	}
	return strings.Join(out, " ")
}

var trailingNumberRe = regexp.MustCompile(`\s+\d+$`)

func stripTrailingNumber(s string) string {
	return trailingNumberRe.ReplaceAllString(s, "")
}

// BuildIndex creates a SceneIndex from a slice of scenes, building exact and substring lookup tables.
func BuildIndex(scenes []models.Scene) *SceneIndex {
	idx := &SceneIndex{
		byTitle:             make(map[string][]models.Scene),
		byTitleSanitized:    make(map[string][]models.Scene),
		byTitleNoTrailingID: make(map[string][]models.Scene),
		all:                 scenes,
	}
	for _, s := range scenes {
		norm := Normalize(s.Title)
		if norm != "" {
			idx.byTitle[norm] = append(idx.byTitle[norm], s)
			sanitized := stripNoise(norm)
			if sanitized != "" {
				idx.byTitleSanitized[sanitized] = append(idx.byTitleSanitized[sanitized], s)
			}
			noID := stripTrailingNumber(norm)
			if noID != "" && noID != norm {
				idx.byTitleNoTrailingID[noID] = append(idx.byTitleNoTrailingID[noID], s)
			}
		}
	}
	idx.titleSubstrIdx = newSubstringIndex(idx.byTitle)
	idx.sanitizedSubstrIdx = newSubstringIndex(idx.byTitleSanitized)
	idx.noTrailingIDSubstrIdx = newSubstringIndex(idx.byTitleNoTrailingID)
	return idx
}

const maxJSONFileBytes = 256 << 20 // 256 MB

func readBounded(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("file exceeds %d bytes", limit)
	}
	return data, nil
}

// LoadJSONFiles reads FSS JSON files and returns all scenes.
// Files that don't match the expected format are silently skipped.
func LoadJSONFiles(paths []string) ([]models.Scene, error) {
	var all []models.Scene
	for _, p := range paths {
		data, err := readBounded(p, maxJSONFileBytes)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", p, err)
		}
		var sf models.StudioFile
		if err := json.Unmarshal(data, &sf); err != nil {
			continue
		}
		if len(sf.Scenes) == 0 {
			continue
		}
		all = append(all, sf.Scenes...)
	}
	return all, nil
}

// LoadJSONDir reads all *.json files in a directory.
func LoadJSONDir(dir string) ([]models.Scene, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading directory %s: %w", dir, err)
	}
	var paths []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
			paths = append(paths, filepath.Join(dir, e.Name()))
		}
	}
	if len(paths) == 0 {
		return nil, nil
	}
	return LoadJSONFiles(paths)
}

// Match finds FSS scenes matching a filename. fileDurationSec is the
// duration of the Stash file in seconds (0 means unknown / skip check).
func (idx *SceneIndex) Match(filename string, fileDurationSec float64) MatchResult {
	raw := stripExtension(filename)
	norm := Normalize(raw)
	if norm == "" {
		return MatchResult{Confidence: MatchNone}
	}

	// Pass 1: match against original titles.
	if r := matchAgainst(idx.byTitle, idx.titleSubstrIdx, norm, fileDurationSec, 0.5); r.Confidence != MatchNone {
		return r
	}

	// Pass 2: try sanitized index (noise words like "step" stripped from titles).
	// The filename may or may not contain noise words — either way the
	// sanitized title index might match.
	sanitized := stripNoise(norm)
	if sanitized != "" {
		if r := matchAgainst(idx.byTitleSanitized, idx.sanitizedSubstrIdx, sanitized, fileDurationSec, 0.5); r.Confidence != MatchNone {
			return r
		}
	}

	// Pass 3: strip trailing numbers from both sides. Handles sites where the
	// FSS title uses a per-performer sequence number (e.g. "Hester Prynne 1")
	// but the filename uses a site-wide episode number (e.g. "044"). The
	// performer name is the real match key; duration disambiguates. Only
	// attempted when file duration is known — without it, single-word names
	// like "Marsha" would match too broadly.
	noID := stripTrailingNumber(norm)
	if noID != "" && noID != norm && fileDurationSec > 0 {
		if r := matchAgainst(idx.byTitleNoTrailingID, idx.noTrailingIDSubstrIdx, noID, fileDurationSec, 0); r.Confidence != MatchNone {
			return r
		}
	}

	return MatchResult{Confidence: MatchNone}
}

func matchAgainst(titleMap map[string][]models.Scene, si *substringIndex, norm string, fileDurationSec float64, minWordRatio float64) MatchResult {
	if scenes, ok := titleMap[norm]; ok {
		filtered := filterByDuration(scenes, fileDurationSec)
		if len(filtered) > 0 {
			// Multiple scenes share this normalized title. Only merge them
			// (MergeScenes unions casts/URLs onto one Stash scene) when they
			// plausibly are the same scene across sites — i.e. they don't
			// conflict on date or duration. Two genuinely different scenes that
			// happen to share a title (e.g. a yearly "Christmas Special") must
			// not be merged; report ambiguous so the import skips them.
			if exactScenesAgree(filtered) {
				return MatchResult{Confidence: MatchExact, Scenes: filtered}
			}
			return MatchResult{Confidence: MatchAmbiguous, Candidates: len(filtered)}
		}
		// An exact title exists but no scene's duration matches the file
		// (filtered is only empty here when fileDurationSec > 0). Don't declare
		// NONE — fall through to the substring candidates, where a different
		// indexed title that is a subset of the filename may have the right
		// duration.
	}

	type candidate struct {
		title  string
		scenes []models.Scene
	}
	var candidates []candidate
	for _, title := range si.findCandidateTitles(strings.Fields(norm), minWordRatio) {
		scenes := titleMap[title]
		filtered := filterByDuration(scenes, fileDurationSec)
		if len(filtered) > 0 {
			candidates = append(candidates, candidate{title: title, scenes: filtered})
		} else if fileDurationSec == 0 {
			candidates = append(candidates, candidate{title: title, scenes: scenes})
		}
	}

	if len(candidates) == 0 {
		return MatchResult{Confidence: MatchNone}
	}

	if len(candidates) == 1 {
		return MatchResult{Confidence: MatchSubstring, Scenes: candidates[0].scenes}
	}

	distinctTitles := map[string]bool{}
	for _, c := range candidates {
		distinctTitles[c.title] = true
	}

	if len(distinctTitles) == 1 {
		var allScenes []models.Scene
		for _, c := range candidates {
			allScenes = append(allScenes, c.scenes...)
		}
		return MatchResult{Confidence: MatchSubstring, Scenes: allScenes}
	}

	// Pick the longest matching title as the most-specific match (more
	// characters = more discriminating). Length is in Unicode codepoints,
	// not bytes — `len(s)` would over-count multi-byte titles like "Café"
	// (5 bytes / 4 chars) and bias the tie-break against ASCII titles.
	// Two titles of equal codepoint length but different content stays
	// ambiguous (we can't pick one over the other without more signal).
	best := candidates[0]
	tied := false
	for _, c := range candidates[1:] {
		codepoints := countCodepoints(c.title)
		bestCodepoints := countCodepoints(best.title)
		if codepoints > bestCodepoints {
			best = c
			tied = false
		} else if codepoints == bestCodepoints && c.title != best.title {
			tied = true
		}
	}

	if tied {
		return MatchResult{
			Confidence: MatchAmbiguous,
			Candidates: len(distinctTitles),
		}
	}
	return MatchResult{Confidence: MatchSubstring, Scenes: best.scenes}
}

// sceneDateTolerance is how far two scenes' dates may differ and still be
// treated as the same scene across sites. Cross-site release dates can lag by a
// day; a larger gap signals genuinely different scenes.
const sceneDateTolerance = 36 * time.Hour

// exactScenesAgree reports whether every pair of scenes sharing one normalized
// title is mutually compatible (could be the same scene on different sites). A
// single conflicting pair makes the whole set ambiguous — conservative, but it
// prevents unrelated same-title scenes from being merged.
func exactScenesAgree(scenes []models.Scene) bool {
	for i := 0; i < len(scenes); i++ {
		for j := i + 1; j < len(scenes); j++ {
			if !scenesCompatible(scenes[i], scenes[j]) {
				return false
			}
		}
	}
	return true
}

// scenesCompatible reports whether two scenes could be the same underlying
// scene. Known durations that aren't close, or known dates more than
// sceneDateTolerance apart, mark them as different. Missing data can't
// disagree, so it never blocks a legitimate cross-site merge.
func scenesCompatible(a, b models.Scene) bool {
	if a.Duration > 0 && b.Duration > 0 && !durationClose(a.Duration, float64(b.Duration)) {
		return false
	}
	if !a.Date.IsZero() && !b.Date.IsZero() {
		gap := a.Date.Sub(b.Date)
		if gap < 0 {
			gap = -gap
		}
		if gap > sceneDateTolerance {
			return false
		}
	}
	return true
}

const durationTolerancePct = 0.10
const durationToleranceMin = 30.0

func durationClose(fssDuration int, fileDuration float64) bool {
	if fssDuration == 0 || fileDuration == 0 {
		return true
	}
	diff := fileDuration - float64(fssDuration)
	if diff < 0 {
		diff = -diff
	}
	tolerance := fileDuration * durationTolerancePct
	if tolerance < durationToleranceMin {
		tolerance = durationToleranceMin
	}
	return diff <= tolerance
}

func filterByDuration(scenes []models.Scene, fileDuration float64) []models.Scene {
	if fileDuration == 0 {
		return scenes
	}
	var out []models.Scene
	for _, s := range scenes {
		if durationClose(s.Duration, fileDuration) {
			out = append(out, s)
		}
	}
	return out
}

// countCodepoints returns the number of Unicode codepoints (runes) in s.
// `range string` walks runes, so this avoids the allocation of `len([]rune(s))`
// while still being correct for non-ASCII input where `len(s)` (byte count)
// would be wrong.
func countCodepoints(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}
