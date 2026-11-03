//go:build integration

package vipsexvault

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveScrape(t *testing.T) {
	for _, u := range []string{
		"https://vipsexvault.com/",
		"https://vipsexvault.com/channels/fucked-in-traffic.en.html",
	} {
		t.Run(u, func(t *testing.T) { testutil.RunLiveScrape(t, New(), u, 3) })
	}
}
