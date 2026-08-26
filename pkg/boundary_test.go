package pkg_test

import (
	"go/build"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pkg/ is imported by ackstorm/ach, which is a different module, so an
// internal/ import here would simply not compile there. Go enforces that for
// ach and not for us, which means we would find out in the wrong repository.
// This test finds out here.
func TestPkgNeverImportsInternal(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := filepath.Glob(filepath.Join(root, "pkg", "*"))
	if err != nil {
		t.Fatal(err)
	}
	// The glob also matches this file's own siblings (boundary_test.go itself
	// among them) — only directories are packages.
	var pkgs []string
	for _, e := range entries {
		if fi, err := os.Stat(e); err == nil && fi.IsDir() {
			pkgs = append(pkgs, e)
		}
	}
	if len(pkgs) == 0 {
		t.Fatal("no packages under pkg/")
	}
	for _, dir := range pkgs {
		p, err := build.ImportDir(dir, 0)
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		for _, imp := range append(p.Imports, p.TestImports...) {
			if strings.Contains(imp, "/internal/") {
				t.Errorf("%s imports %s; pkg/ must not import internal/", dir, imp)
			}
		}
	}
}
