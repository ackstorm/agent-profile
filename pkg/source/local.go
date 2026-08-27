package source

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ackstorm/agent-profile/pkg/schema"
)

// ResolveLocal resolves §19's local source against the manifest's own
// directory.
//
// A local source is NOT a licence to read the filesystem. A manifest arrives
// from a cloned repository as often as from a hand-written file, and its author
// is not always its reader, so a path is contained to the directory the
// manifest sits in. `../../etc` is refused rather than resolved.
//
// Local sources are never cached and carry no resolved ref: they are mutable
// development content by definition, and a receipt for bytes that can change
// under you would be a lie (§32).
func ResolveLocal(base string, s schema.LocalSource) (Resolved, error) {
	root, err := filepath.Abs(base)
	if err != nil {
		return Resolved{}, err
	}
	dir, err := contain(root, s.Path)
	if err != nil {
		return Resolved{}, fmt.Errorf("local source path %q: %w", s.Path, err)
	}
	if s.Subpath != "" {
		// subtree is the same containment check the fetchers use, including
		// the symlink resolution schema cannot do.
		if dir, err = subtree(dir, s.Subpath); err != nil {
			return Resolved{}, err
		}
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return Resolved{}, fmt.Errorf("local source %q: %w", s.Path, err)
	}
	if !fi.IsDir() && s.Subpath != "" {
		return Resolved{}, fmt.Errorf("local source %q: subpath %q is not a directory", s.Path, s.Subpath)
	}
	return Resolved{Dir: dir, Anonymous: true}, nil
}

// contain joins p onto root and refuses anything that leaves it, following
// symlinks first — the escape a lexical check alone would miss.
func contain(root, p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("must not be empty")
	}
	if filepath.IsAbs(p) {
		return "", fmt.Errorf("must be relative to the manifest directory")
	}
	joined := filepath.Join(root, filepath.FromSlash(p))
	real, err := filepath.EvalSymlinks(joined)
	if err != nil {
		return "", err
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(realRoot, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("escapes the manifest directory")
	}
	return real, nil
}
