package cmd

import (
	"sort"

	"github.com/spf13/cobra"
)

// The --format json result of `fss compare`; see docs/json-output.md.

type compareResultJSON struct {
	Creators []compareCreatorJSON `json:"creators"`
	Message  string               `json:"message,omitempty"`
	CSVPath  string               `json:"csvPath,omitempty"`
	Notices  []string             `json:"notices,omitempty"`
}

type compareCreatorJSON struct {
	Name                string                 `json:"name"`
	HeuristicGrouping   bool                   `json:"heuristicGrouping"`
	Stores              []compareStoreJSON     `json:"stores"`
	Cheapest            []compareCheapestJSON  `json:"cheapest"`
	WidestGaps          []compareGapJSON       `json:"widestGaps"`
	MixedCurrencyTitles int                    `json:"mixedCurrencyTitles"`
	Exclusives          []compareExclusiveJSON `json:"exclusives,omitempty"`
}

type compareStoreJSON struct {
	URL           string   `json:"url"`
	Label         string   `json:"label"`
	Scenes        int      `json:"scenes"`
	Shared        int      `json:"shared"`
	Exclusive     int      `json:"exclusive"`
	AvgPrice      *float64 `json:"avgPrice"`
	Currency      string   `json:"currency,omitempty"`
	MixedCurrency bool     `json:"mixedCurrency,omitempty"`
}

type compareCheapestJSON struct {
	Store        string  `json:"store"`
	Titles       int     `json:"titles"`
	SavingsTotal float64 `json:"savingsTotal"`
}

type compareOfferJSON struct {
	Store    string  `json:"store"`
	Price    float64 `json:"price"`
	Currency string  `json:"currency,omitempty"`
}

type compareGapJSON struct {
	Title string           `json:"title"`
	Low   compareOfferJSON `json:"low"`
	High  compareOfferJSON `json:"high"`
	Gap   float64          `json:"gap"`
}

type compareExclusiveJSON struct {
	Store  string   `json:"store"`
	Titles []string `json:"titles"`
}

func buildCompareResult(cmd *cobra.Command, reports []creatorReport, inferred bool, message string) (*compareResultJSON, error) {
	top, _ := cmd.Flags().GetInt("top")
	exclusives, _ := cmd.Flags().GetBool("exclusives")
	res := &compareResultJSON{Creators: []compareCreatorJSON{}, Message: message}
	for _, r := range reports {
		res.Creators = append(res.Creators, creatorJSON(r, inferred, top, exclusives))
	}
	if inferred && len(reports) > 0 && !noticesSuppressed() {
		res.Notices = []string{inferredNoticeText()}
	}
	if path, _ := cmd.Flags().GetString("csv"); path != "" && len(reports) > 0 {
		if err := writeCompareCSV(path, reports); err != nil {
			return res, err
		}
		res.CSVPath = path
	}
	return res, nil
}

func inferredNoticeText() string {
	return "No creators defined — storefronts were grouped by the `fss creators suggest` heuristic. Run `fss creators suggest --write` to make the grouping exact."
}

func creatorJSON(r creatorReport, inferred bool, top int, exclusives bool) compareCreatorJSON {
	out := compareCreatorJSON{
		Name:                r.name,
		HeuristicGrouping:   inferred,
		Stores:              []compareStoreJSON{},
		Cheapest:            []compareCheapestJSON{},
		WidestGaps:          []compareGapJSON{},
		MixedCurrencyTitles: r.mixed,
	}
	for _, s := range r.stores {
		js := compareStoreJSON{
			URL: s.url, Label: r.label(s.url), Scenes: s.scenes, Shared: s.shared,
			Exclusive: s.exclusive, Currency: s.currency, MixedCurrency: s.mixed,
		}
		if v, ok := s.avgPrice(); ok {
			js.AvgPrice = &v
		}
		out.Stores = append(out.Stores, js)
	}
	out.Cheapest = cheapestJSON(r)
	for _, g := range r.groups {
		if len(out.WidestGaps) >= top {
			break
		}
		low, high, gap, _ := g.spread()
		if gap <= 0 {
			break
		}
		out.WidestGaps = append(out.WidestGaps, compareGapJSON{
			Title: g.title, Gap: gap,
			Low:  compareOfferJSON{Store: low.storeURL, Price: low.price, Currency: low.currency},
			High: compareOfferJSON{Store: high.storeURL, Price: high.price, Currency: high.currency},
		})
	}
	if exclusives {
		out.Exclusives = exclusivesJSON(r)
	}
	return out
}

func cheapestJSON(r creatorReport) []compareCheapestJSON {
	out := []compareCheapestJSON{}
	for _, s := range r.stores {
		if s.cheapest > 0 {
			out = append(out, compareCheapestJSON{Store: s.url, Titles: s.cheapest, SavingsTotal: s.savings})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Titles != out[j].Titles {
			return out[i].Titles > out[j].Titles
		}
		return out[i].Store < out[j].Store
	})
	return out
}

func exclusivesJSON(r creatorReport) []compareExclusiveJSON {
	out := []compareExclusiveJSON{}
	for _, s := range r.stores {
		if s.exclusive == 0 {
			continue
		}
		titles := append([]string(nil), r.exclusive[s.url]...)
		sort.Strings(titles)
		out = append(out, compareExclusiveJSON{Store: s.url, Titles: titles})
	}
	return out
}
