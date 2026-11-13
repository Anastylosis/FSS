package selfupdate

import (
	"regexp"
	"strconv"
	"strings"
)

// CompareVersions orders two release versions by semver precedence, returning
// -1, 0 or 1. ok is false when either side is not a version (a "dev" build),
// in which case nothing may be inferred about their order.
func CompareVersions(a, b string) (cmp int, ok bool) {
	ca, pa, oka := splitVersion(a)
	cb, pb, okb := splitVersion(b)
	if !oka || !okb {
		return 0, false
	}
	for i := range ca {
		if ca[i] != cb[i] {
			if ca[i] < cb[i] {
				return -1, true
			}
			return 1, true
		}
	}
	if da, db := isDescribe(pa), isDescribe(pb); da != db {
		if da {
			return 1, true
		}
		return -1, true
	}
	return comparePre(pa, pb), true
}

var describeSuffix = regexp.MustCompile(`^(\d+-g[0-9a-f]+)?(-?dirty)?$`)

// isDescribe recognises a `git describe` suffix (-N-gHASH, -dirty): a build
// made after the tag, not a pre-release of it.
func isDescribe(pre []string) bool {
	s := strings.Join(pre, ".")
	s = strings.ReplaceAll(s, ".", "-")
	return len(pre) > 0 && describeSuffix.MatchString(s)
}

// ValidTag reports whether a release tag is a plain version string.
func ValidTag(tag string) bool {
	if !tagCharset.MatchString(tag) {
		return false
	}
	_, _, ok := splitVersion(tag)
	return ok
}

var tagCharset = regexp.MustCompile(`^[0-9A-Za-z.+v-]+$`)

func splitVersion(v string) (core [3]int, pre []string, ok bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	if i := strings.IndexByte(v, '-'); i >= 0 {
		pre = strings.Split(v[i+1:], ".")
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return core, nil, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return core, nil, false
		}
		core[i] = n
	}
	return core, pre, true
}

// comparePre follows semver: a release outranks any of its pre-releases, and
// identifiers compare numerically when both are numbers, else lexically, with
// numbers below text and a shorter list below a longer one it prefixes.
func comparePre(a, b []string) int {
	switch {
	case len(a) == 0 && len(b) == 0:
		return 0
	case len(a) == 0:
		return 1
	case len(b) == 0:
		return -1
	}
	for i := 0; i < len(a) && i < len(b); i++ {
		na, ea := strconv.Atoi(a[i])
		nb, eb := strconv.Atoi(b[i])
		switch {
		case ea == nil && eb == nil && na != nb:
			if na < nb {
				return -1
			}
			return 1
		case ea == nil && eb != nil:
			return -1
		case ea != nil && eb == nil:
			return 1
		case ea != nil && eb != nil && a[i] != b[i]:
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}
