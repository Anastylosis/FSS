// Package models defines the data types produced by scrapers.
package models

import "time"

// PriceSnapshot captures pricing at a single point in time.
type PriceSnapshot struct {
	Date            time.Time `json:"date"`
	Regular         float64   `json:"regular"`
	Discounted      float64   `json:"discounted,omitempty"`
	IsFree          bool      `json:"isFree"`
	IsOnSale        bool      `json:"isOnSale"`
	DiscountPercent int       `json:"discountPercent,omitempty"`
	// Currency is the ISO 4217 code (or the site's own token name) the amounts
	// are in. Empty means the scraper did not say.
	Currency string `json:"currency,omitempty"`
}

// Effective returns what you would pay at this snapshot.
func (p PriceSnapshot) Effective() float64 {
	if p.IsFree {
		return 0
	}
	if p.IsOnSale {
		return p.Discounted
	}
	return p.Regular
}

// StoreSchemaVersion is the layout version stamped into every StudioFile FSS
// writes, so a reader can refuse a file it does not understand. Bump it only
// for changes that alter how an existing file must be interpreted, not for
// additive optional fields. Version 0 means "written before versioning" and
// loads normally. See docs/metadata.md.
const StoreSchemaVersion = 1

// Well-known Scene.ExternalIDs keys. A stashbox instance uses its own site ID
// (e.g. "stashdb", "pmvstash"), so those are not enumerated here.
const (
	ExternalStashDB = "stashdb"
	ExternalTPDB    = "tpdb"
	ExternalIAFD    = "iafd"
	ExternalIndexxx = "indexxx"
)

// StudioFile is the top-level JSON structure for a per-studio file.
type StudioFile struct {
	SchemaVersion int       `json:"schemaVersion"`
	StudioURL     string    `json:"studioUrl"`
	ScrapedAt     time.Time `json:"scrapedAt"`
	SceneCount    int       `json:"sceneCount"`
	Scenes        []Scene   `json:"scenes"`
}

// Scene holds all metadata for a single scraped scene. Fields vary by site —
// only ID, SiteID, Title, URL, and ScrapedAt are guaranteed to be populated.
type Scene struct {
	// Identity
	ID        string `json:"id"`
	SiteID    string `json:"siteId"`
	StudioURL string `json:"studioUrl"`

	// ExternalIDs maps a metadata database to this scene's ID in it, keyed by
	// the well-known source names below. (ID, SiteID) is site-local, so these
	// are the only keys that identify the same scene across sites.
	ExternalIDs map[string]string `json:"externalIds,omitempty"`

	// Core
	Title       string    `json:"title"`
	URL         string    `json:"url"`
	Date        time.Time `json:"date,omitzero"`
	Description string    `json:"description,omitempty"`

	// Media
	Thumbnail string `json:"thumbnail,omitempty"`
	Preview   string `json:"preview,omitempty"`

	// People & production
	Performers []string `json:"performers,omitempty"`
	Director   string   `json:"director,omitempty"`
	Studio     string   `json:"studio,omitempty"`

	// Classification
	Tags       []string `json:"tags,omitempty"`
	Categories []string `json:"categories,omitempty"`

	// Series
	Series     string `json:"series,omitempty"`
	SeriesPart int    `json:"seriesPart,omitempty"`

	// Technical
	Duration   int    `json:"duration,omitempty"`
	Resolution string `json:"resolution,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	Format     string `json:"format,omitempty"`

	// Engagement
	Views    int `json:"views,omitempty"`
	Likes    int `json:"likes,omitempty"`
	Comments int `json:"comments,omitempty"`

	// Pricing history
	PriceHistory    []PriceSnapshot `json:"priceHistory,omitempty"`
	LowestPrice     float64         `json:"lowestPrice,omitempty"`
	LowestPriceDate *time.Time      `json:"lowestPriceDate,omitempty"`

	// Scraper housekeeping
	FirstSeenAt time.Time  `json:"firstSeenAt,omitzero"`
	ScrapedAt   time.Time  `json:"scrapedAt"`
	DeletedAt   *time.Time `json:"deletedAt,omitempty"`
}

// sameTerms reports whether two snapshots describe identical pricing, ignoring
// when they were taken.
func (p PriceSnapshot) sameTerms(o PriceSnapshot) bool {
	return p.Regular == o.Regular &&
		p.Discounted == o.Discounted &&
		p.IsFree == o.IsFree &&
		p.IsOnSale == o.IsOnSale &&
		p.DiscountPercent == o.DiscountPercent &&
		p.Currency == o.Currency
}

// AddPrice records a PriceSnapshot and updates LowestPrice/LowestPriceDate if the
// effective price beats the current record.
//
// A snapshot whose terms match the previous one is dropped rather than appended:
// price history is a log of *changes*, and every scrape re-adds the full existing
// history (see carryOverPriceHistory), so appending unconditionally grows the
// stored history without bound on a repeating cron --refresh while adding no
// information. Only consecutive repeats collapse, so a genuine change away and
// back is still recorded.
//
// Snapshots carrying no price information at all (not free, zero amount) are
// recorded but do not affect lowest-price tracking. A 100%-off sale is the
// exception: it really does cost zero, so it counts.
func (s *Scene) AddPrice(p PriceSnapshot) {
	if n := len(s.PriceHistory); n > 0 && s.PriceHistory[n-1].sameTerms(p) {
		return
	}
	s.PriceHistory = append(s.PriceHistory, p)

	// A zero effective price only counts when it is a real price: explicitly
	// free, or a 100%-off sale. Otherwise it means "amount unknown".
	effective := p.Effective()
	genuinelyZero := p.IsFree || (p.IsOnSale && p.DiscountPercent == 100)
	if effective == 0 && !genuinelyZero {
		return
	}
	if s.LowestPriceDate == nil || effective < s.LowestPrice {
		s.LowestPrice = effective
		s.LowestPriceDate = &p.Date
	}
}
