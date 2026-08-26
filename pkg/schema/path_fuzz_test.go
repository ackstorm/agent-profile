package schema

import (
	"path/filepath"
	"strings"
	"testing"
)

// FuzzResolveExtends and FuzzValidRelPath live in their own file, separate
// from fuzz_test.go, because both were added in a batch that ran alongside
// other work touching fuzz_test.go — same targets, different file, to avoid
// a collision.

func FuzzResolveExtends(f *testing.F) {
	f.Add("base")
	f.Add("./mid.yaml")
	f.Add("../secret.yaml")
	f.Add("../../etc/passwd")
	f.Add("a/b/../../..")
	f.Fuzz(func(t *testing.T, name string) {
		dir := filepath.Join(string(filepath.Separator), "profile", "manifests")
		p, err := resolveExtends(dir, &Node{Kind: Scalar, Str: name})
		if err != nil {
			return
		}
		// Anything accepted must resolve inside dir.
		rel, rerr := filepath.Rel(dir, p)
		if rerr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			t.Fatalf("resolveExtends(%q) escaped the manifest directory: %q", name, p)
		}
	})
}

func FuzzValidRelPath(f *testing.F) {
	f.Add("a/b")
	f.Add("../x")
	f.Fuzz(func(t *testing.T, p string) {
		if err := validRelPath("destination", p); err != nil {
			return
		}
		// Anything accepted must stay under the root when joined to it.
		root := "/profile"
		got := filepath.Join(root, p)
		if !strings.HasPrefix(got, root+string(filepath.Separator)) && got != root {
			t.Fatalf("accepted %q escapes: %q", p, got)
		}
	})
}
