package creators

import (
	"strings"
	"testing"
)

// emmaWoodhouse has one store whose branding differs from her name only in the
// studio field, and one that spells it differently again — the two cases the
// two rules cover.
func emmaWoodhouse() Creator {
	return Creator{
		Name: "Emma Woodhouse",
		Stores: []Store{
			{URL: "https://clipmarket.example/studio/4021/emma-woodhouse"},
			{URL: "https://fanhub.example/emmawoodhousefilms"},
		},
	}
}

const (
	clipStore = "https://clipmarket.example/studio/4021/emma-woodhouse"
	fanStore  = "https://fanhub.example/emmawoodhousefilms"
)

// The rule that needs nothing configured: a storefront crediting itself as the
// performer says so in the studio field too.
func TestCanonRewritesACreditMatchingTheScenesOwnStudio(t *testing.T) {
	c := NewCanon([]Creator{emmaWoodhouse()})
	sc := c.For(fanStore)
	if !sc.Active() {
		t.Fatal("a listed store got no rewrite")
	}
	got := sc.Apply("Emma Woodhouse Films", []string{"Emma Woodhouse Films"})
	if strings.Join(got, ",") != "Emma Woodhouse" {
		t.Errorf("Apply = %v, want [Emma Woodhouse]", got)
	}
}

// No aliases anywhere in this creator file. The studio rule has to carry it,
// or every operator is back to hand-writing what FSS already knows.
func TestCanonNeedsNoAliases(t *testing.T) {
	c := NewCanon([]Creator{emmaWoodhouse()})
	if len(emmaWoodhouse().Aliases) != 0 {
		t.Fatal("fixture is supposed to declare no aliases")
	}
	got := c.For(clipStore).Apply("EmmaWoodhouse", []string{"EmmaWoodhouse", "Hester Prynne"})
	if strings.Join(got, ",") != "Emma Woodhouse,Hester Prynne" {
		t.Errorf("Apply = %v, want [Emma Woodhouse Hester Prynne]", got)
	}
}

// The residue the studio rule cannot reach: the store spells its own name one
// way in the studio field and another in the credit. That is what an alias is
// for, and all an alias is for.
func TestCanonAliasCoversAStudioMismatch(t *testing.T) {
	c := NewCanon([]Creator{{
		Name:    "Jane Eyre",
		Aliases: []string{"JaneEyre"},
		Stores:  []Store{{URL: "https://clipmarket.example/studio/77/jane-eyre-1"}},
	}})
	sc := c.For("https://clipmarket.example/studio/77/jane-eyre-1")
	// The studio field says "Jane Eyre 1"; the credit says "JaneEyre".
	// Neither matches the other, so only the alias resolves it.
	got := sc.Apply("Jane Eyre 1", []string{"JaneEyre"})
	if strings.Join(got, ",") != "Jane Eyre" {
		t.Errorf("Apply = %v, want [Jane Eyre]", got)
	}
}

// The limit that makes this safe to leave on: a name that is neither the shop
// nor a declared spelling of it is somebody else, and stays as published.
func TestCanonLeavesCoStarsAlone(t *testing.T) {
	c := NewCanon([]Creator{emmaWoodhouse()})
	in := []string{"Emma Woodhouse Films", "Hester Prynne", "Emma", "Woodhouse Bishop"}
	got := c.For(fanStore).Apply("Emma Woodhouse Films", in)
	want := "Emma Woodhouse,Hester Prynne,Emma,Woodhouse Bishop"
	if strings.Join(got, ",") != want {
		t.Errorf("Apply = %v, want %s", got, want)
	}
	if strings.Join(in, ",") != "Emma Woodhouse Films,Hester Prynne,Emma,Woodhouse Bishop" {
		t.Errorf("Apply mutated its input: %v", in)
	}
}

// A name that means the creator on their own store means nothing on anyone
// else's, where it may well be a different person.
func TestCanonIgnoresStoresTheCreatorDoesNotList(t *testing.T) {
	c := NewCanon([]Creator{emmaWoodhouse()})
	other := "https://fanhub.example/somebody-else"
	if c.For(other).Active() {
		t.Fatal("an unlisted store got a rewrite")
	}
	got := c.For(other).Apply("Emma Woodhouse Films", []string{"Emma Woodhouse Films"})
	if strings.Join(got, ",") != "Emma Woodhouse Films" {
		t.Errorf("Apply = %v, want the credit untouched", got)
	}
}

// A scene crediting the shop and the person separately names one performer.
func TestCanonCollapsesTheShopAndThePerson(t *testing.T) {
	c := NewCanon([]Creator{emmaWoodhouse()})
	got := c.For(fanStore).Apply("Emma Woodhouse Films", []string{"Emma Woodhouse", "Emma Woodhouse Films", "Hester Prynne"})
	if strings.Join(got, ",") != "Emma Woodhouse,Hester Prynne" {
		t.Errorf("Apply = %v, want [Emma Woodhouse Hester Prynne]", got)
	}
}

// A scraper that leaves Studio empty must not turn the studio rule into a
// match-anything: the empty key would otherwise catch every unkeyable credit.
func TestCanonWithNoStudioRewritesOnlyDeclaredNames(t *testing.T) {
	c := NewCanon([]Creator{{
		Name:    "Emma Woodhouse",
		Aliases: []string{"EmmaWoodhouse"},
		Stores:  []Store{{URL: fanStore}},
	}})
	got := c.For(fanStore).Apply("", []string{"???", "EmmaWoodhouse", "Hester Prynne"})
	if strings.Join(got, "|") != "???|Emma Woodhouse|Hester Prynne" {
		t.Errorf("Apply = %v, want the unkeyable credit kept and only the alias rewritten", got)
	}
}

func TestCanonMatchesStoreURLsCanonically(t *testing.T) {
	c := NewCanon([]Creator{emmaWoodhouse()})
	// The store layer keys on the canonical form, so this has to agree with it
	// or a store is recognised in one place and not the other.
	for _, u := range []string{
		"http://fanhub.example/emmawoodhousefilms",
		"https://fanhub.example/emmawoodhousefilms/",
		"https://FanHub.EXAMPLE/emmawoodhousefilms",
	} {
		if !c.For(u).Active() {
			t.Errorf("%s was not recognised as a listed store", u)
		}
	}
}

// A storefront claimed by two creator files cannot be relabelled to either name
// without guessing. Load already warns; the rewrite simply declines.
func TestCanonDeclinesAStoreClaimedByTwoCreators(t *testing.T) {
	shared := "https://fanhub.example/shared-shop"
	c := NewCanon([]Creator{
		{Name: "Hester Prynne", Stores: []Store{{URL: shared}}},
		{Name: "Jane Eyre", Stores: []Store{{URL: shared}}},
	})
	if c.For(shared).Active() {
		t.Error("a doubly-claimed store got a rewrite; that is a guess, not a fact")
	}
}

// The zero Canon is what every operator with no creators.d has.
func TestZeroCanonRewritesNothing(t *testing.T) {
	var c Canon
	sc := c.For("https://fanhub.example/anything")
	if sc.Active() {
		t.Fatal("zero Canon reported an active rewrite")
	}
	in := []string{"Hester Prynne", ""}
	got := sc.Apply("Hester Prynne", in)
	if len(got) != 2 || got[0] != "Hester Prynne" || got[1] != "" {
		t.Errorf("Apply = %v, want the input unchanged", got)
	}
	if NewCanon(nil).For("https://fanhub.example/anything").Active() {
		t.Error("NewCanon(nil) produced an active rewrite")
	}
}
