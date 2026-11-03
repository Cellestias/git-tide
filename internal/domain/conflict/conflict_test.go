package conflict

import "testing"

func TestIsMarker(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{"<<<<<<< HEAD", true},
		{">>>>>>> feature/x", true},
		{"<<<<<<<", true},
		{"<<<<<<< HEAD // tide:allow-conflict-marker", false},
		{"=======", false},                // Markdown heading underline
		{"    <<<<<<< HEAD", false},       // indented: not written by Git
		{"<<<<<<<<< not a marker", false}, // longer run
		{"<<<<<< short", false},
		{"a <<<<<<< b", false},
		{"", false},
	}
	for _, c := range cases {
		if got := IsMarker(c.line); got != c.want {
			t.Errorf("IsMarker(%q) = %v, want %v", c.line, got, c.want)
		}
	}
}
