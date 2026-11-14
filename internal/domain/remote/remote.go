// Package remote validates the remote names and URLs Tide hands to Git. It is
// pure: no I/O.
package remote

import (
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	// transportRe matches "ext::", "fd::" and other remote-helper forms that make
	// Git run a program instead of connecting to a host.
	transportRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*::`)
	// scpRe matches the scp-like form [user@]host:path, where host has no slash.
	scpRe = regexp.MustCompile(`^([A-Za-z0-9._-]+@)?[A-Za-z0-9._-]+:[^/\\:][^\\:]*$|^([A-Za-z0-9._-]+@)?[A-Za-z0-9._-]+:/[^\\:]*$`)
)

// ValidName reports whether name can be a remote name.
func ValidName(name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("invalid remote name %q: use letters, digits, '.', '_' or '-', starting with a letter or digit", name)
	}
	return nil
}

// ValidURL accepts https, http, ssh, git and file URLs, scp-like addresses
// (git@host:owner/repo.git) and absolute or ./ ../ local paths. It rejects
// option-like input, remote-helper transports (ext::) and credentials written
// into the URL, which would end up in .git/config.
func ValidURL(raw string) error {
	u := strings.TrimSpace(raw)
	switch {
	case u == "":
		return fmt.Errorf("the remote URL is empty")
	case strings.HasPrefix(u, "-"):
		return fmt.Errorf("invalid remote URL %q", raw)
	case strings.ContainsFunc(u, func(r rune) bool { return r <= ' ' || r == 0x7f }):
		return fmt.Errorf("the remote URL must not contain spaces or control characters")
	case transportRe.MatchString(u):
		return fmt.Errorf("remote-helper URLs (%s) are not accepted", u[:strings.Index(u, "::")+2])
	}

	if strings.Contains(u, "://") {
		parsed, err := url.Parse(u)
		if err != nil || parsed.Host == "" && parsed.Scheme != "file" {
			return fmt.Errorf("invalid remote URL %q", raw)
		}
		switch strings.ToLower(parsed.Scheme) {
		case "https", "http", "ssh", "git", "file":
		default:
			return fmt.Errorf("unsupported URL scheme %q; use https, ssh or an scp-like address", parsed.Scheme)
		}
		if _, hasPassword := parsed.User.Password(); hasPassword {
			return fmt.Errorf("the remote URL holds a password or token; it would be saved in .git/config. Use SSH or a Git credential helper instead")
		}
		return nil
	}
	if strings.HasPrefix(u, "./") || strings.HasPrefix(u, "../") || filepath.IsAbs(u) {
		return nil
	}
	if scpRe.MatchString(u) {
		return nil
	}
	return fmt.Errorf("invalid remote URL %q; use https://host/owner/repo.git or git@host:owner/repo.git", raw)
}
