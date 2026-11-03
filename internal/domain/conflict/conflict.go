// Package conflict detects leftover merge-conflict markers in added lines.
package conflict

import "strings"

// AllowMarker on a line suppresses the finding for that line, for content
// that legitimately contains a marker (documentation about Git, test data).
const AllowMarker = "tide:allow-conflict-marker"

// IsMarker reports whether line is a "<<<<<<<" or ">>>>>>>" conflict marker.
// Git writes them at the start of a line, followed by a space and a label or
// by nothing. The "=======" separator is not matched on its own: it is a
// common heading underline in Markdown and reStructuredText.
func IsMarker(line string) bool {
	if strings.Contains(line, AllowMarker) {
		return false
	}
	for _, m := range []string{"<<<<<<<", ">>>>>>>"} {
		if rest, ok := strings.CutPrefix(line, m); ok && (rest == "" || rest[0] == ' ' || rest[0] == '\t') {
			return true
		}
	}
	return false
}
