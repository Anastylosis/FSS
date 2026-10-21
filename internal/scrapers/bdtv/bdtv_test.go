package bdtv

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Anastylosis/FSS/internal/scrapers/bdtvutil"
	"github.com/Anastylosis/FSS/scraper"
)

func TestSiteTable(t *testing.T) {
	ids, studios := map[string]bool{}, map[int]string{}
	for _, c := range sites {
		if c.SiteID == "" || c.StudioName == "" || c.StudioID == 0 {
			t.Errorf("incomplete row: %+v", c)
		}
		if !strings.HasPrefix(c.Base, "https://") || strings.HasSuffix(c.Base, "/") {
			t.Errorf("%s: bad Base %q", c.SiteID, c.Base)
		}
		if ids[c.SiteID] {
			t.Errorf("duplicate SiteID %s", c.SiteID)
		}
		ids[c.SiteID] = true
		if prev, dup := studios[c.StudioID]; dup {
			t.Errorf("%s and %s share studio id %d", prev, c.SiteID, c.StudioID)
		}
		studios[c.StudioID] = c.SiteID
	}
}

// Every row's own unfiltered URL must reach that row alone, or a scrape is
// stored under another studio.
func TestRowsClaimDistinctURLs(t *testing.T) {
	for _, c := range sites {
		s := bdtvutil.New(c)
		for _, d := range c.Domains {
			if !s.MatchesURL("https://" + d + "/") {
				t.Errorf("%s does not match its own domain %s", c.SiteID, d)
			}
			for _, o := range sites {
				if o.SiteID != c.SiteID && bdtvutil.New(o).MatchesURL("https://"+d+"/") {
					t.Errorf("%s also claims %s", o.SiteID, d)
				}
			}
		}
	}
}

func TestEveryRowRegistered(t *testing.T) {
	for _, c := range sites {
		if _, err := scraper.ForID(c.SiteID); err != nil {
			t.Errorf("ForID(%q): %v", c.SiteID, err)
		}
	}
}

// A row without a domain is reachable by its studio id alone.
func TestDomainlessRowMatchesStudioURL(t *testing.T) {
	for _, c := range sites {
		if len(c.Domains) > 0 {
			continue
		}
		s := bdtvutil.New(c)
		u := fmt.Sprintf("https://bdtv.com/studio-display?studio_id=%d", c.StudioID)
		if !s.MatchesURL(u) {
			t.Errorf("%s does not match %s", c.SiteID, u)
		}
		if s.MatchesURL("https://bdtv.com/") {
			t.Errorf("%s claims the bare bdtv.com", c.SiteID)
		}
	}
}
