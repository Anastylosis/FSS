package paysiteutil

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// benefitmonkey.com's /scenes feed is merged with lezkey.com's; the Benefit
// Monkey row keeps only its own, even when a whole page belongs to the sister.
func TestOnlySiteDomainFiltersMergedFeed(t *testing.T) {
	item := func(id int, domain string) string {
		return fmt.Sprintf(`{"id":%d,"title":"T%d","slug":"t%d","site":"S","site_domain":%q}`, id, id, id, domain)
	}
	pages := map[string]string{
		"1": `{"contents":{"total":4,"page":"1","total_pages":3,"data":[` + item(1, "benefitmonkey.com") + `,` + item(2, "lezkey.com") + `]}}`,
		"2": `{"contents":{"total":4,"page":"2","total_pages":3,"data":[` + item(3, "lezkey.com") + `]}}`,
		"3": `{"contents":{"total":4,"page":"3","total_pages":3,"data":[` + item(4, "benefitmonkey.com") + `]}}`,
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, nextPage(pages[r.URL.Query().Get("page")]))
	}))
	defer ts.Close()

	s := New(SiteConfig{SiteID: "benefitmonkey", Domain: "benefitmonkey.com", ListPath: "scenes", OnlySiteDomain: "benefitmonkey.com"})
	s.client = ts.Client()
	got := collect(t, s, ts.URL+"/scenes")
	if len(got) != 2 || got[0].Scene.ID != "1" || got[1].Scene.ID != "4" {
		t.Fatalf("got %d scenes, want IDs 1 and 4 (a lezkey-only page must not end the walk)", len(got))
	}

	all := New(SiteConfig{SiteID: "lezkey", Domain: "lezkey.com", ListPath: "scenes"})
	all.client = ts.Client()
	if n := len(collect(t, all, ts.URL+"/scenes")); n != 4 {
		t.Errorf("unfiltered config yielded %d scenes, want 4", n)
	}
}
