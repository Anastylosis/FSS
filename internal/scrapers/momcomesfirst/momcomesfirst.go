package momcomesfirst

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/internal/scrapers/wputil"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
)

type Scraper struct {
	client   *http.Client
	siteBase string
	headers  map[string]string
}

func New() *Scraper {
	return &Scraper{
		client:   httpx.NewClient(30 * time.Second),
		siteBase: "https://momcomesfirst.com",
		headers:  httpx.BrowserHeaders(httpx.UserAgentFirefox),
	}
}

var _ scraper.StudioScraper = (*Scraper)(nil)

func init() {
	scraper.Register(New())
}

func (s *Scraper) ID() string { return "momcomesfirst" }

func (s *Scraper) Patterns() []string {
	return []string{"momcomesfirst.com"}
}

var matchRe = regexp.MustCompile(`^https?://(?:www\.)?momcomesfirst\.com(?:/|$)`)

func (s *Scraper) MatchesURL(u string) bool {
	return matchRe.MatchString(u)
}

func (s *Scraper) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	if opts.Workers <= 0 {
		opts.Workers = 3
	}
	out := make(chan scraper.SceneResult)
	go func() {
		defer close(out)
		wputil.RunWorkerPool(ctx, s.client, s.headers,
			[]string{s.siteBase + "/sitemap.xml"},
			studioURL, opts, parsePage, out)
	}()
	return out, nil
}

// siteSuffixRe strips the site name from the og:title, whose format is
// "Title - tag1, tag2, performer - Mom Comes First".
var siteSuffixRe = regexp.MustCompile(`\s*-\s*Mom Comes First\s*$`)

// stripTitleSuffix removes the site name and the tag list the og:title carries.
// The segments are separated by a spaced hyphen, which a hyphenated tag
// ("step-mother") does not contain — matching on a bare hyphen truncated the
// title at the tag's own hyphen instead. The tag segment is only dropped when
// it actually opens with one of the page's declared tags, so a scene with no
// tags keeps its whole title.
func stripTitleSuffix(title string, tags []string) string {
	title = siteSuffixRe.ReplaceAllString(title, "")
	i := strings.LastIndex(title, " - ")
	if i < 0 || len(tags) == 0 {
		return strings.TrimSpace(title)
	}
	first := strings.TrimSpace(strings.SplitN(title[i+3:], ",", 2)[0])
	for _, t := range tags {
		if strings.EqualFold(t, first) {
			return strings.TrimSpace(title[:i])
		}
	}
	return strings.TrimSpace(title)
}

func parsePage(studioURL, pageURL string, body []byte, now time.Time) (models.Scene, bool, error) {
	meta := wputil.ParseMeta(body, "")

	if meta.Title != "" {
		meta.Title = stripTitleSuffix(meta.Title, meta.Tags)
	}

	// Skip non-video pages (homepage, about, etc.)
	if meta.PostID == "" && len(meta.Tags) == 0 {
		return models.Scene{}, true, nil
	}

	id := meta.PostID
	if id == "" {
		id = wputil.SlugFromURL(pageURL)
	}

	// Combine article:tag and articleSection categories into tags.
	tagSet := make(map[string]bool)
	var tags []string
	for _, t := range meta.Tags {
		if !tagSet[t] {
			tagSet[t] = true
			tags = append(tags, t)
		}
	}
	for _, c := range meta.Categories {
		if !tagSet[c] {
			tagSet[c] = true
			tags = append(tags, c)
		}
	}

	width := meta.Width
	height := meta.Height
	resolution := ""
	if width == 0 && height > 0 {
		width = wputil.VideoWidth(height)
	}
	if height >= 2160 {
		resolution = "4K"
	} else if height >= 1080 {
		resolution = "1080p"
	} else if height >= 720 {
		resolution = "720p"
	}

	scene := models.Scene{
		ID:          id,
		SiteID:      "momcomesfirst",
		StudioURL:   studioURL,
		Title:       meta.Title,
		URL:         pageURL,
		Date:        meta.Date,
		Description: meta.Description,
		Thumbnail:   meta.Thumbnail,
		Studio:      "Mom Comes First",
		Tags:        tags,
		Width:       width,
		Height:      height,
		Resolution:  resolution,
		ScrapedAt:   now,
	}

	return scene, false, nil
}
