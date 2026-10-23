package philflash

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/parseutil"
	"github.com/Anastylosis/FSS/scraper"
)

const (
	kariSiteID   = "karisweets"
	kariBase     = "https://karisweets.com"
	kariStudio   = "Kari Sweets"
	kariListPath = "/latest-updates/"
)

var kariMatchRe = regexp.MustCompile(`^https?://(?:www\.)?karisweets\.com(?:[/?#]|$)`)

// KariSweets scrapes karisweets.com, phil-flash's older "enginev12" site. Its
// /latest-updates/ grid links only the handful of public preview sets; the
// rest of the 97 updates point at /join.
type KariSweets struct {
	client *http.Client
	base   string
}

func NewKariSweets() *KariSweets {
	return &KariSweets{client: httpx.NewClient(30 * time.Second), base: kariBase}
}

var _ scraper.StudioScraper = (*KariSweets)(nil)

func (s *KariSweets) ID() string { return kariSiteID }

func (s *KariSweets) Patterns() []string {
	return []string{"karisweets.com", "karisweets.com/latest-updates/", "karisweets.com/video/"}
}

func (s *KariSweets) MatchesURL(u string) bool { return kariMatchRe.MatchString(u) }

func (s *KariSweets) ListScenes(ctx context.Context, studioURL string, opts scraper.ListOpts) (<-chan scraper.SceneResult, error) {
	out := make(chan scraper.SceneResult)
	go s.run(ctx, studioURL, opts, out)
	return out, nil
}

var kariSetLinkRe = regexp.MustCompile(`href="/latest-updates/(\d+)/([^"/]+)\.php"`)

type kariRef struct{ id, path string }

func parseKariListing(body []byte) []kariRef {
	var refs []kariRef
	seen := map[string]bool{}
	for _, m := range kariSetLinkRe.FindAllSubmatch(body, -1) {
		id := string(m[1])
		if seen[id] {
			continue
		}
		seen[id] = true
		refs = append(refs, kariRef{id: id, path: "/latest-updates/" + id + "/" + string(m[2]) + ".php"})
	}
	return refs
}

func (s *KariSweets) run(ctx context.Context, studioURL string, opts scraper.ListOpts, out chan<- scraper.SceneResult) {
	defer close(out)
	send := func(r scraper.SceneResult) bool {
		select {
		case out <- r:
			return true
		case <-ctx.Done():
			return false
		}
	}

	listURL := s.base + kariListPath
	body, err := fetchPage(ctx, s.client, listURL, opts.Cookie)
	if err != nil {
		send(scraper.Error(fmt.Errorf("listing: %w", err)))
		return
	}
	refs := parseKariListing(body)
	if len(refs) == 0 {
		send(scraper.Error(scraper.ParseError(listURL, fmt.Errorf("no public sets on listing"))))
		return
	}
	scraper.Debugf(1, "karisweets: %d public sets on listing", len(refs))
	if !send(scraper.Progress(len(refs))) {
		return
	}

	skipped := 0
	for _, r := range refs {
		if opts.KnownIDs[r.id] {
			skipped++
			continue
		}
		if !scraper.Pace(ctx, opts.Delay) {
			return
		}
		pageURL := s.base + r.path
		page, err := fetchPage(ctx, s.client, pageURL, opts.Cookie)
		if err != nil {
			if !send(scraper.Error(fmt.Errorf("set %s: %w", r.id, err))) {
				return
			}
			continue
		}
		scene, ok, err := parseKariSet(page)
		if err != nil {
			if !send(scraper.Error(scraper.ParseError(pageURL, err))) {
				return
			}
			continue
		}
		if !ok {
			scraper.Debugf(1, "karisweets: set %s is a photo set, skipped", r.id)
			continue
		}
		scene.ID = r.id
		scene.SiteID = kariSiteID
		scene.StudioURL = studioURL
		scene.URL = kariBase + r.path
		scene.Studio = kariStudio
		scene.ScrapedAt = time.Now().UTC()
		if !send(scraper.Scene(scene)) {
			return
		}
	}
	if skipped > 0 {
		send(scraper.StoppedEarly())
	}
}

var (
	kariRowRe   = regexp.MustCompile(`<tr><td>([^<]+)</td><td>([^<]*)</td></tr>`)
	kariH1Re    = regexp.MustCompile(`(?s)<h1 class="card-title[^"]*">\s*(.*?)<br><small class="text-muted">([^<]+)</small>`)
	kariImageRe = regexp.MustCompile(`image:\s*"([^"]+)"`)
	kariFileRe  = regexp.MustCompile(`file:\s*"([^"]+\.mp4)"`)
)

// parseKariSet reads a set page. ok is false for a photo set, which has no
// Videos row in its Set Data table.
func parseKariSet(body []byte) (models.Scene, bool, error) {
	page := string(body)
	var sc models.Scene
	// Only the Set Data card: the page also carries the network-wide Sweet
	// Pass totals in the same row markup, Videos included.
	setData := page
	if i := strings.Index(page, "Set Data"); i >= 0 {
		setData = page[i:]
		if j := strings.Index(setData, "</table>"); j >= 0 {
			setData = setData[:j]
		}
	}
	rows := map[string]string{}
	for _, m := range kariRowRe.FindAllStringSubmatch(setData, -1) {
		rows[strings.ToLower(strings.TrimSpace(m[1]))] = strings.TrimSpace(html.UnescapeString(m[2]))
	}
	sc.Title = rows["title"]
	if m := kariH1Re.FindStringSubmatch(page); m != nil {
		if sc.Title == "" {
			// The heading may carry a subtitle after a <br>.
			t, _, _ := strings.Cut(m[1], "<br>")
			sc.Title = strings.TrimSpace(html.UnescapeString(t))
		}
		sc.Performers = splitCast(m[2])
	}
	if sc.Title == "" {
		return sc, false, fmt.Errorf("no set title")
	}
	var videos int
	_, _ = fmt.Sscanf(rows["videos"], "%d", &videos)
	if videos == 0 {
		return sc, false, nil
	}
	if len(sc.Performers) == 0 {
		sc.Performers = []string{kariStudio}
	}
	sc.Duration = parseutil.ParseDurationColon(rows["runtime"])
	if t, err := parseutil.TryParseDate(rows["release date"], "2006-01-02"); err == nil {
		sc.Date = t.UTC()
	}
	if m := metaDescRe.FindStringSubmatch(page); m != nil {
		sc.Description = strings.TrimSpace(html.UnescapeString(m[1]))
	}
	if m := kariImageRe.FindStringSubmatch(page); m != nil {
		sc.Thumbnail = m[1]
	}
	if all := kariFileRe.FindAllStringSubmatch(page, -1); len(all) > 0 {
		sc.Preview = all[len(all)-1][1]
	}
	return sc, true, nil
}
