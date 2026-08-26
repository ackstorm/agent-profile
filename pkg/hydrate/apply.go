package hydrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/ackstorm/agent-profile/pkg/schema"
	"github.com/ackstorm/agent-profile/pkg/source"
)

// Plan is one apply. Root is a parameter, always (§33.2).
type Plan struct {
	Root    string
	Adapter Adapter
	Profile schema.Profile
	Fetched map[string]source.Resolved
	// Now stamps the ledger. A parameter so a test can assert a stable
	// ledger, and so nothing here reaches for a clock it cannot control.
	Now func() time.Time
}

// Change is one file this apply touched. Op is "create" or "overwrite".
//
// §33 requires every overwritten file to be LOGGED. A silent overwrite is the
// one thing an additive policy cannot afford: the user's evidence that apply
// did not quietly eat something is this list.
type Change struct{ Path, Op string }

// Result is what an apply did and what it declined to do.
type Result struct {
	Changes  []Change
	Warnings []string
}

// Apply materializes a resolved profile into a root and records what it wrote.
//
// The order is fixed and is a correctness property (§37.2): lock, materialize,
// write the ledger, release. The ledger is LAST because a ledger claiming files
// that were never written is worse than no ledger — every later verdict would
// rest on a record that was never true. A crash in between leaves files
// unclaimed, and re-running the apply repairs it.
func Apply(ctx context.Context, p Plan) (Result, error) {
	var res Result
	if p.Root == "" {
		return res, errors.New("apply: root must not be empty")
	}
	if p.Now == nil {
		p.Now = time.Now
	}

	release, err := LockRoot(p.Root)
	if err != nil {
		return res, err
	}
	defer func() { _ = release() }()

	ledger, err := LoadLedger(p.Root)
	if err != nil {
		return res, err
	}

	stamp := p.Now().UTC().Format(time.RFC3339)
	if err := applySkills(ctx, p, ledger, &res, stamp); err != nil {
		return res, err
	}
	if err := applyArtifacts(ctx, p, ledger, &res, stamp); err != nil {
		return res, err
	}
	recordDefinitions(p, ledger, stamp)
	warnDisabledButMaterialized(p, ledger, &res)

	if err := ledger.Save(p.Root); err != nil {
		return res, fmt.Errorf("writing the ledger: %w", err)
	}
	return res, nil
}

func applySkills(ctx context.Context, p Plan, l *Ledger, res *Result, stamp string) error {
	for _, name := range sortedKeys(p.Profile.Skills) {
		r := p.Profile.Skills[name]
		if !r.Enabled {
			continue
		}
		fetched, ok := p.Fetched["skill "+name]
		if !ok {
			// A marketplace ref: Phase 6 resolves the item. Reported, never
			// dropped silently (§8).
			res.Warnings = append(res.Warnings,
				fmt.Sprintf("skill %q resolves through a marketplace; item resolution is Phase 6 and it was not installed", name))
			continue
		}
		rel, supported := p.Adapter.SkillDir(name)
		if !supported {
			// §8: an unsupported concept warns, it never drops silently. The
			// message names the runtime, because "why is my skill missing" has
			// exactly one useful answer and this is it.
			res.Warnings = append(res.Warnings,
				fmt.Sprintf("runtime %q has no skills destination inside its configuration directory; skipping skill %q",
					p.Adapter.Name(), name))
			continue
		}
		files, err := copyTree(ctx, fetched.Dir, p.Root, rel, res)
		if err != nil {
			return fmt.Errorf("skill %q: %w", name, err)
		}
		l.Put(ResourceRec{
			Name: name, Kind: "skill", Ref: r.Ref, Source: r.Source,
			ResolvedRef: fetched.ResolvedRef, InstalledAt: stamp, Files: files,
		})
	}
	return nil
}

func applyArtifacts(ctx context.Context, p Plan, l *Ledger, res *Result, stamp string) error {
	for _, name := range sortedKeys(p.Profile.Artifacts) {
		a := p.Profile.Artifacts[name]
		if !a.Enabled {
			continue
		}
		fetched, ok := p.Fetched["artifact "+name]
		if !ok {
			continue
		}
		rel, err := p.Adapter.ArtifactDest(a.Destination)
		if err != nil {
			return fmt.Errorf("artifact %q: %w", name, err)
		}
		files, err := copyTree(ctx, fetched.Dir, p.Root, rel, res)
		if err != nil {
			return fmt.Errorf("artifact %q: %w", name, err)
		}
		l.Put(ResourceRec{
			Name: name, Kind: "artifact", Source: a.Source,
			ResolvedRef: fetched.ResolvedRef, InstalledAt: stamp, Files: files,
		})
	}
	return nil
}

// recordDefinitions fills the ledger's second arm. A marketplace writes no file
// into the root, so without this it would vanish with the manifest that
// declared it — and a manifest is an input that is not kept (F3).
func recordDefinitions(p Plan, l *Ledger, _ string) {
	for _, name := range sortedKeys(p.Profile.Marketplaces) {
		m := p.Profile.Marketplaces[name]
		if !m.Enabled {
			continue
		}
		d := DefinitionRec{Name: name, Kind: "marketplace", Source: m.Source}
		if f, ok := p.Fetched["marketplace "+name]; ok {
			d.ResolvedRef = f.ResolvedRef
			d.AuthScheme = string(f.SchemeUsed)
		}
		// The binding NAME only. §34 forbids persisting the value, and there
		// is no field for one.
		if m.Source != nil && m.Source.Git != nil && m.Source.Git.Auth != nil {
			d.AuthBinding = m.Source.Git.Auth.ValueFrom.Secret
		}
		l.PutDefinition(d)
	}
}

// warnDisabledButMaterialized is §4, and v0.6.1 made the detection
// LEDGER-driven rather than filesystem-driven. A merged file's contributed keys
// are invisible to the filesystem: the file is still there and looks untouched,
// so only the ledger knows this resource put something in it.
func warnDisabledButMaterialized(p Plan, l *Ledger, res *Result) {
	for _, name := range sortedKeys(p.Profile.Skills) {
		if p.Profile.Skills[name].Enabled {
			continue
		}
		rec, ok := l.Resource("skill", name)
		if !ok || (len(rec.Files) == 0) {
			continue
		}
		res.Warnings = append(res.Warnings, fmt.Sprintf(
			"skill %q is disabled but previously materialized state remains (%d file(s) in the ledger)", name, len(rec.Files)))
	}
}

// copyTree copies src into <root>/<rel>, recording every file it wrote.
//
// It never removes anything under rel. Apply is ADDITIVE (§33): a file the user
// added by hand survives an apply that overwrites its siblings, which is the
// difference between this and a sync.
func copyTree(ctx context.Context, src, root, rel string, res *Result) ([]FileRec, error) {
	dstRoot := filepath.Join(root, rel)
	info, err := os.Stat(src)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		rec, err := copyFile(src, dstRoot, rel, res)
		if err != nil {
			return nil, err
		}
		return []FileRec{rec}, nil
	}

	var recs []FileRec
	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		suffix, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if d.IsDir() {
			if suffix == "." {
				return os.MkdirAll(dstRoot, 0o755)
			}
			return os.MkdirAll(filepath.Join(dstRoot, suffix), 0o755)
		}
		// A symlink in fetched content is not copied as a link: pkg/source
		// already refused any that leave the tree, but reproducing one here
		// would put a link into a root whose target may not exist there.
		if d.Type()&fs.ModeSymlink != 0 {
			res.Warnings = append(res.Warnings,
				fmt.Sprintf("%s is a symlink and was not copied", filepath.Join(rel, suffix)))
			return nil
		}
		rec, err := copyFile(path, filepath.Join(dstRoot, suffix), filepath.Join(rel, suffix), res)
		if err != nil {
			return err
		}
		recs = append(recs, rec)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].RelPath < recs[j].RelPath })
	return recs, nil
}

// copyFile writes one file and returns its ledger record.
//
// The recorded hash is the hash of what was WRITTEN, computed from the bytes on
// their way to disk. Hashing the source instead would be one indirection away
// from the truth, and §33.1's every later verdict rests on this hash matching
// what is actually there.
func copyFile(src, dst, rel string, res *Result) (FileRec, error) {
	op := "create"
	if _, err := os.Stat(dst); err == nil {
		op = "overwrite"
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return FileRec{}, err
	}
	in, err := os.Open(src)
	if err != nil {
		return FileRec{}, err
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return FileRec{}, err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, h), in); err != nil {
		_ = out.Close()
		return FileRec{}, err
	}
	if err := out.Close(); err != nil {
		return FileRec{}, err
	}
	res.Changes = append(res.Changes, Change{Path: rel, Op: op})
	return FileRec{RelPath: filepath.ToSlash(rel), Hash: hex.EncodeToString(h.Sum(nil))}, nil
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
