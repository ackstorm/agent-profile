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
	// Reconcile is the commands that materialize the declarations this apply
	// wrote — one per runtime-native package, in the order they landed.
	//
	// Reported rather than run HERE: hydrate writes files and knows nothing
	// about a profile's environment, and a root that is a bare directory has
	// no runtime to run anything with. The caller decides, because the caller
	// is the one that knows which of those it is holding.
	Reconcile [][]string
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
	// Before applyPlugins, and load-bearing: the suppression set has to exist
	// before the common plugins are walked, or a native override could never
	// take effect.
	if err := applyNativePlugins(p, ledger, &res, stamp); err != nil {
		return res, err
	}
	if err := applyPlugins(ctx, p, ledger, &res, stamp); err != nil {
		return res, err
	}
	if err := applyMCPs(p, ledger, &res, stamp); err != nil {
		return res, err
	}
	if err := applyModel(p, ledger, &res, stamp); err != nil {
		return res, err
	}
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
			Secrets: sourceSecrets(p.Profile, r.Source),
		})
	}
	return nil
}

// nativePlugins is the selected runtime's own plugin block. Effective narrows
// Runtimes to one entry, so there is at most one to read.
func nativePlugins(p Plan) map[string]schema.NativePlugin {
	return p.Profile.Runtimes[p.Adapter.Name()].Plugins
}

// applyNativePlugins writes each declared native package into the runtime's own
// settings list (§24.3), and reports the command that materializes it.
//
// ap writes the DECLARATION and nothing else. Running the runtime's installer
// is what `ap sync` did and why it was removed; the honest replacement is to
// name the command, exactly as the clone path names `codex plugin add`.
// Measured: `pi update <source>` clones a package that exists only in
// settings.json, so the declaration alone is enough to act on.
func applyNativePlugins(p Plan, l *Ledger, res *Result, stamp string) error {
	native := nativePlugins(p)
	names := sortedKeys(native)
	active := make([]string, 0, len(names))
	for _, n := range names {
		if native[n].Enabled {
			active = append(active, n)
		}
	}
	if len(active) == 0 {
		return nil
	}
	rel, key, ok := p.Adapter.PackageTarget()
	if !ok {
		for _, n := range active {
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"runtime %q has no native package list; skipping plugin %q — it declares plugins through a marketplace",
				p.Adapter.Name(), n))
		}
		return nil
	}
	path := filepath.Join(p.Root, rel)
	for _, name := range active {
		pkg := native[name].Package
		if _, err := AppendInto(path, key, pkg); err != nil {
			return fmt.Errorf("plugin %q: %w", name, err)
		}
		hash, err := hashFile(path)
		if err != nil {
			return err
		}
		l.Put(ResourceRec{
			Name: name, Kind: "native-plugin", InstalledAt: stamp,
			// Bounded by the ELEMENT. Recording the container key would have
			// uninstall delete every package in the list, the user's included —
			// the same rule a merged MCP record follows.
			Files: []FileRec{{RelPath: filepath.ToSlash(rel), Hash: hash, Merge: "list", Keys: []string{key + "." + pkg}}},
		})
		res.Changes = append(res.Changes, Change{Path: filepath.Join(rel, key+"."+pkg), Op: "merge"})
		if argv := p.Adapter.ReconcileArgv(pkg); len(argv) > 0 {
			res.Reconcile = append(res.Reconcile, argv)
		}
	}
	return nil
}

// applyPlugins routes each component kind of a plugin tree to this runtime's
// destination (§24.2).
//
// The routing is the adapter's and the copying is not: that split is why one
// plugin declaration works on four runtimes, and why it cannot be expressed as
// four artifacts with hand-written destinations — an artifact names ONE
// destination, and a plugin has a different one per runtime.
//
// Everything Route declines to route is REPORTED. A known kind this runtime has
// no destination for, and a kind needing a format conversion that is not
// implemented, both come back as warnings naming the runtime and the kind: §8
// forbids the silent drop, and "why is my hook missing" has exactly one useful
// answer.
func applyPlugins(ctx context.Context, p Plan, l *Ledger, res *Result, stamp string) error {
	native := nativePlugins(p)
	for _, name := range sortedKeys(p.Profile.Plugins) {
		r := p.Profile.Plugins[name]
		if !r.Enabled {
			continue
		}
		// §24.3: a runtime-native entry of the same name OVERRIDES the common
		// one for this runtime — including a disabled one, which is how a
		// single runtime opts out of a plugin the root declares. Materializing
		// both would install ponytail twice by two mechanisms.
		if _, overridden := native[name]; overridden {
			continue
		}
		fetched, ok := p.Fetched["plugin "+name]
		if !ok {
			continue
		}
		entries, err := os.ReadDir(fetched.Dir)
		if err != nil {
			return fmt.Errorf("plugin %q: %w", name, err)
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		routed, err := Route(p.Adapter.Name(), names)
		if err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("plugin %q: %v", name, err))
			continue
		}

		var files []FileRec
		for _, rt := range routed {
			if rt.Dropped != "" {
				res.Warnings = append(res.Warnings, fmt.Sprintf("plugin %q: %s", name, rt.Dropped))
				continue
			}
			if rt.Merge != "" {
				// Structured destinations — a plugin's MCP servers, claude's
				// CLAUDE.md — merge into a file the user co-owns. Phase 5's
				// machinery does that, and wiring a plugin's .mcp.json through
				// it is the remaining piece; reported rather than half-done.
				res.Warnings = append(res.Warnings, fmt.Sprintf(
					"plugin %q: %q merges into %q, which is not wired yet; the files were not written",
					name, rt.Kind, rt.To))
				continue
			}
			recs, err := copyTree(ctx, filepath.Join(fetched.Dir, rt.Kind), p.Root, rt.To, res)
			if err != nil {
				return fmt.Errorf("plugin %q: %s: %w", name, rt.Kind, err)
			}
			files = append(files, recs...)
		}
		if len(files) == 0 {
			continue
		}
		l.Put(ResourceRec{
			Name: name, Kind: "plugin", Ref: r.Ref, Source: r.Source,
			ResolvedRef: fetched.ResolvedRef, InstalledAt: stamp, Files: files,
			Secrets: sourceSecrets(p.Profile, r.Source),
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
			Name: name, Kind: "artifact", Source: a.Source, Destination: a.Destination,
			ResolvedRef: fetched.ResolvedRef, InstalledAt: stamp, Files: files,
			Secrets: sourceSecrets(p.Profile, a.Source),
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
		decl := p.Profile.MCPs[name]
		l.Put(ResourceRec{
			Name: name, Kind: "mcp", InstalledAt: stamp, MCP: &decl,
			Files:   []FileRec{{RelPath: filepath.ToSlash(rel), Hash: hash, Merge: "deep", Keys: keys}},
			Secrets: pickSecrets(p.Profile, mcpSecretNames(decl)),
		})
		res.Changes = append(res.Changes, Change{Path: filepath.Join(rel, key+"."+name), Op: "merge"})
	}
	return nil
}

// applyModel merges §9's model block into the runtime's own configuration
// file, alongside the MCP servers, and records the dotted keys it contributed.
//
// It is a FILE, not an environment the launcher carries. An earlier draft used
// a profile-local env file that only `ap run` read, which silently lost the
// whole model block in the topology it most needed to work in: an init
// container hydrates, the main container execs the runtime directly, and
// nothing reads the launcher's environment.
func applyModel(p Plan, l *Ledger, res *Result, stamp string) error {
	if p.Profile.Model == nil {
		// Absent model means native defaults AND native credentials — the
		// subscription case. Writing anything would break it.
		return nil
	}
	target, block, err := ModelConfig(p.Adapter.Name(), *p.Profile.Model, bindingVars(p.Profile))
	if errors.Is(err, errNoModelTarget) {
		res.Warnings = append(res.Warnings, fmt.Sprintf(
			"runtime %q has no measured model destination; skipping the model block",
			p.Adapter.Name()))
		return nil
	}
	if err != nil {
		return err
	}
	if len(block) == 0 {
		return nil
	}

	// §15's runtime environment shares claude's destination with model, so
	// §15.1's precedence is a property of ONE merge rather than of two
	// mechanisms that might disagree about who ran last.
	if envTarget, ok := RuntimeEnvTarget(p.Adapter.Name()); ok && envTarget == target {
		merged, notices := MergeRuntimeEnv(block, p.Profile.Runtimes[p.Adapter.Name()].Environment)
		block = merged
		res.Warnings = append(res.Warnings, notices...)
	}

	path := filepath.Join(p.Root, target.File)
	keys, err := MergeInto(path, map[string]any{target.Block: block})
	if err != nil {
		return fmt.Errorf("model: %w", err)
	}
	hash, err := hashFile(path)
	if err != nil {
		return err
	}
	l.Put(ResourceRec{
		Name: "model", Kind: "model", InstalledAt: stamp, Model: p.Profile.Model,
		Files:   []FileRec{{RelPath: filepath.ToSlash(target.File), Hash: hash, Merge: "deep", Keys: keys}},
		Secrets: pickSecrets(p.Profile, modelSecretNames(*p.Profile.Model)),
	})
	res.Changes = append(res.Changes, Change{Path: filepath.Join(target.File, target.Block), Op: "merge"})
	return nil
}

// sourceSecrets is the bindings ONE source references, ready for the ledger.
//
// Recording every input the manifest declared would put unrelated bindings into
// every record; recording none would leave an exported manifest referencing a
// secret nothing declares, which fails its own validation (§13.1). What a
// resource references is exactly what export has to re-declare for it.
func sourceSecrets(p schema.Profile, s *schema.Source) map[string]schema.Binding {
	return pickSecrets(p, sourceSecretNames(s))
}

func pickSecrets(p schema.Profile, names []string) map[string]schema.Binding {
	var out map[string]schema.Binding
	for _, n := range names {
		b, ok := p.Inputs.Secrets[n]
		if !ok {
			continue
		}
		if out == nil {
			out = map[string]schema.Binding{}
		}
		out[n] = b
	}
	return out
}

func sourceSecretNames(s *schema.Source) []string {
	if s == nil {
		return nil
	}
	var auth *schema.GitAuth
	switch {
	case s.Git != nil:
		auth = s.Git.Auth
	case s.Archive != nil:
		auth = s.Archive.Auth
	}
	if auth == nil || auth.ValueFrom.Secret == "" {
		return nil
	}
	return []string{auth.ValueFrom.Secret}
}

func mcpSecretNames(m schema.MCP) []string {
	return headerSecretNames(m.Transport.Headers)
}

func modelSecretNames(m schema.Model) []string {
	names := headerSecretNames(m.Headers)
	if m.Auth != nil && m.Auth.ValueFrom.Secret != "" {
		names = append(names, m.Auth.ValueFrom.Secret)
	}
	return names
}

func headerSecretNames(h map[string]schema.HeaderValue) []string {
	var names []string
	for _, k := range sortedKeys(h) {
		if v := h[k]; v.ValueFrom != nil && v.ValueFrom.Secret != "" {
			names = append(names, v.ValueFrom.Secret)
		}
	}
	return names
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
