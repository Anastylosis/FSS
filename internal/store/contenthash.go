package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/Anastylosis/FSS/models"
)

// sceneContentHash fingerprints everything Save would write for a scene, so an
// unchanged scene can be skipped instead of rewritten.
//
// It hashes the *stored* representation — timestamps via timeStr, exactly as
// upsertScene writes them — not the in-memory values. A scene loaded back from
// SQLite has second-precision timestamps while a freshly scraped one has
// nanoseconds; hashing the Go values would make every scene look changed and
// defeat the whole mechanism.
//
// ScrapedAt and FirstSeenAt are deliberately excluded:
//
//   - ScrapedAt changes on every scrape by definition. Including it would mean
//     nothing ever matches, which is the situation this is fixing. Save updates
//     that one column separately when it is the only difference.
//   - FirstSeenAt is owned by the store (see firstSeenFor) and never changes
//     once set, so it cannot distinguish two versions of a scene.
func sceneContentHash(sc models.Scene) string {
	h := sha256.New()

	// Length-prefix every field so no concatenation of values can be confused
	// for a different set of values.
	w := func(parts ...string) {
		for _, p := range parts {
			_, _ = fmt.Fprintf(h, "%d:%s|", len(p), p)
		}
	}
	wn := func(nums ...int) {
		for _, n := range nums {
			_, _ = fmt.Fprintf(h, "%d|", n)
		}
	}
	wList := func(items []string) {
		wn(len(items))
		w(items...)
	}

	w(sc.ID, sc.SiteID, sc.StudioURL, sc.Title, sc.URL, timeStr(sc.Date), sc.Description)
	w(sc.Thumbnail, sc.Preview, sc.Director, sc.Studio, sc.Series)
	wn(sc.SeriesPart, sc.Duration, sc.Width, sc.Height, sc.Views, sc.Likes, sc.Comments)
	w(sc.Resolution, sc.Format)

	// Order is significant — it is stored in the junction tables' position
	// column — so these are hashed as written, not sorted.
	wList(sc.Performers)
	wList(sc.Tags)
	wList(sc.Categories)

	writePriceHistory(h, sc)
	writeExternalIDs(h, sc)

	_, _ = fmt.Fprintf(h, "%.4f|", sc.LowestPrice)
	w(nullableTime(sc.LowestPriceDate), nullableTime(sc.DeletedAt))

	return hex.EncodeToString(h.Sum(nil))
}

func writePriceHistory(h io.Writer, sc models.Scene) {
	_, _ = fmt.Fprintf(h, "%d|", len(sc.PriceHistory))
	for _, p := range sc.PriceHistory {
		_, _ = fmt.Fprintf(h, "%s;%.4f;%.4f;%d;%d;%d",
			timeStr(p.Date), p.Regular, p.Discounted,
			boolInt(p.IsFree), boolInt(p.IsOnSale), p.DiscountPercent)
		if p.Currency != "" {
			// Only when set, so hashes of scenes without a currency are unchanged.
			_, _ = fmt.Fprintf(h, ";%d:%s", len(p.Currency), p.Currency)
		}
		_, _ = fmt.Fprint(h, "|")
	}
}

// writeExternalIDs hashes the map by sorted key, since Go map iteration order
// is randomised and the stored rows have no inherent order.
func writeExternalIDs(h io.Writer, sc models.Scene) {
	keys := make([]string, 0, len(sc.ExternalIDs))
	for k, v := range sc.ExternalIDs {
		if k == "" || v == "" {
			continue // syncExternalIDs skips these, so they are not stored
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	_, _ = fmt.Fprintf(h, "%d|", len(keys))
	for _, k := range keys {
		_, _ = fmt.Fprintf(h, "%s=%s|", k, sc.ExternalIDs[k])
	}
}

// nullableTime renders an optional timestamp the way timePtrStr stores it,
// with nil and the zero time both collapsing to "".
func nullableTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return timeStr(*t)
}
