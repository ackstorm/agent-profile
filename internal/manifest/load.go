//go:build unix

package manifest

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Load reads one manifest file, or every manifest in one directory.
//
// A directory is scanned NON-RECURSIVELY, sorted by filename so a run is
// deterministic. Recursion is not in v1 for a concrete reason: a node_modules
// under a profiles repository would be walked for nothing.
//
// Everything is parsed and validated before Load returns, so §10's "abort on any
// parse error, with nothing done" is a property of this function rather than a
// discipline the caller has to remember.
func Load(path string) ([]Manifest, error) {
	files, err := manifestFiles(path)
	if err != nil {
		return nil, err
	}
	out := make([]Manifest, 0, len(files))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		m, err := Parse(f, b)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := duplicates(out); err != nil {
		return nil, err
	}
	return out, nil
}

func manifestFiles(path string) ([]string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		// A file is loaded directly, whatever it is called: `ap sync
		// ./team.conf` is a reasonable thing to want and the extension filter
		// exists to pick manifests OUT of a directory, not to police names.
		return []string{path}, nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		switch strings.ToLower(filepath.Ext(e.Name())) {
		case ".yaml", ".yml":
			files = append(files, filepath.Join(path, e.Name()))
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("%s holds no *.yaml or *.yml manifest (the scan is not recursive)", path)
	}
	slices.Sort(files)
	return files, nil
}

// duplicates refuses two manifests producing the same <platform>:<name>.
//
// Sharing a NAME across two platforms is fine and is a documented shape:
// execute-claude.yaml and execute-codex.yaml both say `name: execute`. What is
// refused is two files claiming the same identity, because v1 has no merge and
// no override semantics and picking a winner silently is how half a
// declaration goes missing.
func duplicates(ms []Manifest) error {
	seen := map[string]string{}
	for _, m := range ms {
		for _, p := range m.Platforms {
			id := p.Agent.Name + ":" + m.Name
			if prev, ok := seen[id]; ok {
				return fmt.Errorf("%s is declared twice, in %s and %s: there are no merge or override semantics",
					id, prev, m.Path)
			}
			seen[id] = m.Path
		}
	}
	return nil
}
