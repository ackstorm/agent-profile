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

// Clone copies an agent's declared configuration from one config directory into
// another. src may be another profile or, via `--from default`, the agent's real
// config directory — Clone does not distinguish the two.
//
// One selection rule: only the paths named in a.CloneAllow are copied, each
// walked whole (files and, for a directory entry, everything under it). A
// symlink is never copied, whether it is the allowlisted entry itself or found
// while walking under one — those are the Shared entries, and copying their
// contents would duplicate the user's session history, while copying the link
// itself is pointless since Link recreates it straight after. A missing
// allowlist entry is not an error: profiles and real config directories differ
// in what they happen to contain.
//
// Everything else in src — accumulated runtime state, caches, the config shim —
// is left behind simply by never being named. See Agent.CloneAllow for why that
// is an allowlist and not a list of exclusions.
//
// dst must be an existing empty directory that is not inside src. Clone does not
// create it; the caller does.
func Clone(a agent.Agent, src, dst string) error {
	// Resolve src before anything else. Lstat below would otherwise inspect the
	// symlink rather than the directory it points to.
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

	root, err := os.OpenRoot(dst)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()

	skip := skipPaths(a)

	for _, rel := range a.CloneAllow {
		if err := cloneEntry(root, src, rel, skip); err != nil {
			return fmt.Errorf("cloning %s: %w", rel, err)
		}
	}
	return nil
}

// skipPaths is what is never cloned, regardless of what CloneAllow says: the
// credential, a profile's own session history, and the config shim. Not
// exploitable by the current registry — TestCloneAllowNeverNamesASharedPath
// already keeps a Shared path itself out of CloneAllow — but this backstop does
// not depend on that discipline holding for every future edit, the same reason
// Delete re-checks Default independently of ParseRef.
//
// A function rather than eight lines inside Clone because CloneSettings applies
// the same backstop, and two copies of this would eventually disagree.
func skipPaths(a agent.Agent) []string {
	skip := make([]string, 0, len(a.Shared)+len(a.State)+1)
	for _, s := range a.Shared {
		skip = append(skip, filepath.Clean(s.Rel))
	}
	for _, st := range a.State {
		skip = append(skip, filepath.Clean(st))
	}
	if a.Shim != nil {
		skip = append(skip, filepath.Clean(a.Shim.Rel))
	}
	return skip
}

// cloneEntry copies one CloneAllow entry — a file or a whole directory — from
// src into dst through root, which is confined to dst. skip is the Shared/
// State/Shim backstop computed once by Clone; isShared checks it by prefix, so
// an entry that is itself skipped, or a parent directory of something skipped,
// is refused before anything is written.
//
// escapesRoot is checked before anything else, rather than left to os.Root to
// catch: filepath.Join(src, rel) Cleans the two together, so a "rel" such as
// "../../etc/passwd" can collapse into a path that simply does not exist under
// src at typical temp-dir depths — which "tolerate missing" would then wave
// through instead of rejecting.
func cloneEntry(root *os.Root, src, rel string, skip []string) error {
	if escapesRoot(rel) {
		return fmt.Errorf("allowlist entry %q escapes the profile directory", rel)
	}
	if isShared(rel, skip) {
		return nil // never clone the credential, session history, or the shim
	}
	srcPath := filepath.Join(src, rel)
	fi, err := os.Lstat(srcPath)
	if os.IsNotExist(err) {
		return nil // tolerated: profiles and real config dirs differ in contents
	}
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return nil // never copy a symlink; Link recreates the shared ones
	}
	if !fi.IsDir() {
		return copyFileToRoot(root, srcPath, rel, fi)
	}
	return filepath.WalkDir(srcPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		entryRel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if isShared(entryRel, skip) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			// Also creates the entry's own root directory, on the first call
			// (entryRel == rel), which is what makes an allowlisted directory that
			// happens to be empty in the source still appear in the clone.
			return root.MkdirAll(entryRel, info.Mode().Perm())
		}
		// A symlink lands here too, never in the branch above: WalkDir's
		// DirEntry.IsDir() reflects the dirent's own type bit, and a symlink's
		// type bit is ModeSymlink even when it points at a directory. So this
		// is what actually skips a symlink — a dedicated branch before this
		// point would be dead code, unreachable because IsDir() is already
		// false for every symlink.
		if !d.Type().IsRegular() {
			return nil // symlinks, sockets, fifos, devices: nothing a profile needs
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return copyFileToRoot(root, path, entryRel, info)
	})
}

// escapesRoot reports whether rel is not a safe, single-name-scoped entry:
// "." (a valid filepath.Clean result, but never what a CloneAllow entry
// means — it would clone the entire source directory rather than a named
// subset of it), an absolute path (not a name relative to anything), or a
// path that, once cleaned, references something above the directory it will
// be joined onto — checked precisely rather than with
// strings.Contains(rel, ".."), which would also reject a name that merely
// contains the two characters, like "settings..bak". Clean already collapses
// any interior ".." against a preceding component, so a cleaned rel that
// still has one can only carry it as a leading run.
func escapesRoot(rel string) bool {
	if filepath.IsAbs(rel) {
		return true
	}
	cleaned := filepath.Clean(rel)
	if cleaned == "." {
		return true
	}
	return cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(os.PathSeparator))
}

// isShared reports whether rel is one of the skip paths (Shared, State, or the
// config shim) or lives under one — checked by prefix, not exact match, so a
// CloneAllow entry that is a PARENT of a skip path (e.g. "plugins" when
// "plugins/cache" is Shared) cannot walk into it either.
func isShared(rel string, skip []string) bool {
	rel = filepath.Clean(rel)
	for _, s := range skip {
		if rel == s || strings.HasPrefix(rel, s+string(os.PathSeparator)) {
			return true
		}
	}
	return false
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

// copyFileToRoot copies the regular file at src to rel inside root. Every write
// — the parent directory and the file itself — goes through root, confined to
// dst, so a rel containing ".." fails instead of writing outside the clone.
func copyFileToRoot(root *os.Root, src, rel string, fi os.FileInfo) error {
	if parent := filepath.Dir(rel); parent != "." {
		if err := root.MkdirAll(parent, 0o700); err != nil {
			return err
		}
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }() // read-only: a close error says nothing
	// Same permissions as the source: profiles hold credentials and settings.
	out, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, fi.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close() // returning the copy error, which is the useful one
		return err
	}
	return out.Close()
}
