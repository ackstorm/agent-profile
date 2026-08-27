package source

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// FuzzExtractTar is the traversal-bug lesson applied to the one place in this
// package that parses attacker-chosen bytes. Whatever the extractor does with
// a malformed archive, nothing may appear outside the root it was given.
func FuzzExtractTar(f *testing.F) {
	f.Add(tarFiles(f, map[string]string{"a/b.md": "hi"}))
	f.Add(tarWithPath(f, "../../escape"))
	f.Add(tarWithSymlink(f, "esc", "../../etc"))
	f.Add(tarWithSize(f, "big", 1<<17))
	f.Add([]byte("not a gzip stream at all"))

	f.Fuzz(func(t *testing.T, b []byte) {
		parent := t.TempDir()
		root := filepath.Join(parent, "root")
		if err := os.Mkdir(root, 0o755); err != nil {
			t.Fatal(err)
		}
		canary := filepath.Join(parent, "canary")
		if err := os.WriteFile(canary, []byte("untouched"), 0o600); err != nil {
			t.Fatal(err)
		}

		_ = extractTar(context.Background(), b, root, 1<<16)

		// Nothing new beside root, and the canary unchanged: the two ways an
		// escape would show up.
		ents, err := os.ReadDir(parent)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range ents {
			if e.Name() != "root" && e.Name() != "canary" {
				t.Fatalf("extraction created %q outside the root", e.Name())
			}
		}
		got, err := os.ReadFile(canary)
		if err != nil || string(got) != "untouched" {
			t.Fatalf("the canary was written through: %q %v", got, err)
		}
		// A symlink inside the root that points out of it is an escape the
		// next reader would follow, so it must not have been created.
		_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil //nolint:nilerr // a malformed archive may leave an unreadable path; that is not an escape
			}
			if d.Type()&os.ModeSymlink == 0 {
				return nil
			}
			target, err := os.Readlink(p)
			if err != nil {
				return nil //nolint:nilerr // same
			}
			if filepath.IsAbs(target) || strings.HasPrefix(filepath.Clean(target), "..") {
				t.Fatalf("extraction created an escaping symlink %q -> %q", p, target)
			}
			return nil
		})
	})
}
