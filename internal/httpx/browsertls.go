package httpx

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	utls "github.com/refraction-networking/utls"
)

// A handful of WAFs classify clients by the shape of the TLS ClientHello
// (JA3/JA4) and the HTTP/2 SETTINGS that follow, before any request header is
// read. Go's crypto/tls emits a hello nothing else emits, so such a host
// answers every Go request identically — NaughtyAmerica's AWS load balancer
// returns a bare 403 to the homepage, with or without browser headers, over
// HTTP/1.1 or HTTP/2, from any address. A browser on the same machine and the
// same address loads the site fine.
//
// utls replays a recorded browser hello byte for byte, which is enough to be
// served normally. This is emphatically NOT a way around a login, a paywall,
// or a rate limit: it makes an ordinary anonymous request look like the
// browser the site already serves, and everything it reaches is public.
//
// Certificates are verified exactly as elsewhere — utls performs the same
// verification against the same root pool. What changes is the hello, not the
// trust decision.

// browserHello is the fingerprint to present. HelloChrome_Auto tracks the
// newest Chrome profile the pinned utls version carries, so bumping utls
// refreshes it. That is also the maintenance cost: fingerprints drift as
// browsers move, and a preset that falls far behind starts looking anomalous
// in its own right. If a site that worked starts returning 403 again, bump
// utls before suspecting the scraper.
var browserHello = utls.HelloChrome_Auto

// browserRootCAs overrides the trust store. nil means the system pool, which
// is what production always uses; the tests set it so a handshake against an
// httptest server can succeed and exercise the real HTTP/2 path.
var browserRootCAs *x509.CertPool

// dialBrowserTLS completes a handshake presenting a browser ClientHello.
func dialBrowserTLS(ctx context.Context, network, addr string) (net.Conn, error) {
	// Split before dialing: the host drives SNI and verification, and a
	// malformed addr should fail without opening a connection first.
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	raw, err := (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	// ServerName drives both SNI and verification; MinVersion guards against a
	// downgrade, since the hello preset advertises a browser's full range.
	conn := utls.UClient(raw, &utls.Config{
		ServerName: host,
		MinVersion: tls.VersionTLS12,
		RootCAs:    browserRootCAs,
	}, browserHello)
	if err := conn.HandshakeContext(ctx); err != nil {
		_ = raw.Close()
		return nil, err
	}
	// The transport speaks HTTP/2 without asking, so a server that did not
	// agree to it must fail here rather than receive frames it cannot read.
	if p := conn.ConnectionState().NegotiatedProtocol; p != "h2" {
		_ = conn.Close()
		return nil, fmt.Errorf("browser TLS: %s negotiated %q, not h2", host, p)
	}
	return conn, nil
}

var (
	browserTLSOnce      sync.Once
	browserTLSTransport http.RoundTripper
)

// browserTransport builds the shared browser-fingerprint transport.
//
// net/http negotiates HTTP/2 only over its own *tls.Conn, so it cannot be
// handed a utls connection. Instead the transport is told the connection is
// unencrypted HTTP/2 — prior knowledge, no ALPN of its own — and the dialer
// does the browser handshake underneath it. browserScheme rewrites each
// request to http:// on the way in, so the transport takes that path, and
// restores the caller's request on the response. See docs/scrapers.md.
//
// It is HTTP/2 only, deliberately: a browser hello advertises h2, and a
// fallback to HTTP/1.1 would never be selected. A host that refuses h2 fails
// in the dialer instead.
func browserTransport() http.RoundTripper {
	browserTLSOnce.Do(func() {
		var protocols http.Protocols
		protocols.SetUnencryptedHTTP2(true)
		browserTLSTransport = browserScheme{&http.Transport{
			Protocols: &protocols,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialBrowserTLS(ctx, network, addr)
			},
			MaxIdleConnsPerHost: 4,
			IdleConnTimeout:     90 * time.Second,
		}}
	})
	return browserTLSTransport
}

// browserScheme carries an https request over the transport's unencrypted
// HTTP/2 path, whose dialer is in fact the browser TLS handshake.
type browserScheme struct{ next *http.Transport }

func (b browserScheme) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme != "https" {
		return nil, fmt.Errorf("browser TLS: refusing %s URL %s", r.URL.Scheme, r.URL.Redacted())
	}
	out := r.Clone(r.Context())
	out.URL.Scheme = "http"
	if out.URL.Port() == "" {
		out.URL.Host = net.JoinHostPort(out.URL.Hostname(), "443")
	}
	if out.Host == "" {
		out.Host = r.URL.Host
	}
	resp, err := b.next.RoundTrip(out)
	if resp != nil {
		resp.Request = r
	}
	return resp, err
}

// CloseIdleConnections lets http.Client.CloseIdleConnections reach the pool.
func (b browserScheme) CloseIdleConnections() { b.next.CloseIdleConnections() }

// NewBrowserTLSClient returns a client that presents a browser's TLS
// fingerprint instead of Go's.
//
// Reach for it only after a scraper has been shown to fail without it — the
// signature is a WAF answering an identical 403 to every request including the
// site's own homepage, while a browser on the same machine loads it. It is the
// mirror image of NewLegacyTLSClient: that one widens what Go will accept from
// an old server, this one changes what Go presents to a picky one. Both verify
// certificates.
//
// The connection pool is separate from NewClient's, so using it for one site
// does not change how every other site is reached.
func NewBrowserTLSClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: browserTransport(),
	}
}
