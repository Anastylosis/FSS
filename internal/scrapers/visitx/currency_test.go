package visitx

import (
	"testing"
	"time"
)

func TestToSceneRecordsCurrency(t *testing.T) {
	v := gqlVideo{ID: 1, Slug: "1-x", Price: &gqlPrice{Value: 15, Currency: "VXC"}, BasePrice: &gqlPrice{Value: 20, Currency: "VXC"}}
	sc := toScene(v, "https://www.visit-x.net/en/amateur/t/videos/", time.Now())
	if len(sc.PriceHistory) != 1 || sc.PriceHistory[0].Currency != "VXC" {
		t.Errorf("PriceHistory = %+v, want Currency VXC", sc.PriceHistory)
	}
}
