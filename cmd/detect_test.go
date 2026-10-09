package cmd

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	_ "github.com/Anastylosis/FSS/internal/scrapers/all"
)

// A supported URL short-circuits before the fetch, so this stays offline.
func TestDetect_alreadySupportedWithoutScheme(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"detect", "www.brazzers.com/videos"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("detect: %v", err)
	}

	if !strings.Contains(buf.String(), "Already supported by scraper: brazzers") {
		t.Errorf("expected early return for a supported URL, got:\n%s", buf.String())
	}
}

// hasPkg reports whether the results contain a detection for the given util
// package, returning the matched detail string for further assertions.
func hasPkg(results []detection, pkg string) (string, bool) {
	for _, d := range results {
		if d.pkg == pkg {
			return d.detail, true
		}
	}
	return "", false
}

func TestDetectPlatform_noSignalsIsEmpty(t *testing.T) {
	got := detectPlatform("<html><body>nothing to see here</body></html>", nil, nil)
	if len(got) != 0 {
		t.Fatalf("expected no detections, got %+v", got)
	}
}

func TestDetectPlatform_ayloCookie(t *testing.T) {
	cookies := []*http.Cookie{
		{Name: "session", Value: "x"},
		{Name: "instance_token", Value: "abc"},
	}
	got := detectPlatform("", cookies, nil)
	if _, ok := hasPkg(got, "ayloutil"); !ok {
		t.Fatalf("expected ayloutil from instance_token cookie, got %+v", got)
	}
}

func TestDetectPlatform_ayloCookieAbsent(t *testing.T) {
	cookies := []*http.Cookie{{Name: "session", Value: "x"}}
	got := detectPlatform("", cookies, nil)
	if _, ok := hasPkg(got, "ayloutil"); ok {
		t.Fatalf("did not expect ayloutil without instance_token cookie, got %+v", got)
	}
}

func TestDetectPlatform_gammaDetailVariants(t *testing.T) {
	// The applicationID signal should set the more specific detail string.
	got := detectPlatform(`<script>applicationID:"TSMKFA364Q"</script>`, nil, nil)
	detail, ok := hasPkg(got, "gammautil")
	if !ok {
		t.Fatalf("expected gammautil, got %+v", got)
	}
	if detail != "Algolia applicationID TSMKFA364Q" {
		t.Errorf("expected specific applicationID detail, got %q", detail)
	}

	// The generic algolia.net + applicationID path yields the generic detail.
	got = detectPlatform(`algolia.net ... applicationid: "other"`, nil, nil)
	detail, ok = hasPkg(got, "gammautil")
	if !ok {
		t.Fatalf("expected gammautil from generic algolia signal, got %+v", got)
	}
	if detail != "Algolia API detected" {
		t.Errorf("expected generic algolia detail, got %q", detail)
	}
}

func TestDetectPlatform_nuxtFYCSignals(t *testing.T) {
	// Bare __NUXT_DATA__ → generic detail.
	got := detectPlatform(`<script id="__NUXT_DATA__">[]</script>`, nil, nil)
	detail, ok := hasPkg(got, "fycutil")
	if !ok {
		t.Fatalf("expected fycutil, got %+v", got)
	}
	if detail != "__NUXT_DATA__ tag found" {
		t.Errorf("unexpected detail %q", detail)
	}

	// __NUXT_DATA__ + pornpros CDN → augmented detail.
	got = detectPlatform(`<script id="__NUXT_DATA__">x</script> cdn.pornpros.com`, nil, nil)
	detail, _ = hasPkg(got, "fycutil")
	if detail != "__NUXT_DATA__ tag found + FYC/PornPros signals" {
		t.Errorf("expected augmented FYC detail, got %q", detail)
	}
}

func TestDetectPlatform_nextNestedWankItNow(t *testing.T) {
	// Wank It Now requires __NEXT_DATA__ AND mjedge.net AND wankitnow — all nested.
	page := `<script id="__NEXT_DATA__">x</script> https://cdn.mjedge.net/wankitnow/img.jpg`
	got := detectPlatform(page, nil, nil)
	if _, ok := hasPkg(got, "wankitnowutil"); !ok {
		t.Fatalf("expected wankitnowutil from nested NEXT+mjedge+wankitnow, got %+v", got)
	}

	// mjedge.net without wankitnow must NOT yield wankitnowutil.
	got = detectPlatform(`<script id="__NEXT_DATA__">x</script> cdn.mjedge.net/other`, nil, nil)
	if _, ok := hasPkg(got, "wankitnowutil"); ok {
		t.Fatalf("did not expect wankitnowutil without wankitnow signal, got %+v", got)
	}
}

func TestDetectPlatform_ghostProNextSignal(t *testing.T) {
	got := detectPlatform(`<script id="__NEXT_DATA__">x</script> yppcdn.com`, nil, nil)
	if _, ok := hasPkg(got, "ghostpro / kbproductions / hobybuchanon"); !ok {
		t.Fatalf("expected ghostpro/kbproductions, got %+v", got)
	}
}

func TestDetectPlatform_isCaseInsensitive(t *testing.T) {
	// page is lowercased internally, so an uppercase CDN host still matches.
	got := detectPlatform("Visit PSMCDN.NET for assets", nil, nil)
	if _, ok := hasPkg(got, "teamskeetutil"); !ok {
		t.Fatalf("expected case-insensitive match for psmcdn.net, got %+v", got)
	}
}

func TestDetectPlatform_compoundSignalRequiresBoth(t *testing.T) {
	// Grooby needs grooby.com AND set-target- together; one alone must not match.
	if _, ok := hasPkg(detectPlatform("grooby.com only", nil, nil), "groobyutil"); ok {
		t.Error("grooby.com alone should not match groobyutil")
	}
	if _, ok := hasPkg(detectPlatform("set-target-foo only", nil, nil), "groobyutil"); ok {
		t.Error("set-target- alone should not match groobyutil")
	}
	if _, ok := hasPkg(detectPlatform("grooby.com ... set-target-foo", nil, nil), "groobyutil"); !ok {
		t.Error("grooby.com + set-target- together should match groobyutil")
	}
}

func TestDetectPlatform_myMemberSite(t *testing.T) {
	got := detectPlatform(`<link href="https://d1mr1qahd2jlw8.cloudfront.net/_next/static/css/a.css"/>`, nil, nil)
	if _, ok := hasPkg(got, "mymemberutil"); !ok {
		t.Fatalf("expected mymemberutil, got %+v", got)
	}
}

func TestDetectPlatform_multipleDetectionsOnOnePage(t *testing.T) {
	// A page can legitimately trip more than one signal.
	page := `psmcdn.net and metartnetwork.com on the same page`
	got := detectPlatform(page, nil, nil)
	if _, ok := hasPkg(got, "teamskeetutil"); !ok {
		t.Errorf("expected teamskeetutil among results, got %+v", got)
	}
	if _, ok := hasPkg(got, "metartutil"); !ok {
		t.Errorf("expected metartutil among results, got %+v", got)
	}
}
