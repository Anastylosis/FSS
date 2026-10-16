package ghostpro

import "testing"

func TestJohnnyRapidRow(t *testing.T) {
	var cfg SiteConfig
	for _, c := range sites {
		if c.ID == "johnnyrapid" {
			cfg = c
		}
	}
	s := New(cfg)
	for _, u := range []string{"https://johnnyrapid.com/", "https://www.johnnyrapid.com/videos?page=2"} {
		if !s.MatchesURL(u) {
			t.Errorf("MatchesURL(%q) = false", u)
		}
	}
	if s.MatchesURL("https://notjohnnyrapid.com/") {
		t.Error("matched foreign domain")
	}

	body := `<script id="__NEXT_DATA__" type="application/json">{"props":{"pageProps":{"contents":{"total":90,"page":"1","per_page":"18","total_pages":5,"data":[{"id":94,"title":"Commanding Codi","publish_date":"2026/10/15 12:00:00","videos_duration":"22:49","thumb":"https://thumbs.example/jr.jpg","tags":["Amateur","Gay Porn"],"models":["Johnny Rapid","Codi Kodak"],"content_price":15,"views":72,"description":"<p>Codi gets &amp; gives.</p>"}]}}}}</script>`
	c, err := parseListing([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if c.Total != 90 || c.TotalPages != 5 {
		t.Fatalf("total=%d pages=%d", c.Total, c.TotalPages)
	}
	sc := s.toScene(c.Data[0], "https://johnnyrapid.com", testNow())
	if sc.ID != "94" || sc.Title != "Commanding Codi" || sc.Studio != "Johnny Rapid" || sc.Series != "" {
		t.Errorf("scene = %+v", sc)
	}
	if sc.Date.Format("2006-01-02") != "2026-10-15" || sc.Duration != 1369 {
		t.Errorf("date=%v duration=%d", sc.Date, sc.Duration)
	}
	if len(sc.Performers) != 2 || sc.Performers[1] != "Codi Kodak" {
		t.Errorf("performers = %v", sc.Performers)
	}
	if sc.StudioURL != "https://johnnyrapid.com" || sc.URL != "https://johnnyrapid.com/videos#scene-94" {
		t.Errorf("urls = %q %q", sc.StudioURL, sc.URL)
	}
	if sc.Description != "Codi gets & gives." || len(sc.PriceHistory) != 1 {
		t.Errorf("desc=%q prices=%d", sc.Description, len(sc.PriceHistory))
	}
}
