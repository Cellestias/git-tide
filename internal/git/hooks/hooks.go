// Package hooks installs Tide-managed Git hooks without destroying existing
// ones. A pre-existing hook is renamed to "<name>.pre-tide" and the Tide hook
// runs it after Tide's own checks pass.
package hooks

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// Marker identifies a hook file written by Tide. Bump the version when the
// script changes so `tide doctor` can report outdated hooks.
const Marker = "# tide-managed-hook v1"

// ChainSuffix is appended to the name of a pre-existing hook that Tide chains to.
const ChainSuffix = ".pre-tide"

type Hook struct {
	Name       string
	ReadsStdin bool
}

// Managed lists the hooks Tide installs.
var Managed = []Hook{
	{Name: "pre-commit"},
	{Name: "commit-msg"},
	{Name: "pre-push", ReadsStdin: true},
}

type Action string

const (
	Installed Action = "installed"
	Updated   Action = "updated"
	Chained   Action = "installed (existing hook preserved as <name>" + ChainSuffix + ")"
	Unchanged Action = "up to date"
)

type Change struct {
	Hook   string
	Action Action
}

var script = template.Must(template.New("hook").Parse(`#!/bin/sh
` + Marker + `
# Installed by "tide init". To undo: delete this file and, if it exists,
# rename "{{.Name}}` + ChainSuffix + `" back to "{{.Name}}".
hook_dir=$(dirname -- "$0")
chained="$hook_dir/{{.Name}}` + ChainSuffix + `"
{{if .ReadsStdin}}input=$(cat)
if command -v tide >/dev/null 2>&1; then
  printf '%s\n' "$input" | tide hook {{.Name}} "$@" || exit $?
else
  echo "tide: not found on PATH; skipping {{.Name}} checks" >&2
fi
if [ -x "$chained" ]; then
  printf '%s\n' "$input" | "$chained" "$@"
  exit $?
fi
exit 0
{{else}}if command -v tide >/dev/null 2>&1; then
  tide hook {{.Name}} "$@" || exit $?
else
  echo "tide: not found on PATH; skipping {{.Name}} checks" >&2
fi
if [ -x "$chained" ]; then
  exec "$chained" "$@"
fi
exit 0
{{end}}`))

// Script returns the content Tide writes for h.
func Script(h Hook) []byte {
	var b bytes.Buffer
	if err := script.Execute(&b, h); err != nil {
		panic(err) // template is static; failure is a programming error
	}
	return b.Bytes()
}

// State describes a hook file found on disk.
type State string

const (
	Missing  State = "missing"
	Current  State = "current"
	Outdated State = "outdated"
	Foreign  State = "foreign" // exists but is not managed by Tide
)

type Status struct {
	Hook    string
	State   State
	Chained bool // a "<name>.pre-tide" file exists
}

// Inspect reports the state of every managed hook in dir.
func Inspect(dir string) ([]Status, error) {
	var out []Status
	for _, h := range Managed {
		path := filepath.Join(dir, h.Name)
		st := Status{Hook: h.Name, State: Missing}
		data, err := os.ReadFile(path)
		switch {
		case errors.Is(err, os.ErrNotExist):
		case err != nil:
			return nil, err
		case !bytes.Contains(data, []byte(Marker)) && !isTideHook(data):
			st.State = Foreign
		case bytes.Equal(data, Script(h)):
			st.State = Current
		default:
			st.State = Outdated
		}
		if _, err := os.Lstat(path + ChainSuffix); err == nil {
			st.Chained = true
		}
		out = append(out, st)
	}
	return out, nil
}

// isTideHook recognises hooks from any marker version.
func isTideHook(data []byte) bool {
	return bytes.Contains(data, []byte("# tide-managed-hook v"))
}

// Install writes every managed hook into dir. Existing foreign hooks are
// preserved by renaming; an existing chain target is never overwritten.
func Install(dir string) ([]Change, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // hook directories are shared with Git and must be traversable
		return nil, fmt.Errorf("create hooks directory: %w", err)
	}
	var changes []Change
	for _, h := range Managed {
		path := filepath.Join(dir, h.Name)
		want := Script(h)

		data, err := os.ReadFile(path)
		switch {
		case errors.Is(err, os.ErrNotExist):
			if err := writeAtomic(path, want); err != nil {
				return changes, err
			}
			changes = append(changes, Change{h.Name, Installed})
		case err != nil:
			return changes, fmt.Errorf("read %s: %w", path, err)
		case isTideHook(data):
			if bytes.Equal(data, want) {
				changes = append(changes, Change{h.Name, Unchanged})
				continue
			}
			if err := writeAtomic(path, want); err != nil {
				return changes, err
			}
			changes = append(changes, Change{h.Name, Updated})
		default:
			chain := path + ChainSuffix
			if _, err := os.Lstat(chain); err == nil {
				return changes, fmt.Errorf("cannot preserve existing %s hook: %s already exists; resolve it manually", h.Name, filepath.Base(chain))
			}
			if err := os.Rename(path, chain); err != nil {
				return changes, fmt.Errorf("preserve existing %s hook: %w", h.Name, err)
			}
			if err := writeAtomic(path, want); err != nil {
				// put the user's hook back rather than leaving the repo without it
				_ = os.Rename(chain, path)
				return changes, err
			}
			changes = append(changes, Change{h.Name, Chained})
		}
	}
	return changes, nil
}

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil { //nolint:gosec // a hook must be executable
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// Describe renders a Change for display.
func (c Change) Describe() string {
	return fmt.Sprintf("%s: %s", c.Hook, strings.ReplaceAll(string(c.Action), "<name>", c.Hook))
}
