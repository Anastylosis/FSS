//go:build integration

package maverickmen

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLiveMaverickMen(t *testing.T) {
	testutil.RunLiveScrape(t, New(cfgFor("maverickmen")), "https://www.maverickmen.com", 3)
}

func TestLiveMaverickMenDirects(t *testing.T) {
	testutil.RunLiveScrape(t, New(cfgFor("maverickmendirects")), "https://www.maverickmendirects.com", 3)
}
