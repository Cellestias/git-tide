// Package config locates, parses, and validates the Tide configuration file.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/cellestias/git-tide/internal/apperr"
	domain "github.com/cellestias/git-tide/internal/domain/config"
)

// FileName is the configuration file at the repository root.
const FileName = ".tide.yml"

// maxFileSize bounds how much of an untrusted file is parsed.
const maxFileSize = 256 * 1024

// Result is a loaded configuration. Found is false when the file does not
// exist, in which case Config holds the defaults.
type Result struct {
	Config domain.Config
	Path   string
	Found  bool
}

// Load reads FileName from repoRoot. A missing file yields defaults with
// Found=false. Parse and validation failures are returned as exit-code-3 errors
// that include the file path.
func Load(repoRoot string) (Result, error) {
	path := filepath.Join(repoRoot, FileName)
	res := Result{Config: domain.Default(), Path: path}

	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return res, nil
	}
	if err != nil {
		return res, apperr.Wrap(apperr.CodeConfig, "cannot read "+path, "", err)
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxFileSize+1))
	if err != nil {
		return res, apperr.Wrap(apperr.CodeConfig, "cannot read "+path, "", err)
	}
	if len(data) > maxFileSize {
		return res, apperr.New(apperr.CodeConfig, fmt.Sprintf("%s is larger than %d bytes", path, maxFileSize), "")
	}

	res.Found = true
	if err := Parse(data, &res.Config); err != nil {
		return res, apperr.Wrap(apperr.CodeConfig, "invalid configuration in "+path, "run `tide config validate` after fixing it", err)
	}
	return res, nil
}

// Parse decodes YAML over cfg (which should hold defaults), rejecting unknown
// keys, then validates the result.
func Parse(data []byte, cfg *domain.Config) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("parse error: %w", err)
	}
	return cfg.Validate()
}

// Marshal renders cfg as YAML.
func Marshal(cfg domain.Config) ([]byte, error) {
	return yaml.Marshal(cfg)
}
