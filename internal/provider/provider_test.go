package provider

import (
	"errors"
	"testing"
)

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

func TestNothingChecked(t *testing.T) {
	off := func(src string) SourceResult { return SourceResult{Source: src, Enabled: false} }
	on := func(src string) SourceResult { return SourceResult{Source: src, Enabled: true} }
	failed := SourceResult{Source: SourceDependabot, Enabled: true, Err: errors.New("denied")}
	for name, tc := range map[string]struct {
		sources []SourceResult
		want    bool
	}{
		"no sources at all":           {nil, false},
		"both off":                    {[]SourceResult{off(SourceDependabot), off(SourceCodeScanning)}, true},
		"one on":                      {[]SourceResult{on(SourceDependabot), off(SourceCodeScanning)}, false},
		"both on":                     {[]SourceResult{on(SourceDependabot), on(SourceCodeScanning)}, false},
		"one off and one that failed": {[]SourceResult{off(SourceCodeScanning), failed}, false},
	} {
		if got := (SecurityReport{Sources: tc.sources}).NothingChecked(); got != tc.want {
			t.Errorf("%s: NothingChecked = %v, want %v", name, got, tc.want)
		}
	}
}
