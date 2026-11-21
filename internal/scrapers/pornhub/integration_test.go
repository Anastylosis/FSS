//go:build integration

package pornhub

import (
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

func TestLivePornhub(t *testing.T) {
	for name, u := range map[string]string{
		"pornstar": "https://www.pornhub.com/pornstar/coco-vandi",
		"channel":  "https://de.pornhub.com/channels/scott-ward",
		"model":    "https://fr.pornhub.com/model/kern-ostrova",
		"org":      "https://www.pornhub.org/pornstar/daisy-taylor",
	} {
		t.Run(name, func(t *testing.T) { testutil.RunLiveScrape(t, New(), u, 2) })
	}
}
