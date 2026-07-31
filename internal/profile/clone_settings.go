//go:build unix

package profile

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/ackstorm/agent-profile/internal/agent"
)

// CloneSettings is `ap create --only-settings`: it builds the new profile's
// settings file out of the named keys of the source's, and copies nothing else
// at all — no other CloneAllow entry is even looked at.
//
// A narrowing of Clone, never a widening. The one path it can read is
// a.Settings, which the registry keeps inside CloneAllow, and the guards Clone
// relies on are re-applied here rather than assumed: containment, escapesRoot,
// the Shared/State/Shim backstop, and a write confined to an os.Root over dst.
//
// The slicing happens before the write, not inside it. cloneEntry stays a
// function that copies bytes without understanding them, which is what keeps the
// containment argument readable; this one understands the bytes and writes
// through the same confinement.
//
// found is the keys that were present, in the order given; missing is the rest.
// A missing key is the caller's warning to print, not an error: a typo must not
// seed silence, and must not fail a create that otherwise worked.
func CloneSettings(a agent.Agent, src, dst string, keys []string) (found, missing []string, err error) {
	if a.Settings == "" {
		return nil, nil, fmt.Errorf("no settings file is known for %s", a.Name)
	}
	if escapesRoot(a.Settings) {
		return nil, nil, fmt.Errorf("settings file %q escapes the profile directory", a.Settings)
	}
	if isShared(a.Settings, skipPaths(a)) {
		return nil, nil, fmt.Errorf("settings file %q is shared or is session state; it is never cloned", a.Settings)
	}
	src, err = filepath.EvalSymlinks(src)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot read source profile: %w", err)
	}
	if err := containment(src, dst); err != nil {
		return nil, nil, err
	}

	srcPath := filepath.Join(src, a.Settings)
	fi, err := os.Lstat(srcPath)
	if os.IsNotExist(err) {
		// Tolerated, like a missing CloneAllow entry: profiles and real config
		// directories differ in what they happen to contain. Every key is
		// missing, and the caller says so.
		return nil, keys, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		// Clone skips a symlink silently because it is one entry of many. Here
		// it is the entire command, so producing an empty profile without a word
		// would be worse than refusing.
		return nil, nil, fmt.Errorf("%s is a symlink; refusing to follow it out of the profile", a.Settings)
	}
	b, err := os.ReadFile(srcPath) // #nosec G304 -- srcPath is a registry name joined onto a validated profile dir
	if err != nil {
		return nil, nil, err
	}
	out, found, err := sliceSettings(a.SettingsFormat, b, keys)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", a.Settings, err)
	}
	for _, k := range keys {
		if !slices.Contains(found, k) {
			missing = append(missing, k)
		}
	}
	if len(found) == 0 {
		return nil, missing, nil // nothing found, so no file: see sliceSettings
	}

	root, err := os.OpenRoot(dst)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = root.Close() }()
	// O_EXCL: the destination is a profile being created and does not have this
	// file. Keeping it that way is what makes this a copy rather than the first
	// half of a config-management feature.
	f, err := root.OpenFile(a.Settings, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, nil, err
	}
	if _, err := f.Write(out); err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	return found, missing, f.Close()
}
