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
}

func init() {
	for _, cfg := range sites {
		scraper.Register(New(cfg))
	}
}
