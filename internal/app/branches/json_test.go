package branches

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/cellestias/git-tide/internal/github"
)

type branchDoc struct {
	Name       string `json:"name"`
	Current    bool   `json:"current"`
	RemoteOnly bool   `json:"remote_only"`
	Author     string `json:"author"`
	Upstream   *struct {
		Name   string `json:"name"`
		Ahead  int    `json:"ahead"`
		Behind int    `json:"behind"`
	} `json:"upstream"`
	Base *struct {
		Name   string `json:"name"`
		Ahead  int    `json:"ahead"`
		Behind int    `json:"behind"`
	} `json:"base"`
	Protected bool `json:"protected"`
	Merged    bool `json:"merged"`
	Hosted    *struct {
		Protected *bool `json:"protected"`
		Request   *int  `json:"request"`
	} `json:"hosted"`
}

func readBranches(t *testing.T, out string) map[string]branchDoc {
	t.Helper()
	var list []branchDoc
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	byName := map[string]branchDoc{}
	for _, b := range list {
		byName[b.Name] = b
	}
	return byName
}

func TestJSON(t *testing.T) {
	f := setup(t)
	out, errOut, err := run(t, f, cfgFor(), nil, Options{JSON: true})
	if err != nil {
		t.Fatal(err)
	}
	if errOut != "" {
		t.Errorf("stderr = %q", errOut)
	}
	got := readBranches(t, out)
	if b := got["feature/new"]; !b.Current || b.Upstream == nil || b.Upstream.Name != "origin/feature/new" || b.Upstream.Ahead != 0 ||
		b.Base == nil || b.Base.Name != "main" || b.Base.Ahead != 2 || b.Merged || b.Hosted != nil {
		t.Errorf("feature/new = %+v", b)
	}
	if b := got["feature/old"]; b.Author != "Old Dev" || b.Upstream != nil || b.Base == nil || b.Base.Ahead != 1 || b.Current {
		t.Errorf("feature/old = %+v", b)
	}
	if b := got["fix/done"]; !b.Merged || b.Base == nil || b.Base.Behind != 1 {
		t.Errorf("fix/done = %+v", b)
	}
	if b := got["main"]; !b.Protected || b.Base != nil {
		t.Errorf("main = %+v", b)
	}
}

func TestJSONEmptyIsAnEmptyList(t *testing.T) {
	f := setup(t)
	out, _, err := run(t, f, cfgFor(), nil, Options{JSON: true, Mine: true, StaleDays: 100000})
	if err != nil || strings.TrimSpace(out) != "[]" {
		t.Fatalf("err=%v out=%q", err, out)
	}
}

func TestJSONHosted(t *testing.T) {
	f := setup(t)
	githubRemote(t, f)
	gh := fakeGH{
		protected: map[string]bool{"main": true, "feature/new": false},
		prs:       []github.PullRequest{{Number: 12, HeadRef: "feature/new", HeadRepo: "acme/tool"}},
	}
	out, _, err := run(t, f, cfgFor(), gh, Options{JSON: true, Provider: true})
	if err != nil {
		t.Fatal(err)
	}
	got := readBranches(t, out)
	if h := got["feature/new"].Hosted; h == nil || h.Protected == nil || *h.Protected || h.Request == nil || *h.Request != 12 {
		t.Errorf("feature/new hosted = %+v", h)
	}
	if h := got["main"].Hosted; h == nil || h.Protected == nil || !*h.Protected || h.Request != nil {
		t.Errorf("main hosted = %+v", h)
	}

	// When the provider cannot be asked the fields are null, not false.
	bad := fakeGH{protectedErr: github.ErrNotFound, prsErr: errors.New("boom")}
	out, errOut, err := run(t, f, cfgFor(), bad, Options{JSON: true, Provider: true})
	if err != nil {
		t.Fatal(err)
	}
	if h := readBranches(t, out)["main"].Hosted; h == nil || h.Protected != nil || h.Request != nil {
		t.Errorf("degraded hosted = %+v", h)
	}
	if !strings.Contains(errOut, "cannot read branch protection") {
		t.Errorf("stderr = %q", errOut)
	}
}
