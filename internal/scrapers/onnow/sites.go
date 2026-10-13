package onnow

import (
	"regexp"

	"github.com/Anastylosis/FSS/scraper"
)

var sites = []SiteConfig{
	{
		ID:       "irmaosdotados",
		BaseURL:  "https://irmaosdotados.com.br",
		SiteName: "Irmãos Dotados",
		Patterns: []string{
			"irmaosdotados.com.br",
			"irmaosdotados.com.br/videos",
			"irmaosdotados.com.br/videos/{category}",
		},
		MatchRe:          regexp.MustCompile(`^https?://(?:www\.)?irmaosdotados\.com\.br(?:/|$)`),
		CardRe:           regexp.MustCompile(`onclick="location\.href='/video/(\d+)'"`),
		TitleRe:          regexp.MustCompile(`<h4[^>]*><a href="/video/\d+"[^>]*>([^<]*)</a>`),
		DurationRe:       regexp.MustCompile(`icon-clock"></i>\s*([^<]*?)\s*</div>`),
		DetailDurationRe: regexp.MustCompile(`Duração:\s*<span>.*?</i>\s*([^<]*?)\s*</span>`),
		SitemapIDRe:      regexp.MustCompile(`<loc>https://irmaosdotados\.com\.br/video/(\d+)</loc>`),
		NewestFirst:      true,
	},
	{
		ID:       "hardbrazil",
		BaseURL:  "https://hardbrazil.com.br",
		SiteName: "HardBrazil",
		Patterns: []string{
			"hardbrazil.com.br",
			"hardbrazil.com.br/videos",
			"hardbrazil.com.br/videos/{category}",
		},
		MatchRe:    regexp.MustCompile(`^https?://(?:www\.)?hardbrazil\.com\.br(?:/|$)`),
		CardRe:     regexp.MustCompile(`<a href="[^"]*/video/([0-9a-f]+)"[^>]*class="thumbs"`),
		TitleRe:    regexp.MustCompile(`<h2[^>]*>([^<]*)</h2>`),
		DurationRe: regexp.MustCompile(`class="duration"><i class="icon-time"></i>\s*([^<]*?)\s*</span>`),
	},
}

func init() {
	for _, cfg := range sites {
		scraper.Register(New(cfg))
	}
}
