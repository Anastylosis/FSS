// Package mediafetch downloads scraped image assets with SSRF and size guards,
// and detects signed URLs whose expiry has already passed.
package mediafetch

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Anastylosis/FSS/internal/httpx"
)

// MaxBytes caps how much of a response is read, so a hostile or misconfigured
// host cannot exhaust memory.
const MaxBytes = 10 * 1024 * 1024

// Asset is a downloaded image.
type Asset struct {
	Data        []byte
	ContentType string
}

// Fetch downloads rawURL after validating it. allowPrivate permits hosts that
// resolve to private or loopback addresses, for local media servers.
func Fetch(ctx context.Context, client *http.Client, rawURL string, allowPrivate bool) (Asset, error) {
	if err := validateURL(ctx, rawURL, allowPrivate); err != nil {
		return Asset{}, fmt.Errorf("rejecting URL: %w", err)
	}

	client, refused := guardedClient(client, allowPrivate)

	resp, err := httpx.Do(ctx, client, httpx.Request{
		URL:     rawURL,
		Headers: map[string]string{"User-Agent": httpx.UserAgentFirefox},
		BackoffSleep: func(ctx context.Context, d time.Duration) error {
			if refused.Load() {
				return ErrBlocked
			}
			select {
			case <-time.After(d):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})
	if err != nil {
		return Asset{}, fmt.Errorf("downloading: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := httpx.ReadBodyN(resp.Body, MaxBytes+1)
	if err != nil {
		return Asset{}, fmt.Errorf("reading: %w", err)
	}
	if len(data) > MaxBytes {
		return Asset{}, fmt.Errorf("image exceeds %d bytes", MaxBytes)
	}

	ct := strings.ToLower(http.DetectContentType(data))
	if !allowedImageTypes[ct] {
		return Asset{}, fmt.Errorf("response is %s, not a supported image", ct)
	}
	return Asset{Data: data, ContentType: ct}, nil
}

var allowedImageTypes = map[string]bool{
	"image/jpeg": true, "image/png": true, "image/webp": true,
	"image/gif": true, "image/avif": true,
}

// ErrBlocked marks a request refused by the SSRF policy. It is never retried.
var ErrBlocked = errors.New("blocked by SSRF policy")

// DataURI fetches an image and encodes it as a data URI, the form Stash's
// cover_image field takes.
//
// A URL carrying an already-passed signed expiry is rejected without a request:
// scraped CDN thumbnails are often dead within hours of the scrape.
func DataURI(ctx context.Context, client *http.Client, rawURL string, allowPrivate bool) (string, error) {
	if Expired(rawURL, time.Now()) {
		return "", fmt.Errorf("URL signature expired — re-scrape the studio for a fresh URL")
	}
	asset, err := Fetch(ctx, client, rawURL, allowPrivate)
	if err != nil {
		return "", err
	}
	return "data:" + asset.ContentType + ";base64," + base64.StdEncoding.EncodeToString(asset.Data), nil
}

// ValidateURL enforces the SSRF defense: http(s) only, and unless allowPrivate
// is set, no host that resolves to a non-public address. It is a fast pre-check;
// the dialer (guardedClient) re-checks the address actually connected to, which
// closes DNS rebinding, and every redirect hop is validated the same way.
func ValidateURL(rawURL string, allowPrivate bool) error {
	return validateURL(context.Background(), rawURL, allowPrivate)
}

var lookupIPs = func(ctx context.Context, host string) ([]net.IP, error) {
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	out := make([]net.IP, len(addrs))
	for i, a := range addrs {
		out[i] = a.IP
	}
	return out, nil
}

func validateURL(ctx context.Context, rawURL string, allowPrivate bool) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parsing URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%w: scheme %q not allowed (only http/https)", ErrBlocked, u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("URL has no host")
	}
	if allowPrivate {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil {
		if isPrivateOrLocal(ip) {
			return fmt.Errorf("%w: host %s is a private/loopback address", ErrBlocked, ip)
		}
		return nil
	}
	ips, err := lookupIPs(ctx, host)
	if err != nil {
		return fmt.Errorf("resolving host %q: %w", host, err)
	}
	for _, ip := range ips {
		if isPrivateOrLocal(ip) {
			return fmt.Errorf("%w: host %q resolves to private/loopback IP %s", ErrBlocked, host, ip)
		}
	}
	return nil
}

const maxRedirects = 5

// guardedClient returns a copy of base whose redirects are validated hop by
// hop and, unless allowPrivate, whose connections are refused to non-public
// addresses at dial time. The flag is set when the policy refused a request.
func guardedClient(base *http.Client, allowPrivate bool) (*http.Client, *atomic.Bool) {
	refused := &atomic.Bool{}
	c := &http.Client{Timeout: base.Timeout}
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			refused.Store(true)
			return fmt.Errorf("%w: stopped after %d redirects", ErrBlocked, maxRedirects)
		}
		if err := validateURL(req.Context(), req.URL.String(), allowPrivate); err != nil {
			refused.Store(true)
			return fmt.Errorf("redirect rejected: %w", err)
		}
		return nil
	}
	if allowPrivate {
		c.Transport = base.Transport
		return c, refused
	}
	c.Transport = flagRefusals{publicTransport, refused}
	return c, refused
}

type flagRefusals struct {
	next    http.RoundTripper
	refused *atomic.Bool
}

func (f flagRefusals) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := f.next.RoundTrip(r)
	if errors.Is(err, ErrBlocked) {
		f.refused.Store(true)
	}
	return resp, err
}

var dialRewrite = func(addr string) string { return addr }

var dialBlocked = func(_ string, ip net.IP) bool { return isPrivateOrLocal(ip) }

var baseDialer = &net.Dialer{Timeout: 15 * time.Second, Control: dialControl}

var publicTransport = &http.Transport{
	DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		return baseDialer.DialContext(ctx, network, dialRewrite(addr))
	},
	TLSHandshakeTimeout: 15 * time.Second,
	ForceAttemptHTTP2:   true,
	MaxIdleConns:        10,
	IdleConnTimeout:     30 * time.Second,
}

func dialControl(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("%w: dial address %q is not an IP", ErrBlocked, address)
	}
	if dialBlocked(address, ip) {
		return fmt.Errorf("%w: refusing to connect to non-public address %s", ErrBlocked, ip)
	}
	return nil
}

var blockedPrefixes = parsePrefixes(
	"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
	"172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24",
	"192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24",
	"224.0.0.0/4", "240.0.0.0/4", "255.255.255.255/32",
	"::/96", "::ffff:0:0:0/96", "100::/64", "2001::/32", "2001:2::/48",
	"2001:10::/28", "2001:20::/28", "2001:db8::/32", "2002::/16", "3fff::/20",
	"5f00::/16", "fc00::/7", "fe80::/10", "fec0::/10", "ff00::/8",
)

// translationPrefixes embed an IPv4 address in their last 32 bits; DNS64
// networks use them, so the embedded address is judged instead.
var translationPrefixes = parsePrefixes("64:ff9b::/96", "64:ff9b:1::/48")

func parsePrefixes(ss ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(ss))
	for i, s := range ss {
		out[i] = netip.MustParsePrefix(s)
	}
	return out
}

func isPrivateOrLocal(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	return blockedAddr(addr.Unmap().WithZone(""))
}

func blockedAddr(addr netip.Addr) bool {
	for _, p := range translationPrefixes {
		if p.Contains(addr) {
			b := addr.As16()
			return blockedAddr(netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}))
		}
	}
	for _, p := range blockedPrefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// plausible bounds for a signed-URL expiry, so a random numeric query parameter
// is not mistaken for a timestamp.
var (
	expiryFloor   = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	expiryCeiling = time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
)

// absoluteExpiryParams are query keys (matched case-insensitively) whose value
// is a Unix timestamp after which the URL stops working.
var absoluteExpiryParams = []string{"expires", "exp", "expiry", "valid_until"}

// Expired reports whether rawURL carries a signed expiry that has already
// passed at time now.
//
// Many CDNs hand out short-lived signed URLs — `?expires=…&token=…` — so a
// stored thumbnail is often dead within hours. Detecting that offline lets a
// caller skip a request it knows will 403, and lets the NFO writer omit a
// `<thumb>` rather than bake in a broken link.
//
// It is deliberately conservative: an unrecognised or unparseable expiry
// returns false. A false negative costs one failed request; a false positive
// would discard a working URL.
func Expired(rawURL string, now time.Time) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	q := u.Query()

	for key, values := range q {
		if !matchesAny(key, absoluteExpiryParams) {
			continue
		}
		for _, v := range values {
			if t, ok := parseUnixTimestamp(v); ok {
				return t.Before(now)
			}
		}
	}

	// AWS SigV4 presigned: X-Amz-Date is the signing time, X-Amz-Expires the
	// lifetime in seconds.
	if signed, ok := lookupFold(q, "X-Amz-Date"); ok {
		if lifetime, ok2 := lookupFold(q, "X-Amz-Expires"); ok2 {
			base, err := time.Parse("20060102T150405Z", signed)
			if err != nil {
				return false
			}
			secs, err := strconv.ParseInt(lifetime, 10, 64)
			if err != nil || secs <= 0 {
				return false
			}
			return base.Add(time.Duration(secs) * time.Second).Before(now)
		}
	}
	return false
}

func matchesAny(key string, candidates []string) bool {
	for _, c := range candidates {
		if strings.EqualFold(key, c) {
			return true
		}
	}
	return false
}

func lookupFold(q url.Values, want string) (string, bool) {
	for key, values := range q {
		if strings.EqualFold(key, want) && len(values) > 0 {
			return values[0], true
		}
	}
	return "", false
}

// parseUnixTimestamp accepts seconds or milliseconds since the epoch, rejecting
// values outside a plausible date range.
func parseUnixTimestamp(s string) (time.Time, bool) {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || n <= 0 {
		return time.Time{}, false
	}
	t := time.Unix(n, 0).UTC()
	if t.After(expiryCeiling) {
		t = time.UnixMilli(n).UTC() // the value was milliseconds
	}
	if t.Before(expiryFloor) || t.After(expiryCeiling) {
		return time.Time{}, false
	}
	return t, true
}
