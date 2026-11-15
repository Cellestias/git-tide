package repository

import (
	"reflect"
	"testing"
)

func TestParseRefs(t *testing.T) {
	tests := []struct {
		in   string
		want []Ref
	}{
		{"", nil},
		{"HEAD -> refs/heads/feature/x, tag: refs/tags/v1.0.0, refs/remotes/origin/feature/x, refs/heads/main",
			[]Ref{
				{"feature/x", RefBranch, true}, {"v1.0.0", RefTag, false},
				{"origin/feature/x", RefRemote, false}, {"main", RefBranch, false},
			}},
		{"HEAD, refs/heads/main", []Ref{{"HEAD", RefHead, true}, {"main", RefBranch, false}}},
		// a local branch and a remote one may both contain a slash: the full name tells them apart
		{"refs/heads/origin/main, refs/remotes/origin/main", []Ref{{"origin/main", RefBranch, false}, {"origin/main", RefRemote, false}}},
		// remote HEAD pointers and refs that are not branches or tags are left out
		{"refs/remotes/origin/HEAD, refs/stash, refs/notes/commits, refs/tide/review/pr-1", nil},
	}
	for _, tt := range tests {
		if got := parseRefs(tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("parseRefs(%q) = %+v, want %+v", tt.in, got, tt.want)
		}
	}
}
