package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/cellestias/git-tide/internal/httpx"
	"github.com/cellestias/git-tide/internal/provider"
)

// Types for the security alerts, shared with every adapter.
type (
	SecurityAlert  = provider.SecurityAlert
	SecurityReport = provider.SecurityReport
	SourceResult   = provider.SourceResult
)

// securityMaxPages caps each list: 10 pages of 100 alerts.
const securityMaxPages = 10

// SecurityAlerts lists the open Dependabot and code scanning alerts of the
// repository's default branch. Each source is asked on its own: one that is not
// turned on for the repository is reported as such (nothing to list), and one
// that cannot be read (token, rights, network) is reported with its error, so
// the caller can tell "no alerts" from "could not look".
func (c *Client) SecurityAlerts(ctx context.Context, repo Repo) (SecurityReport, error) {
	var rep SecurityReport
	for _, src := range []struct {
		name   string
		path   string
		decode func(json.RawMessage) (SecurityAlert, bool, error)
	}{
		{provider.SourceDependabot, "/dependabot/alerts?state=open", decodeDependabot},
		{provider.SourceCodeScanning, "/code-scanning/alerts?state=open", decodeCodeScanning},
	} {
		alerts, truncated, err := c.alertList(ctx, src.name, c.repoURL(repo)+src.path, src.decode)
		switch {
		case errors.Is(err, errNotEnabled):
			rep.Sources = append(rep.Sources, SourceResult{Source: src.name, Enabled: false})
		case err != nil:
			rep.Sources = append(rep.Sources, SourceResult{Source: src.name, Enabled: true, Err: err})
		default:
			rep.Sources = append(rep.Sources, SourceResult{Source: src.name, Enabled: true})
			rep.Alerts = append(rep.Alerts, alerts...)
			rep.Truncated = rep.Truncated || truncated
		}
	}
	return rep, nil
}

// errNotEnabled marks a source the repository does not use (not a failure).
var errNotEnabled = errors.New("not enabled")

// alertList reads one list, following the Link header GitHub sends for the
// next page (Dependabot alerts use cursors, so a page number would not work).
func (c *Client) alertList(ctx context.Context, source, endpoint string, decode func(json.RawMessage) (SecurityAlert, bool, error)) (alerts []SecurityAlert, truncated bool, err error) {
	next := endpoint
	for page := 1; next != ""; page++ {
		if page > securityMaxPages {
			return alerts, true, nil
		}
		body, link, err := c.securityGet(ctx, source, next)
		if err != nil {
			return nil, false, err
		}
		var items []json.RawMessage
		if err := json.Unmarshal(body, &items); err != nil {
			return nil, false, fmt.Errorf("decode GitHub API response: %w", err)
		}
		for _, raw := range items {
			a, open, err := decode(raw)
			if err != nil {
				return nil, false, fmt.Errorf("decode GitHub API response: %w", err)
			}
			if open {
				alerts = append(alerts, a)
			}
		}
		next = c.sameHost(nextLink(link))
	}
	return alerts, false, nil
}

var linkNext = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

func nextLink(header string) string {
	if m := linkNext.FindStringSubmatch(header); m != nil {
		return m[1]
	}
	return ""
}

// sameHost returns next only when it points at the API the client talks to, so
// a Link header can never send the token somewhere else.
func (c *Client) sameHost(next string) string {
	if next == "" {
		return ""
	}
	n, err1 := url.Parse(next)
	b, err2 := url.Parse(c.BaseURL)
	if err1 != nil || err2 != nil || n.Scheme != b.Scheme || n.Host != b.Host {
		return ""
	}
	return next
}

// securityGet is one GET that tells apart "this feature is off for the
// repository" from "I could not read it", which the generic helper cannot: both
// come as 403 or 404 and only the message differs. Errors never carry the token
// or the URL.
func (c *Client) securityGet(ctx context.Context, source, endpoint string) (body []byte, link string, err error) {
	hdr := map[string]string{
		"Accept":               "application/vnd.github+json",
		"X-GitHub-Api-Version": "2022-11-28",
		"User-Agent":           "git-tide",
	}
	if c.Token != "" {
		hdr["Authorization"] = "Bearer " + c.Token
	}
	if !strings.Contains(endpoint, "per_page=") { // the Link header's URLs already carry it
		sep := "?"
		if strings.Contains(endpoint, "?") {
			sep = "&"
		}
		endpoint += sep + "per_page=100"
	}
	resp, err := httpx.Do(ctx, c.HTTP, httpx.Request{Method: http.MethodGet, URL: endpoint, Header: hdr}, httpx.DefaultPolicy)
	if err != nil {
		return nil, "", fmt.Errorf("request GitHub API: %w", err)
	}
	if resp.Status == http.StatusOK {
		return resp.Body, resp.Header.Get("Link"), nil
	}
	msg := strings.ToLower(apiMessage(resp.Body))
	hint := ""
	if c.Token == "" {
		hint = " (no token: set GITHUB_TOKEN, with the security_events scope or access to this repository)"
	}
	switch resp.Status {
	case http.StatusNotFound:
		if source == provider.SourceCodeScanning && strings.Contains(msg, "no analysis found") {
			return nil, "", errNotEnabled
		}
		return nil, "", fmt.Errorf("GitHub cannot find the repository, or the token cannot see its %s alerts%s", sourceLabel(source), hint)
	case http.StatusUnauthorized:
		return nil, "", fmt.Errorf("GitHub rejected the token (401); check GITHUB_TOKEN")
	case http.StatusForbidden, http.StatusTooManyRequests:
		if resp.Header.Get("X-RateLimit-Remaining") == "0" {
			return nil, "", fmt.Errorf("GitHub API rate limit exceeded; retry later")
		}
		for _, off := range []string{"disabled", "not enabled", "must be enabled", "advanced security"} {
			if strings.Contains(msg, off) {
				return nil, "", errNotEnabled
			}
		}
		return nil, "", fmt.Errorf("GitHub denied access to the %s alerts (%d): the token needs the security_events scope, or the right to read them%s", sourceLabel(source), resp.Status, hint)
	}
	return nil, "", fmt.Errorf("GitHub API returned status %d", resp.Status)
}

func sourceLabel(source string) string {
	if source == provider.SourceCodeScanning {
		return "code scanning"
	}
	return "Dependabot"
}

func apiMessage(body []byte) string {
	var v struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &v) != nil {
		return ""
	}
	return v.Message
}

func decodeDependabot(raw json.RawMessage) (SecurityAlert, bool, error) {
	var v struct {
		Number     int    `json:"number"`
		State      string `json:"state"`
		HTMLURL    string `json:"html_url"`
		Dependency struct {
			Package struct {
				Ecosystem string `json:"ecosystem"`
				Name      string `json:"name"`
			} `json:"package"`
			ManifestPath string `json:"manifest_path"`
		} `json:"dependency"`
		Advisory struct {
			Summary  string `json:"summary"`
			Severity string `json:"severity"`
		} `json:"security_advisory"`
		Vulnerability struct {
			Severity string `json:"severity"`
			Patched  *struct {
				Identifier string `json:"identifier"`
			} `json:"first_patched_version"`
		} `json:"security_vulnerability"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return SecurityAlert{}, false, err
	}
	a := SecurityAlert{
		Source:   provider.SourceDependabot,
		Number:   v.Number,
		Severity: severity(v.Vulnerability.Severity, v.Advisory.Severity),
		Title:    v.Advisory.Summary,
		Package:  strings.TrimPrefix(v.Dependency.Package.Ecosystem+":"+v.Dependency.Package.Name, ":"),
		Path:     v.Dependency.ManifestPath,
		URL:      v.HTMLURL,
	}
	if v.Vulnerability.Patched != nil {
		a.FixedIn = v.Vulnerability.Patched.Identifier
	}
	return a, v.State == "" || v.State == "open", nil
}

func decodeCodeScanning(raw json.RawMessage) (SecurityAlert, bool, error) {
	var v struct {
		Number  int    `json:"number"`
		State   string `json:"state"`
		HTMLURL string `json:"html_url"`
		Rule    struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Description string `json:"description"`
			Severity    string `json:"severity"`
			Security    string `json:"security_severity_level"`
		} `json:"rule"`
		Instance struct {
			Location struct {
				Path      string `json:"path"`
				StartLine int    `json:"start_line"`
			} `json:"location"`
		} `json:"most_recent_instance"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return SecurityAlert{}, false, err
	}
	title := v.Rule.Description
	if title == "" {
		title = v.Rule.Name
	}
	if title == "" {
		title = v.Rule.ID
	}
	sev := ""
	if provider.SeverityValid(v.Rule.Security) {
		sev = strings.ToLower(v.Rule.Security)
	} else {
		// A finding without a security severity (a quality rule) has only error, warning or note.
		sev = map[string]string{"error": provider.SeverityHigh, "warning": provider.SeverityMedium, "note": provider.SeverityLow}[strings.ToLower(v.Rule.Severity)]
	}
	a := SecurityAlert{
		Source:   provider.SourceCodeScanning,
		Number:   v.Number,
		Severity: severity(sev),
		Title:    title,
		Path:     v.Instance.Location.Path,
		Line:     v.Instance.Location.StartLine,
		URL:      v.HTMLURL,
	}
	return a, v.State == "" || v.State == "open", nil
}

// severity returns the first of candidates that is a known severity, else low:
// an alert of unknown weight is shown, never dropped.
func severity(candidates ...string) string {
	for _, c := range candidates {
		if provider.SeverityValid(c) {
			return strings.ToLower(c)
		}
	}
	return provider.SeverityLow
}
