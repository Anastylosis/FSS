package beautifulagony

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

// siteBase is a var so offline tests can point the walk at an httptest server;
// nothing else reassigns it.
var siteBase = "https://beautifulagony.com"

const (
	pageSize = 20
	// maxPages bounds the offset walk. The catalogue is a few thousand scenes;
	// this is far above it and exists only so a misbehaving origin cannot spin.
	maxPages = 500
)

type Scraper struct {
	client *http.Client
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second)}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return "beautifulagony" }

func (s *Scraper) Patterns() []string {
	return []string{"beautifulagony.com"}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?beautifulagony\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	delay := opts.Delay

	totalSent := false
	// The offset walk has no page count to end on and the listing carries no
	// pager, so an origin that ever ignored `offset` and echoed page 1 would
	// loop forever re-emitting the same scenes. A page yielding no id the walk
	// has not already seen is that echo, and ends it; maxPages is the backstop.
	seen := map[string]bool{}
	for offset := 0; offset < maxPages*pageSize; offset += pageSize {
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
		scraper.Debugf(1, "beautifulagony: fetching page %d", offset)

		pageURL := fmt.Sprintf("%s/public/main.php?page=view&mode=all&offset=%d", siteBase, offset)
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
			scraper.Debugf(1, "beautifulagony: %d total scenes", 0)
			select {
			case out <- scraper.Progress(0):
			case <-ctx.Done():
				return
			}
			totalSent = true
		}

		fresh := 0
		for _, scene := range scenes {
			if seen[scene.ID] {
				continue
			}
			seen[scene.ID] = true
			fresh++
			if opts.KnownIDs[scene.ID] {
				scraper.Debugf(1, "beautifulagony: hit known ID, stopping early")
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
		if fresh == 0 {
			scraper.Debugf(1, "beautifulagony: offset %d repeated a page already seen, stopping", offset)
			return
		}
	}
	scraper.Debugf(1, "beautifulagony: stopped at the %d-page cap", maxPages)
}

var (
	idRe    = regexp.MustCompile(`class="agonyid">#(\d+)</font>`)
	dateRe  = regexp.MustCompile(`class="thumb_release_date_div">\s*(\d{2}\s+\w+\s+\d{4})`)
	thumbRe = regexp.MustCompile(`src="(https://bcdn\.beautifulagony\.com/[^"]+)"`)
	hdRe    = regexp.MustCompile(`class="hdtext"`)
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
	blocks := splitVidBlocks(body)
	now := time.Now().UTC()

	var scenes []models.Scene
	for _, block := range blocks {
		im := idRe.FindSubmatch(block)
		if im == nil {
			continue
		}
		agonyID := string(im[1])

		var date time.Time
		if dm := dateRe.FindSubmatch(block); dm != nil {
			date, _ = time.Parse("02 Jan 2006", strings.TrimSpace(string(dm[1])))
		}

		var thumbnail string
		if tm := thumbRe.FindSubmatch(block); tm != nil {
			thumbnail = string(tm[1])
		}

		var resolution string
		if hdRe.Match(block) {
			resolution = "HD"
		}

		scenes = append(scenes, models.Scene{
			ID:         agonyID,
			SiteID:     "beautifulagony",
			StudioURL:  studioURL,
			Title:      "Agony #" + agonyID,
			URL:        siteBase + "/public/main.php?page=view&mode=all",
			Date:       date.UTC(),
			Thumbnail:  thumbnail,
			Studio:     "Beautiful Agony",
			Resolution: resolution,
			ScrapedAt:  now,
		})
	}

	return scenes
}

var vidBlockRe = regexp.MustCompile(`(?s)<div class="vid">`)

func splitVidBlocks(body []byte) [][]byte {
	locs := vidBlockRe.FindAllIndex(body, -1)
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
