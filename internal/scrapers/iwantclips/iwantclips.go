package iwantclips

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
	"github.com/Anastylosis/FSS/scraper"
)

const (
	defaultSiteBase   = "https://iwantclips.com"
	defaultTypesense  = "https://bajc2td3pou5fs7mp.a1.typesense.net"
	defaultCollection = "prod_content"
	defaultPerPage    = 250
)

// Scraper implements scraper.StudioScraper for IWantClips.
type Scraper struct {
	client     *http.Client
	siteBase   string
	tsBase     string
	collection string
	perPage    int
}

func New() *Scraper {
	return &Scraper{
		client:     httpx.NewClient(30 * time.Second),
		siteBase:   defaultSiteBase,
		tsBase:     defaultTypesense,
		collection: defaultCollection,
		perPage:    defaultPerPage,
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() {
	scraper.Register(New())
}

// ---- StudioScraper interface ----

func (s *Scraper) ID() string { return "iwantclips" }

func (s *Scraper) Patterns() []string {
	return []string{
		"iwantclips.com/store/{memberId}/{username}",
	}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?iwantclips\.com/store/\d+/`)
var storeRe = regexp.MustCompile(`/store/(\d+)/([^/?]+)`)

func (s *Scraper) MatchesURL(u string) bool {
	return matchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	memberID, _, err := storeParams(studioURL)
	if err != nil {
		return nil, err
	}
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, memberID, opts, out)
	return out, nil
}

// ---- runner ----

func (s *Scraper) run(ctx context.Context, studioURL, memberID string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	// Fetch a fresh Typesense API key from the store page.
	apiKey, tsBase, err := s.fetchAPIKey(ctx, studioURL)
	if err != nil {
		select {
		case out <- scraper.Error(fmt.Errorf("fetching api key: %w", err)):
		case <-ctx.Done():
		}
		return
	}

	now := time.Now().UTC()
	var lastTotal int
	scraper.Paginate(ctx, opts, "iwantclips", out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		docs, total, err := s.fetchPage(ctx, apiKey, tsBase, memberID, page)
		if err != nil {
			// Typesense keys are ephemeral — refresh once on 401 and retry.
			var se *httpx.StatusError
			if errors.As(err, &se) && se.StatusCode == http.StatusUnauthorized {
				apiKey, tsBase, err = s.fetchAPIKey(ctx, studioURL)
				if err == nil {
					docs, total, err = s.fetchPage(ctx, apiKey, tsBase, memberID, page)
				} else {
					err = fmt.Errorf("refreshing api key: %w", err)
				}
			}
			if err != nil {
				return scraper.PageResult{}, err
			}
		}

		if total > 0 {
			lastTotal = total
		}

		scenes := make([]models.Scene, len(docs))
		for i, doc := range docs {
			scenes[i] = toScene(studioURL, doc, now)
		}

		totalPages := (lastTotal + s.perPage - 1) / s.perPage
		done := page >= totalPages

		return scraper.PageResult{Scenes: scenes, Total: total, Done: done}, nil
	})
}

// ---- page fetch (store HTML → Typesense key) ----

var apiKeyRe = regexp.MustCompile(`apiKey:\s*'([^']+)'`)
var tsHostRe = regexp.MustCompile(`host:\s*'([^']+)'`)
var tsProtoRe = regexp.MustCompile(`protocol:\s*'([^']+)'`)

func (s *Scraper) fetchAPIKey(ctx context.Context, studioURL string) (apiKey, tsBase string, err error) {
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     studioURL,
		Headers: iwcDefaultHeaders(),
	})
	if err != nil {
		return "", "", err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("reading store page: %w", err)
	}

	mKey := apiKeyRe.FindSubmatch(body)
	if mKey == nil {
		return "", "", fmt.Errorf("could not find Typesense API key in store page")
	}
	mHost := tsHostRe.FindSubmatch(body)
	if mHost == nil {
		return "", "", fmt.Errorf("could not find Typesense host in store page")
	}

	proto := "https"
	if mProto := tsProtoRe.FindSubmatch(body); mProto != nil {
		proto = string(mProto[1])
	}

	return string(mKey[1]), proto + "://" + string(mHost[1]), nil
}

// ---- Typesense query ----

type tsResponse struct {
	Found int     `json:"found"`
	Hits  []tsHit `json:"hits"`
}

type tsHit struct {
	Document iwcDoc `json:"document"`
}

type iwcDoc struct {
	ContentID     string   `json:"content_id"`
	Title         string   `json:"title"`
	Description   string   `json:"description"`
	ContentURL    string   `json:"content_url"`
	ThumbnailURL  string   `json:"thumbnail_url"`
	PreviewURL    string   `json:"preview_url"`
	Price         float64  `json:"price"`
	PublishTime   int64    `json:"publish_time"`
	VideoLength   string   `json:"video_length"`
	Categories    []string `json:"categories"`
	Keywords      []string `json:"keywords"`
	ModelUsername string   `json:"model_username"`
}

func (s *Scraper) fetchPage(ctx context.Context, apiKey, tsBase, memberID string, page int) ([]iwcDoc, int, error) {
	u := fmt.Sprintf("%s/collections/%s/documents/search", tsBase, s.collection)
	params := url.Values{
		"q":         {"*"},
		"filter_by": {"member_id:" + memberID},
		"sort_by":   {"publish_time:desc"},
		"per_page":  {strconv.Itoa(s.perPage)},
		"page":      {strconv.Itoa(page)},
	}
	u += "?" + params.Encode()

	headers := iwcDefaultHeaders()
	headers["X-TYPESENSE-API-KEY"] = apiKey
	resp, err := httpx.Do(ctx, s.client, httpx.Request{
		URL:     u,
		Headers: headers,
	})
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	var ts tsResponse
	if err := httpx.DecodeJSON(resp.Body, &ts); err != nil {
		return nil, 0, fmt.Errorf("decoding response: %w", err)
	}

	docs := make([]iwcDoc, len(ts.Hits))
	for i, h := range ts.Hits {
		docs[i] = h.Document
	}
	return docs, ts.Found, nil
}

func iwcDefaultHeaders() map[string]string {
	return map[string]string{
		"User-Agent": httpx.UserAgentFirefox,
	}
}

// ---- mapping ----

// publishedAt guards the epoch: a document with no publish_time decoded to 0,
// which stored every such scene as 1970-01-01 rather than leaving it undated.
func publishedAt(unix int64) time.Time {
	if unix <= 0 {
		return time.Time{}
	}
	return time.Unix(unix, 0).UTC()
}

func toScene(studioURL string, doc iwcDoc, now time.Time) models.Scene {
	scene := models.Scene{
		ID:        doc.ContentID,
		SiteID:    "iwantclips",
		StudioURL: studioURL,
		Title:     html.UnescapeString(doc.Title),
		URL:       doc.ContentURL,
		Date:      publishedAt(doc.PublishTime),
		// IWantClips ships descriptions double-encoded (e.g. `&amp;quot;` for `"`),
		// so two passes are intentional.
		Description: html.UnescapeString(html.UnescapeString(doc.Description)),
		Thumbnail:   doc.ThumbnailURL,
		Preview:     doc.PreviewURL,
		Studio:      doc.ModelUsername,
		Categories:  doc.Categories,
		Tags:        doc.Keywords,
		Duration:    parseVideoLength(doc.VideoLength),
		ScrapedAt:   now,
	}

	scene.AddPrice(models.PriceSnapshot{
		Date:    now,
		Regular: doc.Price,
		IsFree:  doc.Price == 0,
	})

	return scene
}

// ---- helpers ----

func storeParams(studioURL string) (memberID, username string, err error) {
	m := storeRe.FindStringSubmatch(studioURL)
	if m == nil {
		return "", "", fmt.Errorf("cannot extract store ID from %q", studioURL)
	}
	return m[1], m[2], nil
}

// parseVideoLength converts "HH:MM:SS" or "MM:SS" to seconds.
func parseVideoLength(s string) int {
	parts := strings.Split(s, ":")
	total := 0
	for _, p := range parts {
		n, _ := strconv.Atoi(p)
		total = total*60 + n
	}
	return total
}
