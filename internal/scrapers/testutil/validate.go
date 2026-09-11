// Package testutil provides helpers for scraper integration tests.
//
// These helpers are only useful from tests built with `-tags integration`,
// but the file itself has no build tag so static analysis (vet, lint) can
// reach it.
package testutil

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

// ValidateScene runs cheap shape checks on a scraped scene. It catches the
// common signs that a scraper is broken without making assumptions about the
// site's specific data. Anything that is *site-specific* (e.g. expected
// performer name, expected duration range) belongs in that scraper's own
// integration test, not here.
func ValidateScene(t *testing.T, s models.Scene) {
	t.Helper()

	for _, problem := range sceneProblems(s) {
		t.Error(problem)
	}
	for _, note := range sceneNotes(s) {
		t.Log(note)
	}
}

// sceneProblems returns one message per shape check the scene fails, or nil if
// it passes. Split out from ValidateScene so the rules are unit-testable
// without a failing *testing.T.
func sceneProblems(s models.Scene) []string {
	var out []string
	if s.ID == "" {
		out = append(out, "scene has empty ID")
	}
	if s.SiteID == "" {
		out = append(out, fmt.Sprintf("scene %q has empty SiteID", s.ID))
	}
	if s.Title == "" {
		out = append(out, fmt.Sprintf("scene %q has empty Title", s.ID))
	}
	if s.URL == "" {
		out = append(out, fmt.Sprintf("scene %q has empty URL", s.ID))
	} else if u, err := url.Parse(s.URL); err != nil || u.Scheme == "" || u.Host == "" {
		out = append(out, fmt.Sprintf("scene %q has malformed URL %q", s.ID, s.URL))
	}
	// Duration is sometimes unavailable from list endpoints; warn but don't fail.
	// Cap is 7 days — generous but catches overflow/unit bugs. JAV compilations can exceed 40h.
	if s.Duration < 0 || s.Duration > 7*24*60*60 {
		out = append(out, fmt.Sprintf("scene %q has implausible Duration %d (expected 0..604800)", s.ID, s.Duration))
	}
	if len(s.Performers) == 0 && s.Studio == "" {
		out = append(out, fmt.Sprintf("scene %q has neither Performers nor Studio", s.ID))
	}
	if s.ScrapedAt.IsZero() {
		out = append(out, fmt.Sprintf("scene %q has zero ScrapedAt", s.ID))
	}
	return out
}

// sceneNotes returns advisory messages that must not fail a test.
func sceneNotes(s models.Scene) []string {
	var out []string
	// Date is unavailable on some sites (e.g. AlternaDudes); warn but don't fail.
	if s.Date.IsZero() {
		out = append(out, fmt.Sprintf("scene %q has zero Date", s.ID))
	}
	// Two scrapers (grandparentsx, seemomsuck) independently hand-rolled their
	// own validation specifically to assert this, which is a good sign the
	// shared harness should. Advisory for now: some sites genuinely publish no
	// thumbnail, and the blast radius across ~1600 sites has not been measured.
	// Promote to a hard failure once a full `make smoke` shows what it catches.
	if s.Thumbnail == "" {
		out = append(out, fmt.Sprintf("scene %q has empty Thumbnail", s.ID))
	}
	// Surrounding whitespace on a name is always a site artefact, never meaning.
	// Aylo serves "Nikki Nuttz " with a trailing space, and 20 other packages
	// append these strings without trimming — but whether their APIs actually
	// carry whitespace is unmeasured, and probing 21 live APIs to find out is a
	// worse use of a smoke run than simply reporting it when it appears.
	//
	// Advisory, not a failure, for two reasons: the user-visible harm is already
	// fixed downstream (match.MergeScenes trims before anything is written to
	// Stash or an NFO, and both write paths go through it), and a hard failure
	// here would break scrapers over a cosmetic detail in stored JSON. Promote it
	// once a full `make smoke` shows the real list.
	for _, p := range s.Performers {
		if p != strings.TrimSpace(p) {
			out = append(out, fmt.Sprintf("scene %q has performer %q with surrounding whitespace", s.ID, p))
		}
	}
	for _, t := range s.Tags {
		if t != strings.TrimSpace(t) {
			out = append(out, fmt.Sprintf("scene %q has tag %q with surrounding whitespace", s.ID, t))
		}
	}
	return out
}

// RunLiveScrape exercises a scraper against a live URL and validates the
// first `limit` scenes. It cancels the context after `limit` is reached so
// the scraper goroutine exits cleanly, then drains the channel.
//
// The first scene is logged in full (via t.Logf) so a developer running
// `go test -v` can eyeball the field mapping after a scraper change.
//
// On transient network failures (0 scenes returned), retries once after a
// short pause before failing the test.
//
// Fails the test if no scenes are returned or any scene fails ValidateScene.
func RunLiveScrape(t *testing.T, s scraper.StudioScraper, studioURL string, limit int) {
	t.Helper()

	if !s.MatchesURL(studioURL) {
		t.Fatalf("scraper %s does not match URL %s", s.ID(), studioURL)
	}

	const maxAttempts = 2
	var lastErrs []error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		count, errs := runOnce(t, s, studioURL, limit, attempt < maxAttempts)
		if count > 0 {
			return
		}
		lastErrs = errs
		if attempt < maxAttempts {
			t.Logf("%s: 0 scenes on attempt %d, retrying after 3s", s.ID(), attempt)
			time.Sleep(3 * time.Second)
		}
	}

	// A scrape that returned nothing is either a scraper regression or a site
	// that has stopped serving. Only the second is out of our hands, so say
	// which it is instead of reporting both as a broken scraper.
	if reason, down := siteIsDown(studioURL, lastErrs); down {
		t.Skipf("%s: %s — %s. The scraper is untested, not known-broken; "+
			"re-run when the site is back.", s.ID(), reason, studioURL)
	}
	t.Fatalf("%s: no scenes returned from %s after %d attempts", s.ID(), studioURL, maxAttempts)
}

// siteIsDown reports whether a scrape returned nothing because the site is not
// serving rather than because the scraper broke.
//
// It is consulted only after a scrape has already failed, never as a gate
// before one. That ordering is deliberate: some CMSes serve real content under
// a 5xx (SexMex does), and pre-checking the status would skip scrapers that
// work perfectly well.
func siteIsDown(studioURL string, errs []error) (string, bool) {
	// The scrape's own errors are the better evidence when there are any: they
	// name the URL the scraper actually could not fetch, which is often not the
	// studio URL at all (a WordPress REST endpoint under it, say).
	if len(errs) > 0 {
		first := ""
		for _, err := range errs {
			reason, ok := siteSideError(err)
			if !ok {
				// Not obviously the site's fault — a parse failure, a 4xx, or
				// a timeout we may have set too low. Report it as a failure.
				return "", false
			}
			if first == "" {
				first = reason
			}
		}
		return first, true
	}
	// No errors at all: the scraper walked a page and found nothing in it.
	// That is usually a parser regression, so ask the site directly.
	return probeSite(studioURL)
}

// siteSideError reports whether err means the site failed, as opposed to the
// scraper asking it for the wrong thing.
func siteSideError(err error) (string, bool) {
	var se *httpx.StatusError
	if errors.As(err, &se) {
		if se.StatusCode >= 500 {
			return fmt.Sprintf("the site answers HTTP %d", se.StatusCode), true
		}
		// 4xx is far more often a URL the scraper built wrongly than a site
		// that has gone away, so it stays a failure.
		return "", false
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return fmt.Sprintf("the domain does not resolve (%s)", dnsErr.Err), true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return fmt.Sprintf("the site refused the connection (%s)", opErr.Err), true
	}
	// A timeout is deliberately not site-side: it is as likely to be a client
	// deadline set below the origin's own ceiling, which is our bug to fix.
	return "", false
}

// probeSite asks the studio URL whether it is still serving this site.
func probeSite(studioURL string) (string, bool) {
	req, err := http.NewRequest(http.MethodGet, studioURL, nil)
	if err != nil {
		return "", false
	}
	req.Header.Set("User-Agent", httpx.UserAgentFirefox)

	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		if reason, ok := siteSideError(err); ok {
			return reason, true
		}
		return fmt.Sprintf("the site could not be reached (%v)", err), true
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode >= 500 {
		return fmt.Sprintf("the site answers HTTP %d", resp.StatusCode), true
	}
	// A site that now redirects to somebody else's domain has been parked,
	// sold or folded into another brand; whatever it is, the catalogue this
	// scraper was written against is not there.
	if final := resp.Request.URL; final != nil && !sameSite(studioURL, final) {
		return fmt.Sprintf("the site redirects to %s, a different domain", final.Host), true
	}
	return "", false
}

// sameSite compares hosts ignoring a leading www.
func sameSite(requested string, final *url.URL) bool {
	u, err := url.Parse(requested)
	if err != nil {
		return true
	}
	return strings.TrimPrefix(u.Hostname(), "www.") == strings.TrimPrefix(final.Hostname(), "www.")
}

func runOnce(t *testing.T, s scraper.StudioScraper, studioURL string, limit int, tolerateErrors bool) (int, []error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	ch, err := s.ListScenes(ctx, studioURL, scraper.ListOpts{Workers: 3})
	if err != nil {
		if tolerateErrors {
			t.Logf("ListScenes(%s): %v (will retry)", studioURL, err)
			return 0, []error{err}
		}
		t.Fatalf("ListScenes(%s): %v", studioURL, err)
	}

	count := 0
	errCount := 0
	var errs []error
	var seen []models.Scene
	for result := range ch {
		switch result.Kind {
		case scraper.KindError:
			errCount++
			errs = append(errs, result.Err)
			t.Logf("scene error: %v", result.Err)
			continue
		case scraper.KindTotal, scraper.KindStoppedEarly:
			continue
		case scraper.KindScene:
		}

		count++
		if count == 1 {
			t.Logf("first scene from %s: %+v", s.ID(), result.Scene)
		}

		seen = append(seen, result.Scene)
		ValidateScene(t, result.Scene)

		if count >= limit {
			cancel()
			break
		}
	}

	for range ch {
	}

	// Not gated on tolerateErrors: a repeated ID is a scraper bug, not a
	// transient network failure, so retrying cannot change it.
	for _, id := range duplicateIDs(seen) {
		t.Errorf("%s: scene ID %q emitted more than once", s.ID(), id)
	}

	// Errors used to be logged and nothing more, so a scraper erroring on most
	// of its fetches still passed as long as `limit` scenes survived — the
	// suite detected "dead" but not "degraded". Failing on *any* error would be
	// too strict (a single bad detail page among thousands is normal), so the
	// signal is: errors occurred AND the scrape could not even reach `limit`.
	// A site with genuinely fewer than `limit` scenes produces no errors, so it
	// is unaffected.
	// Gated on count > 0: a run that collected nothing at all is classified by
	// RunLiveScrape, which can tell a site outage from a scraper regression.
	// Failing it here would mark the test before that decision is made.
	if !tolerateErrors && errCount > 0 && count > 0 && count < limit {
		t.Errorf("%s: %d scene error(s) and only %d/%d scenes — the scrape is degraded, not just slow",
			s.ID(), errCount, count, limit)
	}

	t.Logf("%s: validated %d scenes (limit %d, %d error(s))", s.ID(), count, limit, errCount)
	return count, errs
}

// CollectScenes drains a SceneResult channel, returning all scenes.
// Progress and StoppedEarly signals are silently skipped.
// Errors fail the test via t.Errorf so the remaining scenes are still collected.
func CollectScenes(t *testing.T, ch <-chan scraper.SceneResult) []models.Scene {
	t.Helper()
	scenes, _ := collectAll(t, ch)
	return scenes
}

// CollectScenesWithStop drains a SceneResult channel, returning all scenes
// and whether a StoppedEarly signal was received.
func CollectScenesWithStop(t *testing.T, ch <-chan scraper.SceneResult) ([]models.Scene, bool) {
	t.Helper()
	return collectAll(t, ch)
}

func collectAll(t *testing.T, ch <-chan scraper.SceneResult) ([]models.Scene, bool) {
	t.Helper()
	scenes, stoppedEarly, errs := drain(ch)
	for _, err := range errs {
		t.Errorf("unexpected error: %v", err)
	}
	for _, id := range duplicateIDs(scenes) {
		t.Errorf("scene ID %q emitted more than once", id)
	}
	return scenes, stoppedEarly
}

// duplicateIDs returns every scene ID that appears more than once, in order of
// first repeat.
//
// The store is keyed on (id, site_id), so two scenes sharing an ID silently
// collapse into one at Save: the scrape reports N scenes and the file holds
// fewer, with no error anywhere. That makes a non-unique ID — a slug collision,
// a fallback to a constant, a listing that repeats across pages — a data-loss
// bug that no other check catches.
func duplicateIDs(scenes []models.Scene) []string {
	seen := make(map[string]bool, len(scenes))
	reported := make(map[string]bool)
	var dupes []string
	for _, sc := range scenes {
		if seen[sc.ID] && !reported[sc.ID] {
			reported[sc.ID] = true
			dupes = append(dupes, sc.ID)
		}
		seen[sc.ID] = true
	}
	return dupes
}

// drain reads a SceneResult channel to completion, separating scenes, the
// StoppedEarly signal, and errors. Split out from collectAll so the channel
// bookkeeping is unit-testable without a failing *testing.T.
func drain(ch <-chan scraper.SceneResult) (scenes []models.Scene, stoppedEarly bool, errs []error) {
	for r := range ch {
		switch r.Kind {
		case scraper.KindTotal:
			continue
		case scraper.KindStoppedEarly:
			stoppedEarly = true
			continue
		case scraper.KindError:
			errs = append(errs, r.Err)
			continue
		case scraper.KindScene:
			scenes = append(scenes, r.Scene)
		}
	}
	return scenes, stoppedEarly, errs
}

// cancelDrainTimeout is how long AssertCancellable waits for a cancelled
// scraper to close its channel. Generous because it is spent only by a scraper
// that is genuinely broken; a working one returns immediately. The one test
// that exercises the timeout path shortens it, rather than costing the suite
// ten idle seconds.
var cancelDrainTimeout = 10 * time.Second

// AssertCancellable checks that a scraper stops when its context is cancelled.
//
// Every scraper must select on ctx.Done() in the sends to its output channel,
// and helper loops must check ctx.Err() each iteration — otherwise a cancelled
// scrape leaks a goroutine that keeps fetching pages nobody reads. Deleting
// that select breaks nothing any other test asserts, which is why this exists.
//
// The scraper must already be pointed at a test server; studioURL is passed
// through untouched. It cancels after the first result and fails if the
// scraper's channel has not closed shortly afterwards.
//
// Not usable on scrapers that fetch everything in one request and then stream
// from memory — those have nothing left to cancel — so it is a check to add
// where a scrape does real work between sends.
func AssertCancellable(t *testing.T, s scraper.StudioScraper, studioURL string, opts scraper.ListOpts) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := s.ListScenes(ctx, studioURL, opts)
	if err != nil {
		t.Fatalf("ListScenes(%s): %v", studioURL, err)
	}

	// Wait for the scrape to actually start before cancelling, so this tests
	// mid-flight cancellation rather than a cancel that lands before the first
	// fetch. A closed channel here just means the scrape finished first.
	if _, ok := <-ch; !ok {
		t.Skip("scraper finished before it could be cancelled — nothing to assert")
	}
	cancel()

	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for range ch {
		}
	}()

	select {
	case <-drained:
	case <-time.After(cancelDrainTimeout):
		t.Fatalf("%s: channel still open %s after cancellation — the scraper is "+
			"not selecting on ctx.Done() and leaks a goroutine per cancelled scrape",
			s.ID(), cancelDrainTimeout)
	}
}

// SkipIfPlaceholder skips the test if the URL still looks like a placeholder
// (contains "REPLACE-ME"). Use this for scrapers where the maintainer hasn't
// yet picked a verified live URL.
func SkipIfPlaceholder(t *testing.T, studioURL string) {
	t.Helper()
	if isPlaceholder(studioURL) {
		t.Skipf("placeholder URL — edit liveStudioURL in this file with a verified studio URL")
	}
}

func isPlaceholder(studioURL string) bool {
	return strings.Contains(studioURL, "REPLACE-ME")
}
