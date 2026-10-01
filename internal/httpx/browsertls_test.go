package httpx

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewBrowserTLSClientReusesOnePool(t *testing.T) {
	a := NewBrowserTLSClient(5 * time.Second)
	b := NewBrowserTLSClient(9 * time.Second)
	if a.Transport != b.Transport {
		t.Error("each call built a new transport; the pool should be shared")
	}
	if a.Timeout != 5*time.Second || b.Timeout != 9*time.Second {
		t.Errorf("timeouts = %v, %v; want 5s, 9s", a.Timeout, b.Timeout)
	}
}

// The point of the client is that it does NOT use the shared transport — one
// site needing a browser fingerprint must not change how every other site is
// reached.
func TestBrowserTLSPoolIsSeparateFromTheDefault(t *testing.T) {
	if NewBrowserTLSClient(time.Second).Transport == NewClient(time.Second).Transport {
		t.Error("browser client shares the default transport")
	}
	if NewBrowserTLSClient(time.Second).Transport == NewLegacyTLSClient(time.Second).Transport {
		t.Error("browser client shares the legacy-TLS transport")
	}
}

// It completes a real handshake, and verification is still on: the httptest
// server presents an untrusted certificate, so the request must fail on that
// rather than sail through.
func TestBrowserTLSVerifiesCertificates(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	resp, err := NewBrowserTLSClient(10 * time.Second).Get(srv.URL)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("request to a server with an untrusted certificate succeeded")
	}
	// utls wraps verification failures in its own error type rather than
	// crypto/tls's, so match on the x509 cause, which both share.
	var unknown x509.UnknownAuthorityError
	if !errors.As(err, &unknown) {
		t.Errorf("error was %v, want a certificate-verification failure", err)
	}
}

// The success path: a real handshake with a browser ClientHello, and the
// request completing over HTTP/2. This is what the transport exists to do, so
// it is worth proving offline rather than only against a live site.
func TestBrowserTLSCompletesRequestOverHTTP2(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.Proto))
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	prev := browserRootCAs
	browserRootCAs = pool
	t.Cleanup(func() { browserRootCAs = prev })

	resp, err := NewBrowserTLSClient(15 * time.Second).Get(srv.URL)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if got := string(body); got != "HTTP/2.0" {
		t.Errorf("server saw %q, want HTTP/2.0 — the transport must not fall back", got)
	}
}

// A dial that cannot connect must surface as an error rather than a nil conn.
func TestDialBrowserTLSReportsDialFailure(t *testing.T) {
	// Port 1 on the loopback interface: nothing listens, connection refused.
	if _, err := dialBrowserTLS(context.Background(), "tcp", "127.0.0.1:1"); err == nil {
		t.Error("dial to a closed port succeeded")
	}
}

func TestDialBrowserTLSRejectsAnAddressWithNoPort(t *testing.T) {
	if _, err := dialBrowserTLS(context.Background(), "tcp", "127.0.0.1"); err == nil {
		t.Error("dial with a portless address succeeded")
	}
}

// trustServer points the browser dialer's root pool at srv for one test.
func trustServer(t *testing.T, srv *httptest.Server) {
	t.Helper()
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	prev := browserRootCAs
	browserRootCAs = pool
	t.Cleanup(func() { browserRootCAs = prev })
}

// The transport is told the connection is plain HTTP/2, so the caller must
// never see that: the Host header, a redirect and resp.Request all stay on the
// https URL the caller asked for.
func TestBrowserTLSKeepsTheCallersURL(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, srv.URL+"/end", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte(r.Host))
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()
	trustServer(t, srv)

	resp, err := NewBrowserTLSClient(15 * time.Second).Get(srv.URL + "/start")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)

	if got := resp.Request.URL.String(); got != srv.URL+"/end" {
		t.Errorf("resp.Request.URL = %s, want %s/end", got, srv.URL)
	}
	if want := strings.TrimPrefix(srv.URL, "https://"); string(body) != want {
		t.Errorf("server saw Host %q, want %q", body, want)
	}
}

// A server that will not speak HTTP/2 must fail in the dialer: the transport
// sends HTTP/2 frames without asking, which an HTTP/1.1 server cannot read.
func TestBrowserTLSRefusesAServerWithoutH2(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	trustServer(t, srv)

	resp, err := NewBrowserTLSClient(10 * time.Second).Get(srv.URL)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("request to an HTTP/1.1-only server succeeded")
	}
	if !strings.Contains(err.Error(), "not h2") {
		t.Errorf("error was %v, want the dialer's h2 refusal", err)
	}
}

// Only https is carried: an http URL would otherwise be sent over a TLS
// handshake to whatever port it names.
func TestBrowserTLSRefusesPlainHTTP(t *testing.T) {
	resp, err := NewBrowserTLSClient(time.Second).Get("http://127.0.0.1:1/")
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("plain http URL was accepted")
	}
	if !strings.Contains(err.Error(), "refusing http URL") {
		t.Errorf("error was %v, want the scheme refusal", err)
	}
}
