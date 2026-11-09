package gitlab

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cellestias/git-tide/internal/httpx"
	"github.com/cellestias/git-tide/internal/provider"
)

func noSleep(t *testing.T) {
	t.Helper()
	old := httpx.DefaultPolicy
	httpx.DefaultPolicy.Sleep = func(context.Context, time.Duration) error { return nil }
	t.Cleanup(func() { httpx.DefaultPolicy = old })
}

func serverClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &Client{BaseURL: srv.URL, HTTP: srv.Client(), Token: "tok"}
}

const oneMR = `{"iid":4,"title":"d","state":"opened","source_branch":"f/d","target_branch":"main","author":{"username":"ana"}}`

func TestGetRetriesATransientFailure(t *testing.T) {
	noSleep(t)
	var calls atomic.Int32
	c := serverClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(oneMR))
	})
	pr, err := c.GetPullRequest(context.Background(), Repo{Owner: "acme", Name: "tool"}, 4)
	if err != nil || pr.Number != 4 || calls.Load() != 2 {
		t.Fatalf("%+v %v after %d calls", pr, err, calls.Load())
	}
}

func TestCreateIsNeverRepeated(t *testing.T) {
	noSleep(t)
	var calls atomic.Int32
	c := serverClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	_, err := c.CreatePullRequest(context.Background(), Repo{Owner: "acme", Name: "tool"}, provider.NewPullRequest{Title: "t", Head: "f", Base: "main"})
	if err == nil || calls.Load() != 1 {
		t.Fatalf("%v after %d calls", err, calls.Load())
	}
}

func TestRateLimitSurvivesTheRetries(t *testing.T) {
	noSleep(t)
	var calls atomic.Int32
	c := serverClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	})
	_, err := c.GetPullRequest(context.Background(), Repo{Owner: "acme", Name: "tool"}, 4)
	if err == nil || !strings.Contains(err.Error(), "rate limit") || calls.Load() != 3 {
		t.Fatalf("%v after %d calls", err, calls.Load())
	}
}
