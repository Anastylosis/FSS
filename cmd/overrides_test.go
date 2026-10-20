package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/Anastylosis/FSS/internal/creators"
	"github.com/Anastylosis/FSS/models"
	"github.com/Anastylosis/FSS/scraper"
	"github.com/spf13/cobra"
)

func overrideCmd(t *testing.T, args ...string) *cobra.Command {
	t.Helper()
	c := &cobra.Command{Use: "scrape", RunE: func(*cobra.Command, []string) error { return nil }}
	c.Flags().StringArray("performer", nil, "")
	c.Flags().String("studio", "", "")
	c.SetArgs(args)
	c.SetOut(nil)
	if err := c.Execute(); err != nil {
		t.Fatalf("parsing %v: %v", args, err)
	}
	return c
}

func TestParseOverridesRepeatedAndCommaSeparated(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{"repeated flag", []string{"--performer", "Jodi West", "--performer", "Marcello Bravo"},
			[]string{"Jodi West", "Marcello Bravo"}},
		// The form people reach for first; storing it as one two-person name
		// would be worse than splitting it.
		{"comma separated", []string{"--performer", "Jodi West, Marcello Bravo"},
			[]string{"Jodi West", "Marcello Bravo"}},
		{"mixed with padding", []string{"--performer", " Jodi West ,, ", "--performer", "Red"},
			[]string{"Jodi West", "Red"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o, err := parseOverrides(overrideCmd(t, tt.args...))
			if err != nil {
				t.Fatalf("parseOverrides: %v", err)
			}
			if strings.Join(o.performers, "|") != strings.Join(tt.want, "|") {
				t.Errorf("performers = %v, want %v", o.performers, tt.want)
			}
		})
	}
}

func TestParseOverridesEmpty(t *testing.T) {
	o, err := parseOverrides(overrideCmd(t))
	if err != nil {
		t.Fatalf("parseOverrides: %v", err)
	}
	if !o.empty() {
		t.Errorf("no flags given but overrides = %+v, want empty", o)
	}
}

// A flag passed with nothing usable is rejected rather than ignored: silently
// scraping without the relabel the operator asked for is the worse outcome.
func TestParseOverridesRejectsBlankValues(t *testing.T) {
	for _, args := range [][]string{
		{"--performer", "  "},
		{"--performer", " , ,"},
		{"--studio", "   "},
	} {
		if _, err := parseOverrides(overrideCmd(t, args...)); err == nil {
			t.Errorf("parseOverrides(%v) = nil error, want a rejection", args)
		}
	}
}

func TestOverridesApply(t *testing.T) {
	scene := models.Scene{
		ID:         "1",
		Title:      "A Scene",
		Performers: []string{"Jodi West Milf", "Marcello Bravo"},
		Studio:     "Jodi West Clips",
		Tags:       []string{"MILF"},
	}

	// Performers are replaced outright, co-stars included — the point is to
	// drop the site's spelling entirely.
	got := sceneOverrides{performers: []string{"Jodi West"}}.apply(scene)
	if strings.Join(got.Performers, "|") != "Jodi West" {
		t.Errorf("Performers = %v, want exactly [Jodi West]", got.Performers)
	}
	if got.Studio != "Jodi West Clips" {
		t.Errorf("Studio = %q — an unset override must not touch the field", got.Studio)
	}
	if len(got.Tags) != 1 || got.Title != "A Scene" {
		t.Errorf("unrelated fields changed: %+v", got)
	}

	got = sceneOverrides{studio: "Jodi West"}.apply(scene)
	if got.Studio != "Jodi West" {
		t.Errorf("Studio = %q, want Jodi West", got.Studio)
	}
	if len(got.Performers) != 2 {
		t.Errorf("Performers = %v — an unset override must not touch the field", got.Performers)
	}

	if got = (sceneOverrides{}).apply(scene); got.Studio != scene.Studio || len(got.Performers) != 2 {
		t.Errorf("empty overrides changed the scene: %+v", got)
	}
}

// apply must not hand every scene the same backing array, or editing one
// scene's performers later would edit them all.
func TestOverridesApplyDoesNotShareTheSlice(t *testing.T) {
	o := sceneOverrides{performers: []string{"Jodi West"}}
	a := o.apply(models.Scene{ID: "1"})
	b := o.apply(models.Scene{ID: "2"})
	a.Performers[0] = "Someone Else"
	if b.Performers[0] != "Jodi West" {
		t.Errorf("scene b saw scene a's edit: %v", b.Performers)
	}
	if o.performers[0] != "Jodi West" {
		t.Errorf("the override itself was mutated: %v", o.performers)
	}
}

// The override has to reach the scenes a scrape collects, whichever mode is
// running — collectScenes is the single funnel all three go through.
func TestCollectScenesAppliesOverrides(t *testing.T) {
	sc := &fakeScraper{
		id: "fake",
		batches: [][]models.Scene{{
			{ID: "1", SiteID: "fake", Title: "One", Performers: []string{"Alias A"}, Studio: "Site Brand"},
			{ID: "2", SiteID: "fake", Title: "Two", Performers: []string{"Alias B", "Co Star"}, Studio: "Sub Brand"},
		}},
	}
	ov := sceneOverrides{performers: []string{"Jodi West"}, studio: "Jodi West"}

	scenes, _, err := collectScenes(context.Background(), sc, "https://example.com", scraper.ListOpts{}, ov)
	if err != nil {
		t.Fatalf("collectScenes: %v", err)
	}
	if len(scenes) != 2 {
		t.Fatalf("got %d scenes, want 2", len(scenes))
	}
	for _, s := range scenes {
		if strings.Join(s.Performers, "|") != "Jodi West" {
			t.Errorf("scene %s performers = %v, want [Jodi West]", s.ID, s.Performers)
		}
		if s.Studio != "Jodi West" {
			t.Errorf("scene %s studio = %q, want Jodi West", s.ID, s.Studio)
		}
	}
	// Everything else the scraper produced survives.
	if scenes[0].Title != "One" || scenes[1].Title != "Two" {
		t.Errorf("titles changed: %q %q", scenes[0].Title, scenes[1].Title)
	}
}

const canonStore = "https://fanhub.example/emmawoodhousefilms"

func emmaWoodhouseCanon() creators.Canon {
	return creators.NewCanon([]creators.Creator{{
		Name:   "Emma Woodhouse",
		Stores: []creators.Store{{URL: canonStore}},
	}})
}

// The creator rewrite has to land in the same funnel as the flags, or a store
// reached by its URL stores different names than the same store reached through
// --creator. Note the creator file declares no aliases: the studio field the
// scraper already produced is the whole signal.
func TestCollectScenesAppliesTheCreatorCanon(t *testing.T) {
	sc := &fakeScraper{
		id: "fake",
		batches: [][]models.Scene{{
			{ID: "1", SiteID: "fake", Title: "One", Studio: "Emma Woodhouse Films",
				Performers: []string{"Emma Woodhouse Films"}},
			{ID: "2", SiteID: "fake", Title: "Two", Studio: "Emma Woodhouse Films",
				Performers: []string{"Emma Woodhouse Films", "Hester Prynne"}},
		}},
	}
	ov := sceneOverrides{canon: emmaWoodhouseCanon()}

	scenes, _, err := collectScenes(context.Background(), sc, canonStore, scraper.ListOpts{}, ov)
	if err != nil {
		t.Fatalf("collectScenes: %v", err)
	}
	if got := strings.Join(scenes[0].Performers, "|"); got != "Emma Woodhouse" {
		t.Errorf("scene 1 performers = %q, want Emma Woodhouse", got)
	}
	if got := strings.Join(scenes[1].Performers, "|"); got != "Emma Woodhouse|Hester Prynne" {
		t.Errorf("scene 2 performers = %q, want the co-star kept", got)
	}
}

// The rewrite compares against the studio the site published, so it must run
// before --studio relabels it. Otherwise a run that renames the studio silently
// changes which credits are recognised as branding.
func TestCreatorCanonSeesTheScrapedStudioNotTheOverride(t *testing.T) {
	sc := &fakeScraper{
		id: "fake",
		batches: [][]models.Scene{{{ID: "1", SiteID: "fake", Studio: "Emma Woodhouse Films",
			Performers: []string{"Emma Woodhouse Films", "Hester Prynne"}}}},
	}
	ov := sceneOverrides{studio: "Something Else", canon: emmaWoodhouseCanon()}

	scenes, _, err := collectScenes(context.Background(), sc, canonStore, scraper.ListOpts{}, ov)
	if err != nil {
		t.Fatalf("collectScenes: %v", err)
	}
	if got := strings.Join(scenes[0].Performers, "|"); got != "Emma Woodhouse|Hester Prynne" {
		t.Errorf("performers = %q, want the branding folded and the co-star kept", got)
	}
	if scenes[0].Studio != "Something Else" {
		t.Errorf("studio = %q, want the override applied", scenes[0].Studio)
	}
}

// --performer is an explicit instruction for this run; the creator file is a
// standing default. The flag has to win.
func TestOverrideFlagBeatsTheCreatorCanon(t *testing.T) {
	sc := &fakeScraper{
		id: "fake",
		batches: [][]models.Scene{{{ID: "1", SiteID: "fake", Studio: "Emma Woodhouse Films",
			Performers: []string{"Emma Woodhouse Films"}}}},
	}
	ov := sceneOverrides{performers: []string{"Hester Prynne"}, canon: emmaWoodhouseCanon()}

	scenes, _, err := collectScenes(context.Background(), sc, canonStore, scraper.ListOpts{}, ov)
	if err != nil {
		t.Fatalf("collectScenes: %v", err)
	}
	if got := strings.Join(scenes[0].Performers, "|"); got != "Hester Prynne" {
		t.Errorf("performers = %q, want the flag to win", got)
	}
}

func TestDescribe(t *testing.T) {
	o := sceneOverrides{performers: []string{"Jodi West", "Red"}, studio: "Jodi West"}
	got := o.describe()
	for _, want := range []string{"Jodi West", "Red", "studio"} {
		if !strings.Contains(got, want) {
			t.Errorf("describe() = %q, want it to mention %q", got, want)
		}
	}
}
