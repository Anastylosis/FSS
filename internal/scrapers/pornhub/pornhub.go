package pornhub

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

type Scraper struct {
	client *http.Client
}

func New() *Scraper {
	return &Scraper{
		// Browser fingerprint, not Go's: the edge answers every Go
		// ClientHello with a 403 "Down for Maintenance" page, over HTTP/2 or
		// HTTP/1.1 and whatever headers it carries. See
		// httpx.NewBrowserTLSClient.
		client: httpx.NewBrowserTLSClient(30 * time.Second),
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() {
	scraper.Register(New())
}

func (s *Scraper) ID() string { return "pornhub" }

func (s *Scraper) Patterns() []string {
	return []string{
		"pornhub.com/pornstar/{slug}",
		"pornhub.com/channels/{slug}",
		"pornhub.com/model/{slug}",
		"pornhub.org/pornstar/{slug}",
		"pornhub.org/channels/{slug}",
		"pornhub.org/model/{slug}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:[a-z]{2,3}\.)?pornhub\.(?:com|org)/(?:pornstar|channels|model)/[\w-]+`)
var listingRe = regexp.MustCompile(`/(pornstar|channels|model)/([\w-]+)`)
var pornhubHostRe = regexp.MustCompile(`^(?:[a-z]{2,3}\.)?pornhub\.(?:com|org)$`)

func (s *Scraper) MatchesURL(u string) bool {
	return matchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- runner ----

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	// The listing is not newest-first. Walking riley-reid's page 1 gives
	// 2026-07-23, 07-21, 04-03, 03-18, 02-07, 03-30 — non-monotonic in either
	// direction, and `?o=mr` does not change it on this endpoint. An early
	// stop at the first stored id would therefore drop everything after it,
	// and `--full`'s authoritative Save would delete those scenes. Incremental
	// runs re-walk instead: more requests, no silent loss.
	opts.KnownIDs = nil

	now := time.Now().UTC()
	base := studioURL
	scraper.Paginate(ctx, opts, "pornhub", out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL, err := buildPageURL(base, page)
		if err != nil {
			return scraper.PageResult{}, err
		}
		items, total, moved, err := s.fetchPage(ctx, pageURL)
		if err == nil && page == 1 && moved != "" {
			// A model promoted to pornstar has /model/{slug}/videos
			// redirect to the bare /pornstar/{slug} profile, which holds no
			// listing; follow it to that profile's /videos.
			scraper.Debugf(1, "pornhub: %s moved to %s", pageURL, moved)
			base = moved
			if pageURL, err = buildPageURL(base, page); err != nil {
				return scraper.PageResult{}, err
			}
			items, total, _, err = s.fetchPage(ctx, pageURL)
		}
		if err != nil {
			// Past the last page the listing 404s, and that is the only
			// end-of-list signal the markup offers: `page_next` is rendered on
			// the final page too, and the old `showingCounter` total is no
			// longer in the HTML. Treated as an error it made every --full run
			// report a failure and demoted the traversal to non-authoritative.
			//
			// Only past page 1. A 404 on page 1 is a bad slug or a removed
			// performer, which must stay loud — and the 404 body carries a
			// recommendations rail whose cards parse as scenes, so it is
			// discarded rather than read.
			if page > 1 && isNotFound(err) {
				scraper.Debugf(1, "pornhub: page %d is past the last page (404) — done", page)
				return scraper.PageResult{Done: true}, nil
			}
			return scraper.PageResult{}, err
		}
		scenes := make([]models.Scene, len(items))
		for i, item := range items {
			scenes[i] = toScene(studioURL, item, now)
		}
		return scraper.PageResult{Scenes: scenes, Total: total}, nil
	})
}

// isNotFound reports whether err is the listing answering 404.
func isNotFound(err error) bool {
	var se *httpx.StatusError
	return errors.As(err, &se) && se.StatusCode == http.StatusNotFound
}

// buildPageURL derives the paginated video-list URL from a studio URL:
// /{pornstar|channels|model}/{slug}/videos?page=N. Language subdomains and
// pornhub.org serve the same listing, so they are fetched from www.pornhub.com.
func buildPageURL(studioURL string, page int) (string, error) {
	u, err := url.Parse(studioURL)
	if err != nil {
		return "", fmt.Errorf("invalid URL %q: %w", studioURL, err)
	}
	m := listingRe.FindStringSubmatch(u.Path)
	if m == nil {
		return "", fmt.Errorf("cannot extract pornhub slug from %q", studioURL)
	}
	if pornhubHostRe.MatchString(strings.ToLower(u.Hostname())) {
		u.Scheme = "https"
		u.Host = "www.pornhub.com"
	}
	u.Path = "/" + m[1] + "/" + m[2] + "/videos"
	u.RawQuery = withPage(u.Query(), page)
	return u.String(), nil
}

// withPage sets the page number, keeping everything else the operator wrote.
// Rebuilding the query from scratch silently dropped filters and sort options
// (`?o=mr`, `?hd=1`) from the URL that was actually asked for.
func withPage(q url.Values, page int) string {
	q.Set("page", strconv.Itoa(page))
	return q.Encode()
}

// ---- page fetch ----

var (
	liRe      = regexp.MustCompile(`(?s)<li[^>]*pcVideoListItem[^>]*>.*?</li>`)
	sectionRe = regexp.MustCompile(`<ul[^>]*\sid="(\w+)"`)
)

// listingSections are the lists holding the studio's own videos. Every page
// also carries cards in the header menus (hottestMenuSection and friends),
// which belong to someone else.
var listingSections = map[string]bool{
	"uploadedVideosSection":   true,
	"mostRecentVideosSection": true,
	"showAllChanelVideos":     true,
}

// fetchPage returns the page's items, and the final URL when the request was
// redirected to another performer's profile.
func (s *Scraper) fetchPage(ctx context.Context, rawURL string) ([]phItem, int, string, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL: rawURL,
		Headers: func() map[string]string {
			h := httpx.BrowserHeaders(httpx.UserAgentFirefox)
			h["Cookie"] = "platform=pc; ageVerified=1; accessAgeDisclaimerPH=1"
			return h
		}(),
	})
	if err != nil {
		return nil, 0, "", err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return nil, 0, "", fmt.Errorf("reading page: %w", err)
	}

	moved := ""
	if resp.Request != nil && resp.Request.URL != nil && !strings.HasSuffix(resp.Request.URL.Path, "/videos") {
		final := resp.Request.URL
		if !listingRe.MatchString(final.Path) {
			// Removed performers redirect to the /pornstars index, whose cards
			// are not theirs.
			return nil, 0, "", scraper.ParseError(rawURL, fmt.Errorf("redirected to %s, which is not a video listing", final))
		}
		moved = final.String()
	}

	total := 0
	if m := videoCountRe.FindSubmatch(body); m != nil {
		total, _ = strconv.Atoi(strings.ReplaceAll(string(m[1]), ",", ""))
	}

	return parseItems(body), total, moved, nil
}

// ---- parsing ----

var (
	vkeyRe       = regexp.MustCompile(`data-video-vkey="([\w]+)"`)
	titleRe      = regexp.MustCompile(`href="/view_video\.php\?viewkey=[^"]*"\s+title="([^"]+)"`)
	thumbSrcRe   = regexp.MustCompile(`<img[^>]+src="(https://[^"]+)"`)
	durRe        = regexp.MustCompile(`<var[^>]*duration[^>]*>([^<]+)</var>`)
	cdnDateRe    = regexp.MustCompile(`/videos/(\d{4})(\d{2})/(\d{2})/`)
	uploaderRe   = regexp.MustCompile(`(?s)class="usernameWrap"[^>]*>.*?<a[^>]+>([^<]+)</a>`)
	channelRe    = regexp.MustCompile(`<v-more-action-menu[^>]*\sis-channel="1"[^>]*\stitle="([^"]+)"`)
	videoCountRe = regexp.MustCompile(`showingCounter">\s*(\d[\d,]*)`)
)

type phItem struct {
	vkey      string
	title     string
	thumbnail string
	duration  int
	date      time.Time
	studio    string
}

func parseItems(body []byte) []phItem {
	var items []phItem
	for _, block := range listingBlocks(body) {
		for _, li := range liRe.FindAll(block, -1) {
			if item, ok := parseItem(li); ok {
				items = append(items, item)
			}
		}
	}
	return items
}

// listingBlocks returns the stretches of body that run from a listing
// section's <ul> to the next <ul> with an id.
func listingBlocks(body []byte) [][]byte {
	locs := sectionRe.FindAllSubmatchIndex(body, -1)
	var blocks [][]byte
	for i, loc := range locs {
		if !listingSections[string(body[loc[2]:loc[3]])] {
			continue
		}
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		blocks = append(blocks, body[loc[0]:end])
	}
	return blocks
}

func parseItem(li []byte) (phItem, bool) {
	m := vkeyRe.FindSubmatch(li)
	if m == nil {
		return phItem{}, false
	}
	item := phItem{vkey: string(m[1])}

	if mTitle := titleRe.FindSubmatch(li); mTitle != nil {
		item.title = html.UnescapeString(string(mTitle[1]))
	}

	if mThumb := thumbSrcRe.FindSubmatch(li); mThumb != nil {
		item.thumbnail = string(mThumb[1])
	}

	if mDate := cdnDateRe.FindStringSubmatch(item.thumbnail); mDate != nil {
		y, _ := strconv.Atoi(mDate[1])
		mo, _ := strconv.Atoi(mDate[2])
		d, _ := strconv.Atoi(mDate[3])
		item.date = time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC)
	}

	if mDur := durRe.FindSubmatch(li); mDur != nil {
		item.duration = parseutil.ParseDurationColon(strings.TrimSpace(string(mDur[1])))
	}

	if mStudio := uploaderRe.FindSubmatch(li); mStudio != nil {
		item.studio = strings.TrimSpace(string(mStudio[1]))
	} else if mChannel := channelRe.FindSubmatch(li); mChannel != nil {
		item.studio = strings.TrimSpace(html.UnescapeString(string(mChannel[1])))
	}

	return item, true
}

func toScene(studioURL string, item phItem, now time.Time) models.Scene {
	scene := models.Scene{
		ID:        item.vkey,
		SiteID:    "pornhub",
		StudioURL: studioURL,
		Title:     item.title,
		URL:       "https://www.pornhub.com/view_video.php?viewkey=" + item.vkey,
		Thumbnail: item.thumbnail,
		Duration:  item.duration,
		Date:      item.date,
		Studio:    item.studio,
		ScrapedAt: now,
	}
	scene.AddPrice(models.PriceSnapshot{
		Date:   now,
		IsFree: true,
	})
	return scene
}
