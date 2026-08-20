package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Anastylosis/FSS/internal/creators"
	"github.com/Anastylosis/FSS/internal/store"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/output"
	"github.com/Anastylosis/FSS/scraper"
)

var scrapeCmd = &cobra.Command{
	Use:   "scrape [studio-url ...]",
	Short: "Scrape all scenes from one or more studio URLs, or from a creator's storefronts",
	Long: `Scrape all scenes from one or more studio URLs.

Studios can be named directly, or selected through creators.d — one YAML file
per creator listing the several storefronts one person sells on:

    fss scrape https://clipmarket.example/studio/4021/mara-vance
    fss scrape --creator "Mara Vance"
    fss scrape --all-creators --stale 7d`,
	Args: cobra.ArbitraryArgs,
	RunE: runScrape,
}

func init() {
	rootCmd.AddCommand(scrapeCmd)

	scrapeCmd.Flags().IntP("workers", "w", 0, "max parallel fetchers (0 = use config/default)")
	scrapeCmd.Flags().Bool("full", false, "ignore existing data, scrape everything from scratch")
	scrapeCmd.Flags().Bool("refresh", false, "re-fetch metadata for all known scenes, soft-delete missing")
	scrapeCmd.Flags().Bool("force", false, "allow a 0-scene or collapsed-coverage --full/--refresh to overwrite a populated studio")
	scrapeCmd.Flags().Bool("no-preserve", false, "let a re-scrape blank fields it no longer returns (default: keep the stored value)")
	scrapeCmd.Flags().StringP("output", "o", "", "export formats: json, csv, or json,csv (default from config)")
	scrapeCmd.Flags().String("out-dir", "", "output directory (default from config)")
	scrapeCmd.Flags().String("db", "", "enable SQLite store (no value = the configured db, or ~/.local/share/fss/fss.db; or pass a path)")
	scrapeCmd.Flags().Lookup("db").NoOptDefVal = "default"
	scrapeCmd.Flags().String("name", "", "human-readable label for this studio (stored when --db is set)")
	scrapeCmd.Flags().Int("delay", 0, "milliseconds between page requests (default 500 from config; 0 = no delay)")
	scrapeCmd.Flags().StringSlice("site-delay", nil, "per-scraper delay override, e.g. --site-delay manyvids=0,pornhub=2000 (overrides --delay for matching sites)")
	scrapeCmd.Flags().StringArray("performer", nil, "replace the performers on every scene this run scrapes (repeat, or comma-separate, for several)")
	scrapeCmd.Flags().String("studio", "", "replace the studio on every scene this run scrapes")
	scrapeCmd.Flags().StringArray("creator", nil, "scrape every storefront defined for this creator in creators.d (repeatable)")
	scrapeCmd.Flags().Bool("all-creators", false, "scrape every storefront of every creator in creators.d")
	scrapeCmd.Flags().String("stale", "", "only scrape studios not scraped within this window, e.g. 12h, 7d, 2w (needs --db)")
	scrapeCmd.Flags().String("creators-dir", "", "directory of creator YAML files (default: config creators_dir, else ~/.config/fss/creators.d)")
}

func runScrape(cmd *cobra.Command, args []string) error {
	// URLs are normalised once, inside resolveScrapeTargets: the result is what
	// gets matched, fetched, and used as the store key.
	targets, err := resolveScrapeTargets(cmd, args)
	if err != nil {
		return err
	}

	// --- resolve flags against config ---
	full, _ := cmd.Flags().GetBool("full")
	refresh, _ := cmd.Flags().GetBool("refresh")
	force, _ := cmd.Flags().GetBool("force")
	noPreserve, _ := cmd.Flags().GetBool("no-preserve")
	if full && refresh {
		return fmt.Errorf("--full and --refresh are mutually exclusive")
	}

	workers, _ := cmd.Flags().GetInt("workers")
	if workers <= 0 {
		workers = cfg.Workers
	}

	outputFlag, _ := cmd.Flags().GetString("output")
	outputStr := cfg.Output
	if outputFlag != "" {
		outputStr = outputFlag
	}
	formats, err := parseFormats(outputStr)
	if err != nil {
		return err
	}

	outDir, _ := cmd.Flags().GetString("out-dir")
	if outDir == "" {
		outDir = cfg.OutDir
	}

	dbPath := resolveDBPath(cmd)

	name, _ := cmd.Flags().GetString("name")
	if name != "" && len(targets) > 1 {
		return fmt.Errorf("--name cannot be used when scraping multiple URLs")
	}
	if name != "" && dbPath == "" {
		fmt.Fprintln(os.Stderr, "warning: --name has no effect without --db (studio names are only stored in SQLite)")
	}

	delayMS := cfg.Delay
	if cmd.Flags().Changed("delay") {
		delayMS, _ = cmd.Flags().GetInt("delay")
	}
	defaultDelay := time.Duration(delayMS) * time.Millisecond

	// Per-site delay overrides: config file first, then CLI --site-delay merged on top.
	siteDelayPairs, _ := cmd.Flags().GetStringSlice("site-delay")
	siteDelays, err := mergeSiteDelays(cfg.SiteDelays, siteDelayPairs)
	if err != nil {
		return err
	}

	overrides, err := parseOverrides(cmd)
	if err != nil {
		return err
	}
	// Creator files normalise storefront branding credited as a performer.
	// Loaded for every run, not just --creator ones: naming a store's URL
	// directly must store the same names as reaching it through its creator,
	// or the two routes disagree about who is in a scene.
	creatorList, err := loadCreators(cmd)
	if err != nil {
		return err
	}
	overrides.canon = creators.NewCanon(creatorList)

	// --- pick store (opened once, shared across all URLs) ---
	var st store.Store
	if dbPath != "" {
		if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
			return fmt.Errorf("creating database directory: %w", err)
		}
		db, err := store.NewSQLite(dbPath)
		if err != nil {
			return fmt.Errorf("opening database: %w", err)
		}
		defer func() { _ = db.Close() }()
		st = db
	} else {
		st = store.NewFlat(outDir, formats)
	}

	// Sweep stale `.fss-tmp-*` orphans left by a previous process that
	// was SIGKILLed between os.CreateTemp and the deferred os.Remove
	// inside atomicWriteFile. The 10-minute floor guarantees we never
	// race a concurrent live writer.
	if outDir != "" {
		if n := output.SweepStaleTempFiles(outDir, 10*time.Minute); n > 0 {
			scraper.Debugf(1, "swept %d stale .fss-tmp-* file(s) from %s", n, outDir)
		}
	}

	// --stale is applied after the store is open so it can read the studios
	// table, and before any fetching so a cron run that has nothing due exits
	// without touching the network.
	if staleFlag, _ := cmd.Flags().GetString("stale"); staleFlag != "" {
		maxAge, err := parseStaleDuration(staleFlag)
		if err != nil {
			return err
		}
		targets, err = filterStale(targets, maxAge, st, dbPath)
		if err != nil {
			return err
		}
		if len(targets) == 0 {
			return nil
		}
		fmt.Println()
	}

	// --- graceful shutdown on Ctrl+C ---
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// URLs are scraped sequentially. --workers controls intra-scrape concurrency
	// only; we deliberately do NOT run multiple URLs in parallel so the worker
	// cap is the global cap (no N×workers explosion against unrelated hosts).
	var firstErr error
	for i, tgt := range targets {
		if i > 0 {
			fmt.Println()
		}
		if err := scrapeOne(ctx, st, tgt, name, dbPath, outDir, formats, full, refresh, force, !noPreserve, workers, defaultDelay, siteDelays, overrides); err != nil {
			fmt.Fprintf(os.Stderr, "error scraping %s: %v\n", tgt.url, err)
			if firstErr == nil {
				firstErr = err
			}
		}
		if ctx.Err() != nil {
			break
		}
	}
	return firstErr
}

func scrapeOne(ctx context.Context, st store.Store, tgt scrapeTarget, name, dbPath, outDir string, formats []string, full, refresh, force, preserve bool, workers int, defaultDelay time.Duration, siteDelays map[string]int, ov sceneOverrides) error {
	start := time.Now()
	studioURL := tgt.url

	sc, err := scraper.ForURL(studioURL)
	if err != nil {
		return err
	}

	unlock, err := st.Lock(studioURL)
	if err != nil {
		return fmt.Errorf("locking studio: %w", err)
	}
	defer func() { _ = unlock.Close() }()

	delay := resolveTargetDelay(tgt, sc.ID(), defaultDelay, siteDelays)
	scraper.Debugf(1, "scraper: %s, delay: %v, workers: %d", sc.ID(), delay, workers)

	if !ov.empty() {
		fmt.Printf("  overriding: %s\n", ov.describe())
	}

	var scenes []models.Scene
	var cov coverage
	switch {
	case full:
		fmt.Printf("Full scrape: %s\n", tgt.label())
		scenes, cov, err = scrapeAll(ctx, sc, st, studioURL, workers, delay, preserve, ov)
	case refresh:
		fmt.Printf("Refresh scrape: %s\n", tgt.label())
		scenes, cov, err = scrapeRefresh(ctx, sc, st, studioURL, workers, delay, preserve, ov)
	default:
		fmt.Printf("Incremental scrape: %s\n", tgt.label())
		scenes, cov, err = scrapeIncremental(ctx, sc, st, studioURL, workers, delay, preserve, ov)
	}
	if err != nil {
		return err
	}

	if name == "" && len(scenes) > 0 && scenes[0].Studio != "" {
		name = scenes[0].Studio
	}

	// A2: a clean 0-scene --full/--refresh would hard-delete (or soft-delete)
	// every stored scene — almost always an HTML/JSON drift, not a real empty
	// studio. Refuse the destructive Save when the studio already has data,
	// unless --force is given. (Incremental merges, so it is never destructive.)
	if len(scenes) == 0 && (full || refresh) && !force {
		existing, lerr := st.Load(studioURL)
		if lerr == nil && len(existing) > 0 {
			fmt.Fprintf(os.Stderr,
				"warning: scrape returned 0 scenes but %d are already stored for %s — refusing destructive save (use --force to override)\n",
				len(existing), studioURL)
			return nil
		}
	}

	// A scraper can break without failing: the site drops its pagination, or a
	// redesign leaves one card template matching. The 0-scene guard above only
	// catches the total loss. Everything short of that reaches here looking
	// like a clean run, so check it against what is already stored.
	if len(scenes) > 0 && cov.collapsed() && !proceedAfterCollapse(cov, studioURL, sc.ID(), full || refresh, force) {
		return nil
	}

	// Normalise StudioURL to the URL being saved. Some scrapers stamp a
	// canonical/derived URL on each scene that differs from the requested
	// studioURL; the SQLite store keys rows by (id, site_id, studio_url) and
	// Load/delete filter on the Save parameter, so a mismatch would orphan the
	// rows. This also keeps the Flat store's stored StudioURL consistent.
	for i := range scenes {
		scenes[i].StudioURL = studioURL
	}

	if err := st.Save(studioURL, scenes); err != nil {
		return fmt.Errorf("saving results: %w", err)
	}

	if dbPath != "" {
		slug := output.Slugify(studioURL)
		for _, format := range formats {
			path := filepath.Join(outDir, slug+"."+format)
			if err := st.Export(format, path, studioURL); err != nil {
				return fmt.Errorf("exporting %s: %w", format, err)
			}
		}
	}

	now := time.Now().UTC()
	if uErr := st.UpsertStudio(models.Studio{
		URL:           studioURL,
		SiteID:        sc.ID(),
		Name:          name,
		AddedAt:       now,
		LastScrapedAt: &now,
	}); uErr != nil {
		fmt.Fprintf(os.Stderr, "warning: could not update studio record: %v\n", uErr)
	}

	elapsed := time.Since(start).Round(time.Second)
	if ctx.Err() != nil {
		fmt.Printf("Partial save complete: %d scenes (%s elapsed).\n", len(scenes), elapsed)
	} else {
		fmt.Printf("Done: %d scenes saved (%s elapsed).\n", len(scenes), elapsed)
	}
	return nil
}

// scrapeAll does a full traversal — no KnownIDs early-stop hint — but still
// carries forward existing price history so the historical pricing record is
// preserved across --full re-scrapes. Unlike --refresh, scenes that no longer
// appear on the site are dropped rather than soft-deleted.
// sceneKey identifies a scene within a studio. A studio URL can hold scenes
// from more than one site_id (aggregators, multi-brand scrapers), so merge and
// dedup must key on (id, site_id) — id alone collides across sites.
type sceneKey struct{ id, siteID string }

func keyOf(s models.Scene) sceneKey { return sceneKey{id: s.ID, siteID: s.SiteID} }

// traversal reports how a scrape ended, beyond the scenes it produced.
type traversal struct {
	incomplete   bool // fetch errors cost scenes, or the run was cancelled
	stoppedEarly bool // the KnownIDs early-stop fired
}

// coverage measures a scrape against what the store already holds. A scraper
// that still works but no longer reaches most of the catalogue — a site that
// drops its pagination, or a parser that only matches one of two card
// templates — looks like a clean success to every other check here.
type coverage struct {
	stored int // distinct scenes already in the store
	seen   int // stored scenes this scrape re-collected
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
	cov := coverage{stored: len(existing), traversal: tr}
	for k := range existing {
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

func scrapeAll(ctx context.Context, sc scraper.StudioScraper, st store.Store, studioURL string, workers int, delay time.Duration, preserve bool, ov sceneOverrides) ([]models.Scene, coverage, error) {
	existing, err := st.Load(studioURL)
	if err != nil {
		return nil, coverage{}, fmt.Errorf("loading existing scenes: %w", err)
	}
	existingByKey := make(map[sceneKey]models.Scene, len(existing))
	for _, s := range existing {
		existingByKey[keyOf(s)] = s
	}

	fresh, tr, err := collectScenes(ctx, sc, studioURL, scraper.ListOpts{Workers: workers, Delay: delay}, ov)
	if err != nil {
		return nil, coverage{}, err
	}

	result := make([]models.Scene, 0, len(fresh))
	seen := make(map[sceneKey]bool, len(fresh))
	for _, s := range fresh {
		k := keyOf(s)
		if seen[k] {
			continue // drop duplicate (id, site_id) from pagination overlap
		}
		seen[k] = true
		if prev, ok := existingByKey[k]; ok {
			s = carryOver(s, prev, preserve)
		}
		result = append(result, s)
	}

	// A1: an incomplete traversal (fetch errors / interrupted) must not let the
	// authoritative Save hard-delete scenes that simply weren't reached. Fall
	// back to merge semantics — carry forward existing scenes not re-collected.
	if tr.incomplete {
		carried := 0
		for _, s := range existing {
			if !seen[keyOf(s)] {
				result = append(result, s)
				carried++
			}
		}
		fmt.Fprintf(os.Stderr, "warning: traversal incomplete — preserving %d existing scene(s) and skipping destructive --full delete\n", carried)
	}
	return result, coverageOf(existingByKey, seen, tr), nil
}

// scrapeIncremental loads existing scene IDs, passes them to the scraper as a
// hint for early-stop optimisation (date-sorted sites), then merges results.
//
// Scrapers that cannot use early-stop (e.g. recommended-sorted sites) may emit
// known scenes in correct site order. In that case fresh takes priority and
// price history is carried forward so no history is lost.
func scrapeIncremental(ctx context.Context, sc scraper.StudioScraper, st store.Store, studioURL string, workers int, delay time.Duration, preserve bool, ov sceneOverrides) ([]models.Scene, coverage, error) {
	existing, err := st.Load(studioURL)
	if err != nil {
		return nil, coverage{}, fmt.Errorf("loading existing scenes: %w", err)
	}

	knownIDs := make(map[string]bool, len(existing))
	existingByKey := make(map[sceneKey]models.Scene, len(existing))
	for _, s := range existing {
		knownIDs[s.ID] = true
		existingByKey[keyOf(s)] = s
	}

	// Incremental already merges fresh with existing, so a partial traversal is
	// inherently non-destructive — the incomplete flag needs no special handling.
	fresh, tr, err := collectScenes(ctx, sc, studioURL, scraper.ListOpts{Workers: workers, KnownIDs: knownIDs, Delay: delay}, ov)
	if err != nil {
		return nil, coverage{}, err
	}

	// Merge: emit fresh scenes first (preserving scraper order); carry price
	// history when a fresh scene was already stored (happens when a scraper
	// re-emits known IDs rather than skipping them). Append any existing scenes
	// that were not re-emitted (older entries on date-sorted sites).
	freshKeys := make(map[sceneKey]bool, len(fresh))
	result := make([]models.Scene, 0, len(fresh)+len(existing))
	newCount := 0
	for _, s := range fresh {
		k := keyOf(s)
		if freshKeys[k] {
			continue // drop duplicate (id, site_id) from pagination overlap
		}
		freshKeys[k] = true
		if prev, ok := existingByKey[k]; ok {
			s = carryOver(s, prev, preserve)
		} else {
			newCount++
		}
		result = append(result, s)
	}
	for _, s := range existing {
		if !freshKeys[keyOf(s)] {
			result = append(result, s)
		}
	}

	fmt.Printf("  %d new, %d existing → %d total\n", newCount, len(existing), len(result))
	return result, coverageOf(existingByKey, freshKeys, tr), nil
}

// scrapeRefresh re-fetches all scenes and soft-deletes any that have disappeared.
// Price history from prior scrapes is carried forward onto each re-fetched scene.
func scrapeRefresh(ctx context.Context, sc scraper.StudioScraper, st store.Store, studioURL string, workers int, delay time.Duration, preserve bool, ov sceneOverrides) ([]models.Scene, coverage, error) {
	existing, err := st.Load(studioURL)
	if err != nil {
		return nil, coverage{}, fmt.Errorf("loading existing scenes: %w", err)
	}
	existingByKey := make(map[sceneKey]models.Scene, len(existing))
	for _, s := range existing {
		existingByKey[keyOf(s)] = s
	}

	// Full traversal — no KnownIDs
	fresh, tr, err := collectScenes(ctx, sc, studioURL, scraper.ListOpts{Workers: workers, Delay: delay}, ov)
	if err != nil {
		return nil, coverage{}, err
	}

	// Build result: fresh scenes with accumulated price history.
	scrapedKeys := make(map[sceneKey]bool, len(fresh))
	result := make([]models.Scene, 0, len(existing))
	for _, s := range fresh {
		k := keyOf(s)
		if scrapedKeys[k] {
			continue // drop duplicate (id, site_id) from pagination overlap
		}
		scrapedKeys[k] = true
		if prev, ok := existingByKey[k]; ok {
			s = carryOver(s, prev, preserve)
		}
		result = append(result, s)
	}

	// Carry forward previously-deleted scenes; mark newly-missing ones as
	// deleted. A1: when the traversal was incomplete, an unreached scene may
	// simply not have been fetched, so do NOT soft-delete on a partial run —
	// carry every existing scene forward with its current DeletedAt intact.
	now := time.Now().UTC()
	newlyDeleted := 0
	for _, s := range existing {
		if scrapedKeys[keyOf(s)] {
			continue
		}
		if !tr.incomplete && s.DeletedAt == nil {
			s.DeletedAt = &now
			newlyDeleted++
		}
		result = append(result, s)
	}
	if tr.incomplete {
		fmt.Fprintln(os.Stderr, "warning: traversal incomplete — skipping --refresh soft-delete of unreached scenes")
	}

	if newlyDeleted > 0 {
		fmt.Printf("  %d scenes no longer found, marked deleted\n", newlyDeleted)
	}
	fmt.Printf("  %d scraped, %d total\n", len(fresh), len(result))
	return result, coverageOf(existingByKey, scrapedKeys, tr), nil
}

// collectScenes drains the scraper channel, printing a live count and warnings.
// The second return value reports whether the traversal was *incomplete* — any
// fetch error or a cancelled context — so authoritative-Save callers (--full /
// --refresh) can fall back to non-destructive merge semantics instead of
// treating a partial run as the studio's full state.
func collectScenes(ctx context.Context, sc scraper.StudioScraper, studioURL string, opts scraper.ListOpts, ov sceneOverrides) ([]models.Scene, traversal, error) {
	ch, err := sc.ListScenes(ctx, studioURL, opts)
	if err != nil {
		return nil, traversal{incomplete: true}, fmt.Errorf("starting scrape: %w", err)
	}
	// Resolved once rather than per scene: whether a storefront belongs to a
	// creator is a property of the run, not of the results.
	canon := ov.canon.For(studioURL)
	if canon.Active() {
		fmt.Printf("  performer credits normalised to %q (creators.d)\n", canon.Name())
	}
	start := time.Now()
	var scenes []models.Scene
	errCount := 0
	missing := 0 // errors that cost us scenes — see scraper.FailureKind
	byKind := map[scraper.FailureKind]int{}
	total := 0
	stoppedEarly := false
	for result := range ch {
		switch result.Kind {
		case scraper.KindTotal:
			total = result.Total
			continue
		case scraper.KindStoppedEarly:
			stoppedEarly = true
			continue
		case scraper.KindError:
			errCount++
			kind := scraper.Classify(result.Err)
			byKind[kind]++
			if kind.MissingData() {
				missing++
			}
			fmt.Fprintf(os.Stderr, "\rwarning: %v\n", result.Err)
			continue
		case scraper.KindScene:
		}
		sc := result.Scene
		sc.Performers = canon.Apply(sc.Studio, sc.Performers)
		scenes = append(scenes, ov.apply(sc))
		elapsed := time.Since(start).Round(time.Second)
		if total > 0 {
			fmt.Printf("\r  fetching: %d / %d scenes (%s elapsed)", len(scenes), total, elapsed)
		} else {
			fmt.Printf("\r  fetching: %d scenes (%s elapsed)", len(scenes), elapsed)
		}
	}
	fmt.Println() // end the progress line
	if stoppedEarly {
		fmt.Println("  stopped early at known ID — remaining scenes already stored")
	}
	if errCount > 0 {
		fmt.Fprintf(os.Stderr, "  %d fetch error(s) [%s] — see warnings above\n", errCount, summarizeFailures(byKind))
	}
	if ctx.Err() != nil {
		fmt.Printf("Interrupted — saving %d partial results...\n", len(scenes))
	}
	if errCount > 0 && len(scenes) == 0 {
		return nil, traversal{incomplete: true}, fmt.Errorf("scrape failed with %d error(s) and 0 scenes", errCount)
	}
	// Only failures that cost us scenes make a traversal incomplete. A run
	// whose sole errors were absent resources (an optional sub-listing the
	// site does not have) saw everything there was to see, and used to be
	// demoted to non-authoritative for no reason.
	return scenes, traversal{incomplete: missing > 0 || ctx.Err() != nil, stoppedEarly: stoppedEarly}, nil
}

// summarizeFailures renders the per-kind error breakdown in a stable order, so
// "3 fetch error(s) [2 transport, 1 parse]" says at a glance whether to retry or
// to go and fix a parser.
func summarizeFailures(byKind map[scraper.FailureKind]int) string {
	order := []scraper.FailureKind{
		scraper.FailureTransport,
		scraper.FailureParse,
		scraper.FailureAbsent,
		scraper.FailureUnknown,
	}
	parts := make([]string, 0, len(order))
	for _, k := range order {
		if n := byKind[k]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, k))
		}
	}
	return strings.Join(parts, ", ")
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

// parseFormats splits "json,csv" into ["json","csv"], trimming spaces and deduplicating.
func parseFormats(s string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, f := range strings.Split(s, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if f != "json" && f != "csv" {
			return nil, fmt.Errorf("unknown output format %q (valid: json, csv)", f)
		}
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out, nil
}

// mergeSiteDelays returns a single map of scraper-ID → delay-ms by overlaying
// CLI --site-delay entries (`name=ms` pairs) on top of the config map. Returns
// an error if any CLI entry is malformed.
func mergeSiteDelays(fromConfig map[string]int, cliPairs []string) (map[string]int, error) {
	out := make(map[string]int, len(fromConfig)+len(cliPairs))
	for k, v := range fromConfig {
		out[k] = v
	}
	for _, p := range cliPairs {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		eq := strings.IndexByte(p, '=')
		if eq < 1 || eq == len(p)-1 {
			return nil, fmt.Errorf("--site-delay: %q is not in name=ms form", p)
		}
		name := strings.TrimSpace(p[:eq])
		ms, err := strconv.Atoi(strings.TrimSpace(p[eq+1:]))
		if err != nil {
			return nil, fmt.Errorf("--site-delay %q: ms must be an integer: %w", p, err)
		}
		if ms < 0 {
			return nil, fmt.Errorf("--site-delay %q: ms must not be negative", p)
		}
		out[name] = ms
	}
	return out, nil
}

// resolveSiteDelay returns the per-site delay for siteID, falling back to
// defaultDelay when the site has no override. A site explicitly set to 0
// disables delay even when the global default is non-zero.
func resolveSiteDelay(siteID string, defaultDelay time.Duration, perSite map[string]int) time.Duration {
	if ms, ok := perSite[siteID]; ok {
		return time.Duration(ms) * time.Millisecond
	}
	return defaultDelay
}

// stdinIsTerminal and promptIn are indirected so tests can drive the prompt.
var (
	stdinIsTerminal = func() bool {
		fi, err := os.Stdin.Stat()
		return err == nil && fi.Mode()&os.ModeCharDevice != 0
	}
	promptIn io.Reader = os.Stdin
)

// proceedAfterCollapse reports whether to go ahead with the save after a
// coverage collapse. Incremental merges, so it only reports; --full/--refresh
// hand the stored scenes to an authoritative Save and ask first.
func proceedAfterCollapse(cov coverage, studioURL, siteID string, destructive, force bool) bool {
	fmt.Fprintf(os.Stderr,
		"\n[warn] %s: this scrape re-saw %d of %d stored scenes (%d%%) — the site or the scraper may have changed\n",
		siteID, cov.seen, cov.stored, cov.percent())

	if !destructive {
		fmt.Fprintf(os.Stderr, "       incremental keeps the other %d, so nothing is lost; check %s if this is unexpected\n",
			cov.stored-cov.seen, studioURL)
		return true
	}

	fmt.Fprintf(os.Stderr, "       this is an authoritative save: %d stored scene(s) would be dropped\n", cov.stored-cov.seen)
	if force {
		fmt.Fprintln(os.Stderr, "       --force given, proceeding")
		return true
	}
	if !stdinIsTerminal() {
		fmt.Fprintln(os.Stderr, "       not a terminal — skipping the save (pass --force to allow it unattended)")
		return false
	}

	fmt.Fprint(os.Stderr, "       proceed anyway? [y/N] ")
	line, err := bufio.NewReader(promptIn).ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(os.Stderr, "       no answer — skipping the save")
		return false
	}
	// English-only, deliberately: translating the prompt without also
	// translating this parser is a bug. See docs/translations.md.
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		fmt.Fprintln(os.Stderr, "       skipping the save")
		return false
	}
}
