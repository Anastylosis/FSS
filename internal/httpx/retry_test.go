package httpx

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// retryClient builds a client whose backoff does not actually sleep.
func retryClient(attempts int) *http.Client {
	return &http.Client{Transport: &retryTransport{
		base:     sharedTransport,
		attempts: attempts,
		timeout:  5 * time.Second,
		sleep:    func(context.Context, time.Duration) error { return nil },
	}}
}

func TestRetryClientRetriesServerErrors(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()

	resp, err := retryClient(3).Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "ok" {
		t.Errorf("got %d %q", resp.StatusCode, body)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("server saw %d requests, want 3", got)
	}
}

// A POST body has to survive the replay, or the retry sends an empty request.
func TestRetryClientReplaysBody(t *testing.T) {
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		if len(bodies) < 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()

	resp, err := retryClient(3).Post(srv.URL, "application/json", strings.NewReader(`{"q":1}`))
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if len(bodies) != 2 || bodies[0] != bodies[1] {
		t.Errorf("bodies = %q, want the same payload twice", bodies)
	}
}

// 4xx is the caller's to interpret: stash-go turns it into an HTTPError that
// quotes the server's own message, which needs the body intact.
func TestRetryClientPassesClientErrorsThroughWithBody(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, "bad api key")
	}))
	defer srv.Close()

	resp, err := retryClient(3).Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusUnauthorized || string(body) != "bad api key" {
		t.Errorf("got %d %q", resp.StatusCode, body)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("server saw %d requests, want 1 — 4xx must not be retried", got)
	}
}

// Exhausting the attempts on a retryable status returns that response rather
// than a bare error, so the caller still sees the status and body.
func TestRetryClientReturnsLastResponseWhenAttemptsRunOut(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "down")
	}))
	defer srv.Close()

	resp, err := retryClient(2).Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusServiceUnavailable || string(body) != "down" {
		t.Errorf("got %d %q", resp.StatusCode, body)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("server saw %d requests, want 2", got)
	}
}

func TestRetryClientGivesUpOnNetworkFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	if _, err := retryClient(2).Get(url); err == nil {
		t.Fatal("want an error against a closed server")
	}
}

// The timeout bounds each attempt, not the sequence: a request that outlives it
// must fail rather than hang.
func TestRetryClientTimesOutAnAttempt(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
	}))
	defer srv.Close()
	defer close(release)

	c := &http.Client{Transport: &retryTransport{
		base:     sharedTransport,
		attempts: 1,
		timeout:  50 * time.Millisecond,
		sleep:    func(context.Context, time.Duration) error { return nil },
	}}
	if _, err := c.Get(srv.URL); err == nil {
		t.Fatal("want a timeout error")
	}
}

func TestNewRetryClientHasNoOverallDeadline(t *testing.T) {
	if c := NewRetryClient(time.Second); c.Timeout != 0 {
		t.Errorf("Timeout = %v, want 0 — the budget is per attempt", c.Timeout)
	}
}

// hijackAfterRead answers by reading the request and dropping the connection,
// the way a server that processed a mutation but lost its response does.
func hijackAfterRead(calls *atomic.Int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		calls.Add(1)
		conn, _, _ := w.(http.Hijacker).Hijack()
		_ = conn.Close()
	}))
}

func TestRetryClientDoesNotReplayWrittenPOST(t *testing.T) {
	var calls atomic.Int32
	srv := hijackAfterRead(&calls)
	defer srv.Close()

	resp, err := retryClient(3).Post(srv.URL, "application/json", strings.NewReader(`{}`))
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("want an error")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("server saw %d POSTs, want 1", got)
	}
}

func TestGraphQLRetryClientQueryVsMutation(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       int32
	}{
		{"query", `{"query":"{ findTags { count } }"}`, 3},
		{"mutation", `{"query":"mutation { tagCreate }"}`, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := hijackAfterRead(&calls)
			defer srv.Close()
			c := &http.Client{Transport: &retryTransport{
				base: sharedTransport, attempts: 3, timeout: 5 * time.Second,
				idempotent: graphQLQuery,
				sleep:      func(context.Context, time.Duration) error { return nil },
			}}
			resp, err := c.Post(srv.URL, "application/json", strings.NewReader(tc.body))
			if err == nil {
				_ = resp.Body.Close()
			}
			if got := calls.Load(); got != tc.want {
				t.Errorf("server saw %d requests, want %d", got, tc.want)
			}
		})
	}
}

func TestDoPOSTIdempotentOptIn(t *testing.T) {
	for _, tc := range []struct {
		name       string
		idempotent bool
		want       int32
	}{{"default", false, 1}, {"opt-in", true, 3}} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := hijackAfterRead(&calls)
			defer srv.Close()
			_, _ = Do(context.Background(), srv.Client(), Request{
				URL: srv.URL, Body: []byte(`x`), Idempotent: tc.idempotent,
				BackoffSleep: func(context.Context, time.Duration) error { return nil },
			})
			if got := calls.Load(); got != tc.want {
				t.Errorf("server saw %d requests, want %d", got, tc.want)
			}
		})
	}
}
