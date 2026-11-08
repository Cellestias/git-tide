package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"gopkg.in/yaml.v3"

	"github.com/cellestias/git-tide/internal/apperr"
	domain "github.com/cellestias/git-tide/internal/domain/config"
)

// SecretFileName is the local, never-committed file at the repository root. It
// holds the provider credentials reference and local secret-scanning patterns.
const SecretFileName = "secret.tide.yml" //nolint:gosec // G101 false positive: not a credential

// Local is the content of secret.tide.yml.
type Local struct {
	// ProviderToken must be a reference such as ${GITHUB_TOKEN}; a literal token is rejected.
	ProviderToken string       `yaml:"provider_token"`
	Secrets       LocalSecrets `yaml:"secrets"`
}

type LocalSecrets struct {
	Patterns []domain.CustomPattern `yaml:"patterns"`
}

var envRef = regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$`)

// TokenVar returns the environment variable ProviderToken refers to, or "" when
// no token is configured. ok is false when the value is not a ${NAME} reference.
func (l Local) TokenVar() (name string, ok bool) {
	if l.ProviderToken == "" {
		return "", true
	}
	m := envRef.FindStringSubmatch(l.ProviderToken)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// LocalResult is a loaded secret.tide.yml. Found is false when it does not exist.
type LocalResult struct {
	Local Local
	Path  string
	Found bool
}

// LoadLocal reads SecretFileName from repoRoot. A missing file is not an error.
// The file must be a regular file (never a symlink) and is size-limited.
func LoadLocal(repoRoot string) (LocalResult, error) {
	path := filepath.Join(repoRoot, SecretFileName)
	res := LocalResult{Path: path}

	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return res, nil
	}
	if err != nil {
		return res, apperr.Wrap(apperr.CodeConfig, "cannot read "+path, "", err)
	}
	if !fi.Mode().IsRegular() {
		return res, apperr.New(apperr.CodeConfig, path+" is not a regular file", "")
	}
	f, err := os.Open(path) //nolint:gosec // path is the fixed file name under the repository root
	if err != nil {
		return res, apperr.Wrap(apperr.CodeConfig, "cannot read "+path, "", err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxFileSize+1))
	if err != nil {
		return res, apperr.Wrap(apperr.CodeConfig, "cannot read "+path, "", err)
	}
	if len(data) > maxFileSize {
		return res, apperr.New(apperr.CodeConfig, fmt.Sprintf("%s is larger than %d bytes", path, maxFileSize), "")
	}

	res.Found = true
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&res.Local); err != nil && !errors.Is(err, io.EOF) {
		return res, apperr.Wrap(apperr.CodeConfig, "invalid "+path, "", fmt.Errorf("parse error: %w", err))
	}
	if _, ok := res.Local.TokenVar(); !ok {
		return res, apperr.New(apperr.CodeConfig, path+": provider_token must be an environment variable reference such as ${GITHUB_TOKEN}",
			"never store the token itself in the file")
	}
	if errs := domain.ValidatePatterns(SecretFileName+": secrets.patterns", res.Local.Secrets.Patterns); len(errs) > 0 {
		return res, apperr.Wrap(apperr.CodeConfig, "invalid "+path, "", errors.Join(errs...))
	}
	return res, nil
}
