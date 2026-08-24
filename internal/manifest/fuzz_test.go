//go:build unix

package manifest

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ackstorm/agent-profile/internal/profile"
)

// The traversal bug in this repository got through code review once, in
// `--from`, which at least came from the user's own keyboard. A manifest comes
// from a repository somebody else wrote, so assert the property rather than an
// enumerated list of bad inputs: if Parse accepts a manifest, every name in it
// stays one level under a root when joined.
func FuzzParse(f *testing.F) {
	for _, seed := range []string{
		goodManifest,
		"version: 1\nname: default\nplatforms:\n  claude:\n",
		"version: 1\nname: ../../../.ssh\nplatforms:\n  claude:\n",
		"version: 1\nname: x\nplatforms:\n  claude:\n    variants:\n      ../y:\n        args: -p\n",
		"version: 1\nname: x\nplatforms:\n  claude:\n    variants:\n      v:\n        args: \"a\\\"b\"\n",
		"version: 1\nname: x\nplatforms:\n  claude:\n    install:\n      - \"a # b\"\n",
		"a:\n\tb: c\n",
		"---\n",
		"version: 1\nname: x\nplatforms:\n  claude: &a\n",
		"",
	} {
		f.Add(seed)
	}

	const root = "/profiles/claude"

	f.Fuzz(func(t *testing.T, src string) {
		m, err := Parse("fuzz.yaml", []byte(src))
		if err != nil {
			return // rejected: nothing to prove
		}
		names := []string{m.Name}
		for _, p := range m.Platforms {
			for _, v := range p.Variants {
				names = append(names, v.Name)
			}
		}
		for _, name := range names {
			if name == profile.Default {
				// The sentinel is not a path component. It is only legal as
				// the manifest's name, never as a variant's — which the
				// schema enforces by calling ValidName on variants, and
				// ValidName rejects it.
				if name != m.Name {
					t.Fatalf("the sentinel %q was accepted as a variant name", name)
				}
				continue
			}
			joined := filepath.Join(root, name)
			rel, err := filepath.Rel(root, joined)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) ||
				strings.Contains(rel, string(filepath.Separator)) {
				t.Fatalf("ESCAPE: Parse accepted the name %q, which resolves to %q", name, joined)
			}
		}
	})
}
