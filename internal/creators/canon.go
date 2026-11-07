package creators

import (
	"github.com/Anastylosis/FSS/output"
)

// Canon rewrites the performer names a storefront publishes into the spelling
// its creator is filed under.
//
// Storefronts routinely credit their own branding as the performer: a shop
// trading as "<Name> Films" lists "<Name> Films" as the performer on every
// scene. Stored verbatim, one person becomes several performers, and every
// cross-store view
// — `fss compare`, a Stash import, and `suggest`'s own shared-performer signal
// — fragments along with them.
//
// A credit is branding when it matches either of two things, both already
// known without any extra configuration:
//
//   - the scene's own Studio value. A storefront that credits itself as the
//     performer says so in both fields at once, and on a creator's store the
//     storefront is the creator. This is the dominant case by a wide margin,
//     and it needs nothing written down.
//   - one of the creator's declared aliases. The escape hatch for the residue,
//     where a store spells its own name one way in the studio field and
//     another in the credit.
//
// Everything is scoped to the stores a creator file lists, which is what makes
// it safe: a co-star is never touched, however much their name resembles the
// creator's, because neither rule can fire on a name that is neither the shop
// nor a spelling of it.
//
// It deliberately holds no fuzzy matching. Deciding that an unfamiliar name is
// really this creator needs evidence a single scene does not carry — which
// storefronts credit it, and nothing else — so that judgement lives in
// `fss creators suggest`, which has the whole library to reason over and writes
// its conclusion to the file for review.
type Canon struct {
	byStore   map[string]StoreCanon
	normalize func(string) string
}

// StoreCanon is the rewrite for one storefront. The zero value is valid and
// rewrites nothing, so a caller with no creators defined needs no special case.
type StoreCanon struct {
	name string
	keys map[string]struct{}
}

// NewCanon builds the rewrite table from loaded creator files. normalize maps a
// store URL to the key it is looked up under (nil means CanonicalStudioURL); the
// caller passes the same function the scrape targets are resolved with, so a
// vanity-alias store is found under the spelling that is actually scraped.
//
// Every store of every creator gets an entry, including creators that declare
// no aliases at all: the studio-match rule needs only the name and the links a
// file already carries, so the common case costs the operator nothing to
// configure.
//
// A store listed under two creators is left out entirely rather than assigned
// to whichever file loaded first: Load already warns about the duplicate, and
// silently relabelling a shared storefront's performers to one of the two names
// would be a guess dressed as a fact.
func NewCanon(list []Creator, normalize func(string) string) Canon {
	if normalize == nil {
		normalize = output.CanonicalStudioURL
	}
	claims := map[string]int{}
	for _, c := range list {
		for _, u := range c.URLs() {
			claims[normalize(u)]++
		}
	}

	byStore := map[string]StoreCanon{}
	for _, c := range list {
		keys := map[string]struct{}{}
		for _, k := range c.Keys() {
			keys[k] = struct{}{}
		}
		for _, u := range c.URLs() {
			key := normalize(u)
			if claims[key] > 1 {
				continue
			}
			byStore[key] = StoreCanon{name: c.Name, keys: keys}
		}
	}
	if len(byStore) == 0 {
		return Canon{}
	}
	return Canon{byStore: byStore, normalize: normalize}
}

// For returns the rewrite that applies to one storefront. The result is safe to
// use whether or not the URL belongs to a creator.
func (c Canon) For(studioURL string) StoreCanon {
	if c.byStore == nil {
		return StoreCanon{}
	}
	return c.byStore[c.normalize(studioURL)]
}

// Active reports whether this storefront has any rewrite to apply.
func (s StoreCanon) Active() bool { return s.name != "" && len(s.keys) > 0 }

// Name is the creator's canonical spelling.
func (s StoreCanon) Name() string { return s.name }

// Apply rewrites one scene's performer list, given the studio that scene was
// published under.
//
// A credit becomes the creator's name when it matches the scene's own studio,
// or one of the creator's declared spellings. Everything else passes through
// untouched — the co-stars on a scene are not this function's business.
//
// The result is deduplicated by key, because a scene crediting the shop and the
// person separately names one performer and must not be stored as two.
//
// The input slice is never modified.
func (s StoreCanon) Apply(studio string, names []string) []string {
	if !s.Active() || len(names) == 0 {
		return names
	}
	// An empty studio keys to the empty string, which would then match every
	// credit that has no letters or digits.
	studioKey := Key(studio)

	out := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, n := range names {
		k := Key(n)
		if k == "" {
			// No letters or digits to key on — keep it verbatim rather than
			// dropping something the site did publish.
			out = append(out, n)
			continue
		}
		if _, declared := s.keys[k]; declared || k == studioKey {
			n, k = s.name, Key(s.name)
		}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, n)
	}
	return out
}
