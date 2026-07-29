//go:build unix

package profile

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ackstorm/agent-profile/internal/agent"
)

// Clone copies the contents of one profile directory into another, skipping the
// shared state.
//
// Two skip rules:
//
//   - symlinks, because they are the shared entries; copying their contents
//     would duplicate the user's session history, and copying the link is
//     pointless since Link recreates it straight after.
//   - anything at or under a Shared relative path, or under the config shim,
//     even when it is a real file in the source. That happens with profiles
//     created before an entry was added to the registry, and copying it would
//     make Link fail on the destination. The shim is rebuilt from the real config
//     base anyway, so a copy would only go stale.
//
// No per-agent allowlist is needed: a profile only contains what ap and the
// agent's own installer put there.
//
// dst must be an existing empty directory that is not inside src. Clone does not
// create it; the caller does.
func Clone(a agent.Agent, src, dst string) error {
	// Resolve src before anything else. WalkDir lstats its root, so a symlinked
	// source profile would walk nothing at all and Clone would report success
	// after copying zero files.
	src, err := filepath.EvalSymlinks(src)
	if err != nil {
		return fmt.Errorf("cannot read source profile: %w", err)
	}
	fi, err := os.Lstat(src)
	if err != nil {
		return fmt.Errorf("cannot read source profile %s: %w", src, err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("source profile %s is not a directory", src)
	}
	if err := containment(src, dst); err != nil {
		return err
	}

	shared := make([]string, 0, len(a.Shared)+1)
	for _, s := range a.Shared {
		shared = append(shared, filepath.Clean(s.Rel))
	}
	if a.Shim != nil {
		shared = append(shared, filepath.Clean(a.Shim.Rel))
	}

	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if isShared(rel, shared) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			return os.MkdirAll(target, info.Mode().Perm())
		}
		// Symlinks land here too, and are skipped: WalkDir never reports one as a
		// directory, so it reaches this check rather than the branch above. That is
		// the second skip rule from the doc comment - a separate ModeSymlink test
		// before this one would be dead code. TestCloneSkipsAnUnsharedSymlink pins
		// the behaviour so a future edit to this line cannot change it silently.
		if !d.Type().IsRegular() {
			return nil // symlinks, sockets, fifos, devices: nothing a profile needs
		}
		return copyFile(path, target, d)
	})
}

// containment rejects a destination that is src itself or lives inside src.
//
// Without this, `ap create --from . <name>` resolves the source to the parent of
// the destination, so WalkDir descends into the directory Clone is writing to and
// recurses until the path is too long for the filesystem — 2000+ directories, and
// with a populated source it re-copies every real file at each level.
// src arrives already resolved, so dst must be resolved too or the comparison is
// between a real path and a symlinked one. On macOS that is the normal case:
// /var is a symlink to /private/var, so an unresolved dst under /var/folders
// looks like it escapes a src under /private/var/folders and the guard passes on
// exactly the overlap it exists to catch.
func containment(src, dst string) error {
	dst, err := filepath.EvalSymlinks(dst)
	if err != nil {
		return fmt.Errorf("cannot read destination profile: %w", err)
	}
	rel, err := filepath.Rel(src, dst)
	if err != nil {
		return err // different volumes: cannot overlap
	}
	if rel == "." {
		return fmt.Errorf("source and destination are the same directory: %s", src)
	}
	// Escaping src means rel is ".." or starts with "../" — checked precisely
	// rather than with HasPrefix(rel, ".."), which would also accept a sibling
	// directory literally named "..foo".
	if rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("destination %s is inside the source %s", dst, src)
	}
	return nil
}

// isShared reports whether rel is one of the shared paths or lives under one.
func isShared(rel string, shared []string) bool {
	rel = filepath.Clean(rel)
	for _, s := range shared {
		if rel == s || strings.HasPrefix(rel, s+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

func copyFile(src, dst string, d os.DirEntry) error {
	info, err := d.Info()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }() // read-only: a close error says nothing
	// Same permissions as the source: profiles hold credentials and settings.
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close() // returning the copy error, which is the useful one
		return err
	}
	return out.Close()
}
