package carnalplus

import (
	"context"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

// defaultDetailWorkers sizes the grid detail pool when --workers is unset.
const defaultDetailWorkers = 4

// ---- Grid detail-page regexes (carnalplus.com and jockpack.com share the template) ----

var (
	// `<div class="releasedate">October 28, 2026 | Full Length Video : 45min 9sec</div>`
	detailReleaseRe = regexp.MustCompile(`<div class="releasedate">\s*([^<]+?)\s*</div>`)
	detailHoursRe   = regexp.MustCompile(`(\d+)\s*h`)
	detailMinutesRe = regexp.MustCompile(`(\d+)\s*min`)
	detailSecondsRe = regexp.MustCompile(`(\d+)\s*sec`)
	// `<div class="update-models"><div>Starring <a …>Name</a>, … and <a …>Name</a></div>`
	detailModelsRe = regexp.MustCompile(`(?s)<div class="update-models">(.*?)</div>`)
	detailAnchorRe = regexp.MustCompile(`<a[^>]*>\s*([^<]+?)\s*</a>`)
	// The description runs from textDescription to the credits heading (or the
	// show-less toggle when a scene lists no credits).
	detailDescRe = regexp.MustCompile(`(?s)<div class="textDescription"[^>]*>(.*?)(?:<h5 class="credits-heading">|<span id='showless'>)`)
	// The site truncates long descriptions client-side with these two spans;
	// their text is UI, not part of the description.
	detailDescUIRe   = regexp.MustCompile(`<span id='(?:firstWords|readmore)'>[^<]*</span>`)
	detailParaRe     = regexp.MustCompile(`(?s)<p>(.*?)</p>`)
	detailDirectorRe = regexp.MustCompile(`<div class="txt-desc"><span>Director:</span>\s*([^<]+?)\s*</div>`)
	detailTagsRe     = regexp.MustCompile(`(?s)<div class="update_tags">(.*?)</div>`)
	detailTagRe      = regexp.MustCompile(`<span class="txt-tags">\s*([^<]+?)\s*</span>`)
)

type gridDetail struct {
	date        time.Time
	duration    int
	description string
	director    string
	performers  []string
	tags        []string
}

func parseGridDetail(body []byte) gridDetail {
	page := string(body)
	var d gridDetail

	if m := detailReleaseRe.FindStringSubmatch(page); m != nil {
		parts := strings.SplitN(m[1], "|", 2)
		if t, err := time.Parse("January 2, 2006", strings.TrimSpace(parts[0])); err == nil {
			d.date = t.UTC()
		}
		if len(parts) == 2 {
			d.duration = parseDetailDuration(parts[1])
		}
	}
	if m := detailModelsRe.FindStringSubmatch(page); m != nil {
		seen := make(map[string]bool)
		for _, a := range detailAnchorRe.FindAllStringSubmatch(m[1], -1) {
			name := html.UnescapeString(a[1])
			if name != "" && !seen[name] {
				seen[name] = true
				d.performers = append(d.performers, name)
			}
		}
	}
	if m := detailDescRe.FindStringSubmatch(page); m != nil {
		block := detailDescUIRe.ReplaceAllString(m[1], "")
		var paras []string
		for _, p := range detailParaRe.FindAllStringSubmatch(block, -1) {
			if t := cleanHTML(p[1]); t != "" {
				paras = append(paras, t)
			}
		}
		if len(paras) == 0 {
			if t := cleanHTML(block); t != "" {
				paras = append(paras, t)
			}
		}
		d.description = strings.Join(paras, "\n\n")
	}
	if m := detailDirectorRe.FindStringSubmatch(page); m != nil {
		d.director = html.UnescapeString(m[1])
	}
	if m := detailTagsRe.FindStringSubmatch(page); m != nil {
		for _, t := range detailTagRe.FindAllStringSubmatch(m[1], -1) {
			if name := html.UnescapeString(t[1]); name != "" {
				d.tags = append(d.tags, name)
			}
		}
	}
	return d
}

// parseDetailDuration reads "Full Length Video : 1h 2min 9sec" into seconds.
func parseDetailDuration(s string) int {
	total := 0
	for _, f := range []struct {
		re   *regexp.Regexp
		mult int
	}{{detailHoursRe, 3600}, {detailMinutesRe, 60}, {detailSecondsRe, 1}} {
		if m := f.re.FindStringSubmatch(s); m != nil {
			n, _ := strconv.Atoi(m[1])
			total += n * f.mult
		}
	}
	return total
}

func (d gridDetail) apply(sc *models.Scene) {
	if !d.date.IsZero() {
		sc.Date = d.date
	}
	if d.duration > 0 {
		sc.Duration = d.duration
	}
	if d.description != "" {
		sc.Description = d.description
	}
	if d.director != "" {
		sc.Director = d.director
	}
	if len(d.performers) > 0 {
		sc.Performers = d.performers
	}
	if len(d.tags) > 0 {
		sc.Tags = d.tags
	}
}

// detailFetchURL fetches the card's detail path from the configured SiteBase.
// Live, the card links to the same host; rebasing keeps offline tests on
// httptest instead of following fixtures' absolute production URLs.
func (s *Scraper) detailFetchURL(sceneURL string) string {
	u, err := url.Parse(sceneURL)
	if err != nil || !strings.Contains(u.Path, "/videos/") {
		return ""
	}
	return s.cfg.SiteBase + u.Path
}

// enrichGrid fetches each new scene's detail page in place. A failed fetch is
// reported and the listing scene is kept as-is.
func (s *Scraper) enrichGrid(ctx context.Context, opts scraper.ListOpts, scenes []models.Scene, out chan<- scraper.SceneResult) {
	var idx []int
	for i := range scenes {
		if opts.KnownIDs[scenes[i].ID] || s.detailFetchURL(scenes[i].URL) == "" {
			continue
		}
		idx = append(idx, i)
	}
	if len(idx) == 0 {
		return
	}
	workers := min(scraper.WorkerCount(opts, defaultDetailWorkers), len(idx))
	scraper.Debugf(1, "carnalplus/%s: fetching %d details with %d workers", s.cfg.ID, len(idx), workers)

	jobs := make(chan int)
	var mu sync.Mutex
	var errs []error
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				if !scraper.Pace(ctx, opts.Delay) {
					return
				}
				detailURL := s.detailFetchURL(scenes[i].URL)
				body, _, err := s.fetchPage(ctx, detailURL)
				if err != nil {
					if ctx.Err() != nil {
						return
					}
					mu.Lock()
					errs = append(errs, fmt.Errorf("carnalplus/%s: detail %s: %w", s.cfg.ID, detailURL, err))
					mu.Unlock()
					continue
				}
				d := parseGridDetail(body)
				if d.date.IsZero() && d.description == "" && len(d.tags) == 0 {
					mu.Lock()
					errs = append(errs, scraper.ParseError(detailURL, fmt.Errorf("carnalplus/%s: no scene details on page", s.cfg.ID)))
					mu.Unlock()
					continue
				}
				d.apply(&scenes[i])
			}
		}()
	}
feed:
	for _, i := range idx {
		select {
		case jobs <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()

	for _, err := range errs {
		select {
		case out <- scraper.Error(err):
		case <-ctx.Done():
			return
		}
	}
}
