package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Anastylosis/FSS/internal/creators"
	"github.com/Anastylosis/FSS/internal/store"
	"github.com/Anastylosis/FSS/output"
	"github.com/Anastylosis/FSS/scraper"
)

// scrapeTarget is one studio URL to scrape, plus whatever the creator file said
// about it. A bare command-line argument produces a target with no creator and
// no delay override.
type scrapeTarget struct {
	url     string
	creator string
	delay   *time.Duration
}

// label describes the target for the run header: creator-driven targets say
// whose they are, since --all-creators makes the URL alone hard to place.
func (t scrapeTarget) label() string {
	if t.creator == "" {
		return t.url
	}
	return fmt.Sprintf("%s [%s]", t.url, t.creator)
}

// resolveScrapeTargets expands command-line arguments, --creator and
// --all-creators into the ordered, de-duplicated list of URLs to scrape.
//
// Bare arguments come first and in the order given, then creators in the order
// named (--all-creators in name order). A URL reachable more than one way is
// scraped once, keeping the first occurrence — so naming a URL explicitly
// alongside --all-creators does not scrape it twice.
func resolveScrapeTargets(cmd *cobra.Command, args []string) ([]scrapeTarget, error) {
	named, _ := cmd.Flags().GetStringArray("creator")
	all, _ := cmd.Flags().GetBool("all-creators")

	if len(args) == 0 && len(named) == 0 && !all {
		return nil, fmt.Errorf("nothing to scrape: pass a studio URL, --creator <name>, or --all-creators")
	}

	var targets []scrapeTarget
	seen := map[string]bool{}
	add := func(t scrapeTarget) {
		t.url = scraper.PreferredStudioURL(t.url)
		key := output.CanonicalStudioURL(t.url)
		if seen[key] {
			return
		}
		seen[key] = true
		targets = append(targets, t)
	}

	for _, a := range args {
		add(scrapeTarget{url: normalizeInputURL(a)})
	}

	if len(named) == 0 && !all {
		return targets, nil
	}

	list, err := loadCreators(cmd)
	if err != nil {
		return nil, err
	}

	chosen := list
	if !all {
		chosen = nil
		for _, q := range named {
			c, err := creators.Find(list, q)
			if err != nil {
				return nil, err
			}
			chosen = append(chosen, c)
		}
	}
	if len(chosen) == 0 {
		return nil, fmt.Errorf("no creators defined in %s — bootstrap them with `fss creators suggest`",
			displayCreatorsDir(cmd))
	}

	for _, c := range chosen {
		stores := c.EnabledStores()
		if len(stores) == 0 {
			fmt.Fprintf(os.Stderr, "warning: every store for %q is disabled — skipping\n", c.Name)
			continue
		}
		for _, s := range stores {
			t := scrapeTarget{url: normalizeInputURL(s.URL), creator: c.Name}
			if s.Delay != nil {
				d := time.Duration(*s.Delay) * time.Millisecond
				t.delay = &d
			}
			add(t)
		}
	}

	if len(targets) == 0 {
		return nil, fmt.Errorf("nothing to scrape: every selected store is disabled")
	}
	return targets, nil
}

func displayCreatorsDir(cmd *cobra.Command) string {
	if dir := resolveCreatorsDir(cmd); dir != "" {
		return dir
	}
	return creators.DefaultDir()
}

// filterStale drops targets scraped more recently than maxAge, printing the
// decision for each so a cron log says what it chose and why.
//
// Staleness is read from the SQLite studios table, the only place a last-scrape
// time is recorded; the flat store keeps no such record, so --stale requires a
// database rather than silently scraping everything.
func filterStale(targets []scrapeTarget, maxAge time.Duration, st store.Store, dbPath string) ([]scrapeTarget, error) {
	if dbPath == "" {
		return nil, fmt.Errorf("--stale needs the SQLite store to know when each studio was last scraped — pass --db, or set `db:` in the config")
	}
	studios, err := st.ListStudios()
	if err != nil {
		return nil, err
	}
	// The studios table keys on the canonical URL; targets carry whatever the
	// operator or creator file spelled, so both sides are canonicalised here.
	last := make(map[string]time.Time, len(studios))
	for _, s := range studios {
		if s.LastScrapedAt != nil {
			last[output.CanonicalStudioURL(s.URL)] = *s.LastScrapedAt
		}
	}

	now := time.Now().UTC()
	var due []scrapeTarget
	for _, t := range targets {
		when, known := last[output.CanonicalStudioURL(t.url)]
		switch {
		case !known:
			fmt.Printf("  run   %s  (never scraped)\n", t.url)
			due = append(due, t)
		case now.Sub(when) >= maxAge:
			fmt.Printf("  run   %s  (scraped %s ago)\n", t.url, humanAge(now.Sub(when)))
			due = append(due, t)
		default:
			fmt.Printf("  skip  %s  (scraped %s ago)\n", t.url, humanAge(now.Sub(when)))
		}
	}
	fmt.Printf("\n%d of %d studio(s) due.\n", len(due), len(targets))
	return due, nil
}

// humanAge renders an elapsed duration at one significant unit — cron logs are
// read at a glance, and "9d" answers the question that "223h14m9s" does not.
func humanAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// parseStaleDuration extends time.ParseDuration with the day and week units a
// scrape cadence is actually expressed in. `7d` is the natural way to say it;
// the standard parser stops at hours.
func parseStaleDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("--stale needs a duration, e.g. 12h, 7d, 2w")
	}
	if n := len(s); n > 1 {
		unit := s[n-1]
		if unit == 'd' || unit == 'w' {
			qty, err := strconv.ParseFloat(s[:n-1], 64)
			if err != nil {
				return 0, fmt.Errorf("--stale %q: %w", s, err)
			}
			hours := 24.0
			if unit == 'w' {
				hours = 24 * 7
			}
			d := time.Duration(qty * hours * float64(time.Hour))
			if d < 0 {
				return 0, fmt.Errorf("--stale %q must not be negative", s)
			}
			return d, nil
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("--stale %q: use a duration like 12h, 7d or 2w", s)
	}
	if d < 0 {
		return 0, fmt.Errorf("--stale %q must not be negative", s)
	}
	return d, nil
}

// resolveTargetDelay picks the delay for one target: a per-store value from the
// creator file is the most specific statement there is and wins outright,
// otherwise the usual per-site / global resolution applies.
func resolveTargetDelay(t scrapeTarget, siteID string, defaultDelay time.Duration, siteDelays map[string]int) time.Duration {
	if t.delay != nil {
		return *t.delay
	}
	return resolveSiteDelay(siteID, defaultDelay, siteDelays)
}
