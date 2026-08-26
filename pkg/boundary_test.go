package pkg_test

import (
	"go/build"
	"io/fs"
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
	pkgRoot := filepath.Join(root, "pkg")

	var found int
	err = filepath.WalkDir(pkgRoot, func(dir string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		p, err := build.ImportDir(dir, 0)
		if err != nil {
			// A directory with no .go files (or only files excluded by build
			// constraints) is not a package — walk past it rather than fail.
			if _, ok := err.(*build.NoGoError); ok {
				return nil
			}
			return err
		}
		found++
		imports := p.Imports
		imports = append(imports, p.TestImports...)
		// XTestImports covers external test files (package foo_test), which
		// Imports/TestImports do not. A guard that skips it can be defeated by
		// a single agentreg_test.go importing internal/ — proven by mutation.
		imports = append(imports, p.XTestImports...)
		for _, imp := range imports {
			if strings.Contains(imp, "/internal/") {
				t.Errorf("%s imports %s; pkg/ must not import internal/", dir, imp)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found == 0 {
		t.Fatal("no packages under pkg/")
	}
}
