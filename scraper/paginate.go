package scraper

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/models"
)

// pageKey builds a stable signature from a page's ordered scene IDs, used to
// detect a CMS echoing the same page (see Paginate's repeat-page guard).
func pageKey(scenes []models.Scene) string {
	ids := make([]string, len(scenes))
	for i, sc := range scenes {
		ids[i] = sc.ID
	}
	return strings.Join(ids, "\x00")
}

// paginateSafetyCap is the maximum number of pages Paginate will fetch before
// stopping unconditionally. It is a backstop against a CMS that never reports
// an end (or a callback that forgets to set Done); set far above any real
// listing so it never trips in normal operation.
const paginateSafetyCap = 100000

// PageResult is returned by a FetchPage callback to the pagination loop.
type PageResult struct {
	Scenes []models.Scene
	Total  int
	Done   bool
	// Continue tells the loop not to treat an empty Scenes slice as
	// end-of-listing. Set it when a page legitimately yields zero Scenes but
	// more pages may still follow — either because the page's raw items all
	// filtered out (videos-only, dedup, details that failed to fetch), or
	// because pagination walks a fixed list (DVDs, years) where an empty
	// element is not the end. Callbacks that set Continue MUST also set Done at
	// the true end, or the loop will not terminate. Leave it false for the
	// common case where an empty page means the listing is exhausted.
	Continue bool
}

// Paginate runs a page-numbered pagination loop, handling delay, context
// cancellation, progress reporting, KnownIDs early-stop, and scene
// sending. The caller provides a FetchPage callback that fetches and
// parses a single page; everything else is handled by the loop.
//
// FetchPage receives the 1-based page number and returns a PageResult.
// Set PageResult.Total on the first page for progress display. Set
// PageResult.Done to true when there are no more pages (e.g. items <
// pageSize, page >= totalPages). An empty Scenes slice also stops the
// loop, unless PageResult.Continue is set — see PageResult.Continue for
// pages that yield zero scenes but are not the end of the listing.
//
// The siteID string is used only for debug log messages.
//
// Paginate does NOT call defer close(out) — the caller's run() must
// still do that, since some scrapers do additional work after the
// pagination loop returns (e.g. worker pool teardown).
//
// Two guards bound the loop against CMSes that never report an end: a hard
// page cap (paginateSafetyCap) and repeat-page detection (a page whose first
// scene ID matches the previous page is the CMS echoing its last page, so the
// loop stops). Both prevent an unbounded --full crawl when a callback forgets
// to set Done.
func Paginate(ctx context.Context, opts ListOpts, siteID string, out chan<- SceneResult,
	fetchPage func(ctx context.Context, page int) (PageResult, error),
) {
	progressSent := false
	prevPageKey := ""
	for page := 1; ; page++ {
		if ctx.Err() != nil {
			return
		}
		if page > paginateSafetyCap {
			Debugf(1, "%s: reached %d-page safety cap, stopping", siteID, paginateSafetyCap)
			return
		}
		if page > 1 && opts.Delay > 0 {
			select {
			case <-time.After(opts.Delay):
			case <-ctx.Done():
				return
			}
		}
		Debugf(1, "%s: fetching page %d", siteID, page)

		result, err := fetchPage(ctx, page)
		if err != nil {
			select {
			case out <- Error(fmt.Errorf("page %d: %w", page, err)):
			case <-ctx.Done():
			}
			return
		}

		// An empty Scenes slice ends the loop unless the callback asked to keep
		// going (a page that filtered to zero, or a fixed-list walk with an
		// empty element). Such callbacks signal the true end via Done.
		if len(result.Scenes) == 0 && !result.Continue {
			return
		}

		// Repeat-page detection: a CMS that ignores the page parameter echoes
		// the same scenes forever. If this page's ordered scene IDs match the
		// previous page's exactly, stop rather than loop. Comparing the whole
		// set (not just the first ID) avoids a false stop on listings that pin
		// one scene at the top of every page. (Skipped for empty Continue
		// pages, which carry no IDs to compare.)
		if len(result.Scenes) > 0 {
			key := pageKey(result.Scenes)
			if key != "" && key == prevPageKey {
				Debugf(1, "%s: page %d repeats the previous page's scenes, stopping", siteID, page)
				return
			}
			prevPageKey = key
		}

		if !progressSent && result.Total > 0 {
			progressSent = true
			Debugf(1, "%s: %d total scenes", siteID, result.Total)
			select {
			case out <- Progress(result.Total):
			case <-ctx.Done():
				return
			}
		}

		// The page in hand is already paid for, so a stored scene ends the
		// *walk*, not the page: listings routinely pin a featured scene at the
		// top or bump a re-published one, and stopping at the first known ID
		// hid every new scene behind it. Skipping known scenes and stopping at
		// the end of the page costs at most one page of sends and cannot
		// truncate a page.
		hitKnown := false
		for _, sc := range result.Scenes {
			if opts.KnownIDs[sc.ID] {
				hitKnown = true
				continue
			}
			select {
			case out <- Scene(sc):
			case <-ctx.Done():
				return
			}
		}
		if hitKnown {
			Debugf(1, "%s: page %d reached stored scenes, stopping", siteID, page)
			select {
			case out <- StoppedEarly():
			case <-ctx.Done():
			}
			return
		}

		if result.Done {
			return
		}
	}
}
