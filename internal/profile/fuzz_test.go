//go:build unix

package profile

import (
	"path/filepath"
	"strings"
	"testing"
)

// ValidName is the whole boundary between user input and a path under Root. A
// traversal got through code review here once — `ap create --from ../../../.claude`
// escaped the profile root and copied the real home — so assert the property
// itself rather than trusting an enumerated list of bad inputs.
//
// The property: if ValidName accepts a string, joining it under a root must stay
// under that root, and must be exactly one level deep.
func FuzzValidName(f *testing.F) {
	for _, seed := range []string{
		"plan", "review", "my_profile-2", "a",
		"..", ".", "../x", "../../../.claude", "a/b", `a\b`, ".hidden", "",
		"has space", "x..y/z", "..\x00", "pl\nan", "COM1", "-", "_",
		strings.Repeat("a", 300),
	} {
		f.Add(seed)
	}

	const root = "/profiles/claude"

	f.Fuzz(func(t *testing.T, name string) {
		if err := ValidName(name); err != nil {
			return // rejected: nothing to prove
		}

		joined := filepath.Join(root, name)

		if joined != filepath.Clean(joined) {
			t.Fatalf("ValidName(%q) accepted, but the joined path is not clean: %q", name, joined)
		}
		rel, err := filepath.Rel(root, joined)
		if err != nil {
			t.Fatalf("ValidName(%q) accepted, but Rel failed: %v", name, err)
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			t.Fatalf("ESCAPE: ValidName(%q) accepted, but it resolves outside the root (rel %q)", name, rel)
		}
		if rel != name {
			t.Fatalf("ValidName(%q) accepted, but it does not round-trip: rel is %q", name, rel)
		}
		if strings.ContainsRune(rel, filepath.Separator) {
			t.Fatalf("ValidName(%q) accepted a nested path: %q", name, rel)
		}
		// A leading dot would collide with the dotfiles Link and List rely on.
		if strings.HasPrefix(name, ".") {
			t.Fatalf("ValidName(%q) accepted a dotfile name", name)
		}
	})
}

// The reference splitter is the second place user input becomes a path, and it
// has one job: never hand back a segment ValidName would have rejected.
// FuzzValidName owns the "accepted name stays one level under the root"
// property, so this one only has to prove nothing routes around it.
func FuzzParseVariantRef(f *testing.F) {
	for _, seed := range []string{
		"claude:plan", "claude:plan:ci", "claude:plan:ci:extra",
		"claude:../x:v", "claude:plan:../x", "claude:plan:", "claude::",
		"claude:default:v", "claude:plan:.hidden", "::", ":", "", "claude",
		"claude:plan:" + strings.Repeat("a", 300),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, ref string) {
		_, name, v, err := ParseVariantRef(ref)
		if err != nil {
			return // rejected: nothing to prove
		}
		if err := ValidName(name); err != nil {
			t.Fatalf("ParseVariantRef(%q) accepted profile %q that ValidName rejects: %v", ref, name, err)
		}
		if v != "" {
			if err := ValidName(v); err != nil {
				t.Fatalf("ParseVariantRef(%q) accepted variant %q that ValidName rejects: %v", ref, v, err)
			}
		}
		if n := strings.Count(ref, ":"); n > 2 {
			t.Fatalf("ParseVariantRef(%q) accepted %d segments; depth is exactly three", ref, n+1)
		}
	})
}
