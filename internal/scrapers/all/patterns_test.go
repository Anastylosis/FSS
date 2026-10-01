package all

import (
	"strings"
	"testing"

	"github.com/Anastylosis/FSS/scraper"
)

// A scraper that does not recognise a filtered URL falls through to its whole
// catalogue and stores the result under the filtered URL's key, which reads as
// a successful scrape of something else. `fss scrape` warns about that by
// checking the URL against the scraper's own Patterns(), so those patterns have
// to be honest — a pattern that does not match an instance of itself makes the
// warning fire on URLs the scraper handles perfectly well.
func TestEveryPatternMatchesItself(t *testing.T) {
	for _, s := range scraper.All() {
		for _, pat := range s.Patterns() {
			if strings.Contains(pat, "{") || strings.Contains(pat, " ") {
				continue // a placeholder or a prose note cannot be self-tested
			}
			u := pat
			if !strings.Contains(u, "://") {
				u = "https://" + u
			}
			if !scraper.URLHasNonRootPath(u) {
				continue
			}
			if scraper.URLLooksUnhandled(s, u) {
				t.Errorf("%s: advertised pattern does not match itself: %s", s.ID(), pat)
			}
		}
	}
}
