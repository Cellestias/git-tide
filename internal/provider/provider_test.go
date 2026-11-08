package provider

import "testing"

func TestDetectName(t *testing.T) {
	cases := map[string]string{
		"https://github.com/acme/tool.git":          GitHub,
		"git@github.com:acme/tool.git":              GitHub,
		"ssh://git@github.com/acme/tool.git":        GitHub,
		"https://user:tok@github.com/acme/tool":     GitHub,
		"https://gitlab.com/group/proj.git":         GitLab,
		"git@gitlab.com:group/sub/proj.git":         GitLab,
		"ssh://git@gitlab.example.org:2222/g/p.git": GitLab,
		"https://gitlab.internal.corp/g/p":          GitLab,
		"https://bitbucket.org/a/b.git":             "",
		"https://example.com/gitlab.com/a":          "",
		"/srv/git/repo.git":                         "",
		"":                                          "",
	}
	for in, want := range cases {
		if got := DetectName(in); got != want {
			t.Errorf("DetectName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidState(t *testing.T) {
	for _, s := range []string{StateOpen, StateClosed, StateMerged, StateAll} {
		if !ValidState(s) {
			t.Errorf("%q should be valid", s)
		}
	}
	for _, s := range []string{"", "opened", "OPEN", "draft"} {
		if ValidState(s) {
			t.Errorf("%q should be invalid", s)
		}
	}
}
