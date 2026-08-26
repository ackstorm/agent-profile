package schema

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ackstorm/agent-profile/pkg/agentreg"
)

// Load reads a manifest and folds its extends chain, parent first. It returns
// the composed tree and the runtime names the LEAF document declared for
// itself — §7.2 warns about a locally declared non-target block, while §7.1
// ignores an inherited one silently, and only the leaf's own keys tell them
// apart.
//
// Resolution is local and deterministic (§5.2): a bare name is
// <manifest dir>/<name>.yaml, a relative path is joined to the manifest
// directory, and nothing else is searched. There is no GitHub lookup, no
// registry and no configured search path.
func Load(path string) (*Node, []string, error) {
	return load(path, nil)
}

// maxExtendsDepth bounds the chain the way seen already bounds a cycle: seen
// catches a file appearing twice, but nothing capped a long chain of files
// that never repeat. No real profile extends more than a handful of parents,
// so this is generous headroom against a runaway chain, not a limit any real
// manifest should ever approach.
const maxExtendsDepth = 32

func load(path string, seen []string) (*Node, []string, error) {
	if len(seen) >= maxExtendsDepth {
		return nil, nil, fmt.Errorf("%s: extends chain longer than %d, the maximum (maxExtendsDepth)",
			filepath.Base(path), maxExtendsDepth)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, nil, err
	}
	for _, s := range seen {
		if s == abs {
			return nil, nil, fmt.Errorf("%s: extends cycle: %s is already in the chain",
				filepath.Base(path), filepath.Base(abs))
		}
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return nil, nil, err
	}
	doc, err := ParseYAML(b)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", filepath.Base(abs), err)
	}
	leafRuntimes := runtimeNames(doc)

	ex, ok := doc.Map["extends"]
	if !ok {
		return doc, leafRuntimes, nil
	}
	parentPath, err := resolveExtends(filepath.Dir(abs), ex)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", filepath.Base(abs), err)
	}
	parent, _, err := load(parentPath, append(seen, abs))
	if err != nil {
		return nil, nil, err
	}
	// The child's own extends key is not part of the effective profile: it has
	// been consumed. Removing it here keeps render honest.
	delete(doc.Map, "extends")
	doc.Keys = removeKey(doc.Keys, "extends")

	composed, err := Merge(parent, doc, V1Schema())
	if err != nil {
		return nil, nil, err
	}
	return composed, leafRuntimes, nil
}

// resolveExtends turns manifest text into a path. Every branch stays inside the
// manifest directory: a bare name goes through agentreg.ValidName, and a
// relative path is rejected if it climbs out. `extends` is user input becoming
// a path, which is the class of thing --from got wrong.
//
// Known limit: containment is checked on the path STRING, not the filesystem
// — a symlink planted inside the manifest directory that points outside it is
// not detected, unlike internal/profile/share.go's os.Root-based containment.
// It grants no new privilege (anyone who can plant a symlink there can plant
// the file itself), but the limit is real and stated here rather than left to
// be found later.
func resolveExtends(dir string, ex *Node) (string, error) {
	name, err := ex.Text()
	if err != nil {
		return "", fmt.Errorf("extends: %w", err)
	}
	if filepath.IsAbs(name) {
		return "", fmt.Errorf("extends %q: must be relative", name)
	}
	if strings.ContainsRune(name, '/') || strings.HasSuffix(name, ".yaml") {
		p := filepath.Join(dir, filepath.Clean(name))
		rel, err := filepath.Rel(dir, p)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("extends %q: a parent must live in the manifest directory", name)
		}
		return p, nil
	}
	if err := agentreg.ValidName(name); err != nil {
		return "", fmt.Errorf("extends %q: %w", name, err)
	}
	return filepath.Join(dir, name+".yaml"), nil
}

func runtimeNames(doc *Node) []string {
	r, ok := doc.Map["runtimes"]
	if !ok || r.Kind != Mapping {
		return nil
	}
	return slices.Clone(r.Keys)
}

func removeKey(keys []string, k string) []string {
	if i := slices.Index(keys, k); i >= 0 {
		return slices.Delete(keys, i, i+1)
	}
	return keys
}
