package parseutil

import (
	"regexp"
	"strings"
)

var (
	ogMetaTagRe = regexp.MustCompile(`(?is)<meta\b[^>]*>`)
	// Attribute names are preceded by whitespace (or a quote) so `content=`
	// cannot match inside `data-content=`; the value is double-quoted,
	// single-quoted or bare.
	ogAttrRe = regexp.MustCompile(`(?is)(?:^|[\s"'/])([a-z][a-z0-9:_-]*)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'>]+))`)
)

// OpenGraph extracts every `<meta property="og:*" content="…">` pair
// from `body` into a map keyed by the full property name (e.g.
// `"og:title"`, `"og:image"`, `"og:video:duration"`). Attributes may appear in
// any order, values may be double-quoted, single-quoted or unquoted, the
// property may be spelled `property` or `name`, and `data-content` is not
// mistaken for `content`.
//
// The returned map values are the raw `content` attribute strings as
// they appear in the source — HTML entities are NOT decoded, and
// trailing whitespace is preserved. Callers that need an unescaped
// string should pass the value through `html.UnescapeString` (and
// `strings.TrimSpace` where relevant). The values are kept raw so the
// helper matches what scrapers were doing before extraction without
// taking on entity-handling decisions they each made differently.
//
// Repeated `og:foo` tags (e.g. multiple `og:image` entries on
// articles) collapse to the last occurrence in source order. If a
// caller needs every value, switch to FindAllStringSubmatch directly.
//
// Returns an empty (non-nil) map if no OpenGraph tags are present.
func OpenGraph(body []byte) map[string]string {
	out := make(map[string]string)
	for _, tag := range ogMetaTagRe.FindAll(body, -1) {
		var prop, content string
		var hasContent bool
		for _, m := range ogAttrRe.FindAllSubmatch(tag, -1) {
			val := firstNonEmpty(m[2], m[3], m[4])
			switch strings.ToLower(string(m[1])) {
			case "property", "name":
				if len(val) >= 3 && strings.EqualFold(val[:3], "og:") {
					prop = val
				}
			case "content":
				content, hasContent = val, true
			}
		}
		if prop != "" && hasContent {
			out[prop] = content
		}
	}
	return out
}

// firstNonEmpty returns the first non-empty submatch.
func firstNonEmpty(groups ...[]byte) string {
	for _, g := range groups {
		if len(g) > 0 {
			return string(g)
		}
	}
	return ""
}
