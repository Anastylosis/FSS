package paysiteutil

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The Ducati Studios network feed serves every sister site's scenes on a model
// page, so a row that owns one site keeps just that site's items.
func TestOnlySiteDomainFiltersModelPage(t *testing.T) {
	item := func(id int, domain string) string {
		return fmt.Sprintf(`{"id":%d,"title":"T%d","slug":"t%d","site":"S","site_domain":%q}`, id, id, id, domain)
	}
	page := nextPage(`{"model_contents":[` + item(1, "trailertrashboys.com") + `,` + item(2, "nastydaddy.com") + `]}`)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, page)
	}))
	defer ts.Close()

	s := New(SiteConfig{SiteID: "trailertrashboys", Domain: "ducatistudios.com", OnlySiteDomain: "trailertrashboys.com"})
	s.client = ts.Client()
	got := collect(t, s, ts.URL+"/models/some-model")
	if len(got) != 1 || got[0].Scene.ID != "1" {
		t.Fatalf("got %d scenes, want only ID 1", len(got))
	}
}
