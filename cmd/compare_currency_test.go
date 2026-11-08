package cmd

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/models"
)

func pricedIn(studioURL, title string, amount float64, currency string) models.Scene {
	sc := models.Scene{ID: studioURL + title, SiteID: "t", StudioURL: studioURL, Title: title}
	sc.AddPrice(models.PriceSnapshot{Date: time.Now().UTC(), Regular: amount, Currency: currency})
	return sc
}

func TestCompareDoesNotRankAcrossCurrencies(t *testing.T) {
	r := reportFixture(t, []models.Scene{
		pricedIn(storeA, "Shared", 5, "EUR"),
		pricedIn(storeB, "Shared", 30, ""),
		pricedIn(storeA, "Same", 5, "EUR"),
		pricedIn(storeC, "Same", 9, "EUR"),
	})
	for _, g := range r.groups {
		_, _, _, unique := g.spread()
		if g.title == "Shared" && unique {
			t.Error("ranked a EUR offer against an unlabelled one")
		}
	}
	if r.mixed != 1 {
		t.Errorf("mixed = %d, want 1", r.mixed)
	}
	a := statFor(r, storeA)
	if a.cheapest != 1 || a.currency != "EUR" {
		t.Errorf("store A cheapest=%d currency=%q, want 1/EUR", a.cheapest, a.currency)
	}
	if got := money(a.savings, a.currency); got != "EUR 4.00" {
		t.Errorf("savings = %q", got)
	}
	if money(1.5, "") != "$1.50" || money(1.5, "USD") != "$1.50" {
		t.Error("unlabelled and USD amounts should keep the $ form")
	}
}

func TestCompareAvgPriceIgnoresSupersededListings(t *testing.T) {
	r := reportFixture(t, []models.Scene{
		pricedIn(storeA, "Dup", 50, ""),
		pricedIn(storeA, "Dup", 10, ""),
		pricedIn(storeB, "Dup", 20, ""),
	})
	if v, ok := statFor(r, storeA).avgPrice(); !ok || v != 10 {
		t.Errorf("avg = %v/%v, want 10: the 50 listing was displaced", v, ok)
	}
}

func TestCurrentPriceAgreesWithEffective(t *testing.T) {
	sc := models.Scene{ID: "x", SiteID: "t"}
	sc.AddPrice(models.PriceSnapshot{Date: time.Now().UTC(), Regular: 20, IsOnSale: true, DiscountPercent: 25})
	if _, ok := currentPrice(sc); ok {
		t.Error("on-sale snapshot without an amount reported a known price")
	}
}

func TestWriteCompareCSVGuardsFormulas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.csv")
	r := reportFixture(t, []models.Scene{
		pricedIn(storeA, `=HYPERLINK("http://evil")`, 5, "EUR"),
		pricedIn(storeB, `=HYPERLINK("http://evil")`, 9, "EUR"),
	})
	r.name = "=cmd"
	for i := range r.groups {
		r.groups[i].title = `=HYPERLINK("http://evil")`
	}
	if err := writeCompareCSV(path, []creatorReport{r}); err != nil {
		t.Fatal(err)
	}
	assertNoFormulaCells(t, path)
}

func assertNoFormulaCells(t *testing.T, path string) {
	t.Helper()
	fh, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fh.Close() }()
	cr := csv.NewReader(fh)
	cr.FieldsPerRecord = -1
	recs, err := cr.ReadAll()
	if err != nil {
		t.Fatalf("re-reading CSV: %v", err)
	}
	for _, rec := range recs {
		for _, cell := range rec {
			if cell != "" && strings.ContainsRune("=+-@\t\r", rune(cell[0])) {
				t.Errorf("cell %q would be evaluated as a formula", cell)
			}
		}
	}
}

// FuzzWriteCompareCSVNeverEmitsAFormula pins that no cell of the compare CSV
// can start a spreadsheet formula, whatever scraped text reaches it.
func FuzzWriteCompareCSVNeverEmitsAFormula(f *testing.F) {
	for _, s := range []string{"Normal", "=cmd|' /C calc'!A0", "+1+1", "-2+3", "@SUM(A1)", "\tx", "\rx", "", "a\x00b", "q\" ,\n"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		r := reportFixture(t, []models.Scene{
			pricedIn(storeA, "t", 5, "EUR"),
			pricedIn(storeB, "t", 9, "EUR"),
		})
		r.name = s
		for i := range r.groups {
			r.groups[i].title = s
		}
		r.labels = map[string]string{storeA: s, storeB: s}
		for k, o := range r.groups[0].offers {
			o.sceneURL = s
			o.currency = s
			r.groups[0].offers[k] = o
		}
		path := filepath.Join(t.TempDir(), "c.csv")
		if err := writeCompareCSV(path, []creatorReport{r}); err != nil {
			t.Fatal(err)
		}
		assertNoFormulaCells(t, path)
	})
}
