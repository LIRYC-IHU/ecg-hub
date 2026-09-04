package storage

import (
	"fmt"
	"path/filepath"
	"strings"
)

// SafeName reduces s to something usable as a single path component.
//
// Separators and the characters Windows rejects become "_", as do control
// characters, which have no business in a stored name and are awkward
// everywhere they are later displayed or logged. A value that cleans down to
// nothing, or to a traversal component, becomes "_" so it can never disappear
// and shift the meaning of the path it is joined into.
func SafeName(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '/' || c == '\\' || c == ':' || c == '*' || c == '?' ||
			c == '"' || c == '<' || c == '>' || c == '|':
			b.WriteByte('_')
		case c < 0x20 || c == 0x7f:
			b.WriteByte('_')
		default:
			b.WriteByte(c)
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "" || out == "." || out == ".." {
		return "_"
	}
	return out
}

// EnsureWithin returns base joined with the given components, but only when the
// result stays inside base.
//
// SafeName already removes the separators that make traversal possible, so this
// is the second line rather than the first: it holds for callers that build a
// name some other way, and for the next one added. Failing loudly is the point
// -- a write that lands outside its volume should stop, not be relocated
// silently.
func EnsureWithin(base string, parts ...string) (string, error) {
	cleanBase := filepath.Clean(base)
	target := filepath.Clean(filepath.Join(append([]string{cleanBase}, parts...)...))

	rel, err := filepath.Rel(cleanBase, target)
	if err != nil {
		return "", fmt.Errorf("storage: %q is not resolvable against %q: %w", target, cleanBase, err)
	}
	// filepath.Rel returns ".." or a "../"-prefixed path exactly when target
	// climbs out. "." means target *is* base, which is not a file to write.
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == "." {
		return "", fmt.Errorf("storage: path %q escapes %q", target, cleanBase)
	}
	return target, nil
}
