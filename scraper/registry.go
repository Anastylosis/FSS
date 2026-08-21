package scraper

import "fmt"

var registered []StudioScraper

// Register adds a scraper to the global registry.
// Call this from an init() function in each scraper package.
// Panics if a scraper with the same ID is already registered.
func Register(s StudioScraper) {
	for _, existing := range registered {
		if existing.ID() == s.ID() {
			panic(fmt.Sprintf("duplicate scraper ID: %s", s.ID()))
		}
	}
	registered = append(registered, s)
}

// All returns a copy of every registered scraper.
func All() []StudioScraper {
	out := make([]StudioScraper, len(registered))
	copy(out, registered)
	return out
}

// ForID returns the registered scraper with the given ID,
// or an error if none match.
func ForID(id string) (StudioScraper, error) {
	for _, s := range registered {
		if s.ID() == id {
			return s, nil
		}
	}
	return nil, fmt.Errorf("no scraper registered with ID: %s", id)
}

// ForURL returns the first registered scraper that matches the given URL,
// or an error if none match. Resolution is first-match-wins by registration
// (import) order. When more than one scraper matches — e.g. a broad parent
// regex shadowing a sub-site — the extra matches are reported at debug level
// so the overlap is visible without changing which scraper is chosen.
func ForURL(url string) (StudioScraper, error) {
	var chosen StudioScraper
	var others []string
	for _, s := range registered {
		if s.MatchesURL(url) {
			if chosen == nil {
				chosen = s
			} else {
				others = append(others, s.ID())
			}
		}
	}
	if chosen == nil {
		return nil, fmt.Errorf("no scraper registered for URL: %s", url)
	}
	if len(others) > 0 {
		Debugf(1, "registry: %q matched %d scrapers; using %q, also matched by %v",
			url, len(others)+1, chosen.ID(), others)
	}
	return chosen, nil
}

// StudioURLCanonicalizer is implemented by a scraper whose site serves one
// studio at more than one URL. It is optional: a scraper that does not
// implement it keeps the operator's URL verbatim.
//
// This is distinct from output.CanonicalStudioURL, which normalises scheme and
// host generically and never touches the path. Rewriting a path is only safe
// when the scraper asserts the two forms are the same studio.
type StudioURLCanonicalizer interface {
	// PreferredStudioURL returns the spelling to store studioURL under, or
	// the empty string to keep it as given.
	PreferredStudioURL(studioURL string) string
}

// PreferredStudioURL asks the scraper matching studioURL which spelling of it
// to use as the studio's key, so two URLs for one studio do not become two
// studios. A URL no scraper claims, or one whose scraper expresses no
// preference, is returned unchanged.
func PreferredStudioURL(studioURL string) string {
	s, err := ForURL(studioURL)
	if err != nil {
		return studioURL
	}
	c, ok := s.(StudioURLCanonicalizer)
	if !ok {
		return studioURL
	}
	if preferred := c.PreferredStudioURL(studioURL); preferred != "" {
		return preferred
	}
	return studioURL
}
