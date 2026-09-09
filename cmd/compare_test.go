package cmd

import (
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
)

func priced(studioURL, title string, amount float64) models.Scene {
	sc := models.Scene{ID: studioURL + title, SiteID: "t", StudioURL: studioURL, Title: title}
	sc.AddPrice(models.PriceSnapshot{Date: time.Now().UTC(), Regular: amount})
	return sc
}

func unpriced(studioURL, title string) models.Scene {
	return models.Scene{ID: studioURL + title, SiteID: "t", StudioURL: studioURL, Title: title}
}

func TestStoreLabelsUseHostUntilItCollides(t *testing.T) {
	labels := storeLabels([]string{
		"https://clipmarket.example/studio/5674/ines-dahl-taboo",
		"https://clipmarket.example/studio/4277/velvet-hour-studio",
		"https://inesdahl.example",
		"https://vidvault.example/profile/1003029948/inesdahlofficial/store/videos",
	})
	want := map[string]string{
		"https://clipmarket.example/studio/5674/ines-dahl-taboo":                    "clipmarket.example/ines-dahl-taboo",
		"https://clipmarket.example/studio/4277/velvet-hour-studio":                 "clipmarket.example/velvet-hour-studio",
		"https://inesdahl.example":                                                  "inesdahl.example",
		"https://vidvault.example/profile/1003029948/inesdahlofficial/store/videos": "vidvault.example",
	}
	for url, w := range want {
		if got := labels[url]; got != w {
			t.Errorf("label(%s) = %q, want %q", url, got, w)
		}
	}
}

// Taking the last path segment would label a ManyVids store "Videos".
func TestIdentifyingSegmentSkipsStructuralAndNumericParts(t *testing.T) {
	cases := map[string]string{
		"https://vidvault.example/profile/38331/mara-vance/store/videos": "mara-vance",
		"https://clipmarket.example/studio/5674/ines-dahl-taboo":         "ines-dahl-taboo",
		"https://clipstore.example/store/654478/Duchess-Nyx":             "Duchess-Nyx",
		"https://clipstore.example/creators/mona-reeve-1":                "mona-reeve-1",
		"https://maravance.example":                                      "",
		"https://maravance.example/":                                     "",
	}
	for in, want := range cases {
		if got := identifyingSegment(in); got != want {
			t.Errorf("identifyingSegment(%s) = %q, want %q", in, got, want)
		}
	}
}

// LowestPrice is the lowest ever recorded, which answers "was this cheaper
// once" — the wrong question for "where should I buy it".
func TestCurrentPriceUsesTheLatestSnapshot(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var sc models.Scene
	sc.AddPrice(models.PriceSnapshot{Date: base, Regular: 29.99})
	sc.AddPrice(models.PriceSnapshot{Date: base.AddDate(0, 0, 1), Regular: 9.99})
	sc.AddPrice(models.PriceSnapshot{Date: base.AddDate(0, 0, 2), Regular: 19.99})

	if sc.LowestPrice != 9.99 {
		t.Fatalf("fixture wrong: LowestPrice = %v", sc.LowestPrice)
	}
	got, ok := currentPrice(sc)
	if !ok || got != 19.99 {
		t.Errorf("currentPrice = %v/%v, want 19.99/true", got, ok)
	}
}

func TestCurrentPriceVariants(t *testing.T) {
	now := time.Now().UTC()
	cases := []struct {
		name  string
		snap  models.PriceSnapshot
		want  float64
		known bool
	}{
		{"regular", models.PriceSnapshot{Date: now, Regular: 12.99}, 12.99, true},
		{"on sale", models.PriceSnapshot{Date: now, Regular: 20, Discounted: 8, IsOnSale: true}, 8, true},
		{"free", models.PriceSnapshot{Date: now, IsFree: true}, 0, true},
		{"fully discounted", models.PriceSnapshot{Date: now, Regular: 20, IsOnSale: true, DiscountPercent: 100}, 0, true},
		// "Not free, amount unknown" must not read as $0.00, or the store with
		// the worst data looks like the cheapest.
		{"unknown", models.PriceSnapshot{Date: now}, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sc := models.Scene{ID: "x", SiteID: "t"}
			sc.AddPrice(c.snap)
			got, ok := currentPrice(sc)
			if ok != c.known || got != c.want {
				t.Errorf("currentPrice = %v/%v, want %v/%v", got, ok, c.want, c.known)
			}
		})
	}
	if _, ok := currentPrice(models.Scene{ID: "x", SiteID: "t"}); ok {
		t.Error("a scene with no price history reported a known price")
	}
}

const storeA = "https://a.example.com"
const storeB = "https://b.example.com"
const storeC = "https://c.example.com"

func reportFixture(t *testing.T, scenes []models.Scene) creatorReport {
	t.Helper()
	byStudio := map[string][]models.Scene{}
	for _, sc := range scenes {
		byStudio[sc.StudioURL] = append(byStudio[sc.StudioURL], sc)
	}
	r, ok := buildReport("Someone", []string{storeA, storeB, storeC}, byStudio)
	if !ok {
		t.Fatal("buildReport declined the fixture")
	}
	return r
}

func statFor(r creatorReport, url string) storeStat {
	for _, s := range r.stores {
		if s.url == url {
			return s
		}
	}
	return storeStat{}
}

func TestBuildReportSharedExclusiveAndCheapest(t *testing.T) {
	r := reportFixture(t, []models.Scene{
		// On all three, cheapest at B.
		priced(storeA, "Shared One", 30),
		priced(storeB, "Shared One", 10),
		priced(storeC, "Shared One", 20),
		// On two, cheapest at A. Punctuation and case must not split the group.
		priced(storeA, "Borrowed Time (Part One)", 9.99),
		priced(storeB, "borrowed time part one!", 41.99),
		// One store only.
		priced(storeA, "A Exclusive", 5),
		priced(storeB, "B Exclusive", 5),
	})

	if len(r.groups) != 2 {
		t.Fatalf("shared groups = %d, want 2", len(r.groups))
	}

	a, b, c := statFor(r, storeA), statFor(r, storeB), statFor(r, storeC)
	if a.shared != 2 || a.exclusive != 1 {
		t.Errorf("A: shared=%d exclusive=%d, want 2/1", a.shared, a.exclusive)
	}
	if b.shared != 2 || b.exclusive != 1 {
		t.Errorf("B: shared=%d exclusive=%d, want 2/1", b.shared, b.exclusive)
	}
	if c.shared != 1 || c.exclusive != 0 {
		t.Errorf("C: shared=%d exclusive=%d, want 1/0", c.shared, c.exclusive)
	}

	if b.cheapest != 1 || b.savings != 20 {
		t.Errorf("B cheapest=%d savings=%v, want 1/20", b.cheapest, b.savings)
	}
	if a.cheapest != 1 || a.savings != 32 {
		t.Errorf("A cheapest=%d savings=%v, want 1/32", a.cheapest, a.savings)
	}

	// Groups are ordered widest gap first.
	if _, _, gap, _ := r.groups[0].spread(); gap != 32 {
		t.Errorf("widest gap = %v, want 32", gap)
	}
}

// Two stores at the same price give the buyer no reason to prefer either, so
// neither may be credited as the cheapest source.
func TestSpreadTieHasNoWinner(t *testing.T) {
	r := reportFixture(t, []models.Scene{
		priced(storeA, "Tied", 15),
		priced(storeB, "Tied", 15),
		priced(storeC, "Tied", 25),
	})
	for _, s := range r.stores {
		if s.cheapest != 0 {
			t.Errorf("%s credited as cheapest despite a tie", s.url)
		}
	}
	if _, _, gap, unique := r.groups[0].spread(); gap != 10 || unique {
		t.Errorf("spread = %v unique=%v, want 10/false", gap, unique)
	}
}

// A store that lists a title without exposing a price still carries it, but
// cannot win on price.
func TestUnpricedOfferCountsAsCarriedNotCheapest(t *testing.T) {
	r := reportFixture(t, []models.Scene{
		unpriced(storeA, "Mystery"),
		priced(storeB, "Mystery", 10),
		priced(storeC, "Mystery", 30),
	})
	if statFor(r, storeA).shared != 1 {
		t.Error("unpriced listing not counted as carrying the title")
	}
	if got := statFor(r, storeA).cheapest; got != 0 {
		t.Errorf("unpriced store credited cheapest %d times", got)
	}
	if got := statFor(r, storeB).cheapest; got != 1 {
		t.Errorf("B cheapest = %d, want 1", got)
	}
	if _, ok := statFor(r, storeA).avgPrice(); ok {
		t.Error("a store with no priced listings reported an average")
	}
}

// A store listing the same title twice contributes its cheapest listing only,
// and must not be counted as sharing with itself.
func TestDuplicateTitleWithinOneStoreCollapses(t *testing.T) {
	r := reportFixture(t, func() []models.Scene {
		a1 := priced(storeA, "Repeat", 25)
		a1.ID = "a1"
		a2 := priced(storeA, "Repeat", 15)
		a2.ID = "a2"
		return []models.Scene{a1, a2, priced(storeB, "Repeat", 20)}
	}())

	if len(r.groups) != 1 {
		t.Fatalf("groups = %d, want 1", len(r.groups))
	}
	if got := len(r.groups[0].offers); got != 2 {
		t.Fatalf("offers = %d, want one per store", got)
	}
	low, _, gap, unique := r.groups[0].spread()
	if !unique || low.storeURL != storeA || gap != 5 {
		t.Errorf("spread: low=%s gap=%v unique=%v, want A/5/true", low.storeURL, gap, unique)
	}
}

// A creator whose scenes all sit on one storefront has nothing to compare.
func TestBuildReportSkipsSingleStoreCreators(t *testing.T) {
	byStudio := map[string][]models.Scene{storeA: {priced(storeA, "Only", 10)}}
	if _, ok := buildReport("Someone", []string{storeA, storeB}, byStudio); ok {
		t.Error("built a report for a creator present on one storefront")
	}
}

func TestPlural(t *testing.T) {
	if got := plural(1, "scene"); got != "1 scene" {
		t.Errorf("plural(1) = %q", got)
	}
	if got := plural(2, "scene"); got != "2 scenes" {
		t.Errorf("plural(2) = %q", got)
	}
}

func TestLabelWidthCoversTheLongestLabel(t *testing.T) {
	r := creatorReport{labels: map[string]string{"u": "a-very-long-storefront-label.example.com"}}
	if got := r.labelWidth(); got != len("a-very-long-storefront-label.example.com") {
		t.Errorf("labelWidth = %d", got)
	}
	// An unlabelled store still renders, falling back to its host.
	if got := r.label("https://www.fallback.example.com/x"); got != "fallback.example.com" {
		t.Errorf("label fallback = %q", got)
	}
	if got := (creatorReport{}).labelWidth(); got != len("store") {
		t.Errorf("empty labelWidth = %d, want the header width", got)
	}
}

func TestNounFor(t *testing.T) {
	if got := nounFor(1, "title"); got != "title" {
		t.Errorf("nounFor(1) = %q", got)
	}
	if got := nounFor(0, "title"); got != "titles" {
		t.Errorf("nounFor(0) = %q", got)
	}
	if got := nounFor(2, "title"); got != "titles" {
		t.Errorf("nounFor(2) = %q", got)
	}
}

// formatPrice renders a store's offer for the report. An offer with no amount
// is blank rather than "0.00": a zero would rank the store with the worst data
// as the cheapest place to buy.
func TestFormatPrice(t *testing.T) {
	cases := []struct {
		name string
		o    offer
		want string
	}{
		{"priced", offer{price: 12.5, priced: true}, "12.50"},
		{"rounds to two decimals", offer{price: 19.999, priced: true}, "20.00"},
		{"free is a real price", offer{price: 0, priced: true}, "0.00"},
		{"unpriced is blank, not zero", offer{price: 0, priced: false}, ""},
		{"amount ignored when unpriced", offer{price: 9.99, priced: false}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := formatPrice(c.o); got != c.want {
				t.Errorf("formatPrice(%+v) = %q, want %q", c.o, got, c.want)
			}
		})
	}
}
