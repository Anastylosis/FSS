package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/Anastylosis/FSS/internal/config"
	"github.com/Anastylosis/FSS/models"
)

func cloneFlagsCmd(t *testing.T, from *cobra.Command) *cobra.Command {
	t.Helper()
	c := &cobra.Command{Use: from.Name()}
	from.Flags().VisitAll(func(f *pflag.Flag) {
		switch f.Value.Type() {
		case "bool":
			c.Flags().Bool(f.Name, f.DefValue == "true", f.Usage)
		case "string":
			c.Flags().String(f.Name, f.DefValue, f.Usage)
		case "int":
			c.Flags().Int(f.Name, 0, f.Usage)
		case "stringSlice":
			c.Flags().StringSlice(f.Name, nil, f.Usage)
		case "count":
			c.Flags().CountP(f.Name, f.Shorthand, f.Usage)
		default:
			t.Fatalf("flag --%s has unhandled type %q", f.Name, f.Value.Type())
		}
	})
	return c
}

func writeScenesFile(t *testing.T, dir, name, studioURL string, scenes []models.Scene) {
	t.Helper()
	b, err := json.Marshal(models.StudioFile{SchemaVersion: models.StoreSchemaVersion, StudioURL: studioURL, Scenes: scenes})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// stashJSONServer answers Ping, a one-scene findScenes and "not found" to every
// lookup, which is what makes the dry run list entities as would-create.
func stashJSONServer(t *testing.T, scenesJSON string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query string `json:"query"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		resp := `{}`
		switch {
		case strings.Contains(req.Query, "systemStatus"):
			resp = `{"systemStatus":{"status":"OK"}}`
		case strings.Contains(req.Query, "findScenes"):
			resp = fmt.Sprintf(`{"findScenes":{"count":1,"scenes":%s}}`, scenesJSON)
		case strings.Contains(req.Query, "findTags"):
			resp = `{"findTags":{"tags":[]}}`
		case strings.Contains(req.Query, "findPerformers"):
			resp = `{"findPerformers":{"performers":[]}}`
		case strings.Contains(req.Query, "findStudios"):
			resp = `{"findStudios":{"studios":[]}}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"data":%s}`, resp)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func importCmdFixture(t *testing.T, format string) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	writeScenesFile(t, dir, "s.json", "https://example.com", []models.Scene{{
		ID: "1", SiteID: "example", StudioURL: "https://example.com", Title: "Some Title",
		URL: "https://example.com/1", Performers: []string{"Jane"}, Tags: []string{"Tag One"},
		ScrapedAt: time.Now().UTC(),
	}})
	srv := stashJSONServer(t, `[{"id":"7","title":"","files":[{"path":"/v/Some Title.mp4","basename":"Some Title.mp4"}]}]`)
	withCfg(t, &config.Config{OutDir: dir, Stash: config.StashConfig{URL: srv.URL, Tag: "fss-imported"}})

	c := newImportTestCmd(t)
	setFlag(t, c, "dir", dir)
	setFlag(t, c, "format", format)
	c.SetContext(context.Background())
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&bytes.Buffer{})
	return c, &out
}

func TestStashImportFormatJSONDryRun(t *testing.T) {
	c, out := importCmdFixture(t, "json")
	if err := runStashImport(c, nil); err != nil {
		t.Fatal(err)
	}

	dec := json.NewDecoder(bytes.NewReader(out.Bytes()))
	var env struct {
		Command string `json:"command"`
		OK      bool   `json:"ok"`
		Result  importResultJSON
	}
	if err := dec.Decode(&env); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out.String())
	}
	if dec.More() {
		t.Fatalf("stdout holds more than one document:\n%s", out.String())
	}
	if !env.OK {
		t.Fatalf("ok = false:\n%s", out.String())
	}
	res := env.Result
	if res.Apply || res.WouldCreate == nil {
		t.Fatalf("dry run shape wrong: %+v", res)
	}
	if res.Stats.Total != 1 || res.Stats.Matched != 1 {
		t.Errorf("stats = %+v", res.Stats)
	}
	if len(res.Scenes) != 1 {
		t.Fatalf("scenes = %+v", res.Scenes)
	}
	sc := res.Scenes[0]
	if sc.StashSceneID != "7" || sc.Status != "wouldUpdate" || sc.MatchedTo != "Some Title" {
		t.Errorf("scene = %+v", sc)
	}
	if got := sc.Changes["title"]; got.To != "Some Title" {
		t.Errorf("title change = %+v", got)
	}
	if got := sc.Changes["performers"].Added; len(got) != 1 || got[0] != "Jane" {
		t.Errorf("performers added = %v", got)
	}
	wc := res.WouldCreate
	if len(wc.Performers) != 1 || wc.Performers[0] != "Jane" || len(wc.Tags) == 0 {
		t.Errorf("wouldCreate = %+v", wc)
	}
	if res.Failures == nil {
		t.Error("failures must be an empty list, not null")
	}
}

func TestStashImportTextModeKeepsProse(t *testing.T) {
	c, jsonOut := importCmdFixture(t, "text")
	stdout := captureStdout(t, func() {
		if err := runStashImport(c, nil); err != nil {
			t.Fatal(err)
		}
	})
	if jsonOut.Len() != 0 {
		t.Errorf("text mode wrote to the command writer: %q", jsonOut.String())
	}
	for _, want := range []string{"Connected to Stash", "--- DRY RUN", "Would create on apply:", "Dry-run: 1 would match"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("text output lost %q:\n%s", want, stdout)
		}
	}
}

func TestStashImportFormatJSONStashUnreachable(t *testing.T) {
	c, out := importCmdFixture(t, "json")
	cfg.Stash.URL = "http://127.0.0.1:1"
	err := runStashImport(c, nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	var env jsonEnvelope
	if jErr := json.Unmarshal(out.Bytes(), &env); jErr != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", jErr, out.String())
	}
	if env.OK || env.Error == nil || env.Error.Code != codeStashUnreachable {
		t.Errorf("envelope = %+v", env)
	}
}

func compareCmdFixture(t *testing.T, format string) (*cobra.Command, *bytes.Buffer, string) {
	t.Helper()
	dir := t.TempDir()
	writeScenesFile(t, dir, "a.json", storeA, []models.Scene{
		priced(storeA, "Shared One", 30), priced(storeA, "A Exclusive", 5),
	})
	writeScenesFile(t, dir, "b.json", storeB, []models.Scene{
		priced(storeB, "Shared One", 10),
	})
	cdir := t.TempDir()
	yaml := "name: Someone\nstores:\n  - url: " + storeA + "\n  - url: " + storeB + "\n"
	if err := os.WriteFile(filepath.Join(cdir, "someone.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	withCfg(t, &config.Config{OutDir: dir})

	c := cloneFlagsCmd(t, compareCmd)
	setFlag(t, c, "dir", dir)
	setFlag(t, c, "creators-dir", cdir)
	setFlag(t, c, "format", format)
	setFlag(t, c, "top", "5")
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&bytes.Buffer{})
	return c, &out, dir
}

func TestCompareFormatJSON(t *testing.T) {
	c, out, dir := compareCmdFixture(t, "json")
	setFlag(t, c, "exclusives", "true")
	csvPath := filepath.Join(dir, "out.csv")
	setFlag(t, c, "csv", csvPath)
	if err := runCompare(c, nil); err != nil {
		t.Fatal(err)
	}

	dec := json.NewDecoder(bytes.NewReader(out.Bytes()))
	var env struct {
		OK     bool `json:"ok"`
		Result compareResultJSON
	}
	if err := dec.Decode(&env); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out.String())
	}
	if dec.More() || !env.OK {
		t.Fatalf("bad envelope:\n%s", out.String())
	}
	res := env.Result
	if res.CSVPath != csvPath {
		t.Errorf("csvPath = %q", res.CSVPath)
	}
	if _, err := os.Stat(csvPath); err != nil {
		t.Error("csv not written")
	}
	if len(res.Creators) != 1 {
		t.Fatalf("creators = %+v", res.Creators)
	}
	cr := res.Creators[0]
	if cr.Name != "Someone" || cr.HeuristicGrouping || len(cr.Stores) != 2 {
		t.Errorf("creator = %+v", cr)
	}
	if len(cr.WidestGaps) != 1 || cr.WidestGaps[0].Gap != 20 ||
		cr.WidestGaps[0].Low.Store != storeB || cr.WidestGaps[0].High.Store != storeA {
		t.Errorf("gaps = %+v", cr.WidestGaps)
	}
	if len(cr.Cheapest) != 1 || cr.Cheapest[0].Store != storeB || cr.Cheapest[0].SavingsTotal != 20 {
		t.Errorf("cheapest = %+v", cr.Cheapest)
	}
	if len(cr.Exclusives) != 1 || cr.Exclusives[0].Titles[0] != "A Exclusive" {
		t.Errorf("exclusives = %+v", cr.Exclusives)
	}
	for _, s := range cr.Stores {
		if s.AvgPrice == nil {
			t.Errorf("store %s has no avgPrice", s.URL)
		}
	}
}

func TestCompareFormatJSONNothingToCompare(t *testing.T) {
	c, out, _ := compareCmdFixture(t, "json")
	cdir := t.TempDir()
	yaml := "name: Someone\nstores:\n  - url: " + storeA + "\n  - url: " + storeC + "\n"
	if err := os.WriteFile(filepath.Join(cdir, "someone.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	setFlag(t, c, "creators-dir", cdir)
	if err := runCompare(c, nil); err != nil {
		t.Fatal(err)
	}
	var env struct {
		OK     bool `json:"ok"`
		Result compareResultJSON
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out.String())
	}
	if !env.OK || env.Result.Creators == nil || len(env.Result.Creators) != 0 || !strings.HasPrefix(env.Result.Message, "Nothing to compare") {
		t.Errorf("result = %s", out.String())
	}
}

func TestCompareTextModeUnchanged(t *testing.T) {
	c, jsonOut, _ := compareCmdFixture(t, "text")
	stdout := captureStdout(t, func() {
		if err := runCompare(c, nil); err != nil {
			t.Fatal(err)
		}
	})
	if jsonOut.Len() != 0 {
		t.Errorf("text mode wrote to the command writer")
	}
	for _, want := range []string{"Someone — 3 scenes across 2 storefronts", "Widest price gaps:", "Shared One"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("missing %q in:\n%s", want, stdout)
		}
	}
}
