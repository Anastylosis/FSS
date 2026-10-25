package nanairo

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID      = "nanairo"
	studioName  = "S-Cute"
	defaultBase = "https://nanairo.co"
	pageSize    = 24
)

// catalogueStart is the earliest release date the listing carries: nanairo
// opened in February 2016 and stamped the whole s-cute.com back catalogue
// with that month. The day walk checks for anything older before stopping.
var catalogueStart = time.Date(2016, 2, 1, 0, 0, 0, 0, time.UTC)

// searchFloor bounds the date bisection used for a search URL.
var searchFloor = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

var jst = time.FixedZone("JST", 9*60*60)

type Scraper struct {
	client *http.Client
	base   string
	now    func() time.Time
}

func New() *Scraper {
	return &Scraper{
		client: httpx.NewClient(30 * time.Second),
		base:   defaultBase,
		now:    time.Now,
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)
var _ scraper.StudioURLCanonicalizer = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"nanairo.co",
		"nanairo.co/{lang}",
		"nanairo.co/{lang}/videos",
		"nanairo.co/{lang}/videos?text={query}",
		"nanairo.co/{lang}/search?text={query}",
		"s-cute.com",
	}
}

var (
	catalogueRe = regexp.MustCompile(`^https?://(?:www\.)?nanairo\.co(?:/[a-z]{2}(?:-[a-z]+)?(?:/videos)?)?/?(?:\?.*)?$`)
	searchRe    = regexp.MustCompile(`^https?://(?:www\.)?nanairo\.co/[a-z]{2}(?:-[a-z]+)?/search\?(?:.*&)?text=[^&]+`)
	scuteRe     = regexp.MustCompile(`^https?://(?:www\.)?s-cute\.com/?$`)
	performerRe = regexp.MustCompile(`^(.+?)\s*#\d`)
)

func (s *Scraper) MatchesURL(u string) bool {
	return catalogueRe.MatchString(u) || searchRe.MatchString(u) || scuteRe.MatchString(u)
}

// PreferredStudioURL folds every spelling of the unfiltered catalogue —
// including s-cute.com, which now only points at nanairo — onto one key.
func (s *Scraper) PreferredStudioURL(studioURL string) string {
	if !s.MatchesURL(studioURL) || searchText(studioURL) != "" {
		return ""
	}
	return defaultBase
}

// searchText returns the text= filter of a search URL, or "".
func searchText(studioURL string) string {
	u, err := url.Parse(studioURL)
	if err != nil || !strings.Contains(u.Host, "nanairo.co") {
		return ""
	}
	return strings.TrimSpace(u.Query().Get("text"))
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

type item struct {
	ID           int    `json:"id"`
	Subject      string `json:"subject"`
	Caption      string `json:"caption"`
	ThumbnailURL string `json:"thumbnailUrl"`
}

// fetcher paces every request after the first: a date walk issues many small
// requests, and each one counts against the operator's delay.
type fetcher struct {
	s       *Scraper
	delay   time.Duration
	started bool
}

func (f *fetcher) page(ctx context.Context, text string, from, to time.Time, page int) ([]item, error) {
	if f.started && !scraper.Pace(ctx, f.delay) {
		return nil, ctx.Err()
	}
	f.started = true
	q := url.Values{}
	if text != "" {
		q.Set("text", text)
	}
	if !from.IsZero() {
		q.Set("date", from.Format("2006-01-02")+","+to.Format("2006-01-02"))
	}
	if page > 1 {
		q.Set("page", strconv.Itoa(page))
	}
	u := f.s.base + "/ja/videos.json"
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	// The HTML pages are geo-blocked outside Japan; the JSON listing the
	// page's own script reads is not, and Accept decides which one answers.
	var items []item
	if err := httpx.DoJSON(ctx, f.s.client, httpx.Request{
		URL:     u,
		Headers: httpx.XHRHeaders(httpx.UserAgentFirefox, ""),
	}, &items); err != nil {
		return nil, err
	}
	return items, nil
}

// all walks every page of one query.
func (f *fetcher) all(ctx context.Context, text string, from, to time.Time) ([]item, error) {
	var res []item
	for page := 1; ; page++ {
		items, err := f.page(ctx, text, from, to, page)
		if err != nil {
			return nil, err
		}
		res = append(res, items...)
		if len(items) < pageSize {
			return res, nil
		}
	}
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	f := &fetcher{s: s, delay: opts.Delay}
	if text := searchText(studioURL); text != "" {
		scraper.Debugf(1, "%s: search mode for %q", siteID, text)
		s.runSearch(ctx, f, studioURL, text, opts, out)
		return
	}
	s.runCatalogue(ctx, f, studioURL, opts, out)
}

func (s *Scraper) today() time.Time {
	n := s.now().In(jst)
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
}

// runCatalogue walks the catalogue one release day at a time, newest first.
// The listing carries no dates and its default order is not chronological;
// the date filter is the only source of a release date, so a day-sized
// filter both dates every scene and gives the walk an order KnownIDs can
// stop on.
func (s *Scraper) runCatalogue(ctx context.Context, f *fetcher, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	start := s.today()
	floor := catalogueStart
	now := time.Now().UTC()
	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		day := start.AddDate(0, 0, -(page - 1))
		f.started = false // Paginate has already paced this day
		items, err := f.all(ctx, "", day, day)
		if err != nil {
			return scraper.PageResult{}, fmt.Errorf("%s: %w", day.Format("2006-01-02"), err)
		}
		scenes := make([]models.Scene, 0, len(items))
		for _, it := range items {
			scenes = append(scenes, toScene(s.base, studioURL, it, day, now))
		}
		res := scraper.PageResult{Scenes: scenes, Continue: true}
		if !day.After(floor) {
			older, err := f.page(ctx, "", searchFloor, day.AddDate(0, 0, -1), 1)
			if err != nil {
				return scraper.PageResult{}, err
			}
			if len(older) == 0 {
				res.Done = true
			} else {
				floor = floor.AddDate(-1, 0, 0)
				scraper.Debugf(1, "%s: scenes dated before %s, extending walk to %s", siteID, day.Format("2006-01-02"), floor.Format("2006-01-02"))
			}
		}
		return res, nil
	})
}

// runSearch collects a text search's matches, then dates them by bisecting
// the date filter: each split queries one half and the other half's set
// follows by subtraction, so only ranges holding a match cost a request.
func (s *Scraper) runSearch(ctx context.Context, f *fetcher, studioURL, text string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	items, err := f.all(ctx, text, time.Time{}, time.Time{})
	if err != nil {
		send(ctx, out, scraper.Error(err))
		return
	}
	pending := map[int]item{}
	skipped := false
	for _, it := range items {
		if opts.KnownIDs[strconv.Itoa(it.ID)] {
			skipped = true
			continue
		}
		pending[it.ID] = it
	}
	if len(pending) > 0 && !send(ctx, out, scraper.Progress(len(pending))) {
		return
	}
	scraper.Debugf(1, "%s: dating %d search results", siteID, len(pending))

	now := time.Now().UTC()
	emitted := map[int]bool{}
	emit := func(it item, date time.Time) bool {
		emitted[it.ID] = true
		return send(ctx, out, scraper.Scene(toScene(s.base, studioURL, it, date, now)))
	}
	if err := f.bisect(ctx, text, searchFloor, s.today().AddDate(1, 0, 0), pending, emit); err != nil {
		if ctx.Err() != nil || errors.Is(err, errStopped) {
			return
		}
		if !send(ctx, out, scraper.Error(err)) {
			return
		}
		// Whatever the bisection did not reach is still a match; it goes out
		// undated rather than not at all.
		for id, it := range pending {
			if !emitted[id] && !emit(it, time.Time{}) {
				return
			}
		}
	}
	if skipped {
		send(ctx, out, scraper.StoppedEarly())
	}
}

var errStopped = errors.New("consumer stopped")

// bisect dates set, the matches known to fall within [from, to], by querying
// the older half and deriving the newer half by subtraction. The newer half
// is resolved first, so scenes are emitted newest first as they are dated.
func (f *fetcher) bisect(ctx context.Context, text string, from, to time.Time, set map[int]item, emit func(item, time.Time) bool) error {
	if len(set) == 0 {
		return nil
	}
	if from.Equal(to) {
		ids := make([]int, 0, len(set))
		for id := range set {
			ids = append(ids, id)
		}
		sort.Sort(sort.Reverse(sort.IntSlice(ids)))
		for _, id := range ids {
			if !emit(set[id], from) {
				return errStopped
			}
		}
		return nil
	}
	days := int(to.Sub(from).Hours() / 24)
	mid := from.AddDate(0, 0, days/2)
	got, err := f.all(ctx, text, from, mid)
	if err != nil {
		return err
	}
	inOlder := map[int]bool{}
	for _, it := range got {
		inOlder[it.ID] = true
	}
	older, newer := map[int]item{}, map[int]item{}
	for id, it := range set {
		if inOlder[id] {
			older[id] = it
		} else {
			newer[id] = it
		}
	}
	if err := f.bisect(ctx, text, mid.AddDate(0, 0, 1), to, newer, emit); err != nil {
		return err
	}
	return f.bisect(ctx, text, from, mid, older, emit)
}

func send(ctx context.Context, out chan<- scraper.SceneResult, r scraper.SceneResult) bool {
	select {
	case out <- r:
		return true
	case <-ctx.Done():
		return false
	}
}

func toScene(base, studioURL string, it item, date, now time.Time) models.Scene {
	id := strconv.Itoa(it.ID)
	return models.Scene{
		ID:          id,
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       strings.TrimSpace(it.Subject),
		URL:         base + "/ja/videos/" + id,
		Date:        date,
		Description: strings.TrimSpace(it.Caption),
		Thumbnail:   absURL(it.ThumbnailURL),
		Performers:  performers(it.Subject),
		Studio:      studioName,
		ScrapedAt:   now,
	}
}

func absURL(u string) string {
	if strings.HasPrefix(u, "//") {
		return "https:" + u
	}
	return u
}

// performers reads the cast from a subject such as "Karin & Kaho #1-1".
func performers(subject string) []string {
	m := performerRe.FindStringSubmatch(strings.TrimSpace(subject))
	if m == nil {
		return nil
	}
	var names []string
	for _, n := range strings.Split(m[1], "&") {
		if n = strings.TrimSpace(n); n != "" {
			names = append(names, n)
		}
	}
	return names
}
