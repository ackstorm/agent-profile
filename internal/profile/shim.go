//go:build unix

package profile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/ackstorm/agent-profile/internal/agent"
)

// ConfigBase is the directory the agent's config variable normally resolves to,
// read exactly the way the agent itself reads it: XDG_CONFIG_HOME when set,
// otherwise ~/.config.
//
// This must be evaluated against the environment ap inherited, before ap sets
// anything, or the shim would end up pointing at itself.
//
// Delegates to agent.ConfigBase, which is also what opencode's registry entry
// derives its Config from — one definition, not two that can drift apart.
func ConfigBase() string {
	return agent.ConfigBase()
}

// Shim builds the directory each of a's shims points at, and re-asserts them.
// Returns the names of any entries a program created inside one for real instead
// of resolving through a passthrough link, prefixed by the shim they are in.
//
// The contents of each are:
//
//	<profile>/<Rel>/<Entry>  -> the profile directory
//	<profile>/<Rel>/<other>  -> the real <Base>/<other>, for every other entry
//
// The agent finds only the profile under its own name, which is the isolation.
// Everything else it spawns — git, gh, npm, language servers — follows a
// passthrough link to its own real directory, which is what makes setting a
// shared variable acceptable here. Without the passthrough this would silently
// redirect every one of those programs into the profile.
//
// Re-asserted on every run because the real bases gain entries over time, and a
// profile created last month must not hide a tool installed yesterday.
func Shim(a agent.Agent, dir string) (foundReal []string, err error) {
	for _, s := range a.Shims {
		found, err := shimOne(s, dir, a.Name)
		if err != nil {
			return nil, err
		}
		for _, n := range found {
			foundReal = append(foundReal, filepath.Join(s.Rel, n))
		}
	}
	sort.Strings(foundReal)
	return foundReal, nil
}

// shimOne builds a single shim directory. This is what Shim's body used to be,
// with the base taken from the spec instead of hardcoded to ConfigBase.
func shimOne(s agent.Shim, dir, agentName string) (foundReal []string, err error) {
	shimDir := filepath.Join(dir, s.Rel)
	if err := os.MkdirAll(shimDir, 0o700); err != nil {
		return nil, err
	}
	base := s.Base()
	if base == "" {
		return nil, fmt.Errorf("cannot determine the real %s directory for %s", s.Env, agentName)
	}
	want, err := shimTargets(s, dir, base)
	if err != nil {
		return nil, err
	}

	// Everything below goes through a Root confined to the shim, so no link we
	// write and nothing we remove can land outside it.
	root, err := os.OpenRoot(shimDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()

	linked, err := assertLinks(root, want)
	if err != nil {
		return nil, err
	}
	pruned, err := pruneStale(root, shimDir, want)
	if err != nil {
		return nil, err
	}
	foundReal = append(linked, pruned...)
	sort.Strings(foundReal)
	return foundReal, nil
}

// shimTargets maps each name in the shim to what it must point at: the agent's
// own name to the profile, everything else in the real config base to itself.
func shimTargets(s agent.Shim, dir, base string) (map[string]string, error) {
	want := map[string]string{s.Entry: dir}
	entries, err := os.ReadDir(base)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("cannot read %s: %w", base, err)
	}
	for _, e := range entries {
		if e.Name() == s.Entry {
			// The agent's own real directory: deliberately NOT passed through.
			// This single omission is the whole isolation.
			continue
		}
		want[e.Name()] = filepath.Join(base, e.Name())
	}
	return want, nil
}

// assertLinks makes every wanted name a symlink to its target, replacing existing
// links unconditionally so one whose destination moved is re-pointed. Names that
// are real files or directories are reported, never removed: a program wrote its
// config there, and it is the user's data.
func assertLinks(root *os.Root, want map[string]string) (foundReal []string, err error) {
	for _, name := range sortedKeys(want) {
		fi, err := root.Lstat(name)
		switch {
		case err == nil && fi.Mode()&os.ModeSymlink != 0:
			if err := root.Remove(name); err != nil {
				return nil, err
			}
		case err == nil:
			foundReal = append(foundReal, name)
			continue
		case !errors.Is(err, fs.ErrNotExist):
			return nil, fmt.Errorf("cannot inspect %s in the config shim: %w", name, err)
		}
		if err := root.Symlink(want[name], name); err != nil {
			return nil, err
		}
	}
	return foundReal, nil
}

// pruneStale removes links to entries that have since disappeared from the real
// config base, so the shim does not accumulate dangling names.
func pruneStale(root *os.Root, shimDir string, want map[string]string) (foundReal []string, err error) {
	entries, err := os.ReadDir(shimDir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if _, keep := want[e.Name()]; keep {
			continue
		}
		if e.Type()&os.ModeSymlink == 0 {
			foundReal = append(foundReal, e.Name())
			continue
		}
		if err := root.Remove(e.Name()); err != nil {
			return nil, err
		}
	}
	return foundReal, nil
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
