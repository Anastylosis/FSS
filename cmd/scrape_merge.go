package cmd

import (
	"time"

	"github.com/Anastylosis/FSS/models"
)

// sceneKey identifies a scene within a studio. A studio URL can hold scenes
// from more than one site_id (aggregators, multi-brand scrapers), so merge and
// dedup must key on (id, site_id) — id alone collides across sites.
type sceneKey struct{ id, siteID string }

func keyOf(s models.Scene) sceneKey { return sceneKey{id: s.ID, siteID: s.SiteID} }

// traversal reports how a scrape ended, beyond the scenes it produced.
type traversal struct {
	incomplete   bool // fetch errors cost scenes, or the run was cancelled
	stoppedEarly bool // the KnownIDs early-stop fired
	failures     failureCounts
}

// coverage measures a scrape against what the store already holds. A scraper
// that still works but no longer reaches most of the catalogue — a site that
// drops its pagination, or a parser that only matches one of two card
// templates — looks like a clean success to every other check here.
type coverage struct {
	stored    int // live (not soft-deleted) scenes already in the store
	storedAll int // every stored scene, soft-deleted included
	seen      int // live stored scenes this scrape re-collected
	dropped   int // stored scenes, live or deleted, this scrape did not re-collect
	fresh     int // distinct scenes this scrape collected, before any carry-forward
	traversal
}

const (
	// coverageCollapseRatio is the share of the stored catalogue a completed
	// traversal must re-see. Below it, a site change is likelier than the
	// catalogue genuinely shrinking that far.
	coverageCollapseRatio = 0.5
	// minStoredForCoverageCheck keeps the ratio off small studios, where one
	// or two scenes swing it past any threshold.
	minStoredForCoverageCheck = 10
)

func coverageOf(existing map[sceneKey]models.Scene, seen map[sceneKey]bool, tr traversal) coverage {
	cov := coverage{fresh: len(seen), storedAll: len(existing), traversal: tr}
	for k, s := range existing {
		if !seen[k] {
			cov.dropped++
		}
		if s.DeletedAt != nil {
			continue
		}
		cov.stored++
		if seen[k] {
			cov.seen++
		}
	}
	return cov
}

// collapsed reports a coverage drop worth interrupting for. An incomplete
// traversal or an early stop both explain a small fresh set on their own and
// have their own handling, so neither counts.
func (c coverage) collapsed() bool {
	if c.stored < minStoredForCoverageCheck || c.incomplete || c.stoppedEarly {
		return false
	}
	return float64(c.seen)/float64(c.stored) < coverageCollapseRatio
}

func (c coverage) percent() int {
	if c.stored == 0 {
		return 0
	}
	return int(float64(c.seen) / float64(c.stored) * 100)
}

// absentPolicy is what a run does with stored scenes it did not re-collect.
type absentPolicy int

const (
	absentDrop       absentPolicy = iota // --full: authoritative, unreached scenes are dropped
	absentKeep                           // incremental: merge, unreached scenes stay as stored
	absentSoftDelete                     // --refresh: unreached live scenes are stamped deleted
)

// mergeOutcome is the result of folding one run's fresh scenes into the
// stored ones.
type mergeOutcome struct {
	scenes       []models.Scene
	cov          coverage
	newCount     int // fresh scenes not previously stored
	carried      int // unreached scenes kept despite absentDrop, because the traversal was incomplete
	newlyDeleted int // scenes soft-deleted by this run
}

// mergeRun is the one place the three scrape modes differ: the absent policy.
// An incomplete traversal never drops or soft-deletes, since an unreached scene
// may simply not have been fetched.
func mergeRun(existing, fresh []models.Scene, tr traversal, policy absentPolicy, preserve bool, now time.Time) mergeOutcome {
	existingByKey := make(map[sceneKey]models.Scene, len(existing))
	for _, s := range existing {
		existingByKey[keyOf(s)] = s
	}

	out := mergeOutcome{scenes: make([]models.Scene, 0, len(fresh)+len(existing))}
	seen := make(map[sceneKey]bool, len(fresh))
	for _, s := range fresh {
		k := keyOf(s)
		if seen[k] {
			continue // duplicate (id, site_id) from pagination overlap
		}
		seen[k] = true
		if prev, ok := existingByKey[k]; ok {
			s = carryOver(s, prev, preserve)
		} else {
			out.newCount++
		}
		out.scenes = append(out.scenes, s)
	}

	for _, s := range existing {
		if seen[keyOf(s)] {
			continue
		}
		switch {
		case policy == absentDrop && !tr.incomplete:
			continue
		case policy == absentDrop:
			out.carried++
		case policy == absentSoftDelete && !tr.incomplete && s.DeletedAt == nil:
			t := now
			s.DeletedAt = &t
			out.newlyDeleted++
		}
		out.scenes = append(out.scenes, s)
	}

	out.cov = coverageOf(existingByKey, seen, tr)
	return out
}

// carryOver folds a stored scene into the freshly-scraped one that replaces it:
// price history is always replayed, and unless the operator opted out, fields
// the fresh scrape returned empty fall back to the stored value.
func carryOver(fresh, existing models.Scene, preserve bool) models.Scene {
	if preserve {
		fresh = preserveEnrichment(fresh, existing)
	}
	return carryOverPriceHistory(fresh, existing)
}

// preserveEnrichment restores fields that the fresh scrape left empty from the
// stored scene.
//
// Save is authoritative, so without this a parser that breaks on a site redesign
// and starts returning blank tags, performers or descriptions silently replaces
// good stored metadata with nothing on the next --refresh. Price history had
// this protection; nothing else did.
//
// The trade is deliberate: a field the site genuinely emptied stays populated
// here. Losing a whole catalogue's metadata to a parser regression is the worse
// failure, and `--no-preserve` exists for a real rebuild. Only wholly-empty
// fields are restored — a scrape returning 2 of 3 tags still wins outright.
func preserveEnrichment(fresh, existing models.Scene) models.Scene {
	keepStr := func(f, e string) string {
		if f == "" {
			return e
		}
		return f
	}
	keepInt := func(f, e int) int {
		if f == 0 {
			return e
		}
		return f
	}
	keepSlice := func(f, e []string) []string {
		if len(f) == 0 {
			return e
		}
		return f
	}

	fresh.Title = keepStr(fresh.Title, existing.Title)
	fresh.URL = keepStr(fresh.URL, existing.URL)
	fresh.Description = keepStr(fresh.Description, existing.Description)
	fresh.Thumbnail = keepStr(fresh.Thumbnail, existing.Thumbnail)
	fresh.Preview = keepStr(fresh.Preview, existing.Preview)
	fresh.Director = keepStr(fresh.Director, existing.Director)
	fresh.Studio = keepStr(fresh.Studio, existing.Studio)
	fresh.Series = keepStr(fresh.Series, existing.Series)
	fresh.Resolution = keepStr(fresh.Resolution, existing.Resolution)
	fresh.Format = keepStr(fresh.Format, existing.Format)

	fresh.SeriesPart = keepInt(fresh.SeriesPart, existing.SeriesPart)
	fresh.Duration = keepInt(fresh.Duration, existing.Duration)
	fresh.Width = keepInt(fresh.Width, existing.Width)
	fresh.Height = keepInt(fresh.Height, existing.Height)
	fresh.Views = keepInt(fresh.Views, existing.Views)
	fresh.Likes = keepInt(fresh.Likes, existing.Likes)
	fresh.Comments = keepInt(fresh.Comments, existing.Comments)

	fresh.Performers = keepSlice(fresh.Performers, existing.Performers)
	fresh.Tags = keepSlice(fresh.Tags, existing.Tags)
	fresh.Categories = keepSlice(fresh.Categories, existing.Categories)

	if fresh.Date.IsZero() {
		fresh.Date = existing.Date
	}

	// External IDs accumulate across scrapes — different sources learn the
	// scene at different times — so union rather than replace.
	for source, id := range existing.ExternalIDs {
		if fresh.ExternalIDs == nil {
			fresh.ExternalIDs = map[string]string{}
		}
		if _, ok := fresh.ExternalIDs[source]; !ok {
			fresh.ExternalIDs[source] = id
		}
	}

	return fresh
}

// carryOverPriceHistory replays the existing scene's price history plus every
// fresh snapshot through AddPrice so LowestPrice is recomputed correctly.
// When the fresh scene has no price data (e.g. site stopped returning pricing),
// existing history is preserved so no data is lost.
//
// Each scraper today emits at most one snapshot per scrape, but the function
// must not bake that assumption in — if a scraper starts emitting multiple
// snapshots (backfill, multi-tier pricing, batched updates), every one of them
// should land in the carried-over history.
func carryOverPriceHistory(fresh, existing models.Scene) models.Scene {
	freshSnaps := fresh.PriceHistory
	fresh.PriceHistory = nil
	fresh.LowestPrice = 0
	fresh.LowestPriceDate = nil
	for _, snap := range existing.PriceHistory {
		fresh.AddPrice(snap)
	}
	for _, snap := range freshSnaps {
		fresh.AddPrice(snap)
	}
	return fresh
}
