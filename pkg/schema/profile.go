package schema

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ackstorm/agent-profile/pkg/agentreg"
)

// Profile is one composed manifest, decoded and shape-checked. It is NOT yet
// runtime-specific: Effective produces that.
type Profile struct {
	Version      string
	Name         string
	Targets      []string
	Model        *Model
	Prompt       *Prompt
	Inputs       Inputs
	Marketplaces map[string]Marketplace
	Skills       map[string]Resource
	MCPs         map[string]MCP
	Artifacts    map[string]Artifact
	Runtimes     map[string]Runtime
}

// IsBase reports whether this profile can only be inherited from. §5.3: a base
// is a profile without targets, and there is no abstract flag.
func (p Profile) IsBase() bool { return len(p.Targets) == 0 }

// Resource is a named common resource with an exclusive locator group.
// Enabled defaults to true (§4).
type Resource struct {
	Enabled bool
	Ref     string  // <item>@<marketplace>, §22
	Source  *Source // §16
}

// Source is §16's branch-keyed union: exactly one of Git or Local.
type Source struct {
	Git   *GitSource
	Local *LocalSource
}

type GitSource struct {
	URL, Ref, Subpath string
	Auth              *GitAuth
}

// GitAuth is §17's credential plus §17.1's transport scheme.
//
// Scheme is deliberately NOT defaulted here. Absent means "infer it from the
// host at fetch time" (§17.1): a host containing "gitlab", or beginning
// "git.", yields basic-oauth2 and anything else yields bearer. A default
// written into the decoder would make that inference unreachable — and
// unreportable, which §17.1 requires it to be, because a self-hosted GitLab
// answers Bearer with 401 and the 401 has to name the scheme that was tried.
type GitAuth struct {
	// Scheme is "bearer", "basic-oauth2", or "" for inferred.
	Scheme    string
	ValueFrom ValueFrom
}

// gitAuthSchemes is §17.1's closed set. Anything else is refused by name
// rather than silently ignored: a manifest asking for a scheme this does not
// implement would otherwise be fetched with a different one.
var gitAuthSchemes = []string{"bearer", "basic-oauth2"}

type LocalSource struct{ Path, Subpath string }

type Model struct {
	Type, BaseURL, Model string
	Auth                 *Auth
	Headers              map[string]HeaderValue
	Parameters           map[string]string // lexemes, emitted verbatim
}

type Auth struct {
	Type      string // v1: "bearer"
	ValueFrom ValueFrom
}

// HeaderValue is §14: exactly one of Value or ValueFrom, and Prefix is valid
// only with ValueFrom.
type HeaderValue struct {
	Value     string
	Prefix    string
	ValueFrom *ValueFrom
}

// ValueFrom is §13: exactly one of Secret or Variable.
type ValueFrom struct{ Secret, Variable string }

type Prompt struct {
	Mode    string // "append" (default) or "replace"
	Content string
	Source  *Source
}

type MCP struct {
	Enabled   bool
	Transport Transport
}

type Transport struct {
	Type    string // "http" or "stdio"
	URL     string
	Headers map[string]HeaderValue
	Command string
	Args    []string
}

type Artifact struct {
	Enabled     bool
	Source      *Source
	Destination string
}

type Marketplace struct {
	Enabled bool
	Type    string // "plugins" or "skills"
	Source  *Source
}

// Runtime holds one runtime's overlay. Common-vocabulary keys overlay the root
// in Effective; the rest are runtime-native and stay here.
type Runtime struct {
	Skills       map[string]Resource
	MCPs         map[string]MCP
	Artifacts    map[string]Artifact
	Marketplaces map[string]Marketplace
	Model        *Model
	Prompt       *Prompt
	Inputs       Inputs
	Plugins      map[string]*Node // adapter-owned, §24 — kept as a tree for Phase 6
	Environment  map[string]string
	Variants     map[string][]string
}

type Inputs struct {
	Variables map[string]Binding
	Secrets   map[string]Binding
}

// Binding is §13: exactly one of Env or File.
type Binding struct{ Env, File string }

// Decode shape-checks a composed manifest tree and returns the typed Profile
// §36 describes. Every rejection names the offending key or field and the
// line it came from — an unknown key at any level fails the same way, through
// onlyKeys.
func Decode(n *Node) (Profile, error) {
	var p Profile
	if n == nil {
		return p, fmt.Errorf("empty manifest")
	}
	if n.Kind != Mapping {
		return p, fmt.Errorf("line %d: a manifest is a mapping with version, name and the rest", n.Line)
	}
	if err := onlyKeys(n, "version", "name", "targets", "model", "prompt", "inputs",
		"marketplaces", "skills", "mcps", "artifacts", "runtimes"); err != nil {
		return p, err
	}

	vn, ok := n.Map["version"]
	if !ok {
		return p, fmt.Errorf("line %d: version is required", n.Line)
	}
	v, err := vn.Text()
	if err != nil {
		return p, fmt.Errorf("line %d: version: %w", vn.Line, err)
	}
	if v != "1" {
		return p, fmt.Errorf("line %d: version %s: this ap understands version 1", n.KeyLine["version"], v)
	}
	p.Version = v

	nameNode, ok := n.Map["name"]
	if !ok {
		return p, fmt.Errorf("line %d: name is required", n.Line)
	}
	name, err := nameNode.Text()
	if err != nil {
		return p, fmt.Errorf("line %d: name: %w", nameNode.Line, err)
	}
	// The literal comparison comes first, same as internal/manifest: ValidName
	// rejects "default" on purpose, so the sentinel is routed above it rather
	// than by loosening the guard.
	if name != agentreg.Default {
		if err := agentreg.ValidName(name); err != nil {
			return p, fmt.Errorf("line %d: name: %w", n.KeyLine["name"], err)
		}
	}
	p.Name = name

	// One dispatch loop instead of nine near-identical "if present, decode,
	// assign" blocks: same behavior, most of the branching gone. Each entry's
	// decode func is a top-level function, not a closure — gocyclo folds a
	// closure's own branching into its enclosing function, which would have
	// put Decode right back over the threshold this refactor exists to fix.
	for _, fd := range profileFieldDecoders {
		fn, ok := n.Map[fd.key]
		if !ok {
			continue
		}
		if err := fd.decode(&p, fn); err != nil {
			return p, err
		}
	}
	return p, nil
}

// profileFieldDecoders is Decode's per-key dispatch table, covering every
// optional key after version and name.
var profileFieldDecoders = []struct {
	key    string
	decode func(*Profile, *Node) error
}{
	{"targets", decodeTargetsField},
	{"model", decodeModelField},
	{"prompt", decodePromptField},
	{"inputs", decodeInputsField},
	{"marketplaces", decodeMarketplacesField},
	{"skills", decodeSkillsField},
	{"mcps", decodeMCPsField},
	{"artifacts", decodeArtifactsField},
	{"runtimes", decodeRuntimesField},
}

func decodeTargetsField(p *Profile, n *Node) error {
	v, err := decodeTargets(n)
	if err != nil {
		return err
	}
	p.Targets = v
	return nil
}

func decodeModelField(p *Profile, n *Node) error {
	v, err := decodeModel("model", n)
	if err != nil {
		return err
	}
	p.Model = v
	return nil
}

func decodePromptField(p *Profile, n *Node) error {
	v, err := decodePrompt("prompt", n)
	if err != nil {
		return err
	}
	p.Prompt = v
	return nil
}

func decodeInputsField(p *Profile, n *Node) error {
	v, err := decodeInputs("inputs", n)
	if err != nil {
		return err
	}
	p.Inputs = v
	return nil
}

func decodeMarketplacesField(p *Profile, n *Node) error {
	v, err := decodeMarketplaces("marketplaces", n)
	if err != nil {
		return err
	}
	p.Marketplaces = v
	return nil
}

func decodeSkillsField(p *Profile, n *Node) error {
	v, err := decodeResources("skills", n)
	if err != nil {
		return err
	}
	p.Skills = v
	return nil
}

func decodeMCPsField(p *Profile, n *Node) error {
	v, err := decodeMCPs("mcps", n)
	if err != nil {
		return err
	}
	p.MCPs = v
	return nil
}

func decodeArtifactsField(p *Profile, n *Node) error {
	v, err := decodeArtifacts("artifacts", n)
	if err != nil {
		return err
	}
	p.Artifacts = v
	return nil
}

func decodeRuntimesField(p *Profile, n *Node) error {
	v, err := decodeRuntimes("runtimes", n)
	if err != nil {
		return err
	}
	p.Runtimes = v
	return nil
}

func decodeTargets(n *Node) ([]string, error) {
	if n.Kind != Sequence {
		return nil, fmt.Errorf("line %d: targets: expected a list of agent names", n.Line)
	}
	names := agentreg.Names()
	out := make([]string, 0, len(n.Seq))
	for _, e := range n.Seq {
		t, err := e.Text()
		if err != nil {
			return nil, fmt.Errorf("line %d: targets: %w", e.Line, err)
		}
		if !slices.Contains(names, t) {
			return nil, fmt.Errorf("line %d: targets: unknown agent %q: supported are %s",
				e.Line, t, strings.Join(names, ", "))
		}
		out = append(out, t)
	}
	return out, nil
}

func decodeModel(path string, n *Node) (*Model, error) {
	if n.Kind != Mapping {
		return nil, fmt.Errorf("line %d: %s: expected a mapping", n.Line, path)
	}
	if err := onlyKeys(n, "type", "base_url", "model", "auth", "headers", "parameters"); err != nil {
		return nil, err
	}
	m := &Model{}
	if tn, ok := n.Map["type"]; ok {
		t, err := tn.Text()
		if err != nil {
			return nil, fmt.Errorf("line %d: %s.type: %w", tn.Line, path, err)
		}
		m.Type = t
	}
	if bn, ok := n.Map["base_url"]; ok {
		v, err := bn.Text()
		if err != nil {
			return nil, fmt.Errorf("line %d: %s.base_url: %w", bn.Line, path, err)
		}
		m.BaseURL = v
	}
	if mdn, ok := n.Map["model"]; ok {
		v, err := mdn.Text()
		if err != nil {
			return nil, fmt.Errorf("line %d: %s.model: %w", mdn.Line, path, err)
		}
		m.Model = v
	}
	if an, ok := n.Map["auth"]; ok {
		a, err := decodeAuth(path+".auth", an)
		if err != nil {
			return nil, err
		}
		m.Auth = a
	}
	if hn, ok := n.Map["headers"]; ok {
		// §9: endpoint authentication has exactly one representation — model
		// auth. A literal "Authorization" header here is a second one, so the
		// key itself is refused, not just a literal value under it.
		h, err := decodeModelHeaders(path+".headers", hn)
		if err != nil {
			return nil, err
		}
		m.Headers = h
	}
	if pn, ok := n.Map["parameters"]; ok {
		params, err := decodeStringMap(path+".parameters", pn)
		if err != nil {
			return nil, err
		}
		m.Parameters = params
	}
	return m, nil
}

func decodeAuth(path string, n *Node) (*Auth, error) {
	if n.Kind != Mapping {
		return nil, fmt.Errorf("line %d: %s: expected a mapping", n.Line, path)
	}
	if err := onlyKeys(n, "type", "value_from"); err != nil {
		return nil, err
	}
	t, err := requiredTextField(n, path, "type")
	if err != nil {
		return nil, err
	}
	if t != "bearer" {
		return nil, fmt.Errorf("line %d: %s.type: %q: v1 supports bearer only", n.KeyLine["type"], path, t)
	}
	vfn, ok := n.Map["value_from"]
	if !ok {
		return nil, fmt.Errorf("line %d: %s: value_from is required", n.Line, path)
	}
	vf, err := decodeValueFrom(path+".value_from", vfn)
	if err != nil {
		return nil, err
	}
	return &Auth{Type: t, ValueFrom: vf}, nil
}

func decodeValueFrom(path string, n *Node) (ValueFrom, error) {
	var vf ValueFrom
	if n.Kind != Mapping {
		return vf, fmt.Errorf("line %d: %s: expected a mapping with secret or variable", n.Line, path)
	}
	if err := onlyKeys(n, "secret", "variable"); err != nil {
		return vf, err
	}
	sn, hasSecret := n.Map["secret"]
	vn, hasVar := n.Map["variable"]
	switch {
	case hasSecret && hasVar:
		return vf, fmt.Errorf("line %d: %s: both secret and variable set; use exactly one", n.Line, path)
	case !hasSecret && !hasVar:
		return vf, fmt.Errorf("line %d: %s: neither secret nor variable set; use exactly one", n.Line, path)
	}
	if hasSecret {
		v, err := sn.Text()
		if err != nil {
			return vf, fmt.Errorf("line %d: %s.secret: %w", sn.Line, path, err)
		}
		vf.Secret = v
	} else {
		v, err := vn.Text()
		if err != nil {
			return vf, fmt.Errorf("line %d: %s.variable: %w", vn.Line, path, err)
		}
		vf.Variable = v
	}
	return vf, nil
}

// decodeHeaderValue is §14's rule shared by every header, wherever headers
// appear: exactly one of value or value_from, and prefix only with
// value_from. Whether the key "Authorization" is even allowed is decided by
// the caller — decodeModelHeaders and decodeTransportHeaders disagree on that.
func decodeHeaderValue(path string, n *Node) (HeaderValue, error) {
	var hv HeaderValue
	if n.Kind != Mapping {
		return hv, fmt.Errorf("line %d: %s: expected a mapping with value or value_from", n.Line, path)
	}
	if err := onlyKeys(n, "value", "value_from", "prefix"); err != nil {
		return hv, err
	}
	valN, hasValue := n.Map["value"]
	fromN, hasFrom := n.Map["value_from"]
	switch {
	case hasValue && hasFrom:
		return hv, fmt.Errorf("line %d: %s: both value and value_from set; use exactly one", n.Line, path)
	case !hasValue && !hasFrom:
		return hv, fmt.Errorf("line %d: %s: neither value nor value_from set; use exactly one", n.Line, path)
	}
	if pn, ok := n.Map["prefix"]; ok {
		if !hasFrom {
			return hv, fmt.Errorf("line %d: %s.prefix: only valid alongside value_from", pn.Line, path)
		}
		v, err := pn.Text()
		if err != nil {
			return hv, fmt.Errorf("line %d: %s.prefix: %w", pn.Line, path, err)
		}
		hv.Prefix = v
	}
	if hasValue {
		v, err := valN.Text()
		if err != nil {
			return hv, fmt.Errorf("line %d: %s.value: %w", valN.Line, path, err)
		}
		hv.Value = v
	} else {
		vf, err := decodeValueFrom(path+".value_from", fromN)
		if err != nil {
			return hv, err
		}
		hv.ValueFrom = &vf
	}
	return hv, nil
}

// decodeModelHeaders is §9: Authorization is not a header here at all — model
// authentication has exactly one representation, model.auth.
func decodeModelHeaders(path string, n *Node) (map[string]HeaderValue, error) {
	if n.Kind != Mapping {
		return nil, fmt.Errorf("line %d: %s: expected a mapping of header name to value", n.Line, path)
	}
	out := map[string]HeaderValue{}
	for _, k := range n.Keys {
		if k == "Authorization" {
			return nil, fmt.Errorf("line %d: %s.%s: Authorization is not a header here; set model.auth instead",
				n.KeyLine[k], path, k)
		}
		hv, err := decodeHeaderValue(fmt.Sprintf("%s.%s", path, k), n.Map[k])
		if err != nil {
			return nil, err
		}
		out[k] = hv
	}
	return out, nil
}

// decodeTransportHeaders is §14: Authorization is allowed here, but a literal
// value is a secret embedded in the manifest, which §13 prohibits outright —
// so it must arrive via value_from.
func decodeTransportHeaders(path string, n *Node) (map[string]HeaderValue, error) {
	if n.Kind != Mapping {
		return nil, fmt.Errorf("line %d: %s: expected a mapping of header name to value", n.Line, path)
	}
	out := map[string]HeaderValue{}
	for _, k := range n.Keys {
		hv, err := decodeHeaderValue(fmt.Sprintf("%s.%s", path, k), n.Map[k])
		if err != nil {
			return nil, err
		}
		if k == "Authorization" && hv.ValueFrom == nil {
			return nil, fmt.Errorf("line %d: %s.%s: a literal Authorization is not allowed; use value_from",
				n.KeyLine[k], path, k)
		}
		out[k] = hv
	}
	return out, nil
}

func decodePrompt(path string, n *Node) (*Prompt, error) {
	if n.Kind != Mapping {
		return nil, fmt.Errorf("line %d: %s: expected a mapping", n.Line, path)
	}
	if err := onlyKeys(n, "mode", "content", "source"); err != nil {
		return nil, err
	}
	pr := &Prompt{Mode: "append"}
	if mn, ok := n.Map["mode"]; ok {
		m, err := mn.Text()
		if err != nil {
			return nil, fmt.Errorf("line %d: %s.mode: %w", mn.Line, path, err)
		}
		if m != "append" && m != "replace" {
			return nil, fmt.Errorf("line %d: %s.mode: %q: must be append or replace", mn.Line, path, m)
		}
		pr.Mode = m
	}
	cn, hasContent := n.Map["content"]
	sn, hasSource := n.Map["source"]
	switch {
	case hasContent && hasSource:
		return nil, fmt.Errorf("line %d: %s: both content and source set; use exactly one", n.Line, path)
	case !hasContent && !hasSource:
		return nil, fmt.Errorf("line %d: %s: neither content nor source set; use exactly one", n.Line, path)
	}
	if hasContent {
		c, err := cn.Text()
		if err != nil {
			return nil, fmt.Errorf("line %d: %s.content: %w", cn.Line, path, err)
		}
		pr.Content = c
	} else {
		src, err := decodeSource(path+".source", sn)
		if err != nil {
			return nil, err
		}
		pr.Source = src
	}
	return pr, nil
}

// decodeSource is §16's branch-keyed union: exactly one of git or local. The
// merge engine folds a same-branch overlay recursively (compose.go) rather
// than replacing the node, so a null on the chosen branch's only field can
// leave a composed source with neither key present — and schema validation
// never enforces exclusivity WITHIN one authored document either, trusting
// this function to. Both directions are checked here because this is the
// only place either is caught.
func decodeSource(path string, n *Node) (*Source, error) {
	if n.Kind != Mapping {
		return nil, fmt.Errorf("line %d: %s: expected a mapping with git or local", n.Line, path)
	}
	if err := onlyKeys(n, "git", "local"); err != nil {
		return nil, err
	}
	gn, hasGit := n.Map["git"]
	ln, hasLocal := n.Map["local"]
	switch {
	case hasGit && hasLocal:
		return nil, fmt.Errorf("line %d: %s: both git and local selected; use exactly one", n.Line, path)
	case !hasGit && !hasLocal:
		return nil, fmt.Errorf("line %d: %s: no source selected; use git or local", n.Line, path)
	}
	src := &Source{}
	if hasGit {
		g, err := decodeGitSource(path+".git", gn)
		if err != nil {
			return nil, err
		}
		src.Git = g
	} else {
		l, err := decodeLocalSource(path+".local", ln)
		if err != nil {
			return nil, err
		}
		src.Local = l
	}
	return src, nil
}

func decodeGitSource(path string, n *Node) (*GitSource, error) {
	if n.Kind != Mapping {
		return nil, fmt.Errorf("line %d: %s: expected a mapping", n.Line, path)
	}
	if err := onlyKeys(n, "url", "ref", "subpath", "auth"); err != nil {
		return nil, err
	}
	g := &GitSource{}
	var err error
	if g.URL, err = requiredTextField(n, path, "url"); err != nil {
		return nil, err
	}
	if rn, ok := n.Map["ref"]; ok {
		if g.Ref, err = rn.Text(); err != nil {
			return nil, fmt.Errorf("line %d: %s.ref: %w", rn.Line, path, err)
		}
	}
	if sn, ok := n.Map["subpath"]; ok {
		if g.Subpath, err = sn.Text(); err != nil {
			return nil, fmt.Errorf("line %d: %s.subpath: %w", sn.Line, path, err)
		}
		if err := validRelPath(path+".subpath", g.Subpath); err != nil {
			return nil, err
		}
	}
	if an, ok := n.Map["auth"]; ok {
		if g.Auth, err = decodeGitAuth(path+".auth", an); err != nil {
			return nil, err
		}
	}
	return g, nil
}

// decodeGitAuth reads §17's auth block: value_from, plus §17.1's optional
// scheme. It cannot reuse decodeValueFrom directly because that one owns its
// own onlyKeys check and would refuse "scheme".
func decodeGitAuth(path string, n *Node) (*GitAuth, error) {
	if n.Kind != Mapping {
		return nil, fmt.Errorf("line %d: %s: expected a mapping with value_from", n.Line, path)
	}
	if err := onlyKeys(n, "scheme", "value_from"); err != nil {
		return nil, err
	}
	a := &GitAuth{}
	if sn, ok := n.Map["scheme"]; ok {
		s, err := sn.Text()
		if err != nil {
			return nil, fmt.Errorf("line %d: %s.scheme: %w", sn.Line, path, err)
		}
		if !slices.Contains(gitAuthSchemes, s) {
			return nil, fmt.Errorf("line %d: %s.scheme: unknown scheme %q (v1: %s)",
				sn.Line, path, s, strings.Join(gitAuthSchemes, ", "))
		}
		a.Scheme = s
	}
	vfn, ok := n.Map["value_from"]
	if !ok {
		return nil, fmt.Errorf("line %d: %s: value_from is required", n.Line, path)
	}
	vf, err := decodeValueFrom(path+".value_from", vfn)
	if err != nil {
		return nil, err
	}
	a.ValueFrom = vf
	return a, nil
}

func decodeLocalSource(path string, n *Node) (*LocalSource, error) {
	if n.Kind != Mapping {
		return nil, fmt.Errorf("line %d: %s: expected a mapping", n.Line, path)
	}
	if err := onlyKeys(n, "path", "subpath"); err != nil {
		return nil, err
	}
	l := &LocalSource{}
	var err error
	if l.Path, err = requiredTextField(n, path, "path"); err != nil {
		return nil, err
	}
	if sn, ok := n.Map["subpath"]; ok {
		if l.Subpath, err = sn.Text(); err != nil {
			return nil, fmt.Errorf("line %d: %s.subpath: %w", sn.Line, path, err)
		}
		if err := validRelPath(path+".subpath", l.Subpath); err != nil {
			return nil, err
		}
	}
	return l, nil
}

func decodeResources(path string, n *Node) (map[string]Resource, error) {
	if n.Kind != Mapping {
		return nil, fmt.Errorf("line %d: %s: expected a mapping of name to resource", n.Line, path)
	}
	out := map[string]Resource{}
	for _, k := range n.Keys {
		r, err := decodeResource(fmt.Sprintf("%s.%s", path, k), n.Map[k])
		if err != nil {
			return nil, err
		}
		out[k] = r
	}
	return out, nil
}

func decodeResource(path string, n *Node) (Resource, error) {
	r := Resource{Enabled: true}
	if n.Kind == Empty {
		// `pdf:` with nothing under it: an enabled resource with no locator,
		// refused below exactly like `pdf: {}` would be if this subset
		// admitted flow mappings.
		n = &Node{Kind: Mapping, Line: n.Line, Map: map[string]*Node{}}
	}
	if n.Kind != Mapping {
		return r, fmt.Errorf("line %d: %s: expected a mapping", n.Line, path)
	}
	if err := onlyKeys(n, "enabled", "ref", "source"); err != nil {
		return r, err
	}
	if en, ok := n.Map["enabled"]; ok {
		b, err := en.Bool()
		if err != nil {
			return r, fmt.Errorf("line %d: %s.enabled: %w", en.Line, path, err)
		}
		r.Enabled = b
	}
	refN, hasRef := n.Map["ref"]
	srcN, hasSrc := n.Map["source"]
	// §22: an enabled resource defines exactly one external locator. A
	// disabled one is not materialized, so it needs none.
	if r.Enabled {
		switch {
		case hasRef && hasSrc:
			return r, fmt.Errorf("line %d: %s: both ref and source set; use exactly one", n.Line, path)
		case !hasRef && !hasSrc:
			return r, fmt.Errorf("line %d: %s: no locator; use ref or source", n.Line, path)
		}
	}
	if hasRef {
		ref, err := refN.Text()
		if err != nil {
			return r, fmt.Errorf("line %d: %s.ref: %w", refN.Line, path, err)
		}
		r.Ref = ref
	}
	if hasSrc {
		src, err := decodeSource(path+".source", srcN)
		if err != nil {
			return r, err
		}
		r.Source = src
	}
	return r, nil
}

func decodeMCPs(path string, n *Node) (map[string]MCP, error) {
	if n.Kind != Mapping {
		return nil, fmt.Errorf("line %d: %s: expected a mapping of name to mcp", n.Line, path)
	}
	out := map[string]MCP{}
	for _, k := range n.Keys {
		m, err := decodeMCP(fmt.Sprintf("%s.%s", path, k), n.Map[k])
		if err != nil {
			return nil, err
		}
		out[k] = m
	}
	return out, nil
}

func decodeMCP(path string, n *Node) (MCP, error) {
	mc := MCP{Enabled: true}
	if n.Kind != Mapping {
		return mc, fmt.Errorf("line %d: %s: expected a mapping", n.Line, path)
	}
	if err := onlyKeys(n, "enabled", "transport"); err != nil {
		return mc, err
	}
	if en, ok := n.Map["enabled"]; ok {
		b, err := en.Bool()
		if err != nil {
			return mc, fmt.Errorf("line %d: %s.enabled: %w", en.Line, path, err)
		}
		mc.Enabled = b
	}
	tn, ok := n.Map["transport"]
	if !ok {
		return mc, fmt.Errorf("line %d: %s: transport is required", n.Line, path)
	}
	t, err := decodeTransport(path+".transport", tn)
	if err != nil {
		return mc, err
	}
	mc.Transport = t
	return mc, nil
}

func decodeTransport(path string, n *Node) (Transport, error) {
	var t Transport
	if n.Kind != Mapping {
		return t, fmt.Errorf("line %d: %s: expected a mapping", n.Line, path)
	}
	if err := onlyKeys(n, "type", "url", "headers", "command", "args"); err != nil {
		return t, err
	}
	typ, err := requiredTextField(n, path, "type")
	if err != nil {
		return t, err
	}
	if typ != "http" && typ != "stdio" {
		return t, fmt.Errorf("line %d: %s.type: %q: must be http or stdio", n.KeyLine["type"], path, typ)
	}
	t.Type = typ
	_, hasURL := n.Map["url"]
	_, hasHeaders := n.Map["headers"]
	_, hasCommand := n.Map["command"]
	_, hasArgs := n.Map["args"]
	switch typ {
	case "http":
		if hasCommand || hasArgs {
			return t, fmt.Errorf("line %d: %s: command/args are not valid with type: http", n.Line, path)
		}
		if !hasURL {
			return t, fmt.Errorf("line %d: %s: url is required with type: http", n.Line, path)
		}
		u, err := requiredTextField(n, path, "url")
		if err != nil {
			return t, err
		}
		t.URL = u
		if hasHeaders {
			h, err := decodeTransportHeaders(path+".headers", n.Map["headers"])
			if err != nil {
				return t, err
			}
			t.Headers = h
		}
	case "stdio":
		if hasURL || hasHeaders {
			return t, fmt.Errorf("line %d: %s: url/headers are not valid with type: stdio", n.Line, path)
		}
		if !hasCommand {
			return t, fmt.Errorf("line %d: %s: command is required with type: stdio", n.Line, path)
		}
		c, err := requiredTextField(n, path, "command")
		if err != nil {
			return t, err
		}
		t.Command = c
		if hasArgs {
			args, err := decodeStringList(path+".args", n.Map["args"])
			if err != nil {
				return t, err
			}
			t.Args = args
		}
	}
	return t, nil
}

func decodeArtifacts(path string, n *Node) (map[string]Artifact, error) {
	if n.Kind != Mapping {
		return nil, fmt.Errorf("line %d: %s: expected a mapping of name to artifact", n.Line, path)
	}
	out := map[string]Artifact{}
	for _, k := range n.Keys {
		a, err := decodeArtifact(fmt.Sprintf("%s.%s", path, k), n.Map[k])
		if err != nil {
			return nil, err
		}
		out[k] = a
	}
	return out, nil
}

func decodeArtifact(path string, n *Node) (Artifact, error) {
	a := Artifact{Enabled: true}
	if n.Kind != Mapping {
		return a, fmt.Errorf("line %d: %s: expected a mapping", n.Line, path)
	}
	if err := onlyKeys(n, "enabled", "source", "destination"); err != nil {
		return a, err
	}
	if en, ok := n.Map["enabled"]; ok {
		b, err := en.Bool()
		if err != nil {
			return a, fmt.Errorf("line %d: %s.enabled: %w", en.Line, path, err)
		}
		a.Enabled = b
	}
	if !a.Enabled {
		return a, nil
	}
	srcN, ok := n.Map["source"]
	if !ok {
		return a, fmt.Errorf("line %d: %s: source is required", n.Line, path)
	}
	src, err := decodeSource(path+".source", srcN)
	if err != nil {
		return a, err
	}
	a.Source = src

	dest, err := requiredTextField(n, path, "destination")
	if err != nil {
		return a, err
	}
	// §26.1: relative, and must not escape via "..".
	if err := validRelPath(path+".destination", dest); err != nil {
		return a, err
	}
	a.Destination = dest
	return a, nil
}

// validRelPath enforces the one path rule §20 and §26.1 share: relative, and
// it may not escape its root with "..". Checked on the CLEANED path, because
// "a/../../b" is only visibly an escape after cleaning — and an escape here
// would write outside a profile namespace, which is the whole containment
// boundary this shares with the --from traversal guard.
func validRelPath(field, p string) error {
	if p == "" {
		return fmt.Errorf("%s: must not be empty", field)
	}
	if filepath.IsAbs(p) {
		return fmt.Errorf("%s %q: must be relative", field, p)
	}
	c := filepath.Clean(p)
	if c == ".." || strings.HasPrefix(c, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s %q: must not escape its root with %q", field, p, "..")
	}
	return nil
}

func decodeMarketplaces(path string, n *Node) (map[string]Marketplace, error) {
	if n.Kind != Mapping {
		return nil, fmt.Errorf("line %d: %s: expected a mapping of name to marketplace", n.Line, path)
	}
	out := map[string]Marketplace{}
	for _, k := range n.Keys {
		m, err := decodeMarketplace(fmt.Sprintf("%s.%s", path, k), n.Map[k])
		if err != nil {
			return nil, err
		}
		out[k] = m
	}
	return out, nil
}

func decodeMarketplace(path string, n *Node) (Marketplace, error) {
	m := Marketplace{Enabled: true}
	if n.Kind != Mapping {
		return m, fmt.Errorf("line %d: %s: expected a mapping", n.Line, path)
	}
	if err := onlyKeys(n, "enabled", "type", "source"); err != nil {
		return m, err
	}
	if en, ok := n.Map["enabled"]; ok {
		b, err := en.Bool()
		if err != nil {
			return m, fmt.Errorf("line %d: %s.enabled: %w", en.Line, path, err)
		}
		m.Enabled = b
	}
	typ, err := requiredTextField(n, path, "type")
	if err != nil {
		return m, err
	}
	if typ != "plugins" && typ != "skills" {
		return m, fmt.Errorf("line %d: %s.type: %q: must be plugins or skills", n.KeyLine["type"], path, typ)
	}
	m.Type = typ
	srcN, ok := n.Map["source"]
	if !ok {
		return m, fmt.Errorf("line %d: %s: source is required", n.Line, path)
	}
	src, err := decodeSource(path+".source", srcN)
	if err != nil {
		return m, err
	}
	m.Source = src
	return m, nil
}

func decodeInputs(path string, n *Node) (Inputs, error) {
	var in Inputs
	if n.Kind != Mapping {
		return in, fmt.Errorf("line %d: %s: expected a mapping", n.Line, path)
	}
	if err := onlyKeys(n, "variables", "secrets"); err != nil {
		return in, err
	}
	if vn, ok := n.Map["variables"]; ok {
		v, err := decodeBindings(path+".variables", vn)
		if err != nil {
			return in, err
		}
		in.Variables = v
	}
	if sn, ok := n.Map["secrets"]; ok {
		s, err := decodeBindings(path+".secrets", sn)
		if err != nil {
			return in, err
		}
		in.Secrets = s
	}
	return in, nil
}

func decodeBindings(path string, n *Node) (map[string]Binding, error) {
	if n.Kind != Mapping {
		return nil, fmt.Errorf("line %d: %s: expected a mapping of name to binding", n.Line, path)
	}
	out := map[string]Binding{}
	for _, k := range n.Keys {
		b, err := decodeBinding(fmt.Sprintf("%s.%s", path, k), n.Map[k])
		if err != nil {
			return nil, err
		}
		out[k] = b
	}
	return out, nil
}

func decodeBinding(path string, n *Node) (Binding, error) {
	var b Binding
	if n.Kind != Mapping {
		return b, fmt.Errorf("line %d: %s: expected a mapping with env or file", n.Line, path)
	}
	if err := onlyKeys(n, "env", "file"); err != nil {
		return b, err
	}
	en, hasEnv := n.Map["env"]
	fn, hasFile := n.Map["file"]
	switch {
	case hasEnv && hasFile:
		return b, fmt.Errorf("line %d: %s: both env and file set; use exactly one", n.Line, path)
	case !hasEnv && !hasFile:
		return b, fmt.Errorf("line %d: %s: neither env nor file set; use exactly one", n.Line, path)
	}
	if hasEnv {
		v, err := en.Text()
		if err != nil {
			return b, fmt.Errorf("line %d: %s.env: %w", en.Line, path, err)
		}
		b.Env = v
	} else {
		v, err := fn.Text()
		if err != nil {
			return b, fmt.Errorf("line %d: %s.file: %w", fn.Line, path, err)
		}
		b.File = v
	}
	return b, nil
}

func decodeRuntimes(path string, n *Node) (map[string]Runtime, error) {
	if n.Kind != Mapping {
		return nil, fmt.Errorf("line %d: %s: expected a mapping of name to runtime", n.Line, path)
	}
	out := map[string]Runtime{}
	for _, k := range n.Keys {
		r, err := decodeRuntime(fmt.Sprintf("%s.%s", path, k), n.Map[k])
		if err != nil {
			return nil, err
		}
		out[k] = r
	}
	return out, nil
}

func decodeRuntime(path string, n *Node) (Runtime, error) {
	var r Runtime
	if n.Kind == Empty {
		return r, nil // "this runtime, no overlay"
	}
	if n.Kind != Mapping {
		return r, fmt.Errorf("line %d: %s: expected a mapping", n.Line, path)
	}
	if err := onlyKeys(n, "skills", "mcps", "artifacts", "marketplaces", "model", "prompt",
		"inputs", "plugins", "environment", "variants"); err != nil {
		return r, err
	}
	// Same dispatch-loop shape as Decode, for the same reason: ten near-
	// identical "if present, decode, assign" blocks collapsed into one loop,
	// with each entry a top-level function rather than a closure — see
	// profileFieldDecoders' comment for why that matters to gocyclo.
	for _, fd := range runtimeFieldDecoders {
		fn, ok := n.Map[fd.key]
		if !ok {
			continue
		}
		if err := fd.decode(path, &r, fn); err != nil {
			return r, err
		}
	}
	return r, nil
}

// runtimeFieldDecoders is decodeRuntime's per-key dispatch table.
var runtimeFieldDecoders = []struct {
	key    string
	decode func(path string, r *Runtime, n *Node) error
}{
	{"skills", decodeRuntimeSkillsField},
	{"mcps", decodeRuntimeMCPsField},
	{"artifacts", decodeRuntimeArtifactsField},
	{"marketplaces", decodeRuntimeMarketplacesField},
	{"model", decodeRuntimeModelField},
	{"prompt", decodeRuntimePromptField},
	{"inputs", decodeRuntimeInputsField},
	{"plugins", decodeRuntimePluginsField},
	{"environment", decodeRuntimeEnvironmentField},
	{"variants", decodeRuntimeVariantsField},
}

func decodeRuntimeSkillsField(path string, r *Runtime, n *Node) error {
	v, err := decodeResources(path+".skills", n)
	if err != nil {
		return err
	}
	r.Skills = v
	return nil
}

func decodeRuntimeMCPsField(path string, r *Runtime, n *Node) error {
	v, err := decodeMCPs(path+".mcps", n)
	if err != nil {
		return err
	}
	r.MCPs = v
	return nil
}

func decodeRuntimeArtifactsField(path string, r *Runtime, n *Node) error {
	v, err := decodeArtifacts(path+".artifacts", n)
	if err != nil {
		return err
	}
	r.Artifacts = v
	return nil
}

func decodeRuntimeMarketplacesField(path string, r *Runtime, n *Node) error {
	v, err := decodeMarketplaces(path+".marketplaces", n)
	if err != nil {
		return err
	}
	r.Marketplaces = v
	return nil
}

func decodeRuntimeModelField(path string, r *Runtime, n *Node) error {
	v, err := decodeModel(path+".model", n)
	if err != nil {
		return err
	}
	r.Model = v
	return nil
}

func decodeRuntimePromptField(path string, r *Runtime, n *Node) error {
	v, err := decodePrompt(path+".prompt", n)
	if err != nil {
		return err
	}
	r.Prompt = v
	return nil
}

func decodeRuntimeInputsField(path string, r *Runtime, n *Node) error {
	v, err := decodeInputs(path+".inputs", n)
	if err != nil {
		return err
	}
	r.Inputs = v
	return nil
}

func decodeRuntimePluginsField(path string, r *Runtime, n *Node) error {
	v, err := decodePlugins(path+".plugins", n)
	if err != nil {
		return err
	}
	r.Plugins = v
	return nil
}

func decodeRuntimeEnvironmentField(path string, r *Runtime, n *Node) error {
	v, err := decodeStringMap(path+".environment", n)
	if err != nil {
		return err
	}
	r.Environment = v
	return nil
}

func decodeRuntimeVariantsField(path string, r *Runtime, n *Node) error {
	v, err := decodeVariants(path+".variants", n)
	if err != nil {
		return err
	}
	r.Variants = v
	return nil
}

// decodePlugins captures each entry as a raw Node, no deeper than that: §24
// makes the plugin schema adapter-owned, and `package:` (opencode) is a third
// locator form this package does not know about. Phase 6 decides its shape.
func decodePlugins(path string, n *Node) (map[string]*Node, error) {
	if n.Kind != Mapping {
		return nil, fmt.Errorf("line %d: %s: expected a mapping of name to plugin", n.Line, path)
	}
	out := map[string]*Node{}
	for _, k := range n.Keys {
		out[k] = n.Map[k]
	}
	return out, nil
}

// decodeStringMap reads model.parameters and runtime.environment: every value
// is a lexeme, kept verbatim rather than reinterpreted, so 0.20 stays "0.20"
// and true stays "true".
func decodeStringMap(path string, n *Node) (map[string]string, error) {
	if n.Kind != Mapping {
		return nil, fmt.Errorf("line %d: %s: expected a mapping", n.Line, path)
	}
	out := map[string]string{}
	for _, k := range n.Keys {
		v := n.Map[k]
		switch v.Kind {
		case Scalar:
			out[k] = v.Str
		case Null:
			out[k] = "null"
		default:
			return nil, fmt.Errorf("line %d: %s.%s: expected a scalar", v.Line, path, k)
		}
	}
	return out, nil
}

func decodeStringList(path string, n *Node) ([]string, error) {
	if n.Kind != Sequence {
		return nil, fmt.Errorf("line %d: %s: expected a list of strings", n.Line, path)
	}
	out := make([]string, 0, len(n.Seq))
	for _, e := range n.Seq {
		v, err := e.Text()
		if err != nil {
			return nil, fmt.Errorf("line %d: %s: %w", e.Line, path, err)
		}
		out = append(out, v)
	}
	return out, nil
}

func decodeVariants(path string, n *Node) (map[string][]string, error) {
	if n.Kind != Mapping {
		return nil, fmt.Errorf("line %d: %s: expected a mapping of name to args", n.Line, path)
	}
	out := map[string][]string{}
	for _, k := range n.Keys {
		args, err := decodeStringList(fmt.Sprintf("%s.%s", path, k), n.Map[k])
		if err != nil {
			return nil, err
		}
		out[k] = args
	}
	return out, nil
}

// requiredTextField reads a required scalar field, naming the container's
// line if the key is missing entirely and the field's own line if it is
// present but not text.
func requiredTextField(n *Node, path, key string) (string, error) {
	c, ok := n.Map[key]
	if !ok {
		return "", fmt.Errorf("line %d: %s: %s is required", n.Line, path, key)
	}
	v, err := c.Text()
	if err != nil {
		return "", fmt.Errorf("line %d: %s.%s: %w", c.Line, path, key, err)
	}
	return v, nil
}

// onlyKeys refuses anything not named, naming the key's OWN line — via
// KeyLine, not the value node's Line, which for an indented block is the
// first line of the block rather than the key itself. Adapted from
// internal/manifest.onlyKeys; pkg/ cannot import internal/.
func onlyKeys(n *Node, allowed ...string) error {
	for _, k := range n.Keys {
		if !slices.Contains(allowed, k) {
			return fmt.Errorf("line %d: unknown key %q (accepted here: %s)",
				n.KeyLine[k], k, strings.Join(allowed, ", "))
		}
	}
	return nil
}
