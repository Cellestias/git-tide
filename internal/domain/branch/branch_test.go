package branch

import "testing"

func TestValidName(t *testing.T) {
	good := []string{"main", "feature/login", "release/1.2.0", "fix/a_b-c"}
	bad := []string{"", "-rf", "a..b", "a b", "a/", "/a", "a//b", "x.lock", "a~1", "a:b", "a@{1}", "feature/.hidden", "a\x00b", "a*"}
	for _, n := range good {
		if err := ValidName(n); err != nil {
			t.Errorf("ValidName(%q) = %v, want nil", n, err)
		}
	}
	for _, n := range bad {
		if err := ValidName(n); err == nil {
			t.Errorf("ValidName(%q) = nil, want error", n)
		}
	}
}

func TestMatch(t *testing.T) {
	tests := []struct {
		pattern, name string
		want          bool
	}{
		{"feature/*", "feature/login", true},
		{"feature/*", "feature/a/b", false},
		{"feature/**", "feature/a/b", true},
		{"main", "main", true},
		{"main", "mainline", false},
		{"fix/*", "feature/x", false},
		{"release/1.*", "release/1.2", true},
		{"a.b", "axb", false},
	}
	for _, tt := range tests {
		if got := Match(tt.pattern, tt.name); got != tt.want {
			t.Errorf("Match(%q, %q) = %v, want %v", tt.pattern, tt.name, got, tt.want)
		}
	}
}

func TestResolveBase(t *testing.T) {
	m := map[string]string{
		"feature/*":     "stage",
		"feature/hot-*": "main",
		"release/*":     "main",
		"stage":         "main",
	}
	tests := []struct{ current, want string }{
		{"feature/login", "stage"},
		{"feature/hot-fix", "main"}, // more specific glob beats broader one
		{"release/1.0", "main"},
		{"stage", "main"},
		{"chore/x", "dev"}, // default
	}
	for _, tt := range tests {
		if got := ResolveBase(tt.current, "dev", m); got != tt.want {
			t.Errorf("ResolveBase(%q) = %q, want %q", tt.current, got, tt.want)
		}
	}
}
