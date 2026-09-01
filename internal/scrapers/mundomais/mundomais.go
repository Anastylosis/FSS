// Package mundomais scrapes mundomais.com.br, the Brazilian gay studio.
//
// The catalogue is at `/galerias/videos/netvideos/pagina/{N}` — 60 cards a
// page over ~101 pages — and every field is on the card:
//
//	<article class="video-thumb-post video-preview-on-hover">
//	  <a href="/galerias/videos/netvideos/video25195-policial-a-paisana-no-banheiro">
//	    <img data-src="/mundohot/filmes/25195/foto-16x9.jpg" alt="Policial à paisana no banheiro" />
//	    <video><source data-src="/mundohot/filmes/25195/preview.mp4" /></video></a>
//	  <div class="accordion-body">Policial à paisana faz sexo com dois garotos…</div>
//	  <time datetime="2026-08-27">27 de agosto de 2026</time>
//	  <i class="bi bi-eye …"></i> 7
//	</article>
//
// There is no detail fetch — the card is the whole record.
//
// **The site is served as ISO-8859-1**, and Portuguese titles are full of
// accented characters, so every page is transcoded before parsing; reading the
// bytes as UTF-8 mangles roughly every other title.
package mundomais

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/transform"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID     = "mundomais"
	studioName = "Mundomais"
	defaultURL = "https://www.mundomais.com.br"
)

type Scraper struct {
	client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{client: httpx.NewClient(30 * time.Second), base: defaultURL}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{"mundomais.com.br", "mundomais.com.br/galerias/videos/netvideos"}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?mundomais\.com\.br(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool { return matchRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	scraper.Debugf(1, "%s: scraping full catalogue", siteID)
	seen := make(map[string]bool)
	lastPage := 0
	now := time.Now().UTC()

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		pageURL := fmt.Sprintf("%s/galerias/videos/netvideos/pagina/%d", s.base, page)
		body, err := s.fetchPage(ctx, pageURL)
		if err != nil {
			return scraper.PageResult{}, err
		}

		parsed := parseListing(body)
		if len(parsed) == 0 {
			return scraper.PageResult{}, nil
		}

		total := 0
		if page == 1 {
			lastPage = maxPage(body)
			total = lastPage * len(parsed)
		}

		scenes := make([]models.Scene, 0, len(parsed))
		for _, item := range parsed {
			if seen[item.id] {
				continue
			}
			seen[item.id] = true
			scenes = append(scenes, s.toScene(item, studioURL, now))
		}
		// A page whose cards were all seen already is the listing clamping —
		// the site re-serves the last page past the end — so it ends the walk.
		// Cards do not otherwise repeat wholesale between pages.
		return scraper.PageResult{
			Scenes: scenes,
			Total:  total,
			Done:   len(scenes) == 0 || (lastPage > 0 && page >= lastPage),
		}, nil
	})
}

func (s *Scraper) fetchPage(ctx context.Context, pageURL string) ([]byte, error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     pageURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return nil, err
	}
	return decodeLatin1(raw), nil
}

// decodeLatin1 transcodes the page to UTF-8. The site declares (and serves)
// ISO-8859-1; leaving the bytes alone mangles every accented title.
func decodeLatin1(b []byte) []byte {
	out, _, err := transform.Bytes(charmap.ISO8859_1.NewDecoder(), b)
	if err != nil {
		return b
	}
	return out
}

// ---- parsing ----

type listItem struct {
	id          string
	path        string
	title       string
	description string
	thumbnail   string
	preview     string
	date        string
	views       int
}

var (
	cardStartRe = regexp.MustCompile(`<article class="video-thumb-post`)
	cardLinkRe  = regexp.MustCompile(`href="(/galerias/videos/netvideos/video(\d+)-[^"]*)"`)
	cardAltRe   = regexp.MustCompile(`<img[^>]+alt="([^"]*)"`)
	cardThumbRe = regexp.MustCompile(`<img[^>]+data-src="([^"]+)"`)
	previewRe   = regexp.MustCompile(`<source[^>]+data-src="([^"]+\.mp4)"`)
	descRe      = regexp.MustCompile(`(?s)<div class="accordion-body">(.*?)</div>`)
	dateRe      = regexp.MustCompile(`<time datetime="(\d{4}-\d{2}-\d{2})"`)
	viewsRe     = regexp.MustCompile(`(?s)title="Views"></i>\s*([\d.]+)`)
	pageNumRe   = regexp.MustCompile(`/pagina/(\d+)`)
	tagStripRe  = regexp.MustCompile(`<[^>]*>`)
)

func parseListing(body []byte) []listItem {
	locs := cardStartRe.FindAllIndex(body, -1)
	items := make([]listItem, 0, len(locs))
	for i, loc := range locs {
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := body[loc[0]:end]

		m := cardLinkRe.FindSubmatch(block)
		if m == nil {
			continue
		}
		item := listItem{path: html.UnescapeString(string(m[1])), id: string(m[2])}
		if t := cardAltRe.FindSubmatch(block); t != nil {
			item.title = cleanText(string(t[1]))
		}
		if t := cardThumbRe.FindSubmatch(block); t != nil {
			item.thumbnail = html.UnescapeString(string(t[1]))
		}
		if t := previewRe.FindSubmatch(block); t != nil {
			item.preview = html.UnescapeString(string(t[1]))
		}
		if t := descRe.FindSubmatch(block); t != nil {
			item.description = cleanText(tagStripRe.ReplaceAllString(string(t[1]), " "))
		}
		if t := dateRe.FindSubmatch(block); t != nil {
			item.date = string(t[1])
		}
		if t := viewsRe.FindSubmatch(block); t != nil {
			item.views, _ = strconv.Atoi(strings.ReplaceAll(string(t[1]), ".", ""))
		}
		if item.title == "" {
			continue
		}
		items = append(items, item)
	}
	return items
}

// maxPage reads the highest page the pager names. It is stable across pages
// here — unlike a sliding window — and past the last page the site re-serves
// it, so the count is what ends the walk.
func maxPage(body []byte) int {
	last := 0
	for _, m := range pageNumRe.FindAllSubmatch(body, -1) {
		if n, err := strconv.Atoi(string(m[1])); err == nil && n > last {
			last = n
		}
	}
	return last
}

func cleanText(s string) string {
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, " ", " ")
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

func (s *Scraper) toScene(item listItem, studioURL string, now time.Time) models.Scene {
	sc := models.Scene{
		ID:          item.id,
		SiteID:      siteID,
		StudioURL:   studioURL,
		Title:       item.title,
		URL:         s.base + item.path,
		Description: item.description,
		Thumbnail:   absURL(s.base, item.thumbnail),
		Preview:     absURL(s.base, item.preview),
		Studio:      studioName,
		Views:       item.views,
		ScrapedAt:   now,
	}
	if d, err := parseutil.TryParseDate(item.date, "2006-01-02"); err == nil {
		sc.Date = d
	}
	return sc
}

func absURL(base, u string) string {
	if u == "" || strings.HasPrefix(u, "http") {
		return u
	}
	return base + "/" + strings.TrimPrefix(u, "/")
}
