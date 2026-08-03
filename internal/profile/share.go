//go:build unix

package profile

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/ackstorm/agent-profile/internal/agent"
)

// orphanSuffix names what a share used to be before an agent overwrote the link
// with a file of its own. Not ".bak": this is a credential, and the name has to
// say that ap put it there and that nothing reads it any more.
const orphanSuffix = ".ap-orphan"

// previousSuffix names the shared file a promotion replaced.
//
// Kept rather than overwritten in place because promoting is the only thing in
// this program that writes into the user's real config directory, and ap cannot
// tell whose account a credential belongs to — the identity lives in
// .claude.json, which is deliberately not shared. If a promotion turns out to
// have replaced the machine-wide login with some profile's other account, this
// file is the only way back.
const previousSuffix = ".ap-previous"

// tmpSuffix names the scratch file an atomic write renames from. A fixed name,
// not a random one: a leftover from a crashed run is meant to be overwritten by
// the next, and it lives beside the file it is about to become.
const tmpSuffix = ".ap-tmp"

// Resolution is what the caller wants done with a real file found where a
// share's symlink belongs.
type Resolution int

const (
	// Orphan moves the profile's file aside to <rel>.ap-orphan and relinks,
	// leaving the shared file untouched. A nil resolver means this, so a caller
	// that passes nothing cannot overwrite the user's real credential by
	// omission.
	Orphan Resolution = iota

	// Promote copies the profile's file over the shared one — keeping what it
	// replaced at <shared>.ap-previous — and then does what Orphan does.
	//
	// It exists because a login performed inside a profile can otherwise never
	// reach the shared credential. claude's temp-file-plus-rename replaces the
	// symlink, so a refreshed or re-entered token lands in the profile while the
	// shared file keeps the old one, and the next run relinks the profile back to
	// it. Measured on the reference machine, claudeAiOauth carries a
	// refreshTokenExpiresAt roughly 29 days out that only a refresh moves
	// forward. A shared credential nothing is permitted to update therefore
	// expires outright, and from then on every profile asks for a login it has
	// nowhere to store.
	Promote
)

// Conflict describes a real file sitting where a share's symlink belongs, so a
// caller can decide what happens to it.
//
// The times are modification times, and nothing here parses either file. That is
// deliberate: codex's auth.json has the same conflict for the same reason, and a
// check that understood claude's credential schema would need re-verifying
// against both agents on every release to say what mtimes already say.
type Conflict struct {
	Rel          string // ".credentials.json"
	ProfilePath  string // <profile>/.credentials.json
	SharedPath   string // ~/.claude/.credentials.json
	PreviousPath string // where Promote would keep the file it replaces
	ProfileTime  time.Time
	SharedTime   time.Time
}

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
// resolve decides whether that file is merely moved aside or first promoted over
// the shared one; see Resolution. A nil resolve means Orphan, which is what the
// whole loop did before promotion existed. It is consulted only when the two files
// actually differ, because claude rewrites its credential whether or not anything
// in it changed, and a prompt about a file that would promote to exactly what is
// already there is pure noise.
//
// Link also removes any symlink sitting at a path the registry lists in Unshared —
// state that used to be common and no longer is. That makes a change to the registry
// take effect in profiles created before it, instead of only in new ones.
func Link(a agent.Agent, dir string, resolve func(Conflict) Resolution) (linked, skipped, unshared, orphaned []string, err error) {
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
		sfi, err := os.Stat(s.From)
		if err != nil {
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
			// A real file where the link belongs: the agent rewrote it. Whether the
			// token in it survives is the caller's decision, not this loop's —
			// promoting writes into the user's real config directory, which nothing
			// else in this program does.
			if err := offerPromotion(root, dir, s, fi, sfi, resolve); err != nil {
				return linked, skipped, unshared, orphaned, err
			}
			// Move it aside rather than removing it — it is a credential, and it may
			// hold a token newer than the shared one. Renamed through the same
			// os.Root, so neither name can leave the profile. A previous orphan is
			// overwritten: it is by definition the staler of the two.
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

// offerPromotion asks resolve what should happen to the real file at s.Rel and,
// if the answer is Promote, copies it over the shared one before the caller moves
// it aside.
//
// Identical content is not a conflict and resolve never hears about it. claude
// rewrites its credential on refresh whether or not the tokens changed, and a
// prompt whose two answers produce the same file is noise that teaches people to
// dismiss the prompt that matters.
func offerPromotion(root *os.Root, dir string, s agent.Share, fi, sfi fs.FileInfo, resolve func(Conflict) Resolution) error {
	if resolve == nil {
		return nil
	}
	// Only a regular file can be promoted. Every share in the registry is a
	// credential today, but Link is generic over a.Shared and these used to be
	// directories — projects/, sessions/ — and could be again. Reading a directory
	// with io.ReadAll fails with EISDIR, which would turn healing into a dead
	// `ap run`: precisely the failure the healing was written to end.
	if !fi.Mode().IsRegular() {
		return nil
	}
	mine, err := readIn(root, s.Rel)
	if err != nil {
		return fmt.Errorf("cannot read %s in profile: %w", s.Rel, err)
	}
	shared, err := os.ReadFile(s.From)
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", s.From, err)
	}
	if bytes.Equal(mine, shared) {
		return nil
	}
	if resolve(Conflict{
		Rel:          s.Rel,
		ProfilePath:  filepath.Join(dir, s.Rel),
		SharedPath:   s.From,
		PreviousPath: s.From + previousSuffix,
		ProfileTime:  fi.ModTime(),
		SharedTime:   sfi.ModTime(),
	}) != Promote {
		return nil
	}
	return promote(s.From, mine, shared)
}

// promote replaces the shared file with the profile's copy, keeping what it
// replaced at <shared>.ap-previous.
//
// This is the only code path in the program that writes outside a profile, and so
// the only one that could damage configuration ap did not create.
//
// The operative guard is the refusal below: a shared path that is itself a symlink
// is left alone rather than replaced. People symlink their dotfiles, and both
// outcomes are bad — following the link writes a credential into a directory ap
// was never pointed at, and replacing it strands the file they actually version.
// Nothing has been modified when this returns an error, so the run fails with the
// profile exactly as it was and the same choice is offered on the next one.
// TestPromoteRefusesASymlinkedSharedPath is what keeps that true.
//
// The os.Root below is defence in depth rather than the tested guard: with the
// refusal in place nothing reaches it with a symlink, and the names it is given
// are single components that cannot traverse anywhere on their own. It is there so
// that removing the refusal degrades to replacing a link rather than to writing
// through one.
func promote(from string, mine, shared []byte) error {
	if fi, err := os.Lstat(from); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to promote onto %s: it is a symlink, not a regular file.\n"+
			"    Resolve it by hand, or re-run and keep the shared credential instead", from)
	}
	root, err := os.OpenRoot(filepath.Dir(from))
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()

	base := filepath.Base(from)
	// Back up by copying, not by renaming. A rename would leave the shared path
	// missing for as long as the second write takes, and a crash in that window
	// would log out every profile and the bare agent at once.
	if err := writeIn(root, base+previousSuffix, shared); err != nil {
		return fmt.Errorf("cannot back up %s: %w", from, err)
	}
	if err := writeIn(root, base, mine); err != nil {
		return fmt.Errorf("cannot promote onto %s: %w", from, err)
	}
	return nil
}

// readIn reads rel through root, so that a symlinked ancestor cannot make this
// read a file outside the profile.
func readIn(root *os.Root, rel string) ([]byte, error) {
	f, err := root.Open(rel)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(f)
}

// writeIn writes data to rel through root, atomically: a scratch file beside it,
// synced, then renamed over the target. A concurrent agent never reads a
// half-written credential, and a crash leaves the previous one intact.
func writeIn(root *os.Root, rel string, data []byte) error {
	tmp := rel + tmpSuffix
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = root.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = root.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = root.Remove(tmp)
		return err
	}
	return root.Rename(tmp, rel)
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
