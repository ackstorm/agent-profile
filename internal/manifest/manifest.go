//go:build unix

package manifest

import (
	"fmt"
	"slices"
	"strings"

	"github.com/ackstorm/agent-profile/internal/agent"
	"github.com/ackstorm/agent-profile/internal/profile"
)

// Manifest is one file: a logical profile name, the machine-level commands that
// come before any of it, and the platforms it materialises on.
type Manifest struct {
	// Path is the file it was read from, named in every error and in the report.
	Path string
	// Name is the logical profile name, or profile.Default for `name: default`.
	Name string
	// Bootstrap runs once, before any profile exists, with no agent variable
	// set. See §8.2: that is what stops a manifest expressing "install this
	// into all my profiles at once", which is the thing that leaked.
	Bootstrap []string
	// Platforms is sorted by agent name so a run is deterministic.
	Platforms []Platform
}

// Platform is one agent's half of a manifest.
type Platform struct {
	Agent agent.Agent
	// Install runs once for this platform, in that platform's environment.
	Install []string
	// Variants is sorted by name.
	Variants []Variant
}

// Variant is one launch variant. Args is one argv list whichever form the
// manifest used: §7.1's string is tokenized here, the list is taken verbatim,
// and what lands on disk is identical either way.
type Variant struct {
	Name string
	Args []string
}

// IsDefault reports whether this manifest names the agent's real, machine-wide
// configuration rather than a profile. Nothing is ever created for it — see §6.1
// and profile.Default.
func (m Manifest) IsDefault() bool { return m.Name == profile.Default }

// Parse decodes one manifest file. path is only used for messages; nothing is
// read from disk here.
func Parse(path string, b []byte) (Manifest, error) {
	root, err := parseYAML(b)
	if err != nil {
		return Manifest{}, fmt.Errorf("%s: %w", path, err)
	}
	m, err := decode(root)
	if err != nil {
		return Manifest{}, fmt.Errorf("%s: %w", path, err)
	}
	m.Path = path
	return m, nil
}

func decode(root *node) (Manifest, error) {
	var m Manifest
	if root.kind != mapNode {
		return m, fmt.Errorf("line %d: a manifest is a mapping with version, name and platforms", root.line)
	}
	if err := onlyKeys(root, "version", "name", "bootstrap", "platforms"); err != nil {
		return m, err
	}

	v, err := requiredScalar(root, "version")
	if err != nil {
		return m, err
	}
	// Compared as a string, because §4.2 makes every scalar one. There are no
	// numbers in this format and adding one for a single field would mean
	// adding a type system for it too.
	if v != "1" {
		return m, fmt.Errorf("line %d: version %s: this ap understands version 1", root.keyLine["version"], v)
	}

	if m.Name, err = requiredScalar(root, "name"); err != nil {
		return m, err
	}
	// The literal comparison comes FIRST and ValidName is not consulted for it.
	// ValidName rejects "default" on purpose — every writing path depends on
	// that — so the sentinel is routed above it rather than by loosening it.
	// Same shape as profile.ParseRefAllowDefault, same reason: "is this a safe
	// path component" and "is this the sentinel" are different questions, and
	// merging them is how --from became a path traversal.
	if m.Name != profile.Default {
		if err := profile.ValidName(m.Name); err != nil {
			return m, fmt.Errorf("line %d: name: %w", root.keyLine["name"], err)
		}
	}

	if n, ok := root.m["bootstrap"]; ok {
		if m.Bootstrap, err = commands(n, "bootstrap"); err != nil {
			return m, err
		}
	}

	pn, ok := root.m["platforms"]
	if !ok {
		return m, fmt.Errorf("platforms: required, and needs at least one of %s", strings.Join(agent.Names(), ", "))
	}
	if pn.kind != mapNode || len(pn.keys) == 0 {
		return m, fmt.Errorf("line %d: platforms: needs at least one of %s", pn.line, strings.Join(agent.Names(), ", "))
	}
	for _, name := range slices.Sorted(slices.Values(pn.keys)) {
		a, ok := agent.Lookup(name)
		if !ok {
			return m, fmt.Errorf("line %d: unknown platform %q: supported are %s",
				pn.keyLine[name], name, strings.Join(agent.Names(), ", "))
		}
		p, err := decodePlatform(a, pn.m[name], m.IsDefault())
		if err != nil {
			return m, err
		}
		m.Platforms = append(m.Platforms, p)
	}
	return m, nil
}

func decodePlatform(a agent.Agent, n *node, isDefault bool) (Platform, error) {
	p := Platform{Agent: a}
	if n.kind == emptyNode {
		return p, nil // "this platform, no configuration"
	}
	if n.kind != mapNode {
		return p, fmt.Errorf("line %d: %s: expected install and/or variants under it", n.line, a.Name)
	}
	if err := onlyKeys(n, "install", "variants"); err != nil {
		return p, err
	}
	if in, ok := n.m["install"]; ok {
		var err error
		if p.Install, err = commands(in, "install"); err != nil {
			return p, err
		}
	}
	vn, ok := n.m["variants"]
	if !ok {
		return p, nil
	}
	if isDefault {
		// parseVariantRef refuses a variant over Default today, deliberately
		// and with a test. Accepting one here would mean changing core
		// reference parsing to suit a manifest, and the case `name: default`
		// exists for is install-shaped. If it is ever wanted, change
		// `ap variant` first and let `ap sync` inherit it.
		return p, fmt.Errorf("line %d: variants are not accepted under `name: default`: "+
			"a variant is a file ap writes for a profile, and nothing is ever written for the agent's real config", n.keyLine["variants"])
	}
	if vn.kind != mapNode || len(vn.keys) == 0 {
		return p, fmt.Errorf("line %d: variants: expected a mapping of variant name to args", vn.line)
	}
	for _, name := range slices.Sorted(slices.Values(vn.keys)) {
		// Every name that becomes a path goes through ValidName. A manifest
		// comes from a repository somebody else wrote, so `../../../.ssh` here
		// is the --from traversal with an attacker on the other end.
		if err := profile.ValidName(name); err != nil {
			return p, fmt.Errorf("line %d: variant: %w", vn.keyLine[name], err)
		}
		args, err := variantArgs(vn.m[name])
		if err != nil {
			return p, fmt.Errorf("line %d: variant %q: %w", vn.keyLine[name], name, err)
		}
		p.Variants = append(p.Variants, Variant{Name: name, Args: args})
	}
	return p, nil
}

// variantArgs accepts §7.1's two forms and returns the one thing they both are.
func variantArgs(n *node) ([]string, error) {
	if n.kind != mapNode {
		return nil, fmt.Errorf("expected args under it")
	}
	if err := onlyKeys(n, "args"); err != nil {
		return nil, err
	}
	an, ok := n.m["args"]
	if !ok {
		return nil, fmt.Errorf("args is required")
	}
	var args []string
	switch an.kind {
	case scalarNode:
		var err error
		if args, err = Tokenize(an.str); err != nil {
			return nil, err
		}
	case seqNode:
		args = make([]string, len(an.seq))
		for i, e := range an.seq {
			args[i] = e.str
		}
	default:
		return nil, fmt.Errorf("args takes a string or a non-empty list of strings")
	}
	if len(args) == 0 {
		// The same refusal WriteVariant makes, made earlier: §10 aborts on a
		// schema error before anything is created, and finding this out after
		// two profiles exist is a worse report.
		return nil, fmt.Errorf("args is empty, so the variant would carry no arguments and behave identically to its profile")
	}
	return args, nil
}

// commands reads a list of shell command strings. An empty key is an empty
// list, which is what `bootstrap:` with nothing under it means.
func commands(n *node, key string) ([]string, error) {
	if n.kind == emptyNode {
		return nil, nil
	}
	if n.kind != seqNode {
		return nil, fmt.Errorf("line %d: %s: expected a list of shell commands, one per \"- \" line", n.line, key)
	}
	out := make([]string, 0, len(n.seq))
	for _, e := range n.seq {
		if strings.TrimSpace(e.str) == "" {
			return nil, fmt.Errorf("line %d: %s: empty command", e.line, key)
		}
		out = append(out, e.str)
	}
	return out, nil
}

func requiredScalar(n *node, key string) (string, error) {
	c, ok := n.m[key]
	if !ok {
		return "", fmt.Errorf("%s: required", key)
	}
	if c.kind != scalarNode || c.str == "" {
		return "", fmt.Errorf("line %d: %s: expected a value", n.keyLine[key], key)
	}
	return c.str, nil
}

// onlyKeys refuses anything not named. Unknown keys are an error at every
// level: a schema that ignores what it does not recognise turns `varients:`
// into a silently missing variant, and a manifest whose author believes it took
// effect is worse than one that failed.
func onlyKeys(n *node, allowed ...string) error {
	for _, k := range n.keys {
		if !slices.Contains(allowed, k) {
			return fmt.Errorf("line %d: unknown key %q (accepted here: %s)",
				n.keyLine[k], k, strings.Join(allowed, ", "))
		}
	}
	return nil
}
