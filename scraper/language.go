package scraper

import (
	"fmt"
	"regexp"
	"strings"
)

// MultiLingual is implemented by scrapers whose site serves the same catalogue
// in more than one language. See docs/usage.md.
type MultiLingual interface {
	// Languages returns the tags this scraper accepts, lowercase, default first.
	Languages() []string
}

var langTagRe = regexp.MustCompile(`^[a-z]{2,3}(?:-[a-z0-9]{2,8})*$`)

// NormalizeLanguage lowercases and shape-checks a language tag. An empty input
// returns an empty tag and no error, meaning "the scraper's own default".
func NormalizeLanguage(s string) (string, error) {
	tag := strings.ToLower(strings.TrimSpace(s))
	if tag == "" {
		return "", nil
	}
	if !langTagRe.MatchString(tag) {
		return "", fmt.Errorf("%q is not a language tag — use a code like `de` or `pt-br`", s)
	}
	return tag, nil
}

// LanguagesFor returns the languages sc accepts, or nil if it has no language
// dimension.
func LanguagesFor(sc StudioScraper) []string {
	ml, ok := sc.(MultiLingual)
	if !ok {
		return nil
	}
	return ml.Languages()
}

// ResolveLanguage decides which language tag a run should ask sc for; an empty
// result means the scraper's own default. See docs/usage.md.
func ResolveLanguage(sc StudioScraper, requested string) (string, error) {
	tag, err := NormalizeLanguage(requested)
	if err != nil {
		return "", err
	}
	if tag == "" {
		return "", nil
	}
	langs := LanguagesFor(sc)
	if len(langs) == 0 {
		Debugf(1, "scraper: %s, ignoring requested language %q (site serves one language)", sc.ID(), tag)
		return "", nil
	}
	for _, l := range langs {
		if l == tag {
			return tag, nil
		}
	}
	return "", fmt.Errorf("%s does not serve %q; it has: %s", sc.ID(), tag, strings.Join(langs, ", "))
}
