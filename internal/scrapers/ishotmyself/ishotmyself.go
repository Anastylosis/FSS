package ishotmyself

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteBase = "https://ishotmyself.com"
	pageSize = 25
)

type Scraper struct {
	client *http.Client
	// base is the site root used for *fetches*, a field so offline tests can
	// redirect them. Output fields (Scene.URL, Thumbnail) keep the const: a
	// scene's public address must not become a 127.0.0.1 URL because a test
	// happened to fetch it from there.
	base string
}

// RecommendedDelay is the pace this scraper was written against. It is **not**
// silently enforced — the operator's `opts.Delay` is always honoured, including
// `--delay 0` — but `WarnDelayBelow` surfaces a one-shot stderr warning below it.
const RecommendedDelay = 500 * time.Millisecond

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second), base: siteBase}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return "ishotmyself" }

func (s *Scraper) Patterns() []string {
	return []string{
		"ishotmyself.com",
		"ishotmyself.com/public/view_artist.php?artid={id}&folio={name}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?ishotmyself\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var artistPageRe = regexp.MustCompile(`artid=([A-Za-z0-9]+)`)

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	delay := opts.Delay
	scraper.WarnDelayBelow("ishotmyself", opts.Delay, RecommendedDelay)

	baseURL := s.base + "/public/general.php?p=folios&content=vid&sortby=dt&order=desc&view=tmb"
	if m := artistPageRe.FindStringSubmatch(studioURL); m != nil {
		baseURL = s.base + "/public/general.php?p=folios&content=vid&sortby=dt&order=desc&view=tmb&artid=" + m[1]
	}

	totalSent := false
	for offset := 0; ; offset += pageSize {
		if ctx.Err() != nil {
			return
		}

		if offset > 0 && !scraper.Pace(ctx, delay) {
			return
		}
		scraper.Debugf(1, "ishotmyself: fetching page %d", offset)

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
			scraper.Debugf(1, "ishotmyself: %d total scenes", 0)
			select {
			case out <- scraper.Progress(0):
			case <-ctx.Done():
				return
			}
			totalSent = true
		}

		for _, scene := range scenes {
			if opts.KnownIDs[scene.ID] {
				scraper.Debugf(1, "ishotmyself: hit known ID, stopping early")
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

var (
	artistRe = regexp.MustCompile(`view_artist\.php\?artid=([A-Za-z0-9]+)&(?:amp;)?folio=([A-Za-z0-9_]+)'>([^<]+)</a>`)
	dateRe   = regexp.MustCompile(`(\d{2}\s+\w{3}\s+\d{2})\s*<`)
	thumbRe  = regexp.MustCompile(`SRC='(/public/view_image\.php\?g=[^']+)'`)
)

func (s *Scraper) fetchPage(ctx context.Context, pageURL, studioURL string) ([]models.Scene, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     pageURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return nil, err
	}

	return parseListingPage(body, studioURL), nil
}

func parseListingPage(body []byte, studioURL string) []models.Scene {
	blocks := splitBlocks(body)
	now := time.Now().UTC()

	var scenes []models.Scene
	for _, block := range blocks {
		am := artistRe.FindSubmatch(block)
		if am == nil {
			continue
		}
		artistID := string(am[1])
		folioName := string(am[2])
		performer := strings.TrimSpace(string(am[3]))

		var date time.Time
		if dm := dateRe.FindSubmatch(block); dm != nil {
			date, _ = time.Parse("2 Jan 06", strings.TrimSpace(string(dm[1])))
		}

		var thumbnail string
		if tm := thumbRe.FindSubmatch(block); tm != nil {
			thumbnail = siteBase + string(tm[1])
		}

		title := strings.ReplaceAll(folioName, "_", " ")

		var performers []string
		if performer != "" {
			performers = []string{strings.ReplaceAll(performer, "_", " ")}
		}

		sceneURL := fmt.Sprintf("%s/public/view_artist.php?artid=%s&folio=%s", siteBase, artistID, folioName)

		scenes = append(scenes, models.Scene{
			// The site needs BOTH the artist id and the folio to address a
			// scene, and the folio alone is not unique: across 554 scenes there
			// are only 536 distinct folio names, with 17 of them used by two
			// artists each (`TheJewels` and `supershine` are both F0394's and
			// F0888's). Keyed on the folio alone those pairs overwrote each
			// other on Save and cross-triggered the early stop.
			ID:         artistID + "/" + folioName,
			SiteID:     "ishotmyself",
			StudioURL:  studioURL,
			Title:      title,
			URL:        sceneURL,
			Date:       date.UTC(),
			Thumbnail:  thumbnail,
			Performers: performers,
			Studio:     "I Shot Myself",
			ScrapedAt:  now,
		})
	}

	return scenes
}

var blockSplitRe = regexp.MustCompile(`(?s)<div class='search-results-thumb'>`)

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
