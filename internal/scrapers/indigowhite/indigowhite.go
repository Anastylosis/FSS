// Package indigowhite scrapes indigowhitetv.com.
package indigowhite

import (
	"context"
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
	"github.com/Anastylosis/FSS/scraper"
)

const (
	siteID     = "indigowhite"
	studioName = "Indigo White"
	defaultURL = "https://indigowhitetv.com"
	// maxCollectionPages bounds the cursor walk. Each page carries the offset
	// of the next, and a server that ever echoed the same cursor would
	// otherwise loop; the repeat-cursor check below is the real guard and this
	// is the backstop.
	maxCollectionPages = 200
)

// collections are the two Squarespace collections holding scenes. `videos` is
// short and unpaginated; `freevideoshub` is the bulk of the catalogue.
var collections = []string{"/freevideoshub", "/videos"}

type Scraper struct {
	Client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{Client: httpx.NewClient(45 * time.Second), base: defaultURL}
}

func init() { scraper.Register(New()) }

func (s *Scraper) ID() string { return siteID }

func (s *Scraper) Patterns() []string {
	return []string{
		"indigowhitetv.com",
		"indigowhitetv.com/freevideoshub",
		"indigowhitetv.com/videos",
	}
}

var urlRe = regexp.MustCompile(`^https?://(?:www\.)?indigowhitetv\.com(?:/.*)?$`)

func (s *Scraper) MatchesURL(u string) bool { return urlRe.MatchString(u) }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// ---- API ----

type apiItem struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	FullURL    string   `json:"fullUrl"`
	AssetURL   string   `json:"assetUrl"`
	Body       string   `json:"body"`
	Excerpt    string   `json:"excerpt"`
	Tags       []string `json:"tags"`
	Categories []string `json:"categories"`
	// PublishOn is milliseconds since the epoch, as a JSON number.
	PublishOn int64 `json:"publishOn"`
}

type apiPage struct {
	Items      []apiItem `json:"items"`
	Pagination struct {
		NextPage       bool  `json:"nextPage"`
		NextPageOffset int64 `json:"nextPageOffset"`
	} `json:"pagination"`
}

func (s *Scraper) fetchPage(ctx context.Context, collection string, offset int64) (*apiPage, error) {
	q := url.Values{}
	q.Set("format", "json")
	if offset > 0 {
		q.Set("offset", strconv.FormatInt(offset, 10))
	}
	u := s.base + collection + "?" + q.Encode()

	var page apiPage
	err := httpx.DoJSON(ctx, s.Client, httpx.Request{
		URL: u,
		Headers: map[string]string{
			"User-Agent": httpx.UserAgentFirefox,
			"Accept":     "application/json",
		},
	}, &page)
	if err != nil {
		return nil, err
	}
	return &page, nil
}

// ---- run ----

// collectionFor maps a studio URL onto the collections to walk. A URL naming
// one collection walks only that one; anything else walks both.
func collectionFor(studioURL string) []string {
	u, err := url.Parse(studioURL)
	if err != nil {
		return collections
	}
	path := "/" + strings.Trim(u.Path, "/")
	for _, c := range collections {
		if strings.EqualFold(path, c) {
			return []string{c}
		}
	}
	return collections
}

// walk tracks where a run is across the collections it covers: which one, and
// the cursor the last page handed back for it.
type walk struct {
	collections []string
	index       int
	offset      int64
	pages       int
	seenOffset  map[int64]bool
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	w := &walk{collections: collectionFor(studioURL), seenOffset: map[int64]bool{}}
	scraper.Debugf(1, "%s: walking %v", siteID, w.collections)

	seen := map[string]bool{}

	scraper.Paginate(ctx, opts, siteID, out, func(ctx context.Context, _ int) (scraper.PageResult, error) {
		if w.index >= len(w.collections) {
			return scraper.PageResult{Done: true}, nil
		}
		coll := w.collections[w.index]

		page, err := s.fetchPage(ctx, coll, w.offset)
		if err != nil {
			return scraper.PageResult{}, fmt.Errorf("%s: %s: %w", siteID, coll, err)
		}
		if len(page.Items) == 0 && w.pages == 0 && w.index == 0 {
			// Both collections empty on the first request is a redesign, not
			// an empty catalogue, and the difference decides whether an
			// authoritative Save may delete.
			return scraper.PageResult{}, scraper.ParseError(s.base+coll,
				fmt.Errorf("collection returned no items"))
		}

		var scenes []models.Scene
		for _, it := range page.Items {
			if it.ID == "" || seen[it.ID] {
				continue
			}
			seen[it.ID] = true
			scenes = append(scenes, s.toScene(it, studioURL))
		}

		w.advance(page)
		return scraper.PageResult{
			Scenes: scenes,
			Done:   w.index >= len(w.collections),
			// A page whose every item was already seen is not the end.
			Continue: true,
		}, nil
	})
}

func (w *walk) advance(page *apiPage) {
	w.pages++
	next := page.Pagination.NextPageOffset
	// A cursor the walk has already followed means the server is echoing a
	// page; treating that as "more" would loop forever.
	if !page.Pagination.NextPage || next == 0 || w.seenOffset[next] || w.pages >= maxCollectionPages {
		w.index++
		w.offset = 0
		w.pages = 0
		w.seenOffset = map[int64]bool{}
		return
	}
	w.seenOffset[next] = true
	w.offset = next
}

func (s *Scraper) toScene(it apiItem, studioURL string) models.Scene {
	sc := models.Scene{
		ID:         it.ID,
		SiteID:     siteID,
		StudioURL:  studioURL,
		Studio:     studioName,
		Title:      cleanText(it.Title),
		URL:        s.base + it.FullURL,
		Thumbnail:  it.AssetURL,
		Tags:       clean(it.Tags),
		Categories: clean(it.Categories),
		ScrapedAt:  time.Now().UTC(),
	}
	if it.PublishOn > 0 {
		sc.Date = time.UnixMilli(it.PublishOn).UTC()
	}
	// The body opens with the collection's whole category nav rendered inline,
	// so the excerpt — which is just the post's own copy — is preferred.
	if d := cleanText(it.Excerpt); d != "" {
		sc.Description = d
	} else {
		sc.Description = cleanText(it.Body)
	}
	return sc
}

func clean(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, v := range in {
		v = cleanText(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

var (
	tagStripRe = regexp.MustCompile(`(?s)<[^>]*>`)
	wsRe       = regexp.MustCompile(`\s+`)
)

func cleanText(s string) string {
	s = tagStripRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(html.UnescapeString(s))
	return strings.TrimSpace(wsRe.ReplaceAllString(s, " "))
}
