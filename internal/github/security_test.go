package github

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/cellestias/git-tide/internal/provider"
)

const (
	dependabotOne = `{"number":1,"state":"open","html_url":"https://github.com/acme/tool/security/dependabot/1",
	  "dependency":{"package":{"ecosystem":"npm","name":"lodash"},"manifest_path":"package.json"},
	  "security_advisory":{"summary":"Prototype pollution in lodash","severity":"high"},
	  "security_vulnerability":{"severity":"critical","first_patched_version":{"identifier":"4.17.21"}}}`
	dependabotTwo = `{"number":2,"state":"open","html_url":"u2",
	  "dependency":{"package":{"ecosystem":"go","name":"golang.org/x/net"},"manifest_path":"backend/go.mod"},
	  "security_advisory":{"summary":"HTTP/2 rapid reset","severity":"medium"},
	  "security_vulnerability":{"severity":"","first_patched_version":null}}`
	scanningOne = `{"number":7,"state":"open","html_url":"https://github.com/acme/tool/security/code-scanning/7",
	  "rule":{"id":"js/xss","name":"xss","description":"Cross-site scripting","severity":"error","security_severity_level":"high"},
	  "most_recent_instance":{"location":{"path":"src/render.js","start_line":42}}}`
	scanningQuality = `{"number":8,"state":"open","html_url":"u8",
	  "rule":{"id":"js/unused","description":"Unused variable","severity":"note","security_severity_level":null},
	  "most_recent_instance":{"location":{"path":"src/a.js","start_line":3}}}`
)

type server struct {
	*httptest.Server
	mu   sync.Mutex
	seen []string
	auth []string
}

// newServer serves handler for the two alert endpoints and records what it was asked.
func newServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) (*server, *Client) {
	t.Helper()
	s := &server{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.seen = append(s.seen, r.URL.RequestURI())
		s.auth = append(s.auth, r.Header.Get("Authorization"))
		s.mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(s.Close)
	return s, &Client{BaseURL: s.URL, Token: "sekret-token", HTTP: s.Client()}
}

func reply(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = fmt.Fprint(w, body)
}

func result(r SecurityReport, source string) SourceResult {
	for _, s := range r.Sources {
		if s.Source == source {
			return s
		}
	}
	return SourceResult{Source: "missing"}
}

var tool = Repo{Owner: "acme", Name: "tool"}

func TestSecurityAlertsFromBothSources(t *testing.T) {
	s, c := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/repos/acme/tool/dependabot/alerts"):
			reply(w, 200, "["+dependabotOne+","+dependabotTwo+"]")
		case strings.HasPrefix(r.URL.Path, "/repos/acme/tool/code-scanning/alerts"):
			reply(w, 200, "["+scanningOne+","+scanningQuality+"]")
		default:
			reply(w, 404, `{"message":"Not Found"}`)
		}
	})
	rep, err := c.SecurityAlerts(t.Context(), tool)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Alerts) != 4 || len(rep.Unverified()) != 0 || rep.Truncated {
		t.Fatalf("report = %+v", rep)
	}
	byNumber := map[string]SecurityAlert{}
	for _, a := range rep.Alerts {
		byNumber[fmt.Sprintf("%s#%d", a.Source, a.Number)] = a
	}
	d1 := byNumber["dependabot#1"]
	if d1.Severity != "critical" || d1.Package != "npm:lodash" || d1.Path != "package.json" || d1.FixedIn != "4.17.21" || d1.Title != "Prototype pollution in lodash" || !strings.HasSuffix(d1.URL, "/dependabot/1") {
		t.Errorf("dependabot 1 = %+v", d1)
	}
	if d2 := byNumber["dependabot#2"]; d2.Severity != "medium" || d2.Path != "backend/go.mod" || d2.FixedIn != "" {
		t.Errorf("a vulnerability with no severity takes the advisory's, and no patch is fine: %+v", d2)
	}
	c7 := byNumber["code-scanning#7"]
	if c7.Severity != "high" || c7.Path != "src/render.js" || c7.Line != 42 || c7.Title != "Cross-site scripting" || c7.Package != "" {
		t.Errorf("code scanning 7 = %+v", c7)
	}
	if c8 := byNumber["code-scanning#8"]; c8.Severity != "low" {
		t.Errorf("a note without a security severity is low: %+v", c8)
	}
	for _, a := range s.auth {
		if a != "Bearer sekret-token" {
			t.Errorf("authorization = %q", a)
		}
	}
	for _, u := range s.seen {
		if !strings.Contains(u, "state=open") || !strings.Contains(u, "per_page=100") {
			t.Errorf("only open alerts, 100 at a time: %s", u)
		}
	}
}

func TestSeverityOfAFindingWithoutASecuritySeverity(t *testing.T) {
	for rule, want := range map[string]string{"error": "high", "warning": "medium", "note": "low", "none": "low", "": "low", "weird": "low"} {
		a, _, err := decodeCodeScanning([]byte(fmt.Sprintf(`{"number":1,"state":"open","rule":{"id":"r","severity":%q}}`, rule)))
		if err != nil || a.Severity != want {
			t.Errorf("rule severity %q → %q (%v), want %q", rule, a.Severity, err, want)
		}
	}
	// a security severity wins over the rule's
	a, _, _ := decodeCodeScanning([]byte(`{"number":1,"state":"open","rule":{"id":"r","severity":"note","security_severity_level":"CRITICAL"}}`))
	if a.Severity != "critical" {
		t.Errorf("severity = %q", a.Severity)
	}
}

func TestNextPageIsFollowedFromTheLinkHeader(t *testing.T) {
	var base string
	s, c := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/repos/acme/tool/dependabot/alerts") {
			reply(w, 404, `{"message":"no analysis found for the specified ref"}`)
			return
		}
		if r.URL.Query().Get("after") == "" {
			w.Header().Set("Link", fmt.Sprintf(`<%s/repos/acme/tool/dependabot/alerts?state=open&per_page=100&after=cursor1>; rel="next"`, base))
			reply(w, 200, "["+dependabotOne+"]")
			return
		}
		reply(w, 200, "["+dependabotTwo+"]")
	})
	base = s.URL
	rep, _ := c.SecurityAlerts(t.Context(), tool)
	if len(rep.Alerts) != 2 || rep.Truncated {
		t.Fatalf("alerts = %d truncated=%v", len(rep.Alerts), rep.Truncated)
	}
	for _, u := range s.seen {
		if strings.Count(u, "per_page=") > 1 {
			t.Errorf("per_page given twice: %s", u)
		}
	}
}

func TestALinkToAnotherHostIsNotFollowed(t *testing.T) {
	var evilHit bool
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		evilHit = true
		if r.Header.Get("Authorization") != "" {
			t.Error("the token was sent to another host")
		}
		reply(w, 200, "[]")
	}))
	defer evil.Close()
	_, c := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Link", fmt.Sprintf(`<%s/steal>; rel="next"`, evil.URL))
		reply(w, 200, "["+dependabotOne+"]")
	})
	rep, _ := c.SecurityAlerts(t.Context(), tool)
	if evilHit {
		t.Fatal("a Link to another host was followed")
	}
	if len(rep.Alerts) < 1 {
		t.Errorf("the first page must still be used: %+v", rep)
	}
}

func TestAListIsCutAtTheCap(t *testing.T) {
	var base string
	n := 0
	s, c := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "dependabot") {
			reply(w, 404, `{"message":"no analysis found"}`)
			return
		}
		n++
		w.Header().Set("Link", fmt.Sprintf(`<%s/repos/acme/tool/dependabot/alerts?state=open&after=%d>; rel="next"`, base, n))
		reply(w, 200, "["+dependabotOne+"]")
	})
	base = s.URL
	rep, _ := c.SecurityAlerts(t.Context(), tool)
	if !rep.Truncated || len(rep.Alerts) != securityMaxPages {
		t.Errorf("truncated=%v alerts=%d, want a cut at %d pages", rep.Truncated, len(rep.Alerts), securityMaxPages)
	}
}

func TestAFeatureThatIsOffIsNotAFailure(t *testing.T) {
	cases := map[string]struct {
		dependabot, scanning [2]string // status, body
	}{
		"both off by message": {
			dependabot: [2]string{"403", `{"message":"Dependabot alerts are disabled for this repository."}`},
			scanning:   [2]string{"403", `{"message":"Advanced Security must be enabled for this repository to use code scanning."}`},
		},
		"code scanning never ran": {
			dependabot: [2]string{"200", "[]"},
			scanning:   [2]string{"404", `{"message":"no analysis found"}`},
		},
	}
	for name, tc := range cases {
		_, c := newServer(t, func(w http.ResponseWriter, r *http.Request) {
			v := tc.scanning
			if strings.Contains(r.URL.Path, "dependabot") {
				v = tc.dependabot
			}
			var st int
			_, _ = fmt.Sscanf(v[0], "%d", &st)
			reply(w, st, v[1])
		})
		rep, err := c.SecurityAlerts(t.Context(), tool)
		if err != nil || len(rep.Unverified()) != 0 || len(rep.Alerts) != 0 {
			t.Errorf("%s: %+v err=%v", name, rep, err)
		}
		if name == "both off by message" && (result(rep, provider.SourceDependabot).Enabled || result(rep, provider.SourceCodeScanning).Enabled) {
			t.Errorf("%s: both must be reported as not enabled: %+v", name, rep.Sources)
		}
	}
}

func TestWhatCannotBeReadIsReportedAsAFailureOfThatSource(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		header map[string]string
		want   string
	}{
		{"token rejected", 401, `{"message":"Bad credentials"}`, nil, "rejected the token (401): it is invalid, expired or revoked"},
		{"no rights", 403, `{"message":"Resource not accessible by personal access token"}`, nil, "fine-grained: Dependabot alerts, Read; classic: security_events"},
		{"hidden repository or alerts off", 404, `{"message":"Not Found"}`, nil, "the token cannot see this repository, or Dependabot alerts are not enabled on it"},
		{"rate limit", 403, `{"message":"API rate limit exceeded"}`, map[string]string{"X-RateLimit-Remaining": "0"}, "rate limit"},
		{"single sign-on", 403, `{"message":"Resource protected by organization SAML enforcement. You must grant your Personal Access token access to this organization."}`, nil, "requires single sign-on: authorize the token"},
		{"server error", 500, `{}`, nil, "status 500"},
	}
	for _, tc := range tests {
		_, c := newServer(t, func(w http.ResponseWriter, r *http.Request) {
			for k, v := range tc.header {
				w.Header().Set(k, v)
			}
			if strings.Contains(r.URL.Path, "dependabot") {
				reply(w, tc.status, tc.body)
				return
			}
			reply(w, 200, "["+scanningOne+"]")
		})
		rep, err := c.SecurityAlerts(t.Context(), tool)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		dep := result(rep, provider.SourceDependabot)
		if dep.Err == nil || !strings.Contains(dep.Err.Error(), tc.want) {
			t.Errorf("%s: dependabot error = %v, want %q", tc.name, dep.Err, tc.want)
		}
		if dep.Err != nil && (strings.Contains(dep.Err.Error(), "sekret-token") || strings.Contains(dep.Err.Error(), "http://")) {
			t.Errorf("%s: the error leaks the token or the URL: %v", tc.name, dep.Err)
		}
		// the other source still answered
		if scan := result(rep, provider.SourceCodeScanning); scan.Err != nil || len(rep.Alerts) != 1 || rep.Alerts[0].Source != provider.SourceCodeScanning {
			t.Errorf("%s: code scanning must still be read: %+v", tc.name, rep)
		}
		if len(rep.Unverified()) != 1 {
			t.Errorf("%s: unverified = %d", tc.name, len(rep.Unverified()))
		}
	}
}

func TestWithoutATokenTheErrorSaysWhereToLook(t *testing.T) {
	_, c := newServer(t, func(w http.ResponseWriter, r *http.Request) { reply(w, 404, `{"message":"Not Found"}`) })
	c.Token = ""
	rep, _ := c.SecurityAlerts(t.Context(), tool)
	err := result(rep, provider.SourceDependabot).Err
	if err == nil || !strings.Contains(err.Error(), "no token is set") || !strings.Contains(err.Error(), "README") {
		t.Errorf("error = %v", err)
	}
}

func TestAnAlertThatIsNotOpenIsLeftOut(t *testing.T) {
	_, c := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "dependabot") {
			reply(w, 200, `[`+strings.Replace(dependabotOne, `"state":"open"`, `"state":"fixed"`, 1)+`,`+dependabotTwo+`]`)
			return
		}
		reply(w, 200, "[]")
	})
	rep, _ := c.SecurityAlerts(t.Context(), tool)
	if len(rep.Alerts) != 1 || rep.Alerts[0].Number != 2 {
		t.Errorf("alerts = %+v", rep.Alerts)
	}
}

func TestClientIsASecurityReader(t *testing.T) {
	var _ provider.SecurityReader = (*Client)(nil)
}
