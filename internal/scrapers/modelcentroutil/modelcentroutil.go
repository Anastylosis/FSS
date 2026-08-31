package modelcentroutil

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

const PerPage = 100

type SiteConfig struct {
	SiteID     string
	SiteBase   string
	StudioName string
	Performers []string // hardcoded performers (solo-performer sites)
}

type Scraper struct {
	cfg    SiteConfig
	Client *http.Client
}

func jsonHeaders() map[string]string {
	h := httpx.BrowserHeaders(httpx.UserAgentChrome)
	h["Accept"] = "application/json"
	return h
}

func New(cfg SiteConfig) *Scraper {
	return &Scraper{
		cfg:    cfg,
		Client: httpx.NewClient(30 * time.Second),
	}
}
func (s *Scraper) ID() string         { return s.cfg.SiteID }
func (s *Scraper) Patterns() []string { return []string{domainFromBase(s.cfg.SiteBase) + "/videos"} }

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	for offset := 0; ; offset += PerPage {
		if ctx.Err() != nil {
			return
		}

		if offset > 0 && opts.Delay > 0 {
			select {
			case <-time.After(opts.Delay):
			case <-ctx.Done():
				return
			}
		}
		scraper.Debugf(1, "%s: fetching page %d", s.cfg.SiteID, offset)

		listing, total, err := s.FetchListing(ctx, offset)
		if err != nil {
			send(ctx, out, scraper.Error(err))
			return
		}

		if len(listing) == 0 {
			return
		}

		if offset == 0 && total > 0 {
			if !send(ctx, out, scraper.Progress(total)) {
				return
			}
		}

		now := time.Now().UTC()
		for _, item := range listing {
			id := strconv.Itoa(item.ID)

			if opts.KnownIDs[id] {
				send(ctx, out, scraper.StoppedEarly())
				return
			}

			if opts.Delay > 0 {
				select {
				case <-time.After(opts.Delay):
				case <-ctx.Done():
					return
				}
			}

			// The listing already carries the id, title, duration and date;
			// only the description and tags come from the detail call. A
			// detail that will not load costs those two fields, so the error
			// is reported and the walk continues — aborting here dropped the
			// rest of the catalogue on every site this template serves, and
			// --full's authoritative Save would then delete it.
			detail, err := s.FetchDetail(ctx, item.ID)
			if err != nil {
				if !send(ctx, out, scraper.Error(err)) {
					return
				}
				detail = nil
			}

			scene := ToScene(s.cfg, item, detail, studioURL, now)
			if !send(ctx, out, scraper.Scene(scene)) {
				return
			}
		}

		if offset+len(listing) >= total {
			return
		}
	}
}

func send(ctx context.Context, ch chan<- scraper.SceneResult, r scraper.SceneResult) bool {
	select {
	case ch <- r:
		return true
	case <-ctx.Done():
		return false
	}
}

// API types.

type APIResponse struct {
	Status   bool       `json:"status"`
	Response APIPayload `json:"response"`
}

type APIPayload struct {
	Collection []APIScene `json:"collection"`
	Meta       APIMeta    `json:"meta"`
}

type APIMeta struct {
	TotalCount int `json:"totalCount"`
}

type APIScene struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
	Len   int    `json:"length"`
	Sites struct {
		Collection map[string]APISiteEntry `json:"collection"`
	} `json:"sites"`
	Description string          `json:"description"`
	Tags        json.RawMessage `json:"tags"`
}

type APISiteEntry struct {
	PublishDate string `json:"publishDate"`
}

type apiTagEntry struct {
	Alias string `json:"alias"`
}

func (s *Scraper) FetchListing(ctx context.Context, offset int) ([]APIScene, int, error) {
	u := fmt.Sprintf("%s/api/content.load?_method=content.load&tz=2"+
		"&fields[0]=id&fields[1]=title&fields[2]=length&fields[3]=sites.publishDate"+
		"&limit=%d&offset=%d"+
		"&metaFields[totalCount]=1"+
		"&transitParameters[preset]=videos",
		s.cfg.SiteBase, PerPage, offset)

	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     u,
		Headers: jsonHeaders(),
	})
	if err != nil {
		return nil, 0, fmt.Errorf("fetch listing offset %d: %w", offset, err)
	}
	defer func() { _ = resp.Body.Close() }()

	var ar APIResponse
	if err := httpx.DecodeJSON(resp.Body, &ar); err != nil {
		return nil, 0, fmt.Errorf("parse listing: %w", err)
	}

	return ar.Response.Collection, ar.Response.Meta.TotalCount, nil
}

func (s *Scraper) FetchDetail(ctx context.Context, id int) (*APIScene, error) {
	u := fmt.Sprintf("%s/api/content.load?_method=content.load&tz=2"+
		"&filter[id][fields][0]=id&filter[id][values][0]=%d"+
		"&fields[0]=id&fields[1]=title&fields[2]=description"+
		"&fields[3]=tags&fields[4]=tags.alias"+
		"&fields[5]=length&fields[6]=sites.publishDate"+
		"&limit=1"+
		"&transitParameters[preset]=scene",
		s.cfg.SiteBase, id)

	resp, err := httpx.Do(ctx, s.Client, httpx.Request{
		URL:     u,
		Headers: jsonHeaders(),
	})
	if err != nil {
		return nil, fmt.Errorf("fetch detail %d: %w", id, err)
	}
	defer func() { _ = resp.Body.Close() }()

	var ar APIResponse
	if err := httpx.DecodeJSON(resp.Body, &ar); err != nil {
		return nil, fmt.Errorf("parse detail %d: %w", id, err)
	}

	if len(ar.Response.Collection) == 0 {
		return nil, fmt.Errorf("detail %d: empty response", id)
	}

	return &ar.Response.Collection[0], nil
}

func ParseTags(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}

	var wrapped struct {
		Collection map[string]apiTagEntry `json:"collection"`
	}
	if err := json.Unmarshal(raw, &wrapped); err == nil && len(wrapped.Collection) > 0 {
		tags := make([]string, 0, len(wrapped.Collection))
		for _, t := range wrapped.Collection {
			if t.Alias != "" {
				tags = append(tags, t.Alias)
			}
		}
		return tags
	}

	var flat map[string]apiTagEntry
	if err := json.Unmarshal(raw, &flat); err == nil && len(flat) > 0 {
		tags := make([]string, 0, len(flat))
		for _, t := range flat {
			if t.Alias != "" {
				tags = append(tags, t.Alias)
			}
		}
		return tags
	}

	return nil
}

func ParsePublishDate(sc APIScene) time.Time {
	idStr := strconv.Itoa(sc.ID)
	if entry, ok := sc.Sites.Collection[idStr]; ok && entry.PublishDate != "" {
		if t, err := time.Parse("2006-01-02 15:04:05", entry.PublishDate); err == nil {
			return t
		}
	}
	for _, entry := range sc.Sites.Collection {
		if entry.PublishDate != "" {
			if t, err := time.Parse("2006-01-02 15:04:05", entry.PublishDate); err == nil {
				return t
			}
		}
	}
	return time.Time{}
}

func Slugify(title string) string {
	var sb strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(title) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			sb.WriteRune(r)
			prevDash = false
		} else if !prevDash && sb.Len() > 0 {
			sb.WriteByte('-')
			prevDash = true
		}
	}
	return strings.TrimRight(sb.String(), "-")
}

func ToScene(cfg SiteConfig, listing APIScene, detail *APIScene, studioURL string, now time.Time) models.Scene {
	id := strconv.Itoa(listing.ID)
	sceneURL := fmt.Sprintf("%s/scene/%d/%s", cfg.SiteBase, listing.ID, Slugify(listing.Title))

	sc := models.Scene{
		ID:         id,
		SiteID:     cfg.SiteID,
		StudioURL:  studioURL,
		Title:      listing.Title,
		URL:        sceneURL,
		Duration:   listing.Len,
		Date:       ParsePublishDate(listing),
		Studio:     cfg.StudioName,
		Performers: cfg.Performers,
		ScrapedAt:  now,
	}

	if detail != nil {
		sc.Description = strings.TrimSpace(detail.Description)
		sc.Tags = ParseTags(detail.Tags)
	}

	return sc
}

func domainFromBase(base string) string {
	s := strings.TrimPrefix(base, "https://")
	s = strings.TrimPrefix(s, "http://")
	return strings.TrimRight(s, "/")
}
