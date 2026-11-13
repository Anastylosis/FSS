package httpx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/Anastylosis/FSS/scraper"
)

// NewRetryClient returns an http.Client that retries the way Do does — network
// errors, 429 and 5xx, with the same jittered backoff — for libraries that take
// an *http.Client and drive the request themselves.
//
// timeout bounds each attempt rather than the sequence, so a retried request
// gets the full budget every time. Non-retryable responses, 4xx included, come
// back untouched with their body intact.
//
// Only idempotent requests are replayed after the request was written: a POST
// is retried only when nothing reached the server (a dial failure, or 429).
func NewRetryClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Transport: &retryTransport{
			base:     sharedTransport,
			attempts: 3,
			timeout:  timeout,
		},
	}
}

// NewGraphQLRetryClient is NewRetryClient for a GraphQL endpoint: a POST whose
// body is a query is replayed like a GET, one containing "mutation" is not.
func NewGraphQLRetryClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Transport: &retryTransport{
			base:       sharedTransport,
			attempts:   3,
			timeout:    timeout,
			idempotent: graphQLQuery,
		},
	}
}

func idempotentMethod(m string) bool {
	switch m {
	case "", http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPut, http.MethodDelete, http.MethodTrace:
		return true
	}
	return false
}

// requestNotSent reports an error that proves the server never saw the request.
func requestNotSent(err error) bool {
	var op *net.OpError
	return errors.As(err, &op) && op.Op == "dial"
}

// graphQLQuery reports whether req may be replayed: any non-POST, or a POST
// whose body has no "mutation" (an unreadable body counts as a mutation).
func graphQLQuery(req *http.Request) bool {
	if idempotentMethod(req.Method) {
		return true
	}
	if req.GetBody == nil {
		return false
	}
	rc, err := req.GetBody()
	if err != nil {
		return false
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(io.LimitReader(rc, 4<<20))
	if err != nil {
		return false
	}
	return !bytes.Contains(bytes.ToLower(b), []byte("mutation"))
}

type retryTransport struct {
	base     http.RoundTripper
	attempts int
	timeout  time.Duration
	// idempotent decides whether a request may be replayed after it was
	// written; nil falls back to the method.
	idempotent func(*http.Request) bool
	sleep      func(ctx context.Context, d time.Duration) error // nil uses the default backoff
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	sleep := t.sleep
	if sleep == nil {
		sleep = defaultBackoffSleep
	}

	replayable := idempotentMethod(req.Method)
	if t.idempotent != nil {
		replayable = t.idempotent(req)
	}

	var attemptErrs []error
	for attempt := 0; attempt < t.attempts; attempt++ {
		if attempt > 0 {
			if err := sleep(req.Context(), jitter(time.Duration(attempt)*backoffBase)); err != nil {
				return nil, errors.Join(append(attemptErrs, err)...)
			}
		}

		body, err := replayBody(req, attempt)
		if err != nil {
			return nil, errors.Join(append(attemptErrs, err)...)
		}

		ctx, cancel := context.WithTimeout(req.Context(), t.timeout)
		attemptReq := req.Clone(ctx)
		attemptReq.Body = body

		resp, err := t.base.RoundTrip(attemptReq)
		if err != nil {
			cancel()
			scraper.Debugf(2, "  error: %v", err)
			attemptErrs = append(attemptErrs, fmt.Errorf("attempt %d: %w", attempt+1, err))
			if !replayable && !requestNotSent(err) {
				return nil, errors.Join(attemptErrs...)
			}
			continue
		}

		last := attempt == t.attempts-1 || (!replayable && resp.StatusCode != http.StatusTooManyRequests)
		if last || (resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500) {
			resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
			return resp, nil
		}

		drainAndClose(resp)
		cancel()
		attemptErrs = append(attemptErrs, fmt.Errorf("attempt %d: %w", attempt+1, &StatusError{StatusCode: resp.StatusCode}))
	}
	return nil, errors.Join(attemptErrs...)
}

// replayBody returns the body to send. The first attempt consumes the caller's
// reader; later ones need GetBody, which http.NewRequest supplies for the
// in-memory bodies this is used with.
func replayBody(req *http.Request, attempt int) (io.ReadCloser, error) {
	if req.Body == nil {
		return nil, nil
	}
	if attempt == 0 {
		return req.Body, nil
	}
	if req.GetBody == nil {
		return nil, errors.New("request body cannot be replayed")
	}
	body, err := req.GetBody()
	if err != nil {
		return nil, fmt.Errorf("rewinding request body: %w", err)
	}
	return body, nil
}

// cancelOnClose releases the attempt's context once the caller is done with the
// body — cancelling any earlier would cut the read short.
type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}
