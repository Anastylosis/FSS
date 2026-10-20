package cmd

import (
	"sort"
	"strings"
	"testing"

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
	// Storefront spellings become aliases so --creator matches whichever the
	// operator remembers — but only those that add matching power. "EmmaWoodhouse"
	// keys the same as the name "Emma Woodhouse" and would be dead weight.
	sort.Strings(c.Aliases)
	if strings.Join(c.Aliases, ",") != "Emma Woodhouse Films" {
		t.Errorf("Aliases = %v, want just the spelling that keys differently", c.Aliases)
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
