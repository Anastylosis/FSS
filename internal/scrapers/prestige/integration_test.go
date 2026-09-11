//go:build integration

package prestige

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
	"github.com/Anastylosis/FSS/internal/scrapers/testutil"
)

const liveURL = "https://www.prestige-av.com/goods"

// TestLivePrestige is gated by a quick reachability probe because
// prestige-av.com sits behind a CloudFront WAF that geo-blocks every region
// outside Japan with HTTP 403 ("Error from cloudfront"). Running the smoke
// test from a non-Japan IP would always fail through no fault of the scraper,
// so we skip gracefully in that case.
func TestLivePrestige(t *testing.T) {
	if reachable, why := probeReachable(t); !reachable {
		t.Skipf("prestige-av.com unreachable from this network — %s", why)
	}
	testutil.RunLiveScrape(t, New(), liveURL, 2)
}

func probeReachable(t *testing.T) (bool, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := httpx.NewClient(10 * time.Second)
	resp, err := httpx.DoWithStatus(ctx, client, httpx.Request{
		URL:     liveURL,
		Headers: httpx.BrowserHeaders(httpx.UserAgentFirefox),
	})
	if err != nil {
		return false, "probe failed: " + err.Error()
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusForbidden {
		// CloudFront 403 → geo-block. Distinguish from a real auth-required 403
		// by checking the x-amz-cf-pop header that CloudFront always sets.
		if pop := resp.Header.Get("x-amz-cf-pop"); pop != "" {
			return false, "CloudFront WAF geo-block (x-amz-cf-pop=" + pop + ")"
		}
		return false, "HTTP 403 from origin"
	}
	if resp.StatusCode >= 400 {
		return false, "HTTP " + resp.Status
	}
	return true, ""
}
