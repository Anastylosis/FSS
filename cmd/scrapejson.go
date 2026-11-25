package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/Anastylosis/FSS/output"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	refusalZeroScenes        = "zero_scenes"
	refusalCoverageCollapse  = "coverage_collapse"
	statusSaved              = "saved"
	statusPartial            = "partial"
	statusRefused            = "refused"
	statusFailed             = "failed"
	statusInterrupted        = "interrupted"
	statusSkipped            = "skipped"
	lineTarget, lineSummary  = "target", "summary"
	errSkippedAfterInterrupt = "skipped: run interrupted"
)

// runIO is where a scrape run's prose goes. It rides on the context so the
// helpers that tests call directly keep their signatures; with none attached
// they write to os.Stdout, exactly as before.
type runIO struct {
	out  io.Writer
	json bool
}

type runIOKey struct{}

func withRunIO(ctx context.Context, out io.Writer, json bool) context.Context {
	return context.WithValue(ctx, runIOKey{}, runIO{out: out, json: json})
}

func outOf(ctx context.Context) io.Writer {
	if v, ok := ctx.Value(runIOKey{}).(runIO); ok {
		return v.out
	}
	return os.Stdout
}

func jsonRun(ctx context.Context) bool {
	v, ok := ctx.Value(runIOKey{}).(runIO)
	return ok && v.json
}

// warnPrefix drops the "\r" in json mode, where no progress line is drawn
// for it to overwrite. Text mode keeps it unconditionally, byte for byte.
func warnPrefix(ctx context.Context) string {
	if jsonRun(ctx) {
		return ""
	}
	return "\r"
}

// targetOutcome is what one target's run learned, for the JSON report.
type targetOutcome struct {
	siteID, language string
	cov              coverage
	haveCov          bool
	saved            int
	written          []string
	refusal          string
}

type failureCounts struct {
	Transport int `json:"transport"`
	Parse     int `json:"parse"`
	Absent    int `json:"absent"`
	Unknown   int `json:"unknown"`
}

func failuresOf(by map[scraper.FailureKind]int) failureCounts {
	return failureCounts{
		Transport: by[scraper.FailureTransport],
		Parse:     by[scraper.FailureParse],
		Absent:    by[scraper.FailureAbsent],
		Unknown:   by[scraper.FailureUnknown],
	}
}

type coverageResult struct {
	Stored  int `json:"stored"`
	Seen    int `json:"seen"`
	Dropped int `json:"dropped"`
	Fresh   int `json:"fresh"`
}

type scrapeTargetResult struct {
	URL            string          `json:"url"`
	SiteID         string          `json:"siteId,omitempty"`
	Creator        string          `json:"creator,omitempty"`
	Mode           string          `json:"mode"`
	Language       string          `json:"language,omitempty"`
	Status         string          `json:"status"`
	ScenesSaved    int             `json:"scenesSaved"`
	Coverage       *coverageResult `json:"coverage,omitempty"`
	Complete       *bool           `json:"complete,omitempty"`
	StoppedEarly   bool            `json:"stoppedEarly"`
	Failures       failureCounts   `json:"failures"`
	Written        []string        `json:"written"`
	ElapsedSeconds float64         `json:"elapsedSeconds"`
	RefusalReason  string          `json:"refusalReason,omitempty"`
}

type scrapeSummaryResult struct {
	Targets     int  `json:"targets"`
	Saved       int  `json:"saved"`
	Failed      int  `json:"failed"`
	Refused     int  `json:"refused"`
	Skipped     int  `json:"skipped"`
	Interrupted bool `json:"interrupted"`
}

func (r scrapeRun) mode() string {
	switch {
	case r.full:
		return "full"
	case r.refresh:
		return "refresh"
	}
	return "incremental"
}

func targetStatus(ctx context.Context, oc *targetOutcome, err error) string {
	switch {
	case err == nil && ctx.Err() != nil:
		return statusInterrupted
	case err == nil && oc.haveCov && oc.cov.incomplete:
		return statusPartial
	case err == nil:
		return statusSaved
	case exitCodeOf(err) == ExitInterrupted || ctx.Err() != nil:
		return statusInterrupted
	case exitCodeOf(err) == ExitRefused:
		return statusRefused
	}
	return statusFailed
}

func buildTargetResult(r scrapeRun, tgt scrapeTarget, oc *targetOutcome, status string, elapsed float64) scrapeTargetResult {
	res := scrapeTargetResult{
		URL: tgt.url, SiteID: oc.siteID, Creator: tgt.creator, Mode: r.mode(),
		Language: oc.language, Status: status, ScenesSaved: oc.saved,
		Written: oc.written, ElapsedSeconds: elapsed, RefusalReason: oc.refusal,
	}
	if res.Written == nil {
		res.Written = []string{}
	}
	if oc.haveCov {
		c := oc.cov
		complete := !c.incomplete
		res.Coverage = &coverageResult{Stored: c.stored, Seen: c.seen, Dropped: c.dropped, Fresh: c.fresh}
		res.Complete = &complete
		res.StoppedEarly = c.stoppedEarly
		res.Failures = c.failures
	}
	return res
}

// scrapeReporter turns a run into JSON Lines on stdout and sends all prose to
// stderr. In text mode every method is a no-op or a pass-through.
type scrapeReporter struct {
	json     bool
	stdout   io.Writer
	stderr   io.Writer
	finished bool
	sum      scrapeSummaryResult
}

func newScrapeReporter(cmd *cobra.Command) *scrapeReporter {
	return &scrapeReporter{json: jsonMode(cmd), stdout: cmd.OutOrStdout(), stderr: cmd.ErrOrStderr()}
}

func (p *scrapeReporter) human() io.Writer {
	if p.json {
		return p.stderr
	}
	return os.Stdout
}

func (p *scrapeReporter) bind(ctx context.Context) context.Context {
	if !p.json {
		return ctx
	}
	return withRunIO(ctx, p.stderr, true)
}

func (p *scrapeReporter) line(typ string, ok bool, result any, je *jsonError) {
	env := jsonEnvelope{FormatVersion: jsonFormatVersion, Command: "scrape", Type: typ, OK: ok, Result: result, Error: je}
	enc := json.NewEncoder(p.stdout)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(env); err != nil {
		fmt.Fprintf(os.Stderr, "writing JSON: %v\n", err)
		return
	}
	if f, ok := p.stdout.(interface{ Flush() error }); ok {
		_ = f.Flush()
	}
}

func (p *scrapeReporter) target(ctx context.Context, r scrapeRun, tgt scrapeTarget, oc *targetOutcome, err error, elapsed time.Duration) {
	if !p.json {
		return
	}
	status := targetStatus(ctx, oc, err)
	if status == statusInterrupted && err == nil {
		err = &exitError{code: ExitInterrupted, err: errors.New("interrupted")}
	}
	var je *jsonError
	if err != nil {
		je = toJSONError(err)
		if je.Code == codeRefused {
			je.Reason = oc.refusal
		}
	}
	p.count(status)
	p.line(lineTarget, status == statusSaved || status == statusPartial,
		buildTargetResult(r, tgt, oc, status, elapsed.Seconds()), je)
}

func (p *scrapeReporter) skipped(r scrapeRun, rest []scrapeTarget) {
	if !p.json {
		return
	}
	for _, tgt := range rest {
		p.count(statusSkipped)
		p.line(lineTarget, false, buildTargetResult(r, tgt, &targetOutcome{}, statusSkipped, 0),
			&jsonError{Code: codeInterrupted, Message: errSkippedAfterInterrupt})
	}
}

func (p *scrapeReporter) count(status string) {
	p.sum.Targets++
	switch status {
	case statusSaved, statusPartial:
		p.sum.Saved++
	case statusFailed:
		p.sum.Failed++
	case statusRefused:
		p.sum.Refused++
	case statusSkipped:
		p.sum.Skipped++
	case statusInterrupted:
		p.sum.Interrupted = true
	}
}

func (p *scrapeReporter) summary(err error) {
	p.finished = true
	if !p.json {
		return
	}
	if err != nil && exitCodeOf(err) == ExitInterrupted {
		p.sum.Interrupted = true
	}
	p.line(lineSummary, err == nil, p.sum, toJSONError(err))
}

// finish ends a run that had at least one failing target; the per-target
// errors were already reported, so err comes back unchanged.
func (p *scrapeReporter) finish(err error) error {
	p.summary(err)
	return err
}

// fail ends a run that never reached a target.
func (p *scrapeReporter) fail(err error) error {
	p.summary(err)
	_, _ = fmt.Fprintf(p.stderr, "Error: %v\n", err)
	return &exitError{code: exitCodeOf(err), err: err, reported: true}
}

// writtenPaths lists the files an export of one studio produces.
func writtenPaths(studioURL, outDir string, formats []string) []string {
	slug := output.Slugify(output.CanonicalStudioURL(studioURL))
	paths := make([]string, 0, len(formats))
	for _, f := range formats {
		paths = append(paths, filepath.Join(outDir, slug+"."+f))
	}
	return paths
}
