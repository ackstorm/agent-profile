package hydrate

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Verdict is what removal decided about ONE recorded file.
//
// Op is the whole vocabulary, and it is small on purpose: every case a user can
// hit has a name, so nothing is reported as a generic failure and nothing is
// left unsaid (§8).
//
//	remove  the file is ours and unchanged; it goes
//	keys    the file is shared; only the recorded dotted keys go
//	skip    the file changed since install; it stays, and Reason says why
//	gone    the file is already absent; nothing to do
type Verdict struct {
	Path   string
	Op     string
	Keys   []string
	Reason string
}

// Removal is one uninstall, planned.
//
// The same value is printed by --dry-run and executed by the real run. That is
// §33.3's "the preview MUST be produced by the same classifier as the action",
// and here it is structural rather than a convention: Remove is the only entry
// point and classify is called exactly once inside it.
type Removal struct {
	Kind     string
	Name     string
	Verdicts []Verdict
}

// Removed reports whether anything was actually taken away. A removal whose
// every verdict is skip or gone did nothing, and saying "removed" would be a
// lie the user has no way to check.
func (r Removal) Removed() bool {
	for _, v := range r.Verdicts {
		if v.Op == "remove" || v.Op == "keys" {
			return true
		}
	}
	return false
}

// Remove takes one resource out of a root, or reports what it would take out.
//
// It may not remove anything the ledger does not own (§33.3). The ledger is
// also what makes this safe against the agent's REAL configuration directory —
// the case v0.5 gave up on — because it can tell ap's writes from the user's.
func Remove(root, kind, name string, dryRun bool) (Removal, error) {
	if root == "" {
		return Removal{}, fmt.Errorf("remove: root must not be empty")
	}
	release, err := LockRoot(root)
	if err != nil {
		return Removal{}, err
	}
	defer func() { _ = release() }()

	ledger, err := LoadLedger(root)
	if err != nil {
		return Removal{}, err
	}
	rec, ok := ledger.Resource(kind, name)
	if !ok {
		return Removal{}, notOwned(ledger, kind, name)
	}

	rm := Removal{Kind: kind, Name: name, Verdicts: classify(root, rec)}
	if dryRun {
		return rm, nil
	}
	if err := execute(root, rm); err != nil {
		return rm, err
	}

	// The record goes even when some files were skipped. A skipped file was
	// edited by the user, which makes it THEIRS: continuing to claim it would
	// mean a later uninstall of an unrelated resource, or an export, still
	// speaking for a file ap no longer wrote.
	ledger.Delete(kind, name)
	if err := ledger.Save(root); err != nil {
		return rm, fmt.Errorf("writing the ledger: %w", err)
	}
	return rm, nil
}

// notOwned names what the ledger DOES hold of that kind. "not installed" alone
// sends the user to look at the filesystem, where the answer is not; the ledger
// is the only state and this is the only place that can say so.
func notOwned(l *Ledger, kind, name string) error {
	var have []string
	for _, r := range l.Resources {
		if r.Kind == kind {
			have = append(have, r.Name)
		}
	}
	sort.Strings(have)
	if len(have) == 0 {
		return fmt.Errorf("the ledger holds no %s named %q, and no %s at all", kind, name, kind)
	}
	return fmt.Errorf("the ledger holds no %s named %q; it holds: %s", kind, name, strings.Join(have, ", "))
}

// classify is the ONE classifier. Nothing else decides a verdict.
func classify(root string, rec ResourceRec) []Verdict {
	out := make([]Verdict, 0, len(rec.Files))
	for _, f := range rec.Files {
		out = append(out, classifyFile(root, f))
	}
	return out
}

// classifyFile is where the hash rule and the keys rule part company, and the
// split is not an optimisation — it is the only thing that makes a merged
// uninstall possible at all.
//
// A whole-file record is gated on its hash: the file is ours only while it
// still is what we wrote.
//
// A merged record is NOT, and cannot be. applyMCPs merges each server into one
// document in turn, so installing a second server invalidates the first one's
// recorded hash the moment it lands — on a root with two servers, every
// recorded hash but the last is already stale and honest. Gating on it would
// refuse every merged uninstall on any root holding more than one server, which
// is the ordinary case. The bound is the recorded KEYS instead, which is
// exactly the bound §33.3 states and exactly what MergeInto returns them for.
func classifyFile(root string, f FileRec) Verdict {
	path := filepath.Join(root, filepath.FromSlash(f.RelPath))

	switch f.Merge {
	case "":
		hash, err := hashFile(path)
		if os.IsNotExist(err) {
			return Verdict{Path: f.RelPath, Op: "gone"}
		}
		if err != nil {
			return Verdict{Path: f.RelPath, Op: "skip", Reason: err.Error()}
		}
		if hash != f.Hash {
			return Verdict{Path: f.RelPath, Op: "skip", Reason: "modified since install"}
		}
		return Verdict{Path: f.RelPath, Op: "remove"}

	case "deep":
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return Verdict{Path: f.RelPath, Op: "gone"}
		}
		if len(f.Keys) == 0 {
			return Verdict{Path: f.RelPath, Op: "skip", Reason: "recorded as merged but with no keys"}
		}
		return Verdict{Path: f.RelPath, Op: "keys", Keys: f.Keys}

	default:
		// "composite" is declared in the ledger's vocabulary and nothing
		// writes it yet — Phase 6 warns instead of routing a marker-bounded
		// region. Guessing at its bounds would remove text ap never wrote.
		return Verdict{Path: f.RelPath, Op: "skip",
			Reason: fmt.Sprintf("merge mode %q has no removal rule", f.Merge)}
	}
}

func execute(root string, rm Removal) error {
	for _, v := range rm.Verdicts {
		path := filepath.Join(root, filepath.FromSlash(v.Path))
		switch v.Op {
		case "remove":
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("removing %s: %w", v.Path, err)
			}
			pruneEmptyDirs(root, filepath.Dir(path))
		case "keys":
			if err := MergeOut(path, v.Keys); err != nil {
				return fmt.Errorf("%s: %w", v.Path, err)
			}
		}
	}
	return nil
}

// pruneEmptyDirs removes the directories a removed file leaves behind, and
// stops at the root.
//
// A skill is a tree, so removing its files without this leaves skills/xlsx/ as
// an empty directory that every listing shows and nothing owns. os.Remove on a
// non-empty directory fails, which is the whole guard: a sibling the user added
// keeps the directory, and the walk stops there.
func pruneEmptyDirs(root, dir string) {
	root = filepath.Clean(root)
	for dir = filepath.Clean(dir); dir != root && strings.HasPrefix(dir, root+string(filepath.Separator)); dir = filepath.Dir(dir) {
		// A symlink is never pruned. os.Remove would take the LINK, which is
		// the user's, and this program's standing rule is that removal does
		// not follow one — the same rule that keeps Delete out of the real
		// home.
		if fi, err := os.Lstat(dir); err != nil || fi.Mode()&os.ModeSymlink != 0 {
			return
		}
		if os.Remove(dir) != nil {
			return
		}
	}
}
