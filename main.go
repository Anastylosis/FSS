// FSS (FullStudioScraper) scrapes scene metadata from studio URLs.
//
// # CLI
//
// Install the binary and run:
//
//	fss scrape <studio-url>
//	fss list-scrapers
//	fss stash import --dir ./data
//
// See https://github.com/Anastylosis/FSS for full CLI documentation.
//
// # Library
//
// FSS can be imported as a Go module. Nine public packages are available:
//
//   - [github.com/Anastylosis/FSS/scraper] — scraper registry and streaming interface
//   - [github.com/Anastylosis/FSS/models] — Scene, PriceSnapshot, StudioFile types
//   - [github.com/Anastylosis/FSS/scrapers/all] — blank-import to register all scrapers
//   - [github.com/Anastylosis/FSS/match] — filename→title matching and cross-site merging
//   - [github.com/Anastylosis/FSS/output] — WriteJSON, WriteCSV, Slugify
//   - [github.com/Anastylosis/FSS/parseutil] — video duration parsing (MM:SS, ISO 8601)
//   - [github.com/Anastylosis/FSS/nfo] — Kodi-style NFO XML generation
//   - [github.com/Anastylosis/FSS/identify] — video directory scan + match + NFO write
//
// Blank-import the scraper packages you need to register them, then look up
// by URL or ID:
//
//	import (
//	    "github.com/Anastylosis/FSS/scraper"
//	    _ "github.com/Anastylosis/FSS/scrapers/all"
//	)
//
//	s, err := scraper.ForURL("https://www.manyvids.com/Profile/590705/bettie-bondage/Store/Videos")
//	ch, err := s.ListScenes(ctx, url, scraper.ListOpts{})
//	for r := range ch {
//	    fmt.Println(r.Scene.Title)
//	}
//
// See [docs/library.md] in the repository for the full guide.
package main

import (
	"github.com/Anastylosis/FSS/cmd"
	_ "github.com/Anastylosis/FSS/internal/scrapers/all"
)

// Set by -ldflags at release build time.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
	// channel is stamped by whatever built this binary — see docs/usage.md.
	channel = ""
)

func main() {
	cmd.SetVersion(version, commit, date, channel)
	cmd.Execute()
}
