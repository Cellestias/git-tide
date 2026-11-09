package github

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cellestias/git-tide/internal/httpx"
	"github.com/cellestias/git-tide/internal/provider"
)

// noSleep makes the shared retry policy instant for the test.
func noSleep(t *testing.T) {
	t.Helper()
	old := httpx.DefaultPolicy
	httpx.DefaultPolicy.Sleep = func(context.Context, time.Duration) error { return nil }
	t.Cleanup(func() { httpx.DefaultPolicy = old })
}

const onePR = `{"number":4,"title":"d","state":"open","user":{"login":"ana"},"head":{"ref":"f/d","repo":null},"base":{"ref":"main"}}`

func TestGetRetriesATransientFailure(t *testing.T) {
	noSleep(t)
	var calls atomic.Int32
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(onePR))
	})
	pr, err := c.GetPullRequest(context.Background(), Repo{Owner: "acme", Name: "tool"}, 4)
	if err != nil || pr.Number != 4 || calls.Load() != 2 {
		t.Fatalf("%+v %v after %d calls", pr, err, calls.Load())
	}
}

func TestCreateIsNeverRepeated(t *testing.T) {
	noSleep(t)
	var calls atomic.Int32
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	_, err := c.CreatePullRequest(context.Background(), Repo{Owner: "acme", Name: "tool"}, provider.NewPullRequest{Title: "t", Head: "f", Base: "main"})
	if err == nil || calls.Load() != 1 {
		t.Fatalf("%v after %d calls: a retried POST could open the pull request twice", err, calls.Load())
	}
}

func TestPersistentOutageStillFailsWithAClearMessage(t *testing.T) {
	noSleep(t)
	var calls atomic.Int32
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	})
	_, err := c.GetPullRequest(context.Background(), Repo{Owner: "acme", Name: "tool"}, 4)
	if err == nil || !strings.Contains(err.Error(), "502") || calls.Load() != 3 {
		t.Fatalf("%v after %d calls", err, calls.Load())
	}
}
