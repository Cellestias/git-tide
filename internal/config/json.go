package config

import (
	"errors"
	"strings"

	"github.com/cellestias/git-tide/internal/apperr"
)

type jsonPattern struct {
	Name    string `json:"name"`
	Pattern string `json:"pattern"`
}

// Document is the effective configuration as `gtide config show --json` prints it:
// the file merged over the defaults. The patterns of secret.tide.yml are local
// and never appear in it, only how many there are.
type Document struct {
	Found bool   `json:"found"` // false: .tide.yml does not exist and these are the defaults
	Path  string `json:"path"`
	// SecretFileFound says whether secret.tide.yml exists; LocalPatterns counts the
	// detectors in it, without showing them.
	SecretFileFound bool `json:"secret_file_found"`
	LocalPatterns   int  `json:"local_patterns"`
	Config          struct {
		Version  int    `json:"version"`
		Provider string `json:"provider"`
		Remote   string `json:"remote"`
		Branches struct {
			DefaultBase string            `json:"default_base"`
			Protected   []string          `json:"protected"`
			Base        map[string]string `json:"base"`
		} `json:"branches"`
		Update struct {
			Strategy string `json:"strategy"`
		} `json:"update"`
		Commits struct {
			Convention   string `json:"convention"`
			RequireScope bool   `json:"require_scope"`
		} `json:"commits"`
		Secrets struct {
			Enabled        bool          `json:"enabled"`
			CustomPatterns []jsonPattern `json:"custom_patterns"`
		} `json:"secrets"`
		Security struct {
			Level string `json:"level"`
		} `json:"security"`
		Hooks struct {
			Manage bool `json:"manage"`
		} `json:"hooks"`
	} `json:"config"`
}

// View builds the Document of a loaded configuration.
func View(res Result) Document {
	c := res.Config
	var d Document
	d.Found, d.Path = res.Found, res.Path
	d.SecretFileFound, d.LocalPatterns = res.Local.Found, len(c.Secrets.LocalPatterns)
	d.Config.Version, d.Config.Provider, d.Config.Remote = c.Version, c.Provider, c.Remote
	d.Config.Branches.DefaultBase = c.Branches.DefaultBase
	d.Config.Branches.Protected = append([]string{}, c.Branches.Protected...)
	d.Config.Branches.Base = map[string]string{}
	for k, v := range c.Branches.Base {
		d.Config.Branches.Base[k] = v
	}
	d.Config.Update.Strategy = c.Update.Strategy
	d.Config.Commits.Convention, d.Config.Commits.RequireScope = c.Commits.Convention, c.Commits.RequireScope
	d.Config.Secrets.Enabled = c.Secrets.Enabled
	d.Config.Secrets.CustomPatterns = []jsonPattern{}
	for _, p := range c.Secrets.CustomPatterns {
		d.Config.Secrets.CustomPatterns = append(d.Config.Secrets.CustomPatterns, jsonPattern{p.Name, p.Pattern})
	}
	d.Config.Security.Level = string(c.SecurityLevel())
	d.Config.Hooks.Manage = c.Hooks.Manage
	return d
}

// Validation is what `gtide config validate --json` prints.
type Validation struct {
	Valid  bool     `json:"valid"`
	Path   string   `json:"path"`
	Errors []string `json:"errors"` // one per problem, each prefixed with the field
}

// Validate describes the outcome of loading the configuration: a missing file
// and an invalid one are both "not valid", with the reasons.
func Validate(res Result, err error) Validation {
	v := Validation{Valid: err == nil && res.Found, Path: res.Path, Errors: []string{}}
	switch {
	case err != nil:
		var ae *apperr.Error
		cause := err
		if errors.As(err, &ae) && ae.Err != nil {
			cause = ae.Err
		}
		for _, line := range strings.Split(cause.Error(), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				v.Errors = append(v.Errors, line)
			}
		}
	case !res.Found:
		v.Errors = append(v.Errors, FileName+" not found")
	}
	return v
}
