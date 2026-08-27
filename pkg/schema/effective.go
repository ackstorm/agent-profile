package schema

import (
	"fmt"
	"slices"
)

// Warning is a degradation notice. §8 forbids silent drops, so everything
// this package declines to act on comes back here and the caller prints it.
// --strict (Phase 2) promotes these to errors.
type Warning struct{ Text string }

// Effective runs §31's lifecycle up to the point where inputs are computed:
// load, fold extends, select the runtime, overlay it, validate, evaluate
// enabled. Phase 2 continues from here with inputs and preflight.
func Effective(path, runtime string) (Profile, []Warning, error) {
	tree, leafRuntimes, err := Load(path)
	if err != nil {
		return Profile{}, nil, err
	}
	p, err := Decode(tree)
	if err != nil {
		return Profile{}, nil, err
	}
	// §5.3: a base participates in inheritance but cannot be applied.
	if p.IsBase() {
		return Profile{}, nil, fmt.Errorf("profile %q declares no targets — it is a base profile", p.Name)
	}
	if !slices.Contains(p.Targets, runtime) {
		return Profile{}, nil, fmt.Errorf("profile %q does not target %q; targets are %v", p.Name, runtime, p.Targets)
	}

	var warns []Warning
	// §7.1 vs §7.2: an inherited non-target block is ignored silently; one the
	// LEAF declared warns. leafRuntimes is the only thing that tells them apart.
	for _, r := range leafRuntimes {
		if !slices.Contains(p.Targets, r) {
			warns = append(warns, Warning{fmt.Sprintf(
				"runtimes.%s is declared but %q is not a target of profile %q", r, r, p.Name)})
		}
	}

	// The runtime overlay is applied on the TREE, not on the typed struct, so
	// it goes through exactly the same three rules — including the exclusive
	// groups that let `enabled: false` leave a locator alone.
	if rt, ok := tree.Map["runtimes"]; ok {
		if block, ok := rt.Map[runtime]; ok {
			overlay := commonKeysOnly(block)
			merged, err := Merge(tree, overlay, V1Schema())
			if err != nil {
				return Profile{}, nil, err
			}
			if p, err = Decode(merged); err != nil {
				return Profile{}, nil, err
			}
		}
	}
	// Every runtime block other than the selected one is dropped from the
	// effective profile: it is runtime-specific and this profile now has a
	// runtime. A manifest that never declared runtimes.<runtime> at all keeps
	// p.Runtimes nil rather than gaining an invented, empty one — Render would
	// otherwise print a `runtimes:\n  <runtime>:` block with nothing under it
	// for a manifest that declared none.
	if rt, ok := p.Runtimes[runtime]; ok {
		p.Runtimes = map[string]Runtime{runtime: rt}
	} else {
		p.Runtimes = nil
	}
	return p, warns, nil
}

// commonKeysOnly lifts a runtime block's common-vocabulary keys to the root, so
// runtimes.<r>.skills overlays skills. Runtime-native keys — plugins, variants,
// environment — are left where they are.
//
// `plugins` is the one that looks liftable and is not. There IS a common
// plugins collection now (§24), but a runtime block's `plugins` is the
// RUNTIME-NATIVE mechanism (§24.3): `package: "@scope/name"`, not a source or a
// ref. Lifting it would try to decode a package declaration as a common
// Resource and fail. §30's worked example shows both in one manifest — the
// common skill disabled for opencode, and opencode's own package used instead —
// which only works because these two never merge.
func commonKeysOnly(block *Node) *Node {
	out := &Node{Kind: Mapping, Line: block.Line, Map: map[string]*Node{}, KeyLine: map[string]int{}}
	for _, k := range []string{"model", "prompt", "inputs", "marketplaces", "skills", "mcps", "artifacts"} {
		if v, ok := block.Map[k]; ok {
			out.Keys = append(out.Keys, k)
			out.Map[k] = v
			out.KeyLine[k] = block.KeyLine[k]
		}
	}
	return out
}

// Targets loads and decodes a manifest far enough to read its targets. It is
// exported because ap validate (and ach) need to run Effective for every
// target a manifest declares, and that requires reading the list before any
// one runtime is chosen.
func Targets(path string) ([]string, error) {
	_, targets, err := Identity(path)
	return targets, err
}

// Identity reads what a manifest calls ITSELF: its profile name and the
// runtimes it declares.
//
// Both are addressing information, and reading them here is what lets apply
// take a manifest alone. A manifest is a profile's definition — `name` is that
// profile's name, `targets` are the runtimes it can be materialized for — so
// restating either on the command line is asking the author to repeat their own
// document.
//
// This is NOT the root being inferred from the environment, which §33.2
// forbids: it comes from the input the user named. What the environment still
// cannot supply is which profiles exist or which one is "current" — there is
// no active profile, and there is no default manifest location.
func Identity(path string) (name string, targets []string, err error) {
	tree, _, err := Load(path)
	if err != nil {
		return "", nil, err
	}
	p, err := Decode(tree)
	if err != nil {
		return "", nil, err
	}
	if p.IsBase() {
		return "", nil, fmt.Errorf("profile %q declares no targets — it is a base profile, so it is extended rather than applied", p.Name)
	}
	return p.Name, p.Targets, nil
}
