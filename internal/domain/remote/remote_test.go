package remote

import "testing"

func TestValidURL(t *testing.T) {
	good := []string{
		"https://github.com/o/r.git", "http://git.internal/o/r", "ssh://git@host:2222/o/r.git",
		"git@github.com:o/r.git", "host:o/r.git", "git@gitlab.example.com:group/sub/r.git",
		"file:///srv/git/r.git", "/srv/git/r.git", "./r.git", "../r.git", "https://oauth2@gitlab.com/o/r.git",
		"git@host:/abs/path.git",
	}
	for _, u := range good {
		if err := ValidURL(u); err != nil {
			t.Errorf("ValidURL(%q) = %v", u, err)
		}
	}
	bad := []string{
		"", "   ", "-oProxyCommand=x", "--upload-pack=x", "ext::sh -c id", "fd::3", "git@host:o r.git",
		"https://user:ghp_token@github.com/o/r.git", "ftp://host/r.git", "https://", "justaword", "host:", "a\tb",
	}
	for _, u := range bad {
		if err := ValidURL(u); err == nil {
			t.Errorf("ValidURL(%q) accepted", u)
		}
	}
}

func TestValidName(t *testing.T) {
	for _, n := range []string{"origin", "up-stream", "fork.1", "a_b"} {
		if err := ValidName(n); err != nil {
			t.Errorf("ValidName(%q) = %v", n, err)
		}
	}
	for _, n := range []string{"", "-x", "a b", "a/b", ".hidden", "x:y", "--all"} {
		if err := ValidName(n); err == nil {
			t.Errorf("ValidName(%q) accepted", n)
		}
	}
}
