package ifeelmyself

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

// siteBase is a var, not a const, so tests can point the page walk at an
// httptest server.
var siteBase = "https://ifeelmyself.com"

const pageSize = 12

type Scraper struct {
	client *http.Client
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return "ifeelmyself" }

func (s *Scraper) Patterns() []string {
	return []string{
		"ifeelmyself.com",
		"ifeelmyself.com/public/main.php?page=artist_bio&artist_id={id}",
		"ifeelmyself.com/public/main.php?page=quick_search&keyword={name}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?ifeelmyself\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var (
	artistPageRe = regexp.MustCompile(`artist_id=([A-Za-z0-9]+)`)
	searchPageRe = regexp.MustCompile(`[?&]keyword=([^&]+)`)
	artistLinkRe = regexp.MustCompile(`artist_id=([A-Za-z0-9]+)'[^>]*>([^<]+)</a>`)
)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	if keyword := extractSearchKeyword(studioURL); keyword != "" {
		s.runSearch(ctx, studioURL, keyword, opts, out)
		return
	}

	if m := artistPageRe.FindStringSubmatch(studioURL); m != nil {
		s.runArtist(ctx, studioURL, m[1], opts.Delay, opts, out)
		return
	}

	s.runPaginated(ctx, studioURL, opts.Delay, opts, out)
}

func extractSearchKeyword(studioURL string) string {
	if m := searchPageRe.FindStringSubmatch(studioURL); m != nil {
		decoded, err := url.QueryUnescape(m[1])
		if err != nil {
			return m[1]
		}
		return decoded
	}
	return ""
}

func (s *Scraper) runPaginated(ctx context.Context, studioURL string, delay time.Duration, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	baseURL := siteBase + "/public/main.php?page=view&mode=all"

	totalSent := false
	// The listing offsets by film but renders one card per artist, so a film
	// with several artists pushes the window out of step and the last card of
	// one page comes back as the first card of the next. Emitting it twice
	// broke the (ID, SiteID) uniqueness the store keys on.
	seen := make(map[string]bool)
	for offset := 0; ; offset += pageSize {
		if ctx.Err() != nil {
			return
		}

		if offset > 0 {
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return
			}
		}
		scraper.Debugf(1, "ifeelmyself: fetching page %d", offset)

		pageURL := fmt.Sprintf("%s&offset=%d", baseURL, offset)
		scenes, err := s.fetchPage(ctx, pageURL, studioURL)
		if err != nil {
			select {
			case out <- scraper.Error(fmt.Errorf("offset %d: %w", offset, err)):
			case <-ctx.Done():
			}
			return
		}

		if len(scenes) == 0 {
			return
		}

		if !totalSent {
			scraper.Debugf(1, "ifeelmyself: %d total scenes", 0)
			select {
			case out <- scraper.Progress(0):
			case <-ctx.Done():
				return
			}
			totalSent = true
		}

		for _, scene := range scenes {
			if seen[scene.ID] {
				continue
			}
			seen[scene.ID] = true

			if opts.KnownIDs[scene.ID] {
				scraper.Debugf(1, "ifeelmyself: hit known ID, stopping early")
				select {
				case out <- scraper.StoppedEarly():
				case <-ctx.Done():
				}
				return
			}
			select {
			case out <- scraper.Scene(scene):
			case <-ctx.Done():
				return
			}
		}
	}
}

func (s *Scraper) runArtist(ctx context.Context, studioURL string, artistID string, delay time.Duration, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	name, err := s.resolveArtistName(ctx, artistID, delay)
	if err != nil {
		select {
		case out <- scraper.Error(fmt.Errorf("resolve artist %s: %w", artistID, err)):
		case <-ctx.Done():
		}
		return
	}

	s.runSearch(ctx, studioURL, name, opts, out)
}

func (s *Scraper) resolveArtistName(ctx context.Context, artistID string, delay time.Duration) (string, error) {
	const maxPages = 20
	for page := 0; page < maxPages; page++ {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if page > 0 {
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		scraper.Debugf(1, "ifeelmyself: fetching page %d", page)

		pageURL := fmt.Sprintf("%s/public/main.php?page=view&mode=all&offset=%d", siteBase, page*pageSize)
		body, err := s.fetchBody(ctx, pageURL)
		if err != nil {
			return "", err
		}

		for _, m := range artistLinkRe.FindAllSubmatch(body, -1) {
			if string(m[1]) == artistID {
				return strings.TrimSpace(string(m[2])), nil
			}
		}

		blocks := splitBlocks(body)
		if len(blocks) == 0 {
			break
		}
	}

	return "", fmt.Errorf("artist_id %s not found in first %d listing pages — try quick_search URL instead", artistID, maxPages)
}

func (s *Scraper) runSearch(ctx context.Context, studioURL string, keyword string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	body, err := s.postSearch(ctx, keyword)
	if err != nil {
		select {
		case out <- scraper.Error(fmt.Errorf("search %q: %w", keyword, err)):
		case <-ctx.Done():
		}
		return
	}

	scenes := parseListingPage(body, studioURL)
	if len(scenes) == 0 {
		return
	}

	select {
	case out <- scraper.Progress(len(scenes)):
	case <-ctx.Done():
		return
	}

	for _, scene := range scenes {
		if opts.KnownIDs[scene.ID] {
			scraper.Debugf(1, "ifeelmyself: hit known ID, stopping early")
			select {
			case out <- scraper.StoppedEarly():
			case <-ctx.Done():
			}
			return
		}
		select {
		case out <- scraper.Scene(scene):
		case <-ctx.Done():
			return
		}
	}
}

func (s *Scraper) postSearch(ctx context.Context, keyword string) ([]byte, error) {
	form := url.Values{"keyword": {keyword}, "view_by": {"thumbnails"}}
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:  siteBase + "/public/main.php?page=quick_search",
		Body: []byte(form.Encode()),
		Headers: map[string]string{
			"User-Agent":   httpx.UserAgentFirefox,
			"Content-Type": "application/x-www-form-urlencoded",
		},
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

var (
	cardRe      = regexp.MustCompile(`(?s)<table[^>]*class="ThumbTab ppss-scene"[^>]*data-scene-id="(\d+)"[^>]*data-scene-price="([^"]*)"[^>]*>.*?</TABLE>`)
	performerRe = regexp.MustCompile(`artist_bio&amp;artist_id=([A-Za-z0-9]+)'[^>]*>([^<]+)</a>|artist_bio&artist_id=([A-Za-z0-9]+)'[^>]*>([^<]+)</a>`)
	titleRe     = regexp.MustCompile(`&nbsp;in&nbsp;\s*\n?\s*"([^"]+)"`)
	durationRe  = regexp.MustCompile(`(?:4K|HD|SD)\s+Video,\s*(\d+):(\d+)\s*min`)
	dateRe      = regexp.MustCompile(`(\d{2}\s+\w+\s+\d{4})`)
	thumbRe     = regexp.MustCompile(`(?i:src)='(https://bcdn\.ifeelmyself\.com/[^']+)'`)
	categoryRe  = regexp.MustCompile(`(?s)<b>Categories:</b>(.*?)</table>`)
	catItemRe   = regexp.MustCompile(`>\s*([A-Za-z][A-Za-z ]+?)\s*<`)
	tagsRe      = regexp.MustCompile(`class="tags-list-item-tag">([^<]+)<`)
)

func (s *Scraper) fetchPage(ctx context.Context, pageURL, studioURL string) ([]models.Scene, error) {
	body, err := s.fetchBody(ctx, pageURL)
	if err != nil {
		return nil, err
	}
	return parseListingPage(body, studioURL), nil
}

func (s *Scraper) fetchBody(ctx context.Context, pageURL string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     pageURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return httpx.ReadBody(resp.Body)
}

func parseListingPage(body []byte, studioURL string) []models.Scene {
	// Find the text before each card for performer/title/date/duration.
	// Cards are preceded by a DispResults table with metadata.
	blocks := splitBlocks(body)

	var scenes []models.Scene
	now := time.Now().UTC()

	for _, block := range blocks {
		card := cardRe.Find(block)
		if card == nil {
			continue
		}

		idMatch := cardRe.FindSubmatch(block)
		if idMatch == nil {
			continue
		}
		filmID := string(idMatch[1])
		price := string(idMatch[2])

		var performer, performerID string
		if pm := performerRe.FindSubmatch(block); pm != nil {
			if pm[1] != nil {
				performerID = string(pm[1])
				performer = string(pm[2])
			} else {
				performerID = string(pm[3])
				performer = string(pm[4])
			}
		}

		var title string
		if tm := titleRe.FindSubmatch(block); tm != nil {
			title = string(tm[1])
		}
		if title == "" {
			title = "Scene #" + filmID
		}

		var duration int
		if dm := durationRe.FindSubmatch(block); dm != nil {
			mins, _ := strconv.Atoi(string(dm[1]))
			secs, _ := strconv.Atoi(string(dm[2]))
			duration = mins*60 + secs
		}

		var date time.Time
		if dtm := dateRe.FindSubmatch(block); dtm != nil {
			date, _ = time.Parse("02 Jan 2006", strings.TrimSpace(string(dtm[1])))
		}

		var thumbnail string
		if thm := thumbRe.FindSubmatch(block); thm != nil {
			thumbnail = string(thm[1])
		}

		var categories []string
		if cm := categoryRe.FindSubmatch(block); cm != nil {
			for _, ci := range catItemRe.FindAllSubmatch(cm[1], -1) {
				cat := strings.TrimSpace(string(ci[1]))
				if cat != "" && cat != "Categories" {
					categories = append(categories, cat)
				}
			}
		}

		var tags []string
		for _, tm := range tagsRe.FindAllSubmatch(block, -1) {
			tag := strings.TrimSpace(string(tm[1]))
			if tag != "" {
				tags = append(tags, tag)
			}
		}

		var performers []string
		if performer != "" {
			performers = []string{performer}
		}

		sceneURL := siteBase + "/public/main.php?page=artist_bio&artist_id=" + performerID
		if performerID == "" {
			sceneURL = siteBase + "/public/main.php?page=view&mode=all"
		}

		// data-scene-id is a film id, not a scene id: the listing runs the
		// same film once per artist who appears in it, so three cards with
		// different titles and different artists all carry 24195. Keying on it
		// alone made those scenes collide on (ID, SiteID) and overwrite each
		// other in the store. Qualify it with the artist, the way ishotmyself
		// keys on artist + folio.
		sceneID := filmID
		if performerID != "" {
			sceneID = filmID + "/" + performerID
		}

		scene := models.Scene{
			ID:          sceneID,
			SiteID:      "ifeelmyself",
			StudioURL:   studioURL,
			Title:       title,
			URL:         sceneURL,
			Date:        date.UTC(),
			Duration:    duration,
			Description: "",
			Thumbnail:   thumbnail,
			Performers:  performers,
			Studio:      "I Feel Myself",
			Tags:        tags,
			Categories:  categories,
			ScrapedAt:   now,
		}

		if p, err := strconv.ParseFloat(price, 64); err == nil && p > 0 {
			scene.AddPrice(models.PriceSnapshot{
				Date:    now,
				Regular: p,
			})
		}

		scenes = append(scenes, scene)
	}

	return scenes
}

var blockSplitRe = regexp.MustCompile(`(?s)<div class="ThumbRec">`)

func splitBlocks(body []byte) [][]byte {
	locs := blockSplitRe.FindAllIndex(body, -1)
	if len(locs) == 0 {
		return nil
	}

	var blocks [][]byte
	for i, loc := range locs {
		start := loc[0]
		var end int
		if i+1 < len(locs) {
			end = locs[i+1][0]
		} else {
			end = len(body)
		}
		blocks = append(blocks, body[start:end])
	}
	return blocks
}
