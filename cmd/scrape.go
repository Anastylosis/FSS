package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Anastylosis/FSS/internal/config"
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

    fss scrape https://clipmarket.example/studio/4021/jane-eyre
    fss scrape --creator "Jane Eyre"
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
	scrapeCmd.Flags().StringSlice("site-cookie", nil, "per-scraper Cookie header, e.g. --site-cookie mydirtyhobby=\"KEY=abc; other=1\" (for a gate the operator has passed in a browser)")
	scrapeCmd.Flags().String("content-language", "", "content language to request where a site publishes in several, e.g. --content-language de (sites with one language ignore it)")
	scrapeCmd.Flags().StringSlice("site-language", nil, "per-scraper content language, e.g. --site-language mydirtyhobby=de (overrides --content-language)")
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
		if delayMS < 0 {
			return fmt.Errorf("--delay must not be negative, got %d", delayMS)
		}
	}
	defaultDelay := time.Duration(delayMS) * time.Millisecond

	// Per-site delay overrides: config file first, then CLI --site-delay merged on top.
	siteDelayPairs, _ := cmd.Flags().GetStringSlice("site-delay")
	siteDelays, err := mergeSiteDelays(cfg.SiteDelays, siteDelayPairs)
	if err != nil {
		return err
	}

	if err := checkSiteIDs(cfg, cmd); err != nil {
		return err
	}

	// Per-site cookies, resolved the same way.
	siteCookiePairs, _ := cmd.Flags().GetStringSlice("site-cookie")
	siteCookies, err := mergeSiteCookies(cfg.SiteCookies, siteCookiePairs)
	if err != nil {
		return err
	}

	// Content language, not --lang: this one changes what is scraped.
	defaultLanguage := cfg.ContentLanguage
	if cmd.Flags().Changed("content-language") {
		defaultLanguage, _ = cmd.Flags().GetString("content-language")
	}
	if defaultLanguage, err = scraper.NormalizeLanguage(defaultLanguage); err != nil {
		return fmt.Errorf("content language: %w", err)
	}
	siteLanguagePairs, _ := cmd.Flags().GetStringSlice("site-language")
	siteLanguages, err := mergeSiteLanguages(cfg.SiteLanguages, siteLanguagePairs)
	if err != nil {
		return err
	}

	sites := siteSettings{
		defaultDelay:    defaultDelay,
		delays:          siteDelays,
		cookies:         siteCookies,
		defaultLanguage: defaultLanguage,
		languages:       siteLanguages,
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
	overrides.canon = creators.NewCanon(creatorList, studioKey)

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
	worst := 0
	note := func(err error) {
		if firstErr == nil {
			firstErr = err
		}
		if c := exitCodeOf(err); exitPriority(c) > exitPriority(worst) {
			worst = c
		}
	}
	run := scrapeRun{name: name, dbPath: dbPath, outDir: outDir, formats: formats, full: full, refresh: refresh,
		force: force, preserve: !noPreserve, workers: workers, sites: sites, ov: overrides}
	for i, tgt := range targets {
		if i > 0 {
			fmt.Println()
		}
		if err := run.one(ctx, st, tgt); err != nil {
			if !isReported(err) {
				fmt.Fprintf(os.Stderr, "error scraping %s: %v\n", tgt.url, err)
			}
			note(err)
		}
		if ctx.Err() != nil {
			break
		}
	}
	if ctx.Err() != nil {
		note(&exitError{code: ExitInterrupted, err: errors.New("interrupted")})
	}
	if firstErr == nil {
		return nil
	}
	return &exitError{code: worst, err: firstErr, reported: true}
}

// siteSettings is the per-site knobs a run resolves once from config and flags:
// delay, cookie and content language.
type siteSettings struct {
	defaultDelay    time.Duration
	delays          map[string]int
	cookies         map[string]string
	defaultLanguage string
	languages       map[string]string
}

// scrapeRun is the per-invocation configuration shared by every target.
type scrapeRun struct {
	name, dbPath, outDir string
	formats              []string
	full, refresh        bool
	force, preserve      bool
	workers              int
	sites                siteSettings
	ov                   sceneOverrides
}

func scrapeOne(ctx context.Context, st store.Store, tgt scrapeTarget, name, dbPath, outDir string, formats []string, full, refresh, force, preserve bool, workers int, sites siteSettings, ov sceneOverrides) error {
	return scrapeRun{name, dbPath, outDir, formats, full, refresh, force, preserve, workers, sites, ov}.one(ctx, st, tgt)
}

func (r scrapeRun) one(ctx context.Context, st store.Store, tgt scrapeTarget) error {
	name, dbPath, outDir, formats := r.name, r.dbPath, r.outDir, r.formats
	full, refresh, force, preserve := r.full, r.refresh, r.force, r.preserve
	workers, sites, ov := r.workers, r.sites, r.ov
	start := time.Now()
	studioURL := tgt.url

	sc, err := scraper.ForURL(studioURL)
	if err != nil {
		return err
	}

	// A scraper that does not recognise a filtered URL falls through to its
	// whole catalogue and stores the result under the filtered URL's key,
	// which reads as a successful scrape of something else entirely. Patterns()
	// is what the scraper claims to accept, so a non-root URL matching none of
	// its non-root patterns is worth saying out loud — it is either a URL form
	// the scraper does not handle, or one it handles but never advertised.
	if scraper.URLLooksUnhandled(sc, studioURL) {
		scraper.WarnURLFallthrough(sc.ID(), studioURL)
	}

	unlock, err := st.LockContext(ctx, studioURL)
	if err != nil {
		return fmt.Errorf("locking studio: %w", err)
	}
	defer func() { _ = unlock.Close() }()

	delay := resolveTargetDelay(tgt, sc.ID(), sites)
	scraper.Debugf(1, "scraper: %s, delay: %v, workers: %d (0 = scraper default)", sc.ID(), delay, workers)
	noticePacingIsRunWide(delay, workers)

	lang, err := scraper.ResolveLanguage(sc, resolveTargetLanguage(tgt, sc.ID(), sites))
	if err != nil {
		return err
	}

	// One base ListOpts per run; the incremental mode adds KnownIDs to its copy.
	opts := scraper.ListOpts{Workers: workers, Delay: delay, Cookie: sites.cookies[sc.ID()], Language: lang}
	if opts.Cookie != "" {
		scraper.Debugf(1, "scraper: %s, operator-supplied cookie in use", sc.ID())
	}
	if lang != "" {
		fmt.Printf("  language: %s\n", lang)
	}

	if !ov.empty() {
		fmt.Printf("  overriding: %s\n", ov.describe())
	}

	var scenes []models.Scene
	var cov coverage
	switch {
	case full:
		fmt.Printf("Full scrape: %s\n", tgt.label())
		scenes, cov, err = scrapeAll(ctx, sc, st, studioURL, opts, preserve, ov)
	case refresh:
		fmt.Printf("Refresh scrape: %s\n", tgt.label())
		scenes, cov, err = scrapeRefresh(ctx, sc, st, studioURL, opts, preserve, ov)
	default:
		fmt.Printf("Incremental scrape: %s\n", tgt.label())
		scenes, cov, err = scrapeIncremental(ctx, sc, st, studioURL, opts, preserve, ov)
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
	if cov.fresh == 0 && !cov.incomplete && (full || refresh) && !force {
		// --full hard-deletes every stored scene, deleted ones included;
		// --refresh only soft-deletes, so only live scenes are at stake.
		at := cov.storedAll
		if !full {
			at = cov.stored
		}
		if at > 0 {
			fmt.Fprintf(os.Stderr,
				"warning: scrape returned 0 scenes but %d are already stored for %s — refusing destructive save (use --force to override)\n",
				at, studioURL)
			return &exitError{code: ExitRefused, err: fmt.Errorf("refused destructive save: 0 scenes scraped, %d stored", at), reported: true}
		}
	}

	// A scraper can break without failing: the site drops its pagination, or a
	// redesign leaves one card template matching. The 0-scene guard above only
	// catches the total loss. Everything short of that reaches here looking
	// like a clean run, so check it against what is already stored.
	if (cov.fresh > 0 || (!full && !refresh)) && cov.collapsed() && !proceedAfterCollapse(ctx, cov, studioURL, sc.ID(), full || refresh, force) {
		return &exitError{code: ExitRefused, err: fmt.Errorf("save declined: coverage collapsed for %s", studioURL), reported: true}
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
		_, eErr := exportStudio(ctx, st, store.NewFlat(outDir, nil), studioURL, outDir, formats, nil, false, !sameDir(outDir, filepath.Dir(dbPath)))
		if errors.Is(eErr, errExportRefused) {
			fmt.Fprintf(os.Stderr, "warning: %v\n", eErr)
		} else if eErr != nil {
			return eErr
		}
	}

	now := time.Now().UTC()
	var stamp *time.Time
	if !cov.incomplete && ctx.Err() == nil {
		stamp = &now
	}
	if uErr := st.UpsertStudio(models.Studio{
		URL:           studioURL,
		SiteID:        sc.ID(),
		Name:          name,
		AddedAt:       now,
		LastScrapedAt: stamp,
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

// scrapeAll does a full traversal — no KnownIDs early-stop hint. Scenes that no
// longer appear are dropped, unless the traversal was incomplete.
func scrapeAll(ctx context.Context, sc scraper.StudioScraper, st store.Store, studioURL string, opts scraper.ListOpts, preserve bool, ov sceneOverrides) ([]models.Scene, coverage, error) {
	return scrapeMode(ctx, sc, st, studioURL, opts, preserve, ov, absentDrop)
}

// scrapeIncremental passes stored IDs to the scraper as an early-stop hint
// (date-sorted sites), then merges fresh over existing.
func scrapeIncremental(ctx context.Context, sc scraper.StudioScraper, st store.Store, studioURL string, opts scraper.ListOpts, preserve bool, ov sceneOverrides) ([]models.Scene, coverage, error) {
	return scrapeMode(ctx, sc, st, studioURL, opts, preserve, ov, absentKeep)
}

// scrapeRefresh re-fetches everything and soft-deletes scenes that disappeared.
func scrapeRefresh(ctx context.Context, sc scraper.StudioScraper, st store.Store, studioURL string, opts scraper.ListOpts, preserve bool, ov sceneOverrides) ([]models.Scene, coverage, error) {
	return scrapeMode(ctx, sc, st, studioURL, opts, preserve, ov, absentSoftDelete)
}

func scrapeMode(ctx context.Context, sc scraper.StudioScraper, st store.Store, studioURL string, opts scraper.ListOpts, preserve bool, ov sceneOverrides, policy absentPolicy) ([]models.Scene, coverage, error) {
	existing, err := st.Load(studioURL)
	if err != nil {
		return nil, coverage{}, fmt.Errorf("loading existing scenes: %w", err)
	}

	if policy == absentKeep {
		knownIDs := make(map[string]bool, len(existing))
		for _, s := range existing {
			if s.DeletedAt == nil {
				knownIDs[s.ID] = true
			}
		}
		opts.KnownIDs = knownIDs
	}

	fresh, tr, err := collectScenes(ctx, sc, studioURL, opts, ov)
	if err != nil {
		return nil, coverage{}, err
	}

	out := mergeRun(existing, fresh, tr, policy, preserve, time.Now().UTC())
	switch policy {
	case absentDrop:
		if tr.incomplete {
			fmt.Fprintf(os.Stderr, "warning: traversal incomplete — preserving %d existing scene(s) and skipping destructive --full delete\n", out.carried)
		}
	case absentKeep:
		fmt.Printf("  %d new, %d existing → %d total\n", out.newCount, len(existing), len(out.scenes))
	case absentSoftDelete:
		if tr.incomplete {
			fmt.Fprintln(os.Stderr, "warning: traversal incomplete — skipping --refresh soft-delete of unreached scenes")
		}
		if out.newlyDeleted > 0 {
			fmt.Printf("  %d scenes no longer found, marked deleted\n", out.newlyDeleted)
		}
		fmt.Printf("  %d scraped, %d total\n", len(fresh), len(out.scenes))
	}
	return out.scenes, out.cov, nil
}

// collectScenes drains the scraper channel, printing a live count and warnings.
// The second return value reports whether the traversal was *incomplete* — any
// fetch error or a cancelled context — so authoritative-Save callers (--full /
// --refresh) can fall back to non-destructive merge semantics instead of
// treating a partial run as the studio's full state.
func collectScenes(ctx context.Context, sc scraper.StudioScraper, studioURL string, opts scraper.ListOpts, ov sceneOverrides) ([]models.Scene, traversal, error) {
	// One rate limiter for the whole run, shared by every goroutine the scraper
	// starts. Without it a pool of N workers each waited `--delay` between its
	// own requests, so the site saw bursts of N at a time — eight workers at the
	// 500ms default meant ~16 requests a second, not two.
	ctx = scraper.WithPacer(ctx, scraper.NewPacer(opts.Delay))

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
	if missing > 0 && len(scenes) == 0 {
		return nil, traversal{incomplete: true}, fmt.Errorf("scrape failed with %d error(s) and 0 scenes", missing)
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

// mergeSiteCookies returns a single map of scraper-ID → Cookie header value by
// overlaying CLI --site-cookie entries (`name=cookies` pairs) on top of the
// config map. Only the first `=` separates the two, since a cookie value
// contains one of its own (`KEY=abc`).
func mergeSiteCookies(fromConfig map[string]string, cliPairs []string) (map[string]string, error) {
	out := make(map[string]string, len(fromConfig)+len(cliPairs))
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
			return nil, fmt.Errorf("--site-cookie: %q is not in name=cookies form", p)
		}
		name := strings.TrimSpace(p[:eq])
		value := strings.TrimSpace(p[eq+1:])
		if strings.ContainsAny(value, "\r\n") {
			return nil, fmt.Errorf("--site-cookie %q: value must not contain a newline", name)
		}
		out[name] = value
	}
	return out, nil
}

// mergeSiteLanguages overlays CLI --site-language entries on the config map,
// shape-checking each tag before anything is fetched.
func mergeSiteLanguages(fromConfig map[string]string, cliPairs []string) (map[string]string, error) {
	out := make(map[string]string, len(fromConfig)+len(cliPairs))
	for k, v := range fromConfig {
		tag, err := scraper.NormalizeLanguage(v)
		if err != nil {
			return nil, fmt.Errorf("site_languages.%s: %w", k, err)
		}
		out[k] = tag
	}
	for _, p := range cliPairs {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		name, value, ok := strings.Cut(p, "=")
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		if !ok || name == "" || value == "" {
			return nil, fmt.Errorf("--site-language: %q is not in name=language form", p)
		}
		tag, err := scraper.NormalizeLanguage(value)
		if err != nil {
			return nil, fmt.Errorf("--site-language %s: %w", name, err)
		}
		out[name] = tag
	}
	return out, nil
}

// checkSiteIDs rejects an unknown scraper ID on a --site-* flag and warns about
// one in the config file: a typo there leaves the override silently inactive.
func checkSiteIDs(c *config.Config, cmd *cobra.Command) error {
	for _, flag := range []string{"site-delay", "site-cookie", "site-language"} {
		pairs, _ := cmd.Flags().GetStringSlice(flag)
		for _, p := range pairs {
			name, _, _ := strings.Cut(strings.TrimSpace(p), "=")
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if _, err := scraper.ForID(name); err != nil {
				return fmt.Errorf("--%s: unknown scraper ID %q (see `fss list-scrapers`)", flag, name)
			}
		}
	}
	for key, ids := range map[string][]string{
		"site_delays":    mapKeys(c.SiteDelays),
		"site_cookies":   mapKeys(c.SiteCookies),
		"site_languages": mapKeys(c.SiteLanguages),
	} {
		for _, id := range ids {
			if _, err := scraper.ForID(id); err != nil {
				fmt.Fprintf(os.Stderr, "warning: config %s: unknown scraper ID %q; the override will never apply\n", key, id)
			}
		}
	}
	return nil
}

func mapKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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
func proceedAfterCollapse(ctx context.Context, cov coverage, studioURL, siteID string, destructive, force bool) bool {
	fmt.Fprintf(os.Stderr,
		"\n[warn] %s: this scrape re-saw %d of %d stored scenes (%d%%) — the site or the scraper may have changed\n",
		siteID, cov.seen, cov.stored, cov.percent())

	if !destructive {
		fmt.Fprintf(os.Stderr, "       incremental keeps the other %d, so nothing is lost; check %s if this is unexpected\n",
			cov.stored-cov.seen, studioURL)
		return true
	}

	fmt.Fprintf(os.Stderr, "       this is an authoritative save: --full would drop %d stored scene(s), --refresh would mark %d deleted\n", cov.dropped, cov.stored-cov.seen)
	if force {
		fmt.Fprintln(os.Stderr, "       --force given, proceeding")
		return true
	}
	if !stdinIsTerminal() {
		fmt.Fprintln(os.Stderr, "       not a terminal — skipping the save (pass --force to allow it unattended)")
		return false
	}

	fmt.Fprint(os.Stderr, "       proceed anyway? [y/N] ")
	type answer struct {
		line string
		err  error
	}
	in := promptIn
	answered := make(chan answer, 1)
	go func() {
		line, err := bufio.NewReader(in).ReadString('\n')
		answered <- answer{line, err}
	}()
	var line string
	var err error
	select {
	case a := <-answered:
		line, err = a.line, a.err
	case <-ctx.Done():
		fmt.Fprintln(os.Stderr, "\n       interrupted — skipping the save")
		return false
	}
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

// sameDir reports whether a and b name one directory; scrape --db already
// holds the SQLite lock, whose file is the flat lock when the two coincide.
func sameDir(a, b string) bool {
	if ai, err := os.Stat(a); err == nil {
		if bi, err := os.Stat(b); err == nil {
			return os.SameFile(ai, bi)
		}
	}
	aa, err1 := filepath.Abs(a)
	bb, err2 := filepath.Abs(b)
	return err1 == nil && err2 == nil && filepath.Clean(aa) == filepath.Clean(bb)
}
