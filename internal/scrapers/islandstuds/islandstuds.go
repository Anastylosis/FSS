// Package islandstuds scrapes Island Studs (islandstuds.com), an ElevatedX
// "Classic" tour served from /tour, via julesjordanutil.
package islandstuds

import (
	"github.com/Anastylosis/FSS/internal/scrapers/julesjordanutil"
	"github.com/Anastylosis/FSS/scraper"
)

func New() *julesjordanutil.Scraper {
	return julesjordanutil.New(julesjordanutil.SiteConfig{
		SiteID:     "islandstuds",
		Domain:     "islandstuds.com",
		StudioName: "Island Studs",
		Template:   julesjordanutil.TemplateClassic,
		BasePath:   "/tour",
	})
}

func init() { scraper.Register(New()) }
