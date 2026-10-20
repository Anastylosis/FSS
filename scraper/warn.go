package scraper

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// warnedDelays remembers which (siteID, recommended) pairs have already
// emitted a delay warning so the message is logged at most once per
// scraper-process lifetime even when ListScenes is called repeatedly.
var (
	warnedDelaysMu sync.Mutex
	warnedDelays   = map[string]bool{}
)

// WarnDelayBelow prints a one-shot stderr warning when `actual` is
// strictly below `recommended` for scraper `siteID`. Use it to flag
// when the operator's chosen delay is below a value the upstream site
// is known to need to avoid rate-limiting. Unlike Debugf this prints
// regardless of verbosity, but only once per siteID per process so it
// doesn't spam.
//
// `recommended` of 0 or `actual` of 0 are no-ops if recommended <= 0;
// a recommended floor of zero means "no minimum", so nothing to warn
// about. Callers should pass the package's documented minimum (e.g.
// 500ms for julesjordan / newsensations).
func WarnDelayBelow(siteID string, actual, recommended time.Duration) {
	if recommended <= 0 || actual >= recommended {
		return
	}
	warnedDelaysMu.Lock()
	if warnedDelays[siteID] {
		warnedDelaysMu.Unlock()
		return
	}
	warnedDelays[siteID] = true
	warnedDelaysMu.Unlock()
	fmt.Fprintf(os.Stderr,
		"[warn] %s: configured delay %v is below the recommended minimum %v; "+
			"upstream may rate-limit. Set `site_delays: { %s: %d }` in config or "+
			"`--site-delay %s=%d` to override.\n",
		siteID, actual, recommended, siteID, recommended.Milliseconds(), siteID, recommended.Milliseconds())
}

// ResetWarnDelayBelow clears the once-per-siteID memoisation. Intended
// for tests that need to re-trigger the warning across cases.
func ResetWarnDelayBelow() {
	warnedDelaysMu.Lock()
	warnedDelays = map[string]bool{}
	warnedDelaysMu.Unlock()
}

// warnedFallthrough remembers which (siteID, path) pairs have already warned,
// so the message is logged at most once per scraper-process lifetime.
var (
	warnedFallthroughMu sync.Mutex
	warnedFallthrough   = map[string]bool{}
)

// URLHasNonRootPath reports whether rawURL carries a path (or query) component
// beyond the bare site root — i.e. it looks like a filtered view (model,
// channel, category, tag, DVD) rather than the studio's front page. A trailing
// "/" and the common root files ("/", "/en/", "/tour/") count as root.
//
// Scrapers whose run() dispatches on URL shape can use this in their default
// (full-catalogue) branch to detect a filtered URL that fell through to
// scraping the entire site — see WarnURLFallthrough.
func URLHasNonRootPath(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	if u.RawQuery != "" {
		return true
	}
	p := strings.Trim(u.Path, "/")
	switch p {
	case "", "en", "tour", "trial":
		return false
	}
	return true
}

// WarnURLFallthrough prints a one-shot stderr warning that a filtered URL (one
// with a non-root path) fell through to the default full-catalogue scrape for
// siteID. This is the loud signal for the case where a model/channel/tag URL
// that the scraper did not recognise would otherwise silently scrape the whole
// site under that URL's store key. It is a no-op for root URLs. Prints at most
// once per (siteID, path) per process.
func WarnURLFallthrough(siteID, rawURL string) {
	if !URLHasNonRootPath(rawURL) {
		return
	}
	key := siteID + "\x00" + rawURL
	warnedFallthroughMu.Lock()
	if warnedFallthrough[key] {
		warnedFallthroughMu.Unlock()
		return
	}
	warnedFallthrough[key] = true
	warnedFallthroughMu.Unlock()
	fmt.Fprintf(os.Stderr,
		"[warn] %s: URL %q has a non-root path but was not recognised as a "+
			"filtered view; falling through to a full-catalogue scrape. The result "+
			"will be stored under this URL's key and may not match the filter.\n",
		siteID, rawURL)
}

// ResetWarnURLFallthrough clears the once-per-key memoisation. For tests.
func ResetWarnURLFallthrough() {
	warnedFallthroughMu.Lock()
	warnedFallthrough = map[string]bool{}
	warnedFallthroughMu.Unlock()
}
