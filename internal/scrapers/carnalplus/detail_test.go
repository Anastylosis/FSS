package carnalplus

import (
	"os"
	"strings"
	"testing"
)

func TestParseGridDetailCarnalPlus(t *testing.T) {
	body, err := os.ReadFile("testdata/carnalplus_detail.html")
	if err != nil {
		t.Fatal(err)
	}
	d := parseGridDetail(body)
	if got := d.date.Format("2006-01-02"); got != "2026-10-28" {
		t.Errorf("date = %s", got)
	}
	if d.duration != 45*60+9 {
		t.Errorf("duration = %d", d.duration)
	}
	if want := []string{"Andolini XXL", "Cannikin Key", "Ricky Fox"}; strings.Join(d.performers, "|") != strings.Join(want, "|") {
		t.Errorf("performers = %v", d.performers)
	}
	if d.director != "Timmy Treasure" {
		t.Errorf("director = %q", d.director)
	}
	if len(d.tags) != 20 || d.tags[0] != "Bareback" || d.tags[len(d.tags)-1] != "Uncut" {
		t.Errorf("tags = %v", d.tags)
	}
	if !strings.HasPrefix(d.description, "Cannikin has been to the studio") || strings.Count(d.description, "\n\n") != 1 {
		t.Errorf("description = %q", d.description)
	}
	if strings.Contains(d.description, "Credits") || strings.Contains(d.description, "Show Less") {
		t.Errorf("description carries page chrome: %q", d.description)
	}
}

// Jockpack truncates long descriptions with a "firstWords … More" toggle and
// hides the rest in a div; the toggle text must not leak into the description.
func TestParseGridDetailJockpackReadMore(t *testing.T) {
	body, err := os.ReadFile("testdata/jockpack_detail.html")
	if err != nil {
		t.Fatal(err)
	}
	d := parseGridDetail(body)
	if strings.Contains(d.description, "Adam ...") || strings.Contains(d.description, "More") {
		t.Errorf("description carries the read-more toggle: %q", d.description)
	}
	if strings.Count(d.description, "\n\n") != 3 || !strings.HasSuffix(d.description, "full of spunk!") {
		t.Errorf("description = %q", d.description)
	}
	if len(d.tags) != 13 || d.tags[5] != "Dads & Dilfs" {
		t.Errorf("tags = %v", d.tags)
	}
}

func TestParseDetailDuration(t *testing.T) {
	cases := map[string]int{
		" Full Length Video : 45min 9sec": 2709,
		" Full Length Video : 1h 2min":    3720,
		" Full Length Video : 59sec":      59,
		"":                                0,
	}
	for in, want := range cases {
		if got := parseDetailDuration(in); got != want {
			t.Errorf("parseDetailDuration(%q) = %d, want %d", in, got, want)
		}
	}
}
