package cmd

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/Anastylosis/FSS/internal/creators"
	"github.com/Anastylosis/FSS/models"
)

func studioScene(studioURL, studio string, performers ...string) models.Scene {
	return models.Scene{
		ID: studioURL + studio + strings.Join(performers, ""), SiteID: "t",
		StudioURL: studioURL, Studio: studio, Title: "t", Performers: performers,
	}
}

// repeat builds n scenes for one studio so dominance ratios are meaningful.
func repeat(n int, studioURL, studio string, performers ...string) []models.Scene {
	out := make([]models.Scene, 0, n)
	for i := 0; i < n; i++ {
		sc := studioScene(studioURL, studio, performers...)
		sc.ID = sc.ID + string(rune('a'+i%26)) + string(rune('a'+i/26))
		out = append(out, sc)
	}
	return out
}

func groupNames(groups [][]studioProfile) []string {
	var out []string
	for _, g := range groups {
		names := make([]string, 0, len(g))
		for _, p := range g {
			names = append(names, p.name)
		}
		sort.Strings(names)
		out = append(out, strings.Join(names, "+"))
	}
	sort.Strings(out)
	return out
}

func TestProfileStudiosFindsDominantPerformer(t *testing.T) {
	var scenes []models.Scene
	scenes = append(scenes, repeat(10, "https://solo.example.com", "Solo Store", "Amy Solo")...)
	// A network where no single performer reaches half the catalogue.
	scenes = append(scenes, repeat(5, "https://net.example.com", "Network", "Amy Solo")...)
	scenes = append(scenes, repeat(6, "https://net.example.com", "Network", "Bob")...)
	scenes = append(scenes, repeat(6, "https://net.example.com", "Network", "Carol")...)

	profiles := profileStudios(scenes)
	byURL := map[string]studioProfile{}
	for _, p := range profiles {
		byURL[p.url] = p
	}

	if got := byURL["https://solo.example.com"].performer; got != "Amy Solo" {
		t.Errorf("solo store dominant performer = %q, want Amy Solo", got)
	}
	if got := byURL["https://net.example.com"].performer; got != "" {
		t.Errorf("network reported a dominant performer %q; no one reaches half", got)
	}
	if got := byURL["https://net.example.com"].scenes; got != 17 {
		t.Errorf("scene count = %d, want 17", got)
	}
}

// A performer credited twice on one scene must not manufacture dominance.
func TestProfileStudiosCountsAPerformerOncePerScene(t *testing.T) {
	scenes := []models.Scene{
		studioScene("https://s.example.com", "S", "Amy", "Amy", "Amy"),
		studioScene("https://s.example.com", "S", "Bob"),
		studioScene("https://s.example.com", "S", "Carol"),
		studioScene("https://s.example.com", "S", "Dave"),
	}
	p := profileStudios(scenes)[0]
	if p.performer != "" {
		t.Errorf("dominant performer = %q; Amy is on 1 of 4 scenes", p.performer)
	}
}

func TestClusterStudiosByNameContainment(t *testing.T) {
	var scenes []models.Scene
	scenes = append(scenes, repeat(4, "https://c4s.example.com/emma-woodhouse-films", "Emma Woodhouse Films")...)
	scenes = append(scenes, repeat(4, "https://mv.example.com/emma-woodhouse", "Emma Woodhouse")...)
	scenes = append(scenes, repeat(4, "https://iwc.example.com/emmawoodhouse", "EmmaWoodhouse")...)
	scenes = append(scenes, repeat(4, "https://other.example.com", "Someone Else")...)

	got := groupNames(clusterStudios(profileStudios(scenes)))
	want := []string{"Emma Woodhouse+Emma Woodhouse Films+EmmaWoodhouse", "Someone Else"}
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Errorf("groups = %v, want %v", got, want)
	}
}

// The signal name containment cannot provide: two storefronts whose names share
// nothing, tied together by who is actually in the scenes.
func TestClusterStudiosByDominantPerformer(t *testing.T) {
	var scenes []models.Scene
	scenes = append(scenes, repeat(6, "https://iwc.example.com/duchess-mina", "Duchess Mina", "Mina Harker")...)
	scenes = append(scenes, repeat(6, "https://c4s.example.com/mina-harker", "Mina Harker", "Mina Harker")...)
	scenes = append(scenes, repeat(6, "https://other.example.com", "Unrelated", "Someone Else")...)

	got := groupNames(clusterStudios(profileStudios(scenes)))
	want := []string{"Duchess Mina+Mina Harker", "Unrelated"}
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Errorf("groups = %v, want %v", got, want)
	}
}

// A short name being a substring of a longer one is not evidence. Without the
// minNameOverlap floor, every studio whose name is a few letters would absorb
// any longer name containing them, putting one person's catalogue under
// another's name.
func TestClusterStudiosIgnoresShortNameOverlap(t *testing.T) {
	var scenes []models.Scene
	// "ivy" is genuinely contained in "ivychamberlain" — only the length floor
	// keeps these apart, so this fails if minNameOverlap is lowered below 4.
	scenes = append(scenes, repeat(4, "https://a.example.com", "Ivy", "Ivy Nolan")...)
	scenes = append(scenes, repeat(4, "https://b.example.com", "Ivy Chamberlain", "Bianca Ford")...)

	if got := len(clusterStudios(profileStudios(scenes))); got != 2 {
		t.Errorf("got %d group(s), want 2 — %q is too short to merge on", got, "ivy")
	}
}

// The counterpart: a name long enough to be evidence does merge, so the floor
// is not silently swallowing the whole signal.
func TestClusterStudiosMergesLongEnoughNameOverlap(t *testing.T) {
	var scenes []models.Scene
	scenes = append(scenes, repeat(4, "https://a.example.com", "Woodhouse", "Emma Woodhouse")...)
	scenes = append(scenes, repeat(4, "https://b.example.com", "Woodhouse Films", "Hester Prynne")...)

	if got := len(clusterStudios(profileStudios(scenes))); got != 1 {
		t.Errorf("got %d group(s), want 1 — %q is long enough to merge on", got, "woodhouse")
	}
}

func TestProposalForPrefersThePerformerName(t *testing.T) {
	var scenes []models.Scene
	// Every storefront labels itself with a brand or a handle; the person's
	// actual name only appears in the performer credits.
	scenes = append(scenes, repeat(6, "https://c4s.example.com/x", "Emma Woodhouse Films", "Emma Woodhouse")...)
	scenes = append(scenes, repeat(6, "https://iwc.example.com/x", "EmmaWoodhouse", "Emma Woodhouse")...)

	groups := clusterStudios(profileStudios(scenes))
	if len(groups) != 1 {
		t.Fatalf("got %d groups, want 1", len(groups))
	}
	c := proposalFor(groups[0])
	if c.Name != "Emma Woodhouse" {
		t.Errorf("Name = %q, want the performer name Emma Woodhouse", c.Name)
	}
	if len(c.Stores) != 2 {
		t.Errorf("Stores = %d, want 2", len(c.Stores))
	}
	if len(c.Aliases) != 0 {
		t.Errorf("Aliases = %v; store labels are covered by the studio rule and must not be listed", c.Aliases)
	}
}

// With no performer credits anywhere, the shortest label is the one least
// likely to carry a brand suffix.
func TestProposalForFallsBackToShortestName(t *testing.T) {
	var scenes []models.Scene
	scenes = append(scenes, repeat(3, "https://a.example.com", "Sonia Marek TV")...)
	scenes = append(scenes, repeat(3, "https://b.example.com", "Sonia Marek")...)

	groups := clusterStudios(profileStudios(scenes))
	if len(groups) != 1 {
		t.Fatalf("got %d groups, want 1", len(groups))
	}
	if c := proposalFor(groups[0]); c.Name != "Sonia Marek" {
		t.Errorf("Name = %q, want Sonia Marek", c.Name)
	}
}

func TestMostCommonIsDeterministic(t *testing.T) {
	// A tie must not depend on map iteration order, or `suggest` proposes a
	// different name on every run.
	counts := map[string]int{"Bravo": 3, "Alpha": 3, "Delta": 1}
	for i := 0; i < 50; i++ {
		if got, n := mostCommon(counts); got != "Alpha" || n != 3 {
			t.Fatalf("mostCommon = %q/%d, want Alpha/3", got, n)
		}
	}
	if got, n := mostCommon(map[string]int{}); got != "" || n != 0 {
		t.Errorf("mostCommon of an empty map = %q/%d", got, n)
	}
}

func TestSuggestDoesNotChainThroughACoStar(t *testing.T) {
	var scenes []models.Scene
	scenes = append(scenes, repeat(4, "https://a.example.com/jane", "Jane Eyre", "Jane Eyre")...)
	scenes = append(scenes, repeat(4, "https://b.example.com/polly", "Polly Peachum", "Polly Peachum", "Jane Eyre")...)

	groups := clusterStudios(profileStudios(scenes))
	if len(groups) != 2 {
		t.Fatalf("got %d groups, want 2: Polly's own store credits Polly as dominant", len(groups))
	}
	var all []studioProfile
	for _, g := range groups {
		all = append(all, g...)
	}
	if c := proposalFor(all); len(c.Aliases) != 0 {
		t.Errorf("Aliases = %v, want none", c.Aliases)
	}
}

func TestDominantPerformerNeedsMinimumScenesAndCountsByKey(t *testing.T) {
	one := profileStudios([]models.Scene{studioScene("https://s.example.com", "S", "Guest")})
	if one[0].performer != "" {
		t.Errorf("a one-scene store made %q dominant", one[0].performer)
	}
	scenes := []models.Scene{
		studioScene("https://s.example.com", "S", "Jane Eyre"),
		studioScene("https://s.example.com", "S", "jane eyre "),
		studioScene("https://s.example.com", "S", "JANE EYRE"),
		studioScene("https://s.example.com", "S", "Other"),
	}
	scenes[1].ID, scenes[2].ID = "x", "y"
	if got := profileStudios(scenes)[0].performer; got == "" {
		t.Errorf("spelling variants of one performer were counted separately")
	}
}

func TestProposalForKeepsOnlyResidueSpellings(t *testing.T) {
	var scenes []models.Scene
	scenes = append(scenes, repeat(4, "https://a.example.com/x", "Mina Harker", "Mina Harker")...)
	scenes = append(scenes, repeat(4, "https://b.example.com/x", "Mina Harker Shop", "Mina H")...)
	c := proposalFor(clusterStudios(profileStudios(scenes))[0])
	if c.Name != "Mina H" && c.Name != "Mina Harker" {
		t.Fatalf("Name = %q", c.Name)
	}
	for _, a := range c.Aliases {
		if creators.Key(a) == creators.Key("Mina Harker Shop") {
			t.Errorf("store label %q listed as alias", a)
		}
	}
}

func TestUnnamedStudiosDoNotMergeOnURL(t *testing.T) {
	var scenes []models.Scene
	scenes = append(scenes, repeat(2, "https://www.clips4sale.com/studio/123", "")...)
	scenes = append(scenes, repeat(2, "https://www.clips4sale.com/studio/12345", "")...)
	if got := len(clusterStudios(profileStudios(scenes))); got != 2 {
		t.Errorf("got %d groups, want 2 nameless studios kept apart", got)
	}
}

func TestClusterStudiosOrderIsDeterministic(t *testing.T) {
	var scenes []models.Scene
	for _, u := range []string{"c", "a", "d", "b"} {
		scenes = append(scenes, repeat(3, "https://"+u+".example.com", "Store "+u, "P"+u)...)
	}
	for i := 0; i < 20; i++ {
		var ordered []string
		for _, g := range clusterStudios(profileStudios(scenes)) {
			ordered = append(ordered, g[0].url)
		}
		if !sort.StringsAreSorted(ordered) {
			t.Fatalf("clusters not ordered: %v", ordered)
		}
	}
}

func TestWriteProposalsSkipsCollidingKeys(t *testing.T) {
	dir := t.TempDir()
	existing := "name: Jane Eyre\nstores:\n  - url: https://a.example.com\n"
	if err := os.WriteFile(filepath.Join(dir, "jane-eyre.yaml"), []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := &cobra.Command{}
	cmd.Flags().String("creators-dir", dir, "")
	cmd.Flags().Bool("force", false, "")
	proposals := []creators.Creator{
		{Name: "JaneEyre", Stores: []creators.Store{{URL: "https://b.example.com"}}},
		{Name: "Polly Peachum", Stores: []creators.Store{{URL: "https://c.example.com"}}},
		{Name: "Other", Aliases: []string{"Polly Peachum"}, Stores: []creators.Store{{URL: "https://d.example.com"}}},
	}
	if err := writeProposals(cmd, proposals); err != nil {
		t.Fatal(err)
	}
	if _, err := creators.Load(dir); err != nil {
		t.Fatalf("directory no longer loads: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "janeeyre.yaml")); err == nil {
		t.Error("colliding proposal was written")
	}
	if _, err := os.Stat(filepath.Join(dir, "other.yaml")); err == nil {
		t.Error("proposal colliding within the batch was written")
	}
	if _, err := os.Stat(filepath.Join(dir, "polly-peachum.yaml")); err != nil {
		t.Error("non-colliding proposal was not written")
	}
}
