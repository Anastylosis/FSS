package parseutil

import (
	"math"
	"strconv"
	"strings"
)

const maxDurationSeconds = 24 * 3600 // 24 hours; anything beyond is garbage input

// ParseDurationColon parses colon-separated durations like "MM:SS" or
// "HH:MM:SS" and returns the total seconds. At most three parts;
// a part that is not a number, or a minute/second part of 60 or more, makes the
// whole input unparseable. Returns 0 for empty, unparseable or over-24h input.
func ParseDurationColon(s string) int {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) > 3 {
		return 0
	}
	total := 0
	for i, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil || n < 0 || n > maxDurationSeconds || (i > 0 && n >= 60) {
			return 0
		}
		total = total*60 + n
		if total > maxDurationSeconds {
			return 0
		}
	}
	return total
}

// ParseDurationISO parses ISO 8601 durations like "PT1H2M3S" and returns the
// total seconds. Input case is ignored. Fractional seconds are rounded. Returns 0 for empty or unparseable input.
//
// Only the time components (H/M/S) are understood. A duration carrying a date
// part ("P1DT2H") is not supported and yields a partial result — no FSS scraper
// has been seen to emit one, and guessing at day arithmetic would turn a visibly
// wrong number into a plausibly wrong one.
func ParseDurationISO(s string) int {
	s = strings.TrimSpace(s)
	if s == "" || s == "null" {
		return 0
	}
	// Uppercase *before* trimming the prefix. The other order silently mangles
	// any lowercase input: "pt45s" failed the TrimPrefix, became "PT45S", and the
	// seconds parse then read "PT45" and returned 0 — the whole duration lost,
	// not merely the hours. Feeds are inconsistent about the case of these
	// literals, and a zero duration is not validated anywhere downstream.
	s = strings.ToUpper(s)
	s = strings.TrimPrefix(s, "PT")

	var total int
	if i := strings.Index(s, "H"); i >= 0 {
		if n, err := strconv.Atoi(s[:i]); err == nil && n > 0 && n <= 24 {
			total += n * 3600
		}
		s = s[i+1:]
	}
	if i := strings.Index(s, "M"); i >= 0 {
		if n, err := strconv.Atoi(s[:i]); err == nil && n > 0 && n <= 1440 {
			total += n * 60
		}
		s = s[i+1:]
	}
	if i := strings.Index(s, "S"); i >= 0 && isDecimal(s[:i]) {
		if f, err := strconv.ParseFloat(s[:i], 64); err == nil && f > 0 && f <= maxDurationSeconds {
			total += int(math.Round(f))
		}
	}
	if total > maxDurationSeconds {
		return 0
	}
	return total
}

func isDecimal(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && r != '.' {
			return false
		}
	}
	return true
}
