//go:build unix

package profile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/ackstorm/agent-profile/internal/agent"
)

// orphanSuffix names what a share used to be before an agent overwrote the link
// with a file of its own. Not ".bak": this is a credential, and the name has to
// say that ap put it there and that nothing reads it any more.
const orphanSuffix = ".ap-orphan"

// Link points the profile's shared entries at the agent's real state, so
// sessions, credentials and workspace trust never fork per profile.
//
// Called by `ap create` and again by every `ap run`. The repeat is deliberate:
// agents rewrite their credential files (codex refreshes OAuth tokens into
// auth.json), and a temp-file-plus-rename would replace our symlink with a
// regular file, silently ending the sharing. Re-linking self-heals it.
//
// Measured, claude v2.1.220: this is not hypothetical. Two profiles on the
// reference machine had a 74 KB regular file where the link had been, differing
// from the shared credential in exactly the four claudeAiOauth leaves.
//
// Every share is a file — the agent's credential. A missing one cannot be invented,
// so it is reported in skipped for the caller to surface.
//
// A real file found where the link should be is moved aside to <rel>.ap-orphan and
// reported in orphaned, not deleted: it is a credential, and it may hold a token
// newer than the shared one. Refusing outright was the old behaviour and it was
// wrong — claude does the temp-file-plus-rename described above during ordinary
// use, so the refusal dead-ended every later `ap run` on that profile until
// someone moved the file by hand. Doing it automatically is the same operation the
// error message used to ask for.
//
// Link also removes any symlink sitting at a path the registry lists in Unshared —
// state that used to be common and no longer is. That makes a change to the registry
// take effect in profiles created before it, instead of only in new ones.
func Link(a agent.Agent, dir string) (linked, skipped, unshared, orphaned []string, err error) {
	// Inspect and remove through an os.Root confined to the profile directory.
	// os.Lstat only refuses to follow the FINAL path component: every ancestor is
	// resolved by the kernel, so with a nested Rel such as "plugins/cache" a
	// symlinked "plugins" would make the remove-and-relink happen inside the
	// user's real home instead of the profile. os.Root refuses symlink traversal
	// by construction, and every operation below goes through it — inspect, remove,
	// mkdir and symlink — so no step in this loop can leave the profile.
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	defer func() { _ = root.Close() }()

	for _, s := range a.Shared {
		if _, err := os.Stat(s.From); err != nil {
			// A credential cannot be invented; tell the caller so it can say so.
			// Staying silent about this was a trap: sharing quietly did not happen,
			// the agent then wrote its own real file into the profile, and every
			// later run dead-ended on "refusing to replace real file".
			skipped = append(skipped, s.Rel)
			continue
		}
		fi, err := root.Lstat(s.Rel)
		switch {
		case err == nil && fi.Mode()&os.ModeSymlink != 0:
			// Already a link. Remove it so the target is re-asserted; this is what
			// makes Link idempotent and self-healing.
			if err := root.Remove(s.Rel); err != nil {
				return linked, skipped, unshared, orphaned, err
			}
		case err == nil:
			// A real file where the link belongs: the agent rewrote it. Move it
			// aside rather than removing it — it is a credential, and it may hold a
			// token newer than the shared one. Renamed through the same os.Root, so
			// neither name can leave the profile. A previous orphan is overwritten:
			// it is by definition the staler of the two.
			if err := root.Rename(s.Rel, s.Rel+orphanSuffix); err != nil {
				return linked, skipped, unshared, orphaned, fmt.Errorf(
					"cannot move aside the real file at %s: %w",
					filepath.Join(dir, s.Rel), err)
			}
			orphaned = append(orphaned, s.Rel+orphanSuffix)
		case !errors.Is(err, fs.ErrNotExist):
			// Anything other than "not there" — including a symlinked ancestor,
			// which os.Root reports rather than following.
			return linked, skipped, unshared, orphaned, fmt.Errorf("cannot inspect %s in profile: %w", s.Rel, err)
		}
		if parent := filepath.Dir(s.Rel); parent != "." {
			if err := root.Mkdir(parent, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
				return linked, skipped, unshared, orphaned, err
			}
		}
		if err := root.Symlink(s.From, s.Rel); err != nil {
			return linked, skipped, unshared, orphaned, err
		}
		linked = append(linked, s.Rel)
	}

	// Un-share what the registry used to share. Without this, dropping an entry
	// from Shared is a no-op for every profile that already exists: the old symlink
	// stays, and the file goes on being shared forever.
	//
	// Through the same os.Root as everything above, so a symlinked ancestor cannot
	// turn this into a remove somewhere in the real home. Only a symlink is ever
	// removed, and removing a symlink never touches its target — the profile loses
	// the link, the real file is untouched, and the agent regenerates its own copy.
	for _, rel := range a.Unshared {
		fi, err := root.Lstat(rel)
		if err != nil || fi.Mode()&os.ModeSymlink == 0 {
			continue // absent, or a real file that belongs to the profile now
		}
		if err := root.Remove(rel); err != nil {
			return linked, skipped, unshared, orphaned, fmt.Errorf("cannot un-share %s: %w", rel, err)
		}
		unshared = append(unshared, rel)
	}
	return linked, skipped, unshared, orphaned, nil
}

// Delete removes a profile directory.
//
// os.RemoveAll lstats each entry and unlinks symlinks rather than descending
// into them, so the shared session, credential and trust targets in the real
// home are untouched. TestDeleteDoesNotFollowSymlinks is what keeps that true —
// it is the one bug in this program that would be irreversible.
//
// Default is refused here directly, not only via ParseRef upstream: Dir(a,
// Default) is the agent's real config directory, and this is the one call in
// the whole program that would remove it outright. The guard must not depend
// on a validator having been called correctly somewhere else, the same reason
// Link and Discard go through an os.Root instead of trusting their caller.
func Delete(a agent.Agent, name string) error {
	if name == Default {
		return fmt.Errorf("refusing to delete %s:%s: it is your real config, not a profile ap made",
			a.Name, Default)
	}
	dir := Dir(a, name)
	// Lstat, not Stat: a dangling symlink would otherwise report "does not exist"
	// while staying on disk forever.
	if _, err := os.Lstat(dir); os.IsNotExist(err) {
		return fmt.Errorf("profile %s:%s does not exist", a.Name, name)
	}
	return os.RemoveAll(dir)
}

// Discard removes a profile that was created but never finished, so `ap create`
// can be retried. Best effort: the caller is already returning the real error.
//
// The removal goes through an os.Root confined to the agent's directory, so the
// only thing it can delete is one entry directly inside it. That confinement is
// enforced by the runtime rather than by ValidName having been called correctly
// somewhere upstream — the same reason Link uses a Root.
func Discard(a agent.Agent, name string) {
	root, err := os.OpenRoot(filepath.Join(Root(), a.Name))
	if err != nil {
		return
	}
	defer func() { _ = root.Close() }()
	_ = root.RemoveAll(name)
}
