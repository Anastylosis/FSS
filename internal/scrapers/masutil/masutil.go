package masutil

import (
	"context"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

type SiteConfig struct {
	SiteID string
	Domain string
	PageID string // MAS page template ID for the videos listing
	Base   string // e.g. "https://www.plumperpass.com"
}

type Scraper struct {
	cfg     SiteConfig
	Client  *http.Client
	matchRe *regexp.Regexp
}

func New(cfg SiteConfig) *Scraper {
	re := regexp.MustCompile(`^https?://(?:www\.)?` + regexp.QuoteMeta(cfg.Domain) + `(?:/|$)`)
	return &Scraper{
		cfg:     cfg,
		Client:  httpx.NewClient(30 * time.Second),
		matchRe: re,
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return s.cfg.SiteID }

func (s *Scraper) Patterns() []string {
	return []string{s.cfg.Domain}
}

func (s *Scraper) MatchesURL(u string) bool { return s.matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	now := time.Now().UTC()
	scraper.Debugf(1, "%s: scraping videos listing", s.cfg.SiteID)

	scraper.Paginate(ctx, opts, s.cfg.SiteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := buildPageURL(s.cfg.Base, s.cfg.PageID, page)

		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		cards := ParseCards(body)
		if len(cards) == 0 {
			return scraper.PageResult{Done: true}, nil
		}

		maxPage := ExtractMaxPage(body)

		total := 0
		if page == 1 && maxPage > 0 {
			total = maxPage * len(cards)
		}

		scenes := make([]models.Scene, 0, len(cards))
		for _, c := range cards {
			scenes = append(scenes, toScene(s.cfg, c, studioURL, now))
		}

		return scraper.PageResult{
			Scenes: scenes,
			Total:  total,
			Done:   maxPage > 0 && page >= maxPage,
		}, nil
	})
}

func (s *Scraper) fetchPage(ctx context.Context, pageURL string) (string, error) {
	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     pageURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	b, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func buildPageURL(base, pageID string, page int) string {
	return base + "/index.php?videos&a=" + pageID + "_" + strconv.Itoa(page)
}

// --- card parsing ---

type CardData struct {
	LID        string
	Title      string
	Date       string
	Performers []string
	Thumbnail  string
}

var (
	startLinkRe = regexp.MustCompile(`<!-- start_link -->`)

	refstatRe = regexp.MustCompile(`refstat\.php\?lid=(\d+)`)

	vidnameTitleRe = regexp.MustCompile(`(?s)<p class="vidname"><a[^>]*>(.*?)</a>`)
	// The cast link is the second anchor inside the vidname paragraph. It is
	// found in two steps rather than one pattern, because RE2 has no lookahead
	// and a single `(?s)…<br…<a…` crossed the closing </p> and matched the
	// join call-to-action further down the card ("Watch Full Scene Instantly!").
	vidnameBlockRe     = regexp.MustCompile(`(?s)<p class="vidname">(.*?)</p>`)
	vidnamePerformerRe = regexp.MustCompile(`(?s)<br\s*/?>\s*<a[^>]*>(.*?)</a>`)

	h3PerformerRe = regexp.MustCompile(`(?s)<h3><a[^>]*>(.*?)</a></h3>`)
	plainTitleRe  = regexp.MustCompile(`(?s)<div class="itemminfo">(?:.*?<h3>.*?</h3>)?.*?<p>([^<]+)</p>`)

	dateRe      = regexp.MustCompile(`<p class="date">(.*?)</p>`)
	dateInnerRe = regexp.MustCompile(`(?s)<!--\s*<p class="date">(.*?)</p>\s*-->`)

	thumbRe = regexp.MustCompile(`<img[^>]+src="([^"]*faceimages/[^"]*)"`)

	maxPageRe = regexp.MustCompile(`class="pagenumbers">(\d+)</a>`)
	// The page <select> renders its options inconsistently: some are bare
	// (`value=12`), some quoted (`value="12"`), and the current page carries a
	// `selected` attribute before the closing bracket. Requiring an unquoted
	// value followed immediately by `>` missed all but the plainest form, which
	// left maxPage at 0 and the walk with no page-count termination.
	maxPageSelectRe = regexp.MustCompile(`<option[^>]*\bvalue=["']?(\d+)["']?`)
)

func ParseCards(body string) []CardData {
	indices := startLinkRe.FindAllStringIndex(body, -1)
	if len(indices) == 0 {
		return nil
	}

	var cards []CardData
	for i, idx := range indices {
		end := len(body)
		if i+1 < len(indices) {
			end = indices[i+1][0]
		}
		block := body[idx[1]:end]

		if card := parseCard(block); card != nil {
			cards = append(cards, *card)
		}
	}
	return cards
}

func parseCard(block string) *CardData {
	lids := refstatRe.FindAllStringSubmatch(block, -1)
	if len(lids) == 0 {
		return nil
	}

	card := &CardData{LID: lids[0][1]}

	if m := vidnameTitleRe.FindStringSubmatch(block); m != nil {
		card.Title = cleanText(m[1])
	} else if m := plainTitleRe.FindStringSubmatch(block); m != nil {
		card.Title = cleanText(m[1])
	}

	if m := h3PerformerRe.FindStringSubmatch(block); m != nil {
		name := cleanText(m[1])
		if name != "" {
			card.Performers = []string{name}
		}
	} else if b := vidnameBlockRe.FindStringSubmatch(block); b != nil {
		if m := vidnamePerformerRe.FindStringSubmatch(b[1]); m != nil {
			if name := cleanText(m[1]); name != "" {
				card.Performers = []string{name}
			}
		}
	}

	if m := dateRe.FindStringSubmatch(block); m != nil {
		card.Date = extractDateText(m[1])
	} else if m := dateInnerRe.FindStringSubmatch(block); m != nil {
		card.Date = extractDateText(m[1])
	}

	if m := thumbRe.FindStringSubmatch(block); m != nil {
		card.Thumbnail = m[1]
	}

	return card
}

func extractDateText(raw string) string {
	s := raw
	if idx := strings.Index(s, "<br"); idx >= 0 {
		s = s[:idx]
	}
	if idx := strings.Index(s, "<a"); idx >= 0 {
		s = s[:idx]
	}
	return strings.TrimSpace(html.UnescapeString(s))
}

func cleanText(s string) string {
	s = strings.TrimSpace(s)
	return html.UnescapeString(s)
}

func ExtractMaxPage(body string) int {
	matches := maxPageRe.FindAllStringSubmatch(body, -1)
	maxPage := 0
	for _, m := range matches {
		if n, err := strconv.Atoi(m[1]); err == nil && n > maxPage {
			maxPage = n
		}
	}
	if maxPage > 0 {
		return maxPage
	}

	for _, m := range maxPageSelectRe.FindAllStringSubmatch(body, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil && n > maxPage {
			maxPage = n
		}
	}
	return maxPage
}

var dateLayouts = []string{
	"January 2, 2006",
	"Jan 02, 2006",
	"Jan 2, 2006",
}

func toScene(cfg SiteConfig, card CardData, studioURL string, now time.Time) models.Scene {
	sceneURL := cfg.Base + "/t1/refstat.php?lid=" + card.LID + "&sid=" + cfg.PageID

	sc := models.Scene{
		ID:        card.LID,
		SiteID:    cfg.SiteID,
		StudioURL: studioURL,
		Title:     card.Title,
		URL:       sceneURL,
		ScrapedAt: now,
	}

	if card.Thumbnail != "" {
		if strings.HasPrefix(card.Thumbnail, "http") {
			sc.Thumbnail = card.Thumbnail
		} else {
			sc.Thumbnail = cfg.Base + "/" + card.Thumbnail
		}
	}

	sc.Performers = card.Performers

	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, card.Date); err == nil {
			sc.Date = t
			break
		}
	}

	return sc
}
