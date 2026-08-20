package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Anastylosis/FSS/internal/store"
	"github.com/Anastylosis/FSS/output"
)

// The store diff exists for one failure mode, and it is the one the coming
// SQLite default creates: a studio that lives only as JSON in out_dir is
// invisible to `stash import` and `identify` the moment the database becomes
// the source those commands read. Nothing is lost — the file is still on disk —
// but the catalogue silently reads as empty, and "the upgrade ate my library"
// is the obvious conclusion. Reporting it by name, before the switch, turns
// that into a one-line fix.
//
// Deliberately read-only. `fss import` is opt-in and stays that way; doctor
// says what is missing and names the command, it does not move anyone's data.

// studioInventory is a studio URL -> live scene count for one store.
type studioInventory map[string]int

// flatInventory reads every studio file in dir. It decodes the header rather
// than the scenes: the count is what the file recorded at save time, which is
// all this needs and avoids parsing ~100 MB of scene bodies per studio.
//
// A file that cannot be read or parsed is reported rather than skipped — a
// corrupt studio file is exactly the kind of thing doctor should surface, and
// counting it as "absent" would send the operator to `fss import`, which would
// not fix it.
func flatInventory(dir string) (studioInventory, []string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}

	inv := studioInventory{}
	var broken []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		// fss writes other JSON into out_dir; only studio files carry a
		// studioUrl, so the probe below is also the filter.
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			broken = append(broken, e.Name())
			continue
		}
		var probe struct {
			StudioURL  string `json:"studioUrl"`
			SceneCount int    `json:"sceneCount"`
		}
		if err := json.Unmarshal(data, &probe); err != nil {
			broken = append(broken, e.Name())
			continue
		}
		if probe.StudioURL == "" {
			continue // not a studio file
		}
		// Canonicalise both sides or a studio saved under an http:// or www.
		// spelling reads as missing from a database that stores it canonically.
		inv[output.CanonicalStudioURL(probe.StudioURL)] += probe.SceneCount
	}
	sort.Strings(broken)
	return inv, broken, nil
}

// dbInventory reads live scene counts per studio from the database.
func dbInventory(db *store.SQLite) (studioInventory, error) {
	counts, err := db.SceneCounts()
	if err != nil {
		return nil, err
	}
	inv := studioInventory{}
	for url, n := range counts {
		inv[output.CanonicalStudioURL(url)] += n
	}
	return inv, nil
}

// storeDiff is what the two inventories disagree about.
type storeDiff struct {
	OnlyInFlat []string // studios that `fss import` would bring across
	OnlyInDB   []string // studios whose JSON is gone; usually fine, sometimes not
	Differing  []string // in both, but the scene counts do not match
	flat, db   studioInventory
}

func diffStores(flat, db studioInventory) storeDiff {
	d := storeDiff{flat: flat, db: db}
	for url, n := range flat {
		switch dn, ok := db[url]; {
		case !ok:
			d.OnlyInFlat = append(d.OnlyInFlat, url)
		case dn != n:
			d.Differing = append(d.Differing, url)
		}
	}
	for url := range db {
		if _, ok := flat[url]; !ok {
			d.OnlyInDB = append(d.OnlyInDB, url)
		}
	}
	sort.Strings(d.OnlyInFlat)
	sort.Strings(d.OnlyInDB)
	sort.Strings(d.Differing)
	return d
}

func (d storeDiff) inSync() bool {
	return len(d.OnlyInFlat) == 0 && len(d.Differing) == 0
}

// summarize renders the diff for doctor's one-line detail plus, when there is
// something to act on, an indented list. Studios are named rather than counted:
// "14 studios are missing" tells an operator they have a problem, not which
// one, and the URL is what `fss import` is pointed at.
func (d storeDiff) summarize(w *strings.Builder, maxList int) {
	list := func(label string, urls []string, remedy string) {
		if len(urls) == 0 {
			return
		}
		fmt.Fprintf(w, "\n    %s (%d):", label, len(urls))
		shown := urls
		if len(shown) > maxList {
			shown = shown[:maxList]
		}
		for _, u := range shown {
			fmt.Fprintf(w, "\n      %s", u)
		}
		if len(urls) > len(shown) {
			fmt.Fprintf(w, "\n      … and %d more", len(urls)-len(shown))
		}
		if remedy != "" {
			fmt.Fprintf(w, "\n      %s", remedy)
		}
	}

	list("only in JSON", d.OnlyInFlat, "run `fss import` to bring these into the database")
	list("scene counts differ", d.Differing, "re-run `fss import` for these, or `fss import --dry-run` to see what would change")
	// Not a problem by itself: exporting or deleting a JSON file after
	// importing is a normal thing to do. Reported so the picture is complete.
	list("only in the database", d.OnlyInDB, "")
}
