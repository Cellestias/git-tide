// Package dotenv reads selected keys from a .env file. It is deliberately
// small: no variable expansion, no escapes, and only the keys asked for are
// ever returned, so the rest of the file is never held on to.
package dotenv

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
)

// maxSize bounds how much of the file is read.
const maxSize = 64 * 1024

// Read returns the values of keys found in the file at path. A missing file is
// not an error: it returns an empty map. Anything that is not a regular file
// (a symlink, a directory) is rejected.
func Read(path string, keys ...string) (map[string]string, error) {
	fi, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxSize))
	if err != nil {
		return nil, err
	}
	return Parse(data, keys...), nil
}

// Parse extracts keys from .env content: `KEY=value` or `export KEY=value`,
// optionally quoted, with `#` comments. When a key repeats, the last one wins.
func Parse(data []byte, keys ...string) map[string]string {
	want := make(map[string]bool, len(keys))
	for _, k := range keys {
		want[k] = true
	}
	out := map[string]string{}
	for _, line := range bytes.Split(data, []byte("\n")) {
		s := strings.TrimSpace(string(line))
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		s = strings.TrimSpace(strings.TrimPrefix(s, "export "))
		key, val, ok := strings.Cut(s, "=")
		key = strings.TrimSpace(key)
		if !ok || !want[key] {
			continue
		}
		out[key] = value(strings.TrimSpace(val))
	}
	return out
}

func value(v string) string {
	if v == "" {
		return ""
	}
	if q := v[0]; q == '"' || q == '\'' {
		if end := strings.IndexByte(v[1:], q); end >= 0 {
			return v[1 : 1+end]
		}
		return v[1:] // unterminated quote: take the rest as written
	}
	if i := strings.Index(v, " #"); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}
