package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cellestias/git-tide/internal/dryrun"
)

// fast returns a policy that never really sleeps and records the waits.
func fast(waits *[]time.Duration) Policy {
	return Policy{
		Attempts: 3,
		Base:     100 * time.Millisecond,
		Max:      250 * time.Millisecond,
		Sleep:    func(_ context.Context, d time.Duration) error { *waits = append(*waits, d); return nil },
		Jitter:   func(c time.Duration) time.Duration { return c }, // the ceiling, so waits are predictable
	}
}

// flaky answers with the given statuses in order, then 200.
func flaky(t *testing.T, statuses ...int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := int(n.Add(1)) - 1
		if i < len(statuses) {
			w.WriteHeader(statuses[i])
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

func get(srv *httptest.Server) Request { return Request{Method: http.MethodGet, URL: srv.URL} }

func TestRetriesTransientStatusesThenSucceeds(t *testing.T) {
	for _, status := range []int{429, 502, 503, 504} {
		srv, calls := flaky(t, status, status)
		var waits []time.Duration
		resp, err := Do(context.Background(), srv.Client(), get(srv), fast(&waits))
		if err != nil || resp.Status != 200 || string(resp.Body) != "ok" {
			t.Fatalf("status %d: %+v %v", status, resp, err)
		}
		if calls.Load() != 3 {
			t.Errorf("status %d: %d calls, want 3", status, calls.Load())
		}
		// exponential: 100ms, then 200ms
		if len(waits) != 2 || waits[0] != 100*time.Millisecond || waits[1] != 200*time.Millisecond {
			t.Errorf("status %d: waits %v", status, waits)
		}
	}
}

func TestGivesUpAfterTheAttemptCapAndReturnsTheLastResponse(t *testing.T) {
	srv, calls := flaky(t, 503, 503, 503, 503, 503)
	var waits []time.Duration
	resp, err := Do(context.Background(), srv.Client(), get(srv), fast(&waits))
	if err != nil || resp.Status != 503 {
		t.Fatalf("%+v %v", resp, err)
	}
	if calls.Load() != 3 {
		t.Errorf("%d calls, want 3", calls.Load())
	}
}

func TestBackoffIsCapped(t *testing.T) {
	srv, _ := flaky(t, 503, 503, 503, 503)
	var waits []time.Duration
	p := fast(&waits)
	p.Attempts = 5
	if _, err := Do(context.Background(), srv.Client(), get(srv), p); err != nil {
		t.Fatal(err)
	}
	want := []time.Duration{100, 200, 250, 250}
	for i, w := range want {
		if waits[i] != w*time.Millisecond {
			t.Fatalf("waits %v", waits)
		}
	}
}

func TestDoesNotRetryPermanentStatuses(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 422, 500} {
		srv, calls := flaky(t, status, status, status)
		var waits []time.Duration
		resp, err := Do(context.Background(), srv.Client(), get(srv), fast(&waits))
		if err != nil || resp.Status != status {
			t.Fatalf("status %d: %+v %v", status, resp, err)
		}
		if calls.Load() != 1 || len(waits) != 0 {
			t.Errorf("status %d: %d calls, waits %v", status, calls.Load(), waits)
		}
	}
}

func TestNeverRepeatsAWrite(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		srv, calls := flaky(t, 503, 503)
		var waits []time.Duration
		req := Request{Method: method, URL: srv.URL, Payload: []byte(`{}`)}
		resp, err := Do(context.Background(), srv.Client(), req, fast(&waits))
		if err != nil || resp.Status != 503 || calls.Load() != 1 {
			t.Errorf("%s: %+v %v, %d calls", method, resp, err, calls.Load())
		}
	}
}

func TestSendsHeadersAndPayload(t *testing.T) {
	var gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
	}))
	defer srv.Close()
	req := Request{Method: http.MethodPost, URL: srv.URL, Payload: []byte(`{"a":1}`), Header: map[string]string{"Authorization": "Bearer x"}}
	if _, err := Do(context.Background(), srv.Client(), req, Policy{}); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer x" || gotBody != `{"a":1}` {
		t.Errorf("%q %q", gotAuth, gotBody)
	}
}

func TestHonoursRetryAfter(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(429)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	var waits []time.Duration
	resp, err := Do(context.Background(), srv.Client(), get(srv), fast(&waits))
	if err != nil || resp.Status != 200 {
		t.Fatalf("%+v %v", resp, err)
	}
	if len(waits) != 1 || waits[0] != 2*time.Second {
		t.Errorf("waits %v, want [2s]", waits)
	}
}

func TestDoesNotWaitForALongRetryAfter(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(429)
	}))
	defer srv.Close()
	var waits []time.Duration
	resp, err := Do(context.Background(), srv.Client(), get(srv), fast(&waits))
	if err != nil || resp.Status != 429 {
		t.Fatalf("%+v %v", resp, err)
	}
	if n.Load() != 1 || len(waits) != 0 {
		t.Errorf("%d calls, waits %v: an hour-long wait must not be taken", n.Load(), waits)
	}
}

func TestRetryAfterParsing(t *testing.T) {
	if d, ok := retryAfter("7"); !ok || d != 7*time.Second {
		t.Errorf("seconds: %v %v", d, ok)
	}
	if d, ok := retryAfter(time.Now().Add(5 * time.Second).UTC().Format(http.TimeFormat)); !ok || d <= 0 || d > 6*time.Second {
		t.Errorf("date: %v %v", d, ok)
	}
	if d, ok := retryAfter(time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)); !ok || d != 0 {
		t.Errorf("past date: %v %v", d, ok)
	}
	for _, bad := range []string{"", "soon", "-3"} {
		if _, ok := retryAfter(bad); ok {
			t.Errorf("%q should not parse", bad)
		}
	}
}

// timeoutRT fails the first n calls with a timeout, then answers 200.
type timeoutRT struct {
	fail  int
	calls int
	err   error
}

func (rt *timeoutRT) RoundTrip(*http.Request) (*http.Response, error) {
	rt.calls++
	if rt.calls <= rt.fail {
		return nil, rt.err
	}
	return &http.Response{StatusCode: 200, Body: http.NoBody, Header: http.Header{}}, nil
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

var _ net.Error = timeoutErr{}

func TestRetriesTimeouts(t *testing.T) {
	rt := &timeoutRT{fail: 2, err: timeoutErr{}}
	var waits []time.Duration
	resp, err := Do(context.Background(), &http.Client{Transport: rt}, Request{Method: http.MethodGet, URL: "http://example.test/x"}, fast(&waits))
	if err != nil || resp.Status != 200 || rt.calls != 3 {
		t.Fatalf("%+v %v after %d calls", resp, err, rt.calls)
	}
}

func TestDoesNotRetryOtherTransportErrors(t *testing.T) {
	rt := &timeoutRT{fail: 5, err: errors.New("x509: certificate signed by unknown authority")}
	var waits []time.Duration
	_, err := Do(context.Background(), &http.Client{Transport: rt}, Request{Method: http.MethodGet, URL: "http://example.test/x"}, fast(&waits))
	if err == nil || rt.calls != 1 {
		t.Fatalf("%v after %d calls", err, rt.calls)
	}
}

func TestTransportErrorsDoNotLeakTheURL(t *testing.T) {
	rt := &timeoutRT{fail: 1, err: errors.New("boom")}
	_, err := Do(context.Background(), &http.Client{Transport: rt}, Request{Method: http.MethodGet, URL: "http://example.test/x?token=SECRET"}, Policy{Attempts: 1})
	if err == nil || strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "example.test") {
		t.Fatalf("error leaks the URL: %v", err)
	}
}

func TestStopsWaitingWhenTheContextEnds(t *testing.T) {
	srv, calls := flaky(t, 503, 503, 503)
	ctx, cancel := context.WithCancel(context.Background())
	p := Policy{
		Attempts: 3,
		Sleep: func(ctx context.Context, d time.Duration) error {
			cancel()
			return sleep(ctx, time.Hour) // returns at once: ctx is cancelled
		},
	}
	_, err := Do(ctx, srv.Client(), get(srv), p)
	if !errors.Is(err, context.Canceled) || calls.Load() != 1 {
		t.Fatalf("%v after %d calls", err, calls.Load())
	}
}

func TestACancelledContextIsNotRetried(t *testing.T) {
	srv, calls := flaky(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var waits []time.Duration
	if _, err := Do(ctx, srv.Client(), get(srv), fast(&waits)); err == nil {
		t.Fatal("want an error")
	}
	if calls.Load() != 0 || len(waits) != 0 {
		t.Errorf("%d calls, waits %v", calls.Load(), waits)
	}
}

func TestBodyIsCapped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("a", maxBody+1000)))
	}))
	defer srv.Close()
	resp, err := Do(context.Background(), srv.Client(), get(srv), Policy{})
	if err != nil || len(resp.Body) != maxBody {
		t.Fatalf("%d bytes, %v", len(resp.Body), err)
	}
}

// pages serves n entries over per_page/page and counts requests.
func pages(t *testing.T, total int) (Pager, *atomic.Int32) {
	t.Helper()
	var reqs atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqs.Add(1)
		var per, page int
		_ = json.Unmarshal([]byte(r.URL.Query().Get("per_page")), &per)
		_ = json.Unmarshal([]byte(r.URL.Query().Get("page")), &page)
		var out []int
		for i := (page - 1) * per; i < page*per && i < total; i++ {
			out = append(out, i)
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)
	p := Pager{
		Label: "Test", PageSize: 10, MaxPages: 3,
	}
	p.Get = func(ctx context.Context, u string) ([]byte, error) {
		resp, err := Do(ctx, srv.Client(), Request{Method: http.MethodGet, URL: srv.URL + u[strings.Index(u, "?"):]}, Policy{})
		return resp.Body, err
	}
	return p, &reqs
}

func count(n *int) func([]json.RawMessage) error {
	return func(items []json.RawMessage) error { *n += len(items); return nil }
}

func TestPagerStopsOnAShortPage(t *testing.T) {
	p, reqs := pages(t, 15)
	var n int
	truncated, err := p.Each(context.Background(), "http://x/items", count(&n))
	if err != nil || truncated || n != 15 || reqs.Load() != 2 {
		t.Fatalf("n=%d truncated=%v err=%v reqs=%d", n, truncated, err, reqs.Load())
	}
}

func TestPagerReportsTruncation(t *testing.T) {
	p, reqs := pages(t, 1000)
	var n int
	truncated, err := p.Each(context.Background(), "http://x/items", count(&n))
	if err != nil || !truncated || n != 30 || reqs.Load() != 3 {
		t.Fatalf("n=%d truncated=%v err=%v reqs=%d", n, truncated, err, reqs.Load())
	}
}

func TestPagerKeepsExistingQueryParameters(t *testing.T) {
	var got string
	p := Pager{Label: "Test", PageSize: 5, MaxPages: 1, Get: func(_ context.Context, u string) ([]byte, error) {
		got = u
		return []byte("[]"), nil
	}}
	if _, err := p.Each(context.Background(), "http://x/items?state=open", func([]json.RawMessage) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if got != "http://x/items?state=open&per_page=5&page=1" {
		t.Errorf("url %q", got)
	}
}

func TestPagerErrStopIsNotAnError(t *testing.T) {
	p, reqs := pages(t, 1000)
	truncated, err := p.Each(context.Background(), "http://x/items", func([]json.RawMessage) error { return ErrStop })
	if err != nil || truncated || reqs.Load() != 1 {
		t.Fatalf("truncated=%v err=%v reqs=%d", truncated, err, reqs.Load())
	}
}

func TestPagerWrapsDecodeErrors(t *testing.T) {
	p := Pager{Label: "Acme", PageSize: 5, MaxPages: 1, Get: func(context.Context, string) ([]byte, error) { return []byte("{not a list"), nil }}
	_, err := p.Each(context.Background(), "http://x", func([]json.RawMessage) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "decode Acme API response") {
		t.Fatalf("%v", err)
	}
	boom := errors.New("boom")
	p.Get = func(context.Context, string) ([]byte, error) { return []byte("[]"), nil }
	_, err = p.Each(context.Background(), "http://x", func([]json.RawMessage) error { return boom })
	if !errors.Is(err, boom) || !strings.Contains(err.Error(), "decode Acme API response") {
		t.Fatalf("%v", err)
	}
}

func TestPagerPassesFetchErrorsThrough(t *testing.T) {
	boom := errors.New("not found")
	p := Pager{Label: "Acme", PageSize: 5, MaxPages: 2, Get: func(context.Context, string) ([]byte, error) { return nil, boom }}
	if _, err := p.Each(context.Background(), "http://x", func([]json.RawMessage) error { return nil }); !errors.Is(err, boom) {
		t.Fatalf("%v", err)
	}
}

func TestDryRunSendsNoWrite(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{"read":true}`))
	}))
	defer srv.Close()
	plan := &dryrun.Plan{}
	ctx := dryrun.With(context.Background(), plan)

	resp, err := Do(ctx, srv.Client(), Request{Method: http.MethodGet, URL: srv.URL}, Policy{})
	if err != nil || hits != 1 || !strings.Contains(string(resp.Body), "read") {
		t.Fatalf("a GET must still go out: %+v %v hits=%d", resp, err, hits)
	}
	resp, err = Do(ctx, srv.Client(), Request{Method: http.MethodPost, URL: srv.URL + "/pulls?access_token=SECRET", Payload: []byte("{}")}, Policy{})
	if err != nil || hits != 1 || resp.Status != 200 {
		t.Fatalf("a POST must not go out: %+v %v hits=%d", resp, err, hits)
	}
	steps := plan.Steps()
	if len(steps) != 1 || !strings.HasPrefix(steps[0], "POST ") || strings.Contains(steps[0], "SECRET") {
		t.Errorf("steps = %v", steps)
	}
}
