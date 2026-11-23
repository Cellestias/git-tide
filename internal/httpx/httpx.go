// Package httpx is the HTTP plumbing shared by the hosting-provider adapters:
// one bounded request with a retry policy for transient failures, and the
// page walker for list endpoints. Adapters keep what is theirs (headers, status
// mapping, JSON shapes); nothing here knows about GitHub or GitLab.
package httpx

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"syscall"
	"time"

	"github.com/cellestias/git-tide/internal/dryrun"
)

// maxBody caps how much of a response is read.
const maxBody = 4 << 20

// Request is one HTTP call. Header values are set as given.
type Request struct {
	Method  string
	URL     string
	Payload []byte
	Header  map[string]string
}

// Response is a completed call. Any status counts as a response; mapping
// statuses to errors is the adapter's job.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// Policy decides when and how a failed call is repeated. Zero fields take the
// values of DefaultPolicy, except Sleep and Jitter, whose zero values use the
// real clock and random full jitter.
type Policy struct {
	// Attempts is the total number of tries, first one included.
	Attempts int
	// Base is the backoff ceiling after the first failure; it doubles each time.
	Base time.Duration
	// Max caps the backoff ceiling.
	Max time.Duration
	// MaxRetryAfter is the longest server-requested wait honoured. A longer
	// Retry-After is not waited for: the response is returned as it is.
	MaxRetryAfter time.Duration
	// Sleep waits for d or until ctx ends. Tests replace it.
	Sleep func(ctx context.Context, d time.Duration) error
	// Jitter picks the wait in [0, ceiling]. Tests replace it.
	Jitter func(ceiling time.Duration) time.Duration
}

// DefaultPolicy is what the adapters use. Three tries and short waits keep a
// failing command from hanging: a CLI is a short-lived process, so there is no
// circuit breaker, only a bounded retry.
var DefaultPolicy = Policy{
	Attempts:      3,
	Base:          300 * time.Millisecond,
	Max:           2 * time.Second,
	MaxRetryAfter: 10 * time.Second,
}

func (p Policy) normalized() Policy {
	d := DefaultPolicy
	if p.Attempts > 0 {
		d.Attempts = p.Attempts
	}
	if p.Base > 0 {
		d.Base = p.Base
	}
	if p.Max > 0 {
		d.Max = p.Max
	}
	if p.MaxRetryAfter > 0 {
		d.MaxRetryAfter = p.MaxRetryAfter
	}
	d.Sleep, d.Jitter = p.Sleep, p.Jitter
	if d.Sleep == nil {
		d.Sleep = sleep
	}
	if d.Jitter == nil {
		d.Jitter = func(c time.Duration) time.Duration { return rand.N(c + 1) } //nolint:gosec // G404: retry jitter, not a secret
	}
	return d
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Do sends req and reads the body (capped). Only idempotent requests (GET,
// HEAD) are repeated, and only after a timeout, a reset connection, or a 429,
// 502, 503 or 504. Anything else is returned at once: repeating a POST could
// create the pull request twice. The returned error never contains the URL, so
// it cannot leak a token that travels in a query string.
func Do(ctx context.Context, hc *http.Client, req Request, pol Policy) (Response, error) {
	if plan := dryrun.From(ctx); plan != nil && req.Method != http.MethodGet && req.Method != http.MethodHead {
		// A dry run reads from the provider but never writes to it. The call is
		// recorded and answered with an empty success, so the use case goes on.
		plan.Add(req.Method + " " + redactURL(req.URL))
		return Response{Status: http.StatusOK, Body: []byte("{}")}, nil
	}
	pol = pol.normalized()
	attempts := pol.Attempts
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		attempts = 1
	}
	for attempt := 1; ; attempt++ {
		resp, err := once(ctx, hc, req)
		if attempt >= attempts {
			return resp, err
		}
		wait, retry := decide(ctx, resp, err, attempt, pol)
		if !retry {
			return resp, err
		}
		if serr := pol.Sleep(ctx, wait); serr != nil {
			return Response{}, serr
		}
	}
}

func once(ctx context.Context, hc *http.Client, r Request) (Response, error) {
	var body io.Reader
	if r.Payload != nil {
		body = bytes.NewReader(r.Payload)
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, r.URL, body)
	if err != nil {
		return Response{}, redact(err)
	}
	for k, v := range r.Header {
		req.Header.Set(k, v)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return Response{}, redact(err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return Response{}, err
	}
	return Response{Status: resp.StatusCode, Header: resp.Header, Body: data}, nil
}

// decide says whether to try again and how long to wait first.
func decide(ctx context.Context, resp Response, err error, attempt int, pol Policy) (time.Duration, bool) {
	if ctx.Err() != nil {
		return 0, false
	}
	if err != nil {
		if !transient(err) {
			return 0, false
		}
		return backoff(attempt, pol), true
	}
	switch resp.Status {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
	default:
		return 0, false
	}
	if ra, ok := retryAfter(resp.Header.Get("Retry-After")); ok {
		if ra > pol.MaxRetryAfter {
			return 0, false
		}
		return ra, true
	}
	return backoff(attempt, pol), true
}

// backoff is exponential with full jitter: a random wait up to Base*2^(n-1),
// capped at Max. The jitter keeps concurrent clients from retrying in step.
func backoff(attempt int, pol Policy) time.Duration {
	ceiling := pol.Base
	for i := 1; i < attempt && ceiling < pol.Max; i++ {
		ceiling *= 2
	}
	if ceiling > pol.Max {
		ceiling = pol.Max
	}
	return pol.Jitter(ceiling)
}

// retryAfter reads the header as seconds or as an HTTP date.
func retryAfter(v string) (time.Duration, bool) {
	if v == "" {
		return 0, false
	}
	if s, err := strconv.Atoi(v); err == nil && s >= 0 {
		return time.Duration(s) * time.Second, true
	}
	if t, err := http.ParseTime(v); err == nil {
		return max(time.Until(t), 0), true
	}
	return 0, false
}

func transient(err error) bool {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	return errors.Is(err, syscall.ECONNRESET) || errors.Is(err, io.ErrUnexpectedEOF)
}

// redact drops the URL from *url.Error so it cannot leak into output.
func redact(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// redactURL is the URL without its query string or credentials, which can hold a token.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "(unreadable address)"
	}
	u.User, u.RawQuery, u.Fragment = nil, "", ""
	return u.String()
}
