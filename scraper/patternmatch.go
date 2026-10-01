package scraper

import (
	"net/url"
	"strings"
)

// PatternMatchesURL reports whether rawURL is an instance of one of the display
// patterns a scraper advertises from Patterns().
//
// A pattern is a host followed by an optional path and query, with `{name}`
// standing for one path segment or one query value — `site.com/models/{slug}`,
// `site.com/videos?website={id}`. Matching is on shape, not content: the host
// must be the same (ignoring a leading `www.`), the path must have the same
// number of segments with the literal ones equal case-insensitively, and every
// query key the pattern names must be present. A pattern segment mixing
// literals and placeholders (`{slug}_{page}_d.html`) matches any single
// segment with the same literal parts around them.
func PatternMatchesURL(pattern, rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return false
	}

	patURL := pattern
	if !strings.Contains(patURL, "://") {
		patURL = "https://" + patURL
	}
	p, err := url.Parse(patURL)
	if err != nil || p.Host == "" {
		return false
	}

	if !hostMatches(p.Hostname(), u.Hostname()) {
		return false
	}

	if !segmentsMatch(splitPath(p.Path), splitPath(u.Path)) {
		return false
	}

	return queryMatches(p.Query(), u.Query())
}

// queryMatches checks every key the pattern names against the URL's query. A
// key may itself carry a placeholder — some CMSes put the filter in the key
// rather than the value (`?w_{actress}`) — and a placeholder value matches
// anything, while a literal value must be equal.
func queryMatches(pat, got url.Values) bool {
	for key, want := range pat {
		have, ok := got[key]
		if !ok && hasPlaceholder(key) {
			for gk, gv := range got {
				if segmentMatches(key, gk) {
					have, ok = gv, true
					break
				}
			}
		}
		if !ok {
			return false
		}
		if len(want) > 0 && want[0] != "" && !hasPlaceholder(want[0]) &&
			(len(have) == 0 || have[0] != want[0]) {
			return false
		}
	}
	return true
}

// hostMatches compares a pattern host with a URL host. A tour often lives on a
// subdomain of the domain the pattern names (`tour.girlsoutwest.com`,
// `newtour.belamionline.com`, `megasite.meanworld.com`), and the scraper's own
// MatchesURL has already vouched that the URL belongs to it — so the host is
// only being checked for shape here, and a subdomain of the pattern's host
// counts as the same site.
func hostMatches(pattern, got string) bool {
	pattern, got = strings.ToLower(pattern), strings.ToLower(got)
	pattern = strings.TrimPrefix(pattern, "www.")
	got = strings.TrimPrefix(got, "www.")
	return got == pattern || strings.HasSuffix(got, "."+pattern)
}

func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

func segmentsMatch(pat, got []string) bool {
	if len(pat) != len(got) {
		return false
	}
	for i := range pat {
		if !segmentMatches(pat[i], got[i]) {
			return false
		}
	}
	return true
}

// segmentMatches compares one path segment. A segment with no placeholder must
// be equal; one with placeholders must share every literal run around them, in
// order.
func segmentMatches(pat, got string) bool {
	if !hasPlaceholder(pat) {
		return strings.EqualFold(pat, got)
	}
	got = strings.ToLower(got)
	rest := got
	first := true
	for _, lit := range placeholderLiterals(pat) {
		if lit == "" {
			first = false
			continue
		}
		lit = strings.ToLower(lit)
		idx := strings.Index(rest, lit)
		if idx < 0 || (first && idx != 0) {
			return false
		}
		rest = rest[idx+len(lit):]
		first = false
	}
	return true
}

func hasPlaceholder(s string) bool {
	return strings.Contains(s, "{") && strings.Contains(s, "}")
}

// placeholderLiterals splits a segment on its `{...}` placeholders, returning
// the literal runs between them (including leading and trailing ones).
func placeholderLiterals(pat string) []string {
	var lits []string
	for {
		open := strings.Index(pat, "{")
		if open < 0 {
			lits = append(lits, pat)
			return lits
		}
		shut := strings.Index(pat[open:], "}")
		if shut < 0 {
			lits = append(lits, pat)
			return lits
		}
		lits = append(lits, pat[:open])
		pat = pat[open+shut+1:]
	}
}

// URLLooksUnhandled reports whether rawURL names a path or query the scraper
// does not advertise handling.
//
// A scraper that does not recognise a filtered URL falls through to its full
// catalogue and stores the result under the filtered URL's key, which looks
// like a successful scrape of something it is not. There is no way to ask a
// scraper what it recognised, but Patterns() is what it claims to accept, so a
// non-root URL matching none of the non-root patterns is the signal. It is
// advisory: a scraper may handle a form it never advertised, which is itself
// worth knowing.
func URLLooksUnhandled(s StudioScraper, rawURL string) bool {
	if !URLHasNonRootPath(rawURL) {
		return false
	}
	for _, pat := range s.Patterns() {
		if !patternHasNonRootPath(pat) {
			continue
		}
		if PatternMatchesURL(pat, rawURL) {
			return false
		}
	}
	return true
}

func patternHasNonRootPath(pattern string) bool {
	if !strings.Contains(pattern, "://") {
		pattern = "https://" + pattern
	}
	return URLHasNonRootPath(pattern)
}
