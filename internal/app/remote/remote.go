// Package remote implements `gtide remote`: add, show and change the remote a
// repository publishes to, with the URL checked before Git sees it.
package remote

import (
	"context"
	"fmt"
	"strings"

	"github.com/cellestias/git-tide/internal/apperr"
	dremote "github.com/cellestias/git-tide/internal/domain/remote"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
	"github.com/cellestias/git-tide/internal/provider"
)

// DefaultName is the remote Tide uses when .tide.yml does not say otherwise.
const DefaultName = "origin"

func find(ctx context.Context, repo *repository.Repository, name string) (url string, ok bool, err error) {
	lines, err := repo.RemoteLines(ctx)
	if err != nil {
		return "", false, apperr.Wrap(apperr.CodeExternal, "cannot read the remotes", "", err)
	}
	for _, l := range lines {
		if l[0] == name {
			return l[1], true, nil
		}
	}
	return "", false, nil
}

func check(name, url string) error {
	if err := dremote.ValidName(name); err != nil {
		return apperr.Usage("%v", err)
	}
	if err := dremote.ValidURL(url); err != nil {
		return apperr.Usage("%v", err)
	}
	return nil
}

// report says which provider the URL maps to, or how to set one.
func report(url string, p output.Printer) {
	if name := provider.DetectName(url); name != "" {
		p.Info("provider: %s (detected from the URL)", name)
		return
	}
	p.Warn("the host of %s is not a known provider; set `provider: github` or `provider: gitlab` in .tide.yml", redact(url))
}

// Ensure makes sure name points to url. It adds the remote when it is missing,
// does nothing when it already points there, and never replaces a different
// URL: that is an error that points to set-url.
func Ensure(ctx context.Context, repo *repository.Repository, name, url string) (added bool, err error) {
	if err := check(name, url); err != nil {
		return false, err
	}
	url = strings.TrimSpace(url)
	current, exists, err := find(ctx, repo, name)
	if err != nil {
		return false, err
	}
	if exists {
		if current == url {
			return false, nil
		}
		return false, apperr.New(apperr.CodeUsage, fmt.Sprintf("remote %q already points to %s", name, redact(current)),
			"change it with `gtide remote set-url <url>`")
	}
	if err := repo.AddRemote(ctx, name, url); err != nil {
		return false, apperr.Wrap(apperr.CodeExternal, "cannot add the remote", "", err)
	}
	return true, nil
}

// Add registers name -> url (see Ensure) and reports what happened.
func Add(ctx context.Context, repo *repository.Repository, name, url string, p output.Printer) error {
	added, err := Ensure(ctx, repo, name, url)
	if err != nil {
		return err
	}
	url = strings.TrimSpace(url)
	if !added {
		p.Info("remote %s already points to %s", name, redact(url))
		return nil
	}
	p.Success("added remote %s: %s", name, redact(url))
	report(url, p)
	return nil
}

// SetURL points an existing remote at a new URL.
func SetURL(ctx context.Context, repo *repository.Repository, name, url string, p output.Printer) error {
	if err := check(name, url); err != nil {
		return err
	}
	url = strings.TrimSpace(url)
	current, exists, err := find(ctx, repo, name)
	if err != nil {
		return err
	}
	if !exists {
		return apperr.New(apperr.CodeUsage, fmt.Sprintf("remote %q does not exist", name), "add it with `gtide remote add <url>`")
	}
	if current == url {
		p.Info("remote %s already points to %s", name, redact(url))
		return nil
	}
	if err := repo.SetRemoteURL(ctx, name, url); err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot change the remote", "", err)
	}
	p.Success("remote %s now points to %s (was %s)", name, redact(url), redact(current))
	report(url, p)
	return nil
}

// Show lists the remotes with their provider and marks the one Tide uses.
func Show(ctx context.Context, repo *repository.Repository, configured string, p output.Printer) error {
	lines, err := repo.RemoteLines(ctx)
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot read the remotes", "", err)
	}
	if len(lines) == 0 {
		p.Info("No remote is configured. Add one with `gtide remote add <url>`.")
		return nil
	}
	rows := [][]string{{"REMOTE", "URL", "PROVIDER", ""}}
	used := false
	for _, l := range lines {
		prov, mark := provider.DetectName(l[1]), ""
		if prov == "" {
			prov = "unknown"
		}
		if l[0] == configured {
			mark, used = "used by gtide", true
		}
		rows = append(rows, []string{l[0], redact(l[1]), prov, mark})
	}
	p.Table(rows, func(_, col int, cell string) string {
		if col == 3 {
			return p.Dim(cell)
		}
		return cell
	})
	if !used {
		p.Warn("`remote: %s` in .tide.yml is not among these remotes", configured)
	}
	return nil
}

// redact hides a user:password written into an http(s) URL.
func redact(url string) string {
	scheme, rest, ok := strings.Cut(url, "://")
	if !ok {
		return url
	}
	host, path, _ := strings.Cut(rest, "/")
	if at := strings.LastIndex(host, "@"); at >= 0 && strings.Contains(host[:at], ":") {
		host = "***@" + host[at+1:]
	}
	if path == "" && !strings.Contains(rest, "/") {
		return scheme + "://" + host
	}
	return scheme + "://" + host + "/" + path
}
