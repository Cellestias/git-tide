// Package providers is the registry that turns a configured provider name (or
// "auto") into a concrete adapter. It is the only place that knows every adapter.
package providers

import (
	"fmt"

	"github.com/cellestias/git-tide/internal/github"
	"github.com/cellestias/git-tide/internal/gitlab"
	"github.com/cellestias/git-tide/internal/provider"
)

// Resolve returns the provider name for configured ("auto", "github" or
// "gitlab"). For "auto" it inspects remoteURLs in order (the URL as written
// first, then the one Git resolved) and returns an error when none is recognised.
func Resolve(configured string, remoteURLs ...string) (string, error) {
	switch configured {
	case provider.GitHub, provider.GitLab:
		return configured, nil
	case provider.Auto, "":
		for _, u := range remoteURLs {
			if name := provider.DetectName(u); name != "" {
				return name, nil
			}
		}
		return "", fmt.Errorf("cannot detect the provider from the remote URL")
	}
	return "", fmt.Errorf("unknown provider %q", configured)
}

// New builds the adapter for name. host is the remote's hostname (used by
// self-hosted GitLab). A non-empty token overrides the one read from the environment.
func New(name, host, token string) (provider.Provider, error) {
	switch name {
	case provider.GitHub:
		c, err := github.NewClient()
		if err != nil {
			return nil, err
		}
		if token != "" {
			c.Token = token
		}
		return c, nil
	case provider.GitLab:
		c, err := gitlab.NewClient(host)
		if err != nil {
			return nil, err
		}
		if token != "" {
			c.Token = token
		}
		return c, nil
	}
	return nil, fmt.Errorf("unknown provider %q", name)
}

// TokenKeys lists the environment variables read as a fallback for name, in priority order.
func TokenKeys(name string) []string {
	if name == provider.GitLab {
		return []string{"GITLAB_TOKEN"}
	}
	return []string{"GITHUB_TOKEN", "GH_TOKEN"}
}
