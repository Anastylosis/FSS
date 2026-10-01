package cmd

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/scraper"
)

var detectCmd = &cobra.Command{
	Use:   "detect <url>",
	Short: "Fetch a URL and detect which platform/CMS it uses",
	Long: `Fetches the given URL once and checks the response for known platform
signals (Aylo instance_token, Algolia API, psmcdn.net, ModelCentro, etc.).

Reports the detected platform and the corresponding util package, or
"unknown" if no signal matches. Useful when deciding whether a new site
needs a standalone scraper or belongs to an existing shared package.`,
	Args: cobra.ExactArgs(1),
	RunE: runDetect,
}

func init() {
	rootCmd.AddCommand(detectCmd)
}

type detection struct {
	platform string
	pkg      string
	detail   string
}

func runDetect(cmd *cobra.Command, args []string) error {
	rawURL := normalizeInputURL(args[0])
	w := cmd.OutOrStdout()

	// Check if already supported by a registered scraper.
	if s, err := scraper.ForURL(rawURL); err == nil {
		_, _ = fmt.Fprintf(w, "Already supported by scraper: %s\n", s.ID())
		_, _ = fmt.Fprintf(w, "Patterns: %s\n", strings.Join(s.Patterns(), ", "))
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client := httpx.NewClient(30 * time.Second)
	resp, err := httpx.Do(ctx, client, httpx.Request{
		URL:     rawURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return fmt.Errorf("fetching %s: %w", rawURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := httpx.ReadBody(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	page := string(body)
	cookies := resp.Cookies()

	detections := detectPlatform(page, cookies, resp.Header)

	if len(detections) == 0 {
		_, _ = fmt.Fprintln(w, "Platform: unknown")
		_, _ = fmt.Fprintln(w, "No known platform signals detected. Build a standalone scraper.")
		return nil
	}

	for _, d := range detections {
		_, _ = fmt.Fprintf(w, "Platform: %s\n", d.platform)
		_, _ = fmt.Fprintf(w, "Package:  %s\n", d.pkg)
		if d.detail != "" {
			_, _ = fmt.Fprintf(w, "Detail:   %s\n", d.detail)
		}
		_, _ = fmt.Fprintln(w)
	}
	return nil
}

// platformRule is one platform fingerprint. A rule fires when any of its
// anyLower/anyRaw signals is present, or when every allLower signal is.
// Combining both (Cherry Pimps) means "this domain, or this pair of assets".
//
// anyLower/allLower match against the lowercased page; anyRaw matches the page
// verbatim, for signals whose casing is part of the fingerprint.
type platformRule struct {
	platform string
	pkg      string
	detail   string

	anyLower []string
	anyRaw   []string
	allLower []string

	// custom replaces the signal matching entirely, for rules whose detail
	// string varies or that can emit more than one detection.
	custom func(page, lp string, cookies []*http.Cookie) []detection
}

func (r platformRule) matches(page, lp string) bool {
	for _, s := range r.anyLower {
		if strings.Contains(lp, s) {
			return true
		}
	}
	for _, s := range r.anyRaw {
		if strings.Contains(page, s) {
			return true
		}
	}
	if len(r.allLower) == 0 {
		return false
	}
	for _, s := range r.allLower {
		if !strings.Contains(lp, s) {
			return false
		}
	}
	return true
}

// platformRules is ordered: detections are reported in this sequence, and the
// generic WordPress check sits near the end so more specific platforms are
// reported first.
//
// Note it is not strictly last — Spizoo, Vixen and Nasty Media follow it, so a
// page matching one of those and WordPress reports WordPress first. That is
// long-standing behaviour, preserved here deliberately; moving the WordPress
// rule to the end would change `fss detect` output ordering for those sites.
var platformRules = []platformRule{
	{custom: detectAylo},
	{platform: "TeamSkeet/PSM", pkg: "teamskeetutil", detail: "psmcdn.net CDN detected",
		anyLower: []string{"psmcdn.net"}},
	{custom: detectGamma},
	{platform: "ModelCentro", pkg: "modelcentroutil",
		anyLower: []string{"centrofiles.com", "/api/content.load"},
		anyRaw:   []string{"fox.createApplication"}},
	{platform: "Adult Prime", pkg: "adultprimeutil",
		anyLower: []string{"cdnstatic.imctransfer.com", "portal-video-wrapper"}},
	{custom: detectFYC},
	{custom: detectNextJS},
	{platform: "Score Group", pkg: "scoregrouputil",
		anyLower: []string{"scoreland.com", "scoregroup.com", "score-group"}},
	{platform: "MetArt Network", pkg: "metartutil",
		anyLower: []string{"metartnetwork.com", "gccdn.metartnetwork.com"}},
	{platform: "Up-Timely CMS", pkg: "uptimelyutil",
		anyLower: []string{"cdn.up-timely.com", "p-workpage__title"}},
	{platform: "Czech AV / HQ Media Go", pkg: "czechavutil",
		anyLower: []string{"hqmediago.com", "cdn77.hqmediago.com"}},
	{platform: "Teen Mega World", pkg: "tmwutil",
		anyLower: []string{"teenmegaworld"}},
	{platform: "Full Porn Network", pkg: "fpnutil",
		anyLower: []string{"fullpornnetwork.com", "fpncash.com"}},
	{platform: "Grooby CMS", pkg: "groobyutil",
		allLower: []string{"grooby.com", "set-target-"}},
	{platform: "Jules Jordan Network", pkg: "julesjordanutil",
		anyLower: []string{"julesjordan.com", "jj-content-card"}},
	{platform: "SexMex Pro CMS", pkg: "sexmexutil",
		anyLower: []string{"sexmex.xxx", "sexmexpro"}},
	{platform: "POVR/WankzVR", pkg: "povrutil",
		anyLower: []string{"povr.com", "wankzvr.com"}},
	{platform: "Railway/Express", pkg: "railwayutil",
		anyLower: []string{"sites-api-production.up.railway.app"}},
	{platform: "New Sensations", pkg: "newsensationsutil",
		allLower: []string{"newsensations.com", "videothumb_"}},
	{platform: "Wow Network", pkg: "wownetworkutil",
		anyLower: []string{"wowmodels.com"}},
	{platform: "VNA Girls", pkg: "vnautil",
		anyLower: []string{"vnagirls.com", "stickydollars.htm"}},
	{platform: "MissaX CMS", pkg: "missaxutil",
		allLower: []string{"missax", "photo-thumb video-thumb"}},
	{platform: "Cherry Pimps", pkg: "cherrypimpsutil",
		anyLower: []string{"cherrypimps.com"},
		allLower: []string{"elx_styles.css", "tourhelper.js"}},
	{platform: "Trix Video (classic Elevated X tour)", pkg: "trixvideo",
		detail:   "tourhelper.js click-limited tour with update_details/data-setid cards",
		allLower: []string{"js/tourhelper.js", "maxclicks", "update_details"}},
	{platform: "Real Spankings", pkg: "realspankingsutil",
		anyLower: []string{"realspankingsnetwork.com", "alpine entertainment group"}},
	{platform: "FTV", pkg: "ftvutil",
		anyLower: []string{"ftvcash.com", "cdn.ftvgirls.com", "cdn.ftvmilfs.com"}},
	{platform: "Wankz", pkg: "wankzutil",
		anyLower: []string{"images.wankz.com", "images.lethalpass.com"}},
	{platform: "UTG Networks / Glamose", pkg: "utgutil",
		anyLower: []string{"assets.utgnetworks.com", "utg networks ltd"}},
	{platform: "Pornstar Platinum", pkg: "pornstarplatinum",
		anyLower: []string{"pornstarplatinum.com"}},
	{platform: "My Gay Cash NATS CMS", pkg: "marsmedia",
		anyLower: []string{"nats.mygaycash.com", "natscms-app"}},
	{custom: detectWordPress},
	{platform: "Spizoo", pkg: "spizooutil",
		anyLower: []string{"spizoo.com"}},
	{platform: "Vixen Media Group", pkg: "vixenutil",
		anyLower: []string{"vixen.com", "blacked.com", "tushy.com"}},
	{platform: "Nasty Media Group (WWB18)", pkg: "nastymedia",
		anyLower: []string{"nasty media group"},
		anyRaw:   []string{"WYSIWYG Web Builder 18"}},
}

// TODO: response headers are fetched and passed in but not yet used as a
// detection signal. Server / X-Powered-By / Set-Cookie names would identify
// several CMSes that currently require body-content matching. Renamed to _
// so the unused-parameter lint stays green until that lands.
func detectPlatform(page string, cookies []*http.Cookie, _ http.Header) []detection {
	lp := strings.ToLower(page)

	var results []detection
	for _, r := range platformRules {
		if r.custom != nil {
			results = append(results, r.custom(page, lp, cookies)...)
			continue
		}
		if r.matches(page, lp) {
			results = append(results, detection{r.platform, r.pkg, r.detail})
		}
	}
	return results
}

// detectAylo keys off the instance_token cookie rather than page content.
func detectAylo(_, _ string, cookies []*http.Cookie) []detection {
	for _, c := range cookies {
		if c.Name == "instance_token" {
			return []detection{{"Aylo/Juan", "ayloutil", "instance_token cookie found"}}
		}
	}
	return nil
}

// detectGamma reports the specific Algolia application ID when it is the
// signal that matched, since that is the stronger fingerprint.
func detectGamma(page, lp string, _ []*http.Cookie) []detection {
	hasAppID := strings.Contains(page, "TSMKFA364Q")
	hasAlgolia := strings.Contains(lp, "algolia.net") && strings.Contains(lp, "applicationid")
	if !hasAppID && !hasAlgolia {
		return nil
	}
	detail := "Algolia API detected"
	if hasAppID {
		detail = "Algolia applicationID TSMKFA364Q"
	}
	return []detection{{"Gamma Entertainment", "gammautil", detail}}
}

// detectFYC matches Nuxt, noting whether FYC/PornPros CDN signals accompany it.
func detectFYC(page, lp string, _ []*http.Cookie) []detection {
	if !strings.Contains(page, `id="__NUXT_DATA__"`) {
		return nil
	}
	detail := "__NUXT_DATA__ tag found"
	if strings.Contains(lp, "pornpros") || strings.Contains(lp, "fuckyoucash") {
		detail += " + FYC/PornPros signals"
	}
	return []detection{{"FYC/PornPros (Nuxt)", "fycutil", detail}}
}

// detectNextJS can report both Wank It Now and the generic Next.js paysite,
// since a page may carry the mjedge and yppcdn signals at once.
func detectNextJS(page, lp string, _ []*http.Cookie) []detection {
	if !strings.Contains(page, `id="__NEXT_DATA__"`) {
		return nil
	}
	detail := "__NEXT_DATA__ tag found"

	var results []detection
	if strings.Contains(lp, "mjedge.net") {
		detail += " + mjedge.net CDN"
		if strings.Contains(lp, "wankitnow") {
			results = append(results, detection{"Wank It Now", "wankitnowutil", detail})
		}
	}
	if strings.Contains(lp, "yppcdn.com") || strings.Contains(lp, "nats_site_id") {
		results = append(results, detection{"Next.js Paysite (Ghost Pro / KB Productions)", "ghostpro / kbproductions / hobybuchanon", detail})
	}
	return results
}

// detectWordPress is deliberately generic and runs late; the video-elements
// theme is reported instead of plain WordPress when present.
func detectWordPress(_, lp string, _ []*http.Cookie) []detection {
	if !strings.Contains(lp, "/wp-json/") && !strings.Contains(lp, "/wp-content/") && !strings.Contains(lp, "wp-includes") {
		return nil
	}
	const detail = "WordPress detected"
	if strings.Contains(lp, "video-elements") {
		return []detection{{"WP video-elements", "veutil", detail + " + video-elements theme"}}
	}
	return []detection{{"WordPress", "wputil (standalone)", detail}}
}
