package cmd

import (
	"strings"
	"testing"
)

func fired(results []detection, platform string) bool {
	for _, d := range results {
		if d.platform == platform {
			return true
		}
	}
	return false
}

// TestPlatformRulesAreWellFormed catches a rule added with no signals, which
// would compile and sit in the table forever without ever firing.
func TestPlatformRulesAreWellFormed(t *testing.T) {
	for i, r := range platformRules {
		if r.custom != nil {
			if r.platform != "" || r.pkg != "" || len(r.anyLower)+len(r.anyRaw)+len(r.allLower) > 0 {
				t.Errorf("rule %d: custom rules must not also declare signals or a platform", i)
			}
			continue
		}
		if r.platform == "" || r.pkg == "" {
			t.Errorf("rule %d (%q): platform and pkg are required", i, r.platform)
		}
		if len(r.anyLower)+len(r.anyRaw)+len(r.allLower) == 0 {
			t.Errorf("rule %d (%q): has no signals, so it can never fire", i, r.platform)
		}
		for _, s := range r.anyLower {
			if s != strings.ToLower(s) {
				t.Errorf("rule %d (%q): anyLower signal %q is not lowercase, so it can never match",
					i, r.platform, s)
			}
		}
		for _, s := range r.allLower {
			if s != strings.ToLower(s) {
				t.Errorf("rule %d (%q): allLower signal %q is not lowercase, so it can never match",
					i, r.platform, s)
			}
		}
	}
}

// TestPlatformRulesFire exercises every table rule against its own signals.
// The custom matchers are covered individually in detect_test.go.
func TestPlatformRulesFire(t *testing.T) {
	for _, r := range platformRules {
		if r.custom != nil {
			continue
		}
		t.Run(r.platform, func(t *testing.T) {
			// Each any-of signal must trigger the rule on its own.
			for _, s := range r.anyLower {
				page := "<html>" + s + "</html>"
				if !fired(detectPlatform(page, nil, nil), r.platform) {
					t.Errorf("anyLower signal %q did not fire %q", s, r.platform)
				}
				// anyLower is matched against the lowercased page, so an
				// uppercase page must match too.
				if !fired(detectPlatform(strings.ToUpper(page), nil, nil), r.platform) {
					t.Errorf("anyLower signal %q did not fire %q when upper-cased", s, r.platform)
				}
			}
			for _, s := range r.anyRaw {
				if !fired(detectPlatform("<html>"+s+"</html>", nil, nil), r.platform) {
					t.Errorf("anyRaw signal %q did not fire %q", s, r.platform)
				}
			}

			// All-of signals fire only when every one is present.
			if len(r.allLower) > 0 {
				full := strings.Join(r.allLower, " ")
				if !fired(detectPlatform(full, nil, nil), r.platform) {
					t.Errorf("allLower signals %v together did not fire %q", r.allLower, r.platform)
				}
				if len(r.allLower) > 1 && len(r.anyLower) == 0 && len(r.anyRaw) == 0 {
					for _, s := range r.allLower {
						if fired(detectPlatform(s, nil, nil), r.platform) {
							t.Errorf("allLower signal %q alone fired %q; it must require all of %v",
								s, r.platform, r.allLower)
						}
					}
				}
			}
		})
	}
}

// TestWordPressVariants covers both WordPress outcomes: the video-elements
// theme is reported in place of plain WordPress, not alongside it.
func TestWordPressVariants(t *testing.T) {
	plain := detectPlatform("<html>/wp-content/ plugins</html>", nil, nil)
	if !fired(plain, "WordPress") {
		t.Errorf("plain WordPress page did not fire WordPress: %+v", plain)
	}
	if fired(plain, "WP video-elements") {
		t.Errorf("plain WordPress page should not fire the video-elements theme: %+v", plain)
	}

	themed := detectPlatform("<html>/wp-content/ video-elements</html>", nil, nil)
	if !fired(themed, "WP video-elements") {
		t.Errorf("video-elements page did not fire WP video-elements: %+v", themed)
	}
	if fired(themed, "WordPress") {
		t.Errorf("video-elements page should replace plain WordPress, not add to it: %+v", themed)
	}
	for _, d := range themed {
		if d.platform == "WP video-elements" && d.pkg != "veutil" {
			t.Errorf("WP video-elements pkg = %q, want veutil", d.pkg)
		}
	}
}

// TestGenericWordPressIsReportedLate pins the ordering: a page carrying both a
// specific platform signal and generic WordPress markers reports the specific
// platform first.
//
// This holds for every rule ahead of WordPress in the table, which is all but
// three. Spizoo, Vixen and Nasty Media are declared after it and so are
// reported second — see the note on platformRules. That asymmetry is existing
// behaviour, and TestWordPressPrecedesTrailingRules pins it so it cannot change
// silently.
func TestGenericWordPressIsReportedLate(t *testing.T) {
	results := detectPlatform("nats.mygaycash.com /wp-content/ theme", nil, nil)

	wpIdx, specificIdx := -1, -1
	for i, d := range results {
		switch d.platform {
		case "WordPress":
			wpIdx = i
		case "My Gay Cash NATS CMS":
			specificIdx = i
		}
	}
	if specificIdx == -1 || wpIdx == -1 {
		t.Fatalf("expected both My Gay Cash and WordPress, got %+v", results)
	}
	if specificIdx > wpIdx {
		t.Errorf("WordPress (index %d) was reported before My Gay Cash (index %d); the generic "+
			"check must stay after the specific rules", wpIdx, specificIdx)
	}
}

// TestWordPressPrecedesTrailingRules documents the three rules declared after
// the generic WordPress check. This is not necessarily desirable, but it is
// what `fss detect` has always printed; the test exists so that reordering the
// table is a deliberate, visible change rather than an accident.
func TestWordPressPrecedesTrailingRules(t *testing.T) {
	for _, tc := range []struct{ signal, platform string }{
		{"spizoo.com", "Spizoo"},
		{"vixen.com", "Vixen Media Group"},
		{"nasty media group", "Nasty Media Group (WWB18)"},
	} {
		t.Run(tc.platform, func(t *testing.T) {
			results := detectPlatform(tc.signal+" /wp-content/", nil, nil)
			wpIdx, specificIdx := -1, -1
			for i, d := range results {
				switch d.platform {
				case "WordPress":
					wpIdx = i
				case tc.platform:
					specificIdx = i
				}
			}
			if specificIdx == -1 || wpIdx == -1 {
				t.Fatalf("expected both %s and WordPress, got %+v", tc.platform, results)
			}
			if wpIdx > specificIdx {
				t.Errorf("%s (index %d) now precedes WordPress (index %d); the table order "+
					"changed — update this test if that was intended", tc.platform, specificIdx, wpIdx)
			}
		})
	}
}
