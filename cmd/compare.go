package cmd

import (
	"encoding/csv"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Anastylosis/FSS/internal/creators"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/output"
)

var compareCmd = &cobra.Command{
	Use:   "compare",
	Short: "Compare a creator's catalogue across the storefronts they sell it on",
	Long: `Compare a creator's catalogue across the storefronts they sell it on.

Creators list the same clips on several sites at different prices, and each site
also carries some the others do not. For every creator this reports what each
store holds, which titles are on more than one, where each shared title is
cheapest, and what only one store has.

Storefronts are grouped using creators.d. Without any creator files, the studios
are clustered by the same heuristic as ` + "`fss creators suggest`" + `, and the report
says so — define the creators to make the grouping exact.`,
	RunE: runCompare,
}

func init() {
	rootCmd.AddCommand(compareCmd)
	addSceneSourceFlags(compareCmd)
	compareCmd.Flags().Int("top", 10, "how many of the widest price spreads to list per creator")
	compareCmd.Flags().String("csv", "", "also write every shared title to this CSV file")
	compareCmd.Flags().Bool("exclusives", false, "list the titles only one storefront carries")
}

// offer is one storefront's listing of a title.
type offer struct {
	storeURL string
	sceneURL string
	price    float64
	priced   bool
}

// titleGroup is one title as carried across a creator's storefronts. A store
// listing the same title twice contributes only its cheapest listing.
type titleGroup struct {
	title  string
	offers map[string]offer
}

func (g titleGroup) stores() []string {
	out := make([]string, 0, len(g.offers))
	for k := range g.offers {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// pricedOffers returns only the offers carrying a usable price. A store that
// lists a title without exposing a price cannot be compared on price, but still
// counts as carrying it.
func (g titleGroup) pricedOffers() []offer {
	out := make([]offer, 0, len(g.offers))
	for _, o := range g.offers {
		if o.priced {
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].price != out[j].price {
			return out[i].price < out[j].price
		}
		return out[i].storeURL < out[j].storeURL
	})
	return out
}

// spread reports the price gap across stores, and whether one store is strictly
// the cheapest. A tie has no winner — two stores at the same price give the
// buyer no reason to prefer either.
func (g titleGroup) spread() (low, high offer, gap float64, uniqueLow bool) {
	p := g.pricedOffers()
	if len(p) < 2 {
		return offer{}, offer{}, 0, false
	}
	low, high = p[0], p[len(p)-1]
	return low, high, high.price - low.price, p[0].price < p[1].price
}

type storeStat struct {
	url        string
	scenes     int
	shared     int
	exclusive  int
	priced     int
	priceTotal float64
	cheapest   int
	savings    float64
}

func (s storeStat) avgPrice() (float64, bool) {
	if s.priced == 0 {
		return 0, false
	}
	return s.priceTotal / float64(s.priced), true
}

type creatorReport struct {
	name   string
	stores []storeStat
	groups []titleGroup
	labels map[string]string
}

// label names a storefront for display, falling back to the raw URL for a store
// that produced no scenes and so was never labelled.
func (r creatorReport) label(url string) string {
	if l, ok := r.labels[url]; ok {
		return l
	}
	return storeHost(url)
}

// labelWidth is the column width the store labels need.
func (r creatorReport) labelWidth() int {
	w := len("store")
	for _, l := range r.labels {
		if len(l) > w {
			w = len(l)
		}
	}
	return w
}

func runCompare(cmd *cobra.Command, _ []string) error {
	scenes, src, err := loadFSSScenes(cmd)
	if err != nil {
		return err
	}

	groups, inferred, err := creatorGroupings(cmd, scenes)
	if err != nil {
		return err
	}
	if len(groups) == 0 {
		fmt.Printf("Nothing to compare in %s: no creator has more than one storefront.\n", src)
		return nil
	}

	byStudio := map[string][]models.Scene{}
	for _, sc := range scenes {
		key := output.CanonicalStudioURL(sc.StudioURL)
		byStudio[key] = append(byStudio[key], sc)
	}

	reports := make([]creatorReport, 0, len(groups))
	for name, urls := range groups {
		if r, ok := buildReport(name, urls, byStudio); ok {
			reports = append(reports, r)
		}
	}
	if len(reports) == 0 {
		fmt.Printf("Nothing to compare in %s: no creator has scenes on more than one storefront.\n", src)
		return nil
	}
	sort.Slice(reports, func(i, j int) bool {
		return creators.Key(reports[i].name) < creators.Key(reports[j].name)
	})

	if inferred && !noticesSuppressed() {
		fmt.Println("[notice] No creators defined — storefronts were grouped by the `fss creators suggest`")
		fmt.Println("         heuristic. Run `fss creators suggest --write` to make the grouping exact.")
		fmt.Println()
	}

	top, _ := cmd.Flags().GetInt("top")
	exclusives, _ := cmd.Flags().GetBool("exclusives")
	for i, r := range reports {
		if i > 0 {
			fmt.Println()
		}
		printReport(r, top, exclusives)
	}

	if path, _ := cmd.Flags().GetString("csv"); path != "" {
		if err := writeCompareCSV(path, reports); err != nil {
			return err
		}
		fmt.Printf("\nShared titles written to %s\n", path)
	}
	return nil
}

// creatorGroupings returns creator name → store URLs, from creators.d when it
// is populated and from the clustering heuristic when it is not. The second
// return value reports which, so the output can say so.
func creatorGroupings(cmd *cobra.Command, scenes []models.Scene) (map[string][]string, bool, error) {
	list, err := loadCreators(cmd)
	if err != nil {
		return nil, false, err
	}

	// --from-creator has already narrowed the scene set; the grouping still has
	// to come from the same definitions so the report is grouped, not merged.
	if len(list) > 0 {
		out := map[string][]string{}
		for _, c := range list {
			out[c.Name] = c.URLs()
		}
		return out, false, nil
	}

	if names, _ := cmd.Flags().GetStringSlice("from-creator"); len(names) > 0 {
		return nil, false, fmt.Errorf("--from-creator needs creator definitions — run `fss creators suggest --write` first")
	}

	out := map[string][]string{}
	for _, g := range clusterStudios(profileStudios(scenes)) {
		if len(g) < 2 {
			continue
		}
		c := proposalFor(g)
		out[c.Name] = c.URLs()
	}
	return out, true, nil
}

func buildReport(name string, storeURLs []string, byStudio map[string][]models.Scene) (creatorReport, bool) {
	r := creatorReport{name: name}

	// One group per title, each store contributing its cheapest listing.
	groups := map[string]*titleGroup{}
	stats := map[string]*storeStat{}
	present := 0

	for _, raw := range storeURLs {
		url := output.CanonicalStudioURL(raw)
		scenes := byStudio[url]
		if len(scenes) == 0 {
			continue
		}
		present++
		st := &storeStat{url: url, scenes: len(scenes)}
		stats[url] = st

		for _, sc := range scenes {
			key := creators.Key(sc.Title)
			if key == "" {
				continue
			}
			g := groups[key]
			if g == nil {
				g = &titleGroup{title: sc.Title, offers: map[string]offer{}}
				groups[key] = g
			}
			price, priced := currentPrice(sc)
			o := offer{storeURL: url, sceneURL: sc.URL, price: price, priced: priced}
			// Keep the cheapest listing when a store carries the title more
			// than once; an unpriced listing never displaces a priced one.
			if prev, ok := g.offers[url]; ok {
				if !o.priced || (prev.priced && prev.price <= o.price) {
					continue
				}
			}
			g.offers[url] = o
			if priced {
				st.priced++
				st.priceTotal += price
			}
		}
	}
	if present < 2 {
		return creatorReport{}, false
	}

	for _, g := range groups {
		urls := g.stores()
		if len(urls) == 1 {
			stats[urls[0]].exclusive++
			continue
		}
		for _, u := range urls {
			stats[u].shared++
		}
		if low, _, gap, unique := g.spread(); unique {
			stats[low.storeURL].cheapest++
			stats[low.storeURL].savings += gap
		}
		r.groups = append(r.groups, *g)
	}

	withScenes := make([]string, 0, len(stats))
	for _, st := range stats {
		r.stores = append(r.stores, *st)
		withScenes = append(withScenes, st.url)
	}
	r.labels = storeLabels(withScenes)
	sort.Slice(r.stores, func(i, j int) bool { return r.stores[i].scenes > r.stores[j].scenes })
	sort.Slice(r.groups, func(i, j int) bool {
		_, _, gi, _ := r.groups[i].spread()
		_, _, gj, _ := r.groups[j].spread()
		if gi != gj {
			return gi > gj
		}
		return r.groups[i].title < r.groups[j].title
	})
	return r, true
}

// currentPrice returns the price a buyer would pay today: the most recent
// snapshot's effective price.
//
// It is deliberately not Scene.LowestPrice, which is the lowest ever recorded —
// the right answer for "was this ever cheaper", and the wrong one for "where
// should I buy it". A snapshot that records no amount at all ("not free, price
// unknown") reports priced=false rather than zero, which would otherwise make
// the store with the worst data look like the cheapest.
func currentPrice(sc models.Scene) (float64, bool) {
	if n := len(sc.PriceHistory); n > 0 {
		p := sc.PriceHistory[n-1]
		switch {
		case p.IsFree, p.IsOnSale && p.DiscountPercent == 100:
			return 0, true
		case p.IsOnSale && p.Discounted > 0:
			return p.Discounted, true
		case p.Regular > 0:
			return p.Regular, true
		}
		return 0, false
	}
	if sc.LowestPriceDate != nil {
		return sc.LowestPrice, true
	}
	return 0, false
}

func printReport(r creatorReport, top int, exclusives bool) {
	total := 0
	for _, s := range r.stores {
		total += s.scenes
	}
	lw := r.labelWidth()
	fmt.Printf("%s — %s across %d storefronts\n\n", r.name, plural(total, "scene"), len(r.stores))

	fmt.Printf("  %-*s %8s %8s %10s %10s\n", lw, "store", "scenes", "shared", "exclusive", "avg price")
	for _, s := range r.stores {
		avg := "—"
		if v, ok := s.avgPrice(); ok {
			avg = fmt.Sprintf("$%.2f", v)
		}
		fmt.Printf("  %-*s %8d %8d %10d %10s\n", lw, r.label(s.url), s.scenes, s.shared, s.exclusive, avg)
	}

	cheapest := make([]storeStat, 0, len(r.stores))
	for _, s := range r.stores {
		if s.cheapest > 0 {
			cheapest = append(cheapest, s)
		}
	}
	sort.Slice(cheapest, func(i, j int) bool { return cheapest[i].cheapest > cheapest[j].cheapest })
	if len(cheapest) > 0 {
		fmt.Printf("\n  Cheapest source for the %s carried by more than one store:\n", plural(len(r.groups), "title"))
		for _, s := range cheapest {
			fmt.Printf("    %-*s  %5d %s, $%.2f below the dearest in total\n",
				lw, r.label(s.url), s.cheapest, nounFor(s.cheapest, "title"), s.savings)
		}
	}

	shown := 0
	for _, g := range r.groups {
		if shown >= top {
			break
		}
		low, high, gap, _ := g.spread()
		if gap <= 0 {
			break
		}
		if shown == 0 {
			fmt.Printf("\n  Widest price gaps:\n")
		}
		fmt.Printf("    %-44s  $%6.2f %-*s  vs $%6.2f %s  (-$%.2f)\n",
			truncate(g.title, 44), low.price, lw, r.label(low.storeURL), high.price, r.label(high.storeURL), gap)
		shown++
	}

	if exclusives {
		printExclusives(r, lw)
	}
}

func printExclusives(r creatorReport, lw int) {
	// Exclusive titles are not in r.groups (which holds only shared ones), so
	// they are reported per store from the counts gathered during the build.
	headed := false
	for _, s := range r.stores {
		if s.exclusive == 0 {
			continue
		}
		if !headed {
			fmt.Printf("\n  Carried by one storefront only:\n")
			headed = true
		}
		fmt.Printf("    %-*s  %5d %s\n", lw, r.label(s.url), s.exclusive, nounFor(s.exclusive, "title"))
	}
}

func writeCompareCSV(path string, reports []creatorReport) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	w := csv.NewWriter(f)
	defer w.Flush()
	if err := w.Write([]string{"creator", "title", "stores", "cheapest_store", "cheapest_price", "dearest_store", "dearest_price", "spread", "cheapest_url"}); err != nil {
		return err
	}
	for _, r := range reports {
		for _, g := range r.groups {
			low, high, gap, _ := g.spread()
			row := []string{
				r.name,
				g.title,
				strconv.Itoa(len(g.offers)),
				r.label(low.storeURL),
				formatPrice(low),
				r.label(high.storeURL),
				formatPrice(high),
				strconv.FormatFloat(gap, 'f', 2, 64),
				low.sceneURL,
			}
			if err := w.Write(row); err != nil {
				return err
			}
		}
	}
	w.Flush()
	return w.Error()
}

func formatPrice(o offer) string {
	if !o.priced {
		return ""
	}
	return strconv.FormatFloat(o.price, 'f', 2, 64)
}

// storeLabels names each storefront as briefly as it can be told apart from the
// others in the same report.
//
// The host alone is usually enough and is what an operator recognises. It is
// not always enough: one creator can run five Clips4Sale studios, so a colliding
// host is qualified with the most identifying path segment.
func storeLabels(urls []string) map[string]string {
	counts := map[string]int{}
	for _, u := range urls {
		counts[storeHost(u)]++
	}
	out := make(map[string]string, len(urls))
	for _, u := range urls {
		h := storeHost(u)
		if counts[h] == 1 {
			out[u] = h
			continue
		}
		if seg := identifyingSegment(u); seg != "" {
			out[u] = h + "/" + seg
		} else {
			out[u] = h
		}
	}
	return out
}

func storeHost(u string) string {
	s := strings.TrimPrefix(u, "https://")
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimPrefix(s, "www.")
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	return s
}

// identifyingSegment picks the longest path segment that is not purely digits.
//
// Store URLs bury the name among structural and numeric segments —
// `/studio/5674/ines-dahl-taboo`, `/Profile/38331/mara-vance/Store/Videos`
// — and in both the longest non-numeric segment is the one a human would read.
// Taking the last segment instead yields "Videos".
func identifyingSegment(u string) string {
	s := u
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	i := strings.IndexByte(s, '/')
	if i < 0 {
		return ""
	}
	best := ""
	for _, seg := range strings.Split(s[i+1:], "/") {
		if seg == "" || isAllDigits(seg) {
			continue
		}
		if len(seg) > len(best) {
			best = seg
		}
	}
	return best
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

func plural(n int, word string) string {
	return fmt.Sprintf("%d %s", n, nounFor(n, word))
}

// nounFor pluralises word for n. Separate from plural so a caller that formats
// the count itself — to keep a column aligned — still gets the right noun.
func nounFor(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}
