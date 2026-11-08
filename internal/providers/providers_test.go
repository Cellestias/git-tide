package providers

import (
	"testing"

	"github.com/cellestias/git-tide/internal/provider"
)

func TestResolve(t *testing.T) {
	if n, err := Resolve("auto", "https://gitlab.com/g/p.git"); err != nil || n != provider.GitLab {
		t.Errorf("auto gitlab: %q %v", n, err)
	}
	// the URL as written wins; the second one is the insteadOf-rewritten fallback
	if n, err := Resolve("auto", "https://github.com/a/b.git", "/tmp/bare.git"); err != nil || n != provider.GitHub {
		t.Errorf("auto github: %q %v", n, err)
	}
	if n, err := Resolve("auto", "", "https://github.com/a/b.git"); err != nil || n != provider.GitHub {
		t.Errorf("auto fallback: %q %v", n, err)
	}
	if _, err := Resolve("auto", "https://example.com/a/b.git"); err == nil {
		t.Error("unknown host must fail in auto mode")
	}
	// explicit wins over whatever the remote looks like (self-hosted GitLab)
	if n, err := Resolve("gitlab", "https://git.example.com/a/b.git"); err != nil || n != provider.GitLab {
		t.Errorf("explicit: %q %v", n, err)
	}
	if _, err := Resolve("bitbucket"); err == nil {
		t.Error("unknown provider must fail")
	}
}

func TestNewAndTokenKeys(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITLAB_TOKEN", "")
	t.Setenv("TIDE_GITLAB_API_URL", "")
	for name, noun := range map[string]string{provider.GitHub: "pull request", provider.GitLab: "merge request"} {
		p, err := New(name, "gitlab.example.com", "tok")
		if err != nil || p.Name() != name || p.Noun() != noun || !p.HasToken() {
			t.Errorf("%s: %v %v", name, p, err)
		}
	}
	if keys := TokenKeys(provider.GitLab); len(keys) != 1 || keys[0] != "GITLAB_TOKEN" {
		t.Errorf("gitlab keys: %v", keys)
	}
	if _, err := New("x", "", ""); err == nil {
		t.Error("unknown provider must fail")
	}
}
