package policy

import "testing"

func TestProtection(t *testing.T) {
	p := Protection{Protected: []string{"main", "stage"}}
	if d := p.Commit("main"); d.Allowed || d.Reason == "" {
		t.Errorf("commit on main: %+v", d)
	}
	if !p.Commit("feature/x").Allowed {
		t.Error("commit on feature/x should be allowed")
	}
	if !p.Commit("").Allowed {
		t.Error("detached HEAD should be allowed")
	}
	if p.Push("stage").Allowed {
		t.Error("push to stage should be denied")
	}
	if !p.Push("feature/x").Allowed {
		t.Error("push to feature/x should be allowed")
	}
}
