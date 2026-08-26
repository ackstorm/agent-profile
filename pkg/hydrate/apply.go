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
	if err := applyMCPs(p, ledger, &res, stamp); err != nil {
		return res, err
	}
	if err := applyModelEnv(p, ledger, &res, stamp); err != nil {
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

// applyMCPs merges every active MCP server into the runtime's own
// configuration file and records the dotted keys it contributed.
//
// This is where the ledger's Merge and Keys fields stop being decoration.
// Phase 4 wrote whole files and recorded Merge: ""; a server lives INSIDE a
// document the user also owns, so uninstall must remove "mcp_servers.memory"
// and nothing else. Recording it as a replace would make uninstall delete the
// whole config.toml.
func applyMCPs(p Plan, l *Ledger, res *Result, stamp string) error {
	names := sortedKeys(p.Profile.MCPs)
	active := make([]string, 0, len(names))
	for _, n := range names {
		if p.Profile.MCPs[n].Enabled {
			active = append(active, n)
		}
	}
	if len(active) == 0 {
		return nil
	}

	rel, key, ok := p.Adapter.MCPTarget()
	if !ok {
		res.Warnings = append(res.Warnings, fmt.Sprintf(
			"runtime %q has no MCP configuration inside its configuration directory; skipping %d server(s)",
			p.Adapter.Name(), len(active)))
		return nil
	}
	bindings := bindingVars(p.Profile)
	path := filepath.Join(p.Root, rel)

	for _, name := range active {
		entry, err := MCPEntry(p.Adapter.Name(), name, p.Profile.MCPs[name], bindings)
		if err != nil {
			return fmt.Errorf("mcp %q: %w", name, err)
		}
		keys, err := MergeInto(path, map[string]any{key: map[string]any{name: entry}})
		if err != nil {
			return fmt.Errorf("mcp %q: %w", name, err)
		}
		hash, err := hashFile(path)
		if err != nil {
			return err
		}
		l.Put(ResourceRec{
			Name: name, Kind: "mcp", InstalledAt: stamp,
			Files: []FileRec{{RelPath: filepath.ToSlash(rel), Hash: hash, Merge: "deep", Keys: keys}},
		})
		res.Changes = append(res.Changes, Change{Path: filepath.Join(rel, key+"."+name), Op: "merge"})
	}
	return nil
}

// applyModelEnv writes the profile-local env file the launcher exports.
//
// §9 and §15.1 describe VARIABLES, not files: there is no configuration key to
// merge a base URL and a credential reference into. Apply and launch are
// separate invocations, so something has to hold the answer between them, and
// the profile is the only place that survives both — a manifest is an input and
// may be thrown away.
//
// Nothing is written when there is nothing to write: an absent model with no
// runtime environment is the subscription case, and creating an empty file
// there would make every profile look configured.
func applyModelEnv(p Plan, l *Ledger, res *Result, stamp string) error {
	env, notices, err := ModelEnv(p.Adapter.Name(), p.Profile, bindingVars(p.Profile))
	if err != nil {
		return err
	}
	res.Warnings = append(res.Warnings, notices...)
	if len(env) == 0 {
		return nil
	}
	path := filepath.Join(p.Root, EnvFile)
	body := FormatEnvFile(env)
	op := "create"
	if _, err := os.Stat(path); err == nil {
		op = "overwrite"
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return err
	}
	sum := sha256.Sum256(body)
	res.Changes = append(res.Changes, Change{Path: EnvFile, Op: op})
	l.Put(ResourceRec{
		Name: "model", Kind: "environment", InstalledAt: stamp,
		Files: []FileRec{{RelPath: EnvFile, Hash: hex.EncodeToString(sum[:])}},
	})
	return nil
}

// bindingVars maps an input NAME to the environment variable a reference
// should name. An env-sourced secret references the DECLARED variable, with no
// synthesized name (§34); a file-sourced one references what the launcher
// exports, because runtimes do not read files.
func bindingVars(p schema.Profile) map[string]string {
	out := map[string]string{}
	for name, b := range p.Inputs.Secrets {
		if b.Env != "" {
			out[name] = b.Env
			continue
		}
		if b.File != "" {
			out[name] = APSecretVar(name)
		}
	}
	for name, b := range p.Inputs.Variables {
		if b.Env != "" {
			out[name] = b.Env
		}
	}
	return out
}

// hashFile hashes a file already on disk. A merged file is written by
// MergeInto, so unlike copyFile there is no stream to tee — the hash has to
// come from the result.
func hashFile(path string) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
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
