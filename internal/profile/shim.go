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

// Shim builds the directory that a.ConfigEnv points at for a shimmed agent, and
// re-asserts it. Returns the directory, and the names of any entries a program
// created inside it for real instead of resolving through a passthrough link.
//
// The contents are:
//
//	<profile>/<Rel>/<Entry>  -> the profile directory
//	<profile>/<Rel>/<other>  -> the real <ConfigBase>/<other>, for every other entry
//
// The agent finds only the profile under its own name, which is the isolation.
// Everything else it spawns — git, gh, npm, language servers — follows a
// passthrough link to its own real config, which is what makes setting a shared
// variable acceptable here. Without the passthrough this would silently redirect
// every one of those programs into the profile.
//
// Re-asserted on every run because ~/.config gains entries over time, and a
// profile created last month must not hide a tool installed yesterday.
func Shim(a agent.Agent, dir string) (shimDir string, foundReal []string, err error) {
	// TODO(task2/3): iterate
	if len(a.Shims) == 0 {
		return "", nil, nil
	}
	shimDir = filepath.Join(dir, a.Shims[0].Rel)
	if err := os.MkdirAll(shimDir, 0o700); err != nil {
		return "", nil, err
	}
	base := ConfigBase()
	if base == "" {
		return "", nil, fmt.Errorf("cannot determine the real config directory for %s", a.Name)
	}
	want, err := shimTargets(a, dir, base)
	if err != nil {
		return "", nil, err
	}

	// Everything below goes through a Root confined to the shim, so no link we
	// write and nothing we remove can land outside it.
	root, err := os.OpenRoot(shimDir)
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = root.Close() }()

	linked, err := assertLinks(root, want)
	if err != nil {
		return "", nil, err
	}
	pruned, err := pruneStale(root, shimDir, want)
	if err != nil {
		return "", nil, err
	}
	foundReal = append(linked, pruned...)
	sort.Strings(foundReal)
	return shimDir, foundReal, nil
}

// shimTargets maps each name in the shim to what it must point at: the agent's
// own name to the profile, everything else in the real config base to itself.
func shimTargets(a agent.Agent, dir, base string) (map[string]string, error) {
	// TODO(task2/3): iterate
	want := map[string]string{a.Shims[0].Entry: dir}
	entries, err := os.ReadDir(base)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("cannot read %s: %w", base, err)
	}
	for _, e := range entries {
		if e.Name() == a.Shims[0].Entry {
			// The agent's own real config: deliberately NOT passed through. This
			// single omission is the whole isolation.
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
