package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/cellestias/git-tide/internal/git/executor"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
	"github.com/cellestias/git-tide/internal/testutil"
)

const secretToken = "ghp_" + "abcdefghijklmnopqrstuvwxyz0123456789"

func lookup(t *testing.T, dir string) (token, warnings string) {
	t.Helper()
	var errOut bytes.Buffer
	repo := repository.New(executor.Exec{}, dir)
	token = dotenvToken(context.Background(), repo, output.Printer{Out: &bytes.Buffer{}, Err: &errOut})
	return token, errOut.String()
}

func TestDotenvTokenIgnoredFile(t *testing.T) {
	dir := testutil.NewRepo(t)
	testutil.WriteFile(t, dir, ".gitignore", ".env\n")
	testutil.WriteFile(t, dir, ".env", "GITHUB_TOKEN="+secretToken+"\n")

	token, warn := lookup(t, dir)
	if token != secretToken || warn != "" {
		t.Errorf("token=%q warn=%q", token, warn)
	}
}

func TestDotenvTokenFallsBackToGHToken(t *testing.T) {
	dir := testutil.NewRepo(t)
	testutil.WriteFile(t, dir, ".gitignore", ".env\n")
	testutil.WriteFile(t, dir, ".env", "GH_TOKEN=gh_only\n")
	if token, _ := lookup(t, dir); token != "gh_only" {
		t.Errorf("token = %q", token)
	}
}

func TestDotenvTokenWarnsWhenNotIgnored(t *testing.T) {
	dir := testutil.NewRepo(t)
	testutil.WriteFile(t, dir, ".env", "GITHUB_TOKEN="+secretToken+"\n")

	token, warn := lookup(t, dir)
	if token != secretToken || !strings.Contains(warn, "not in .gitignore") {
		t.Errorf("token=%q warn=%q", token, warn)
	}
	if strings.Contains(warn, secretToken) {
		t.Error("warning leaked the token")
	}
}

func TestDotenvTokenRefusesTrackedFile(t *testing.T) {
	dir := testutil.NewRepo(t)
	testutil.WriteFile(t, dir, ".env", "GITHUB_TOKEN="+secretToken+"\n")
	testutil.Run(t, dir, "add", "-f", ".env")
	testutil.Commit(t, dir, "chore: oops")

	token, warn := lookup(t, dir)
	if token != "" || !strings.Contains(warn, "tracked by Git") {
		t.Errorf("token=%q warn=%q", token, warn)
	}
	if strings.Contains(warn, secretToken) {
		t.Error("warning leaked the token")
	}
}

func TestDotenvTokenRejectsOddValues(t *testing.T) {
	dir := testutil.NewRepo(t)
	testutil.WriteFile(t, dir, ".gitignore", ".env\n")
	testutil.WriteFile(t, dir, ".env", "GITHUB_TOKEN=\"abc def\"\n")
	if token, warn := lookup(t, dir); token != "" || !strings.Contains(warn, "characters") {
		t.Errorf("token=%q warn=%q", token, warn)
	}
}

func TestDotenvTokenAbsentFileOrKey(t *testing.T) {
	dir := testutil.NewRepo(t)
	if token, warn := lookup(t, dir); token != "" || warn != "" {
		t.Errorf("no .env: token=%q warn=%q", token, warn)
	}
	testutil.WriteFile(t, dir, ".env", "OTHER=1\n")
	if token, warn := lookup(t, dir); token != "" || warn != "" {
		t.Errorf("no token key: token=%q warn=%q", token, warn)
	}
}
