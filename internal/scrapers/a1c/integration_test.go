//go:build integration

package a1c

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveA1C(t *testing.T) {
	for name, u := range map[string]string{
		"majin":       "https://www.a1c.jp/~majin/top.html",
		"chuchu":      "http://www.a1c.jp/~chuchu/top.html",
		"collabo":     "https://www.a1c.jp/~collabo/top.html",
		"mousousenka": "https://www.a1c.jp/brandpage/mousousenka/top.html",
	} {
		t.Run(name, func(t *testing.T) { testutil.RunLiveScrape(t, New(), u, 3) })
	}
}
