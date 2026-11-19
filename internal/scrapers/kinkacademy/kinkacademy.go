// Package kinkacademy scrapes public catalog metadata from kinkacademy.com,
// an educational BDSM site on WordPress. The full videos are member-only, but
// the catalog metadata (titles, experts, topics, descriptions, thumbnails) is
// public via the WordPress REST API, which bypasses the site's age gate.
package kinkacademy

import (
	"context"
	"encoding/json"
	"fmt"
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

func init() { scraper.Register(New()) }

// videoCategoryID is the WordPress category ("Video", slug "video") that all
// lesson/video posts belong to. Filtering posts by this category excludes the
// site's blog posts, which share the same `post` type.
const videoCategoryID = 1282

const perPage = 50

type Scraper struct {
	client *http.Client
	base   string
}

func New() *Scraper {
	return &Scraper{
		client: httpx.NewClient(30 * time.Second),
		base:   "https://www.kinkacademy.com",
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func (s *Scraper) ID() string { return "kinkacademy" }

func (s *Scraper) Patterns() []string {
	return []string{
		"kinkacademy.com",
		"kinkacademy.com/topics/video",
	}
}

var matchRe = regexp.MustCompile(`(?i)^https?://(?:www\.)?kinkacademy\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool {
	return matchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

// wpPost mirrors the subset of the WordPress REST API post object that we use.
type wpPost struct {
	ID    int    `json:"id"`
	Date  string `json:"date"`
	Link  string `json:"link"`
	Type  string `json:"type"`
	Title struct {
		Rendered string `json:"rendered"`
	} `json:"title"`
	Excerpt struct {
		Rendered string `json:"rendered"`
	} `json:"excerpt"`
	Embedded struct {
		Author []struct {
			Name string `json:"name"`
			Slug string `json:"slug"`
		} `json:"author"`
		FeaturedMedia []struct {
			SourceURL string `json:"source_url"`
		} `json:"wp:featuredmedia"`
		// wp:term is an array of term groups (categories, tags, series, …),
		// each group normally an array of terms but occasionally an object.
		Terms []json.RawMessage `json:"wp:term"`
	} `json:"_embedded"`
}

func (s *Scraper) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)

	now := time.Now().UTC()
	scraper.Debugf(1, "kinkacademy: scraping video catalog via WP REST API")

	scraper.Paginate(ctx, opts, "kinkacademy", out, func(ctx context.Context, page int) (scraper.PageResult, error) {
		apiURL := fmt.Sprintf(
			"%s/wp-json/wp/v2/posts?per_page=%d&page=%d&categories=%d&_embed=1",
			s.base, perPage, page, videoCategoryID,
		)

		resp, err := httpx.Do(ctx, s.client, httpx.Request{
			URL:     apiURL,
			Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
		})
		if err != nil {
			return scraper.PageResult{}, err
		}
		defer func() { _ = resp.Body.Close() }()

		var posts []wpPost
		if err := httpx.DecodeJSON(resp.Body, &posts); err != nil {
			return scraper.PageResult{}, err
		}
		if len(posts) == 0 {
			return scraper.PageResult{}, nil
		}

		total := 0
		if page == 1 {
			if n, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("X-WP-Total"))); err == nil {
				total = n
			}
		}
		// Stop when WordPress reports we are on the last page (or it returned
		// a short page).
		done := len(posts) < perPage
		if tp, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("X-WP-TotalPages"))); err == nil && page >= tp {
			done = true
		}

		scenes := make([]models.Scene, 0, len(posts))
		for _, p := range posts {
			scenes = append(scenes, s.toScene(p, studioURL, now))
		}

		return scraper.PageResult{Scenes: scenes, Total: total, Done: done}, nil
	})
}

// tagRe strips HTML tags from rendered excerpt/content.
var tagRe = regexp.MustCompile(`<[^>]+>`)

func (s *Scraper) toScene(p wpPost, studioURL string, now time.Time) models.Scene {
	sc := models.Scene{
		ID:        strconv.Itoa(p.ID),
		SiteID:    "kinkacademy",
		StudioURL: studioURL,
		Studio:    "Kink Academy",
		Title:     cleanText(p.Title.Rendered),
		URL:       p.Link,
		ScrapedAt: now,
	}

	if t, err := time.Parse("2006-01-02T15:04:05", strings.TrimSpace(p.Date)); err == nil {
		sc.Date = t.UTC()
	}

	if desc := cleanText(stripTags(p.Excerpt.Rendered)); desc != "" {
		sc.Description = desc
	}

	if len(p.Embedded.FeaturedMedia) > 0 {
		sc.Thumbnail = p.Embedded.FeaturedMedia[0].SourceURL
	}

	// Instructor / expert: the embedded post author carries the clean display
	// name (e.g. "Gray Dancer"), unlike the email-based author taxonomy term.
	seenPerf := make(map[string]bool)
	for _, a := range p.Embedded.Author {
		name := cleanText(a.Name)
		if name != "" && !seenPerf[name] {
			seenPerf[name] = true
			sc.Performers = append(sc.Performers, name)
		}
	}

	// Topics: post_tag terms. Categories: the non-Video category terms.
	seenTag := make(map[string]bool)
	for _, raw := range p.Embedded.Terms {
		var group []struct {
			Taxonomy string `json:"taxonomy"`
			Name     string `json:"name"`
		}
		// WordPress emits some term groups as an object rather than an array.
		if json.Unmarshal(raw, &group) != nil {
			continue
		}
		for _, t := range group {
			name := cleanText(t.Name)
			if name == "" {
				continue
			}
			switch t.Taxonomy {
			case "post_tag":
				if !seenTag[name] {
					seenTag[name] = true
					sc.Tags = append(sc.Tags, name)
				}
			case "series":
				if sc.Series == "" {
					sc.Series = name
				}
			}
		}
	}

	return sc
}

func stripTags(s string) string {
	return tagRe.ReplaceAllString(s, "")
}

func cleanText(s string) string {
	return strings.TrimSpace(html.UnescapeString(s))
}
