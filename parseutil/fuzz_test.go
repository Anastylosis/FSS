package parseutil

import (
	"strings"
	"testing"
)

func FuzzParseDurationColon(f *testing.F) {
	f.Add("12:34")
	f.Add("1:02:03")
	f.Add("0:00")
	f.Add("")
	f.Add("abc:def")
	f.Add("99:99:99")
	f.Add("1:9223372036854775807")
	f.Add("1:xx:30")
	f.Add("-5:-10")

	f.Fuzz(func(t *testing.T, s string) {
		n := ParseDurationColon(s)
		if n < 0 {
			t.Errorf("ParseDurationColon(%q) = %d, must be non-negative", s, n)
		}
	})
}

func FuzzParseDurationISO(f *testing.F) {
	f.Add("PT1H2M3S")
	f.Add("PT30M")
	f.Add("PT45S")
	f.Add("")
	f.Add("null")
	f.Add("garbage")
	f.Add("PT999H999M999S")
	// Regression: lowercase literals lost the whole duration (see
	// TestParseDurationISOIsCaseInsensitive).
	f.Add("pt1h2m3s")
	f.Add("pt45s")
	f.Add("PT25M30.5S")
	f.Add("PT-5S")
	f.Add("PTinfS")
	f.Add("PTNaNS")
	f.Add("PT9223372036854775807S")

	f.Fuzz(func(t *testing.T, s string) {
		n := ParseDurationISO(s)
		if n < 0 {
			t.Errorf("ParseDurationISO(%q) = %d, must be non-negative", s, n)
		}

		// Case-insensitivity as a property rather than a handful of seeds.
		// Non-negativity alone could not catch the bug this guards: lowercase
		// input returned 0, which is still non-negative, so the target ran
		// millions of executions over a defect it was structurally unable to
		// see. Comparing against the uppercased spelling makes every random
		// input a test of the same invariant.
		if up := ParseDurationISO(strings.ToUpper(s)); up != n {
			t.Errorf("ParseDurationISO(%q) = %d but ParseDurationISO(%q) = %d; "+
				"the ISO literals must be case-insensitive", s, n, strings.ToUpper(s), up)
		}
	})
}
