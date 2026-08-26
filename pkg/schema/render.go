package schema

import (
	"fmt"
	"sort"
	"strings"
)

// Render prints the effective profile as this subset's own YAML: two-space
// indent, collection keys sorted, a multi-line prompt as a block scalar. It
// never reads a binding's resolved VALUE — only the logical name a binding is
// declared under, which is structure and is what makes render useful for
// debugging (§31). Phase 2 owns resolution; render has to work before it
// exists, and a Profile never holds a secret's value in the first place, only
// the reference to where one comes from.
func Render(p Profile) []byte {
	w := &yw{b: &strings.Builder{}}
	w.scalar(0, "version", p.Version)
	w.scalar(0, "name", p.Name)
	if len(p.Targets) > 0 {
		w.list(0, "targets", p.Targets)
	}
	if p.Model != nil {
		w.key(0, "model")
		w.modelBlock(1, p.Model)
	}
	if p.Prompt != nil {
		w.key(0, "prompt")
		w.promptBlock(1, p.Prompt)
	}
	w.inputsBlock(0, p.Inputs)
	w.marketplacesBlock(0, "marketplaces", p.Marketplaces)
	w.resourcesBlock(0, "skills", p.Skills)
	w.mcpsBlock(0, "mcps", p.MCPs)
	w.artifactsBlock(0, "artifacts", p.Artifacts)
	if len(p.Runtimes) > 0 {
		w.key(0, "runtimes")
		for _, rt := range sortedKeys(p.Runtimes) {
			w.key(1, rt)
			w.runtimeBlock(2, p.Runtimes[rt])
		}
	}
	return []byte(w.b.String())
}

// yw is a minimal YAML writer over this subset only: it never needs to read
// back what it writes, so it does not share code with the parser.
type yw struct{ b *strings.Builder }

func (w *yw) writeln(indent int, s string) {
	w.b.WriteString(strings.Repeat("  ", indent))
	w.b.WriteString(s)
	w.b.WriteByte('\n')
}

func (w *yw) key(indent int, key string) { w.writeln(indent, key+":") }

func (w *yw) scalar(indent int, key, val string) {
	w.writeln(indent, key+": "+quoteIfNeeded(val))
}

func (w *yw) boolField(indent int, val bool) {
	w.writeln(indent, fmt.Sprintf("enabled: %t", val))
}

func (w *yw) list(indent int, key string, items []string) {
	w.key(indent, key)
	for _, it := range items {
		w.writeln(indent+1, "- "+quoteIfNeeded(it))
	}
}

func (w *yw) stringMap(indent int, key string, m map[string]string) {
	w.key(indent, key)
	for _, k := range sortedKeys(m) {
		w.scalar(indent+1, k, m[k])
	}
}

// blockScalar writes a multi-line value as `key: |` with its body indented
// and unquoted, and a single-line value as an ordinary scalar. Render is not
// required to round-trip through ParseYAML, but this keeps a multi-line
// prompt readable rather than a wall of \n escapes.
func (w *yw) blockScalar(indent int, key, content string) {
	if !strings.Contains(content, "\n") {
		w.scalar(indent, key, content)
		return
	}
	w.writeln(indent, key+": |")
	for _, l := range strings.Split(strings.TrimRight(content, "\n"), "\n") {
		if l == "" {
			w.writeln(0, "")
			continue
		}
		w.writeln(indent+1, l)
	}
}

func (w *yw) modelBlock(indent int, m *Model) {
	if m.Type != "" {
		w.scalar(indent, "type", m.Type)
	}
	if m.BaseURL != "" {
		w.scalar(indent, "base_url", m.BaseURL)
	}
	if m.Model != "" {
		w.scalar(indent, "model", m.Model)
	}
	if m.Auth != nil {
		w.key(indent, "auth")
		w.scalar(indent+1, "type", m.Auth.Type)
		w.key(indent+1, "value_from")
		w.valueFrom(indent+2, m.Auth.ValueFrom)
	}
	if len(m.Headers) > 0 {
		w.headersBlock(indent, "headers", m.Headers)
	}
	if len(m.Parameters) > 0 {
		w.stringMap(indent, "parameters", m.Parameters)
	}
}

func (w *yw) valueFrom(indent int, vf ValueFrom) {
	if vf.Secret != "" {
		w.scalar(indent, "secret", vf.Secret)
		return
	}
	w.scalar(indent, "variable", vf.Variable)
}

func (w *yw) headersBlock(indent int, key string, hs map[string]HeaderValue) {
	w.key(indent, key)
	for _, k := range sortedKeys(hs) {
		w.key(indent+1, k)
		hv := hs[k]
		if hv.ValueFrom != nil {
			if hv.Prefix != "" {
				w.scalar(indent+2, "prefix", hv.Prefix)
			}
			w.key(indent+2, "value_from")
			w.valueFrom(indent+3, *hv.ValueFrom)
			continue
		}
		w.scalar(indent+2, "value", hv.Value)
	}
}

func (w *yw) promptBlock(indent int, p *Prompt) {
	w.scalar(indent, "mode", p.Mode)
	if p.Source != nil {
		w.key(indent, "source")
		w.sourceBlock(indent+1, p.Source)
		return
	}
	w.blockScalar(indent, "content", p.Content)
}

func (w *yw) sourceBlock(indent int, s *Source) {
	if s.Git != nil {
		w.key(indent, "git")
		w.gitSourceBlock(indent+1, s.Git)
		return
	}
	if s.Local != nil {
		w.key(indent, "local")
		w.localSourceBlock(indent+1, s.Local)
		return
	}
	if s.Archive != nil {
		w.key(indent, "archive")
		w.archiveSourceBlock(indent+1, s.Archive)
	}
}

func (w *yw) archiveSourceBlock(indent int, a *ArchiveSource) {
	w.scalar(indent, "url", a.URL)
	w.scalar(indent, "digest", a.Digest)
	if a.Subpath != "" {
		w.scalar(indent, "subpath", a.Subpath)
	}
	if a.Auth != nil {
		w.key(indent, "auth")
		if a.Auth.Scheme != "" {
			w.scalar(indent+1, "scheme", a.Auth.Scheme)
		}
		w.valueFrom(indent+1, a.Auth.ValueFrom)
	}
}

func (w *yw) gitSourceBlock(indent int, g *GitSource) {
	w.scalar(indent, "url", g.URL)
	if g.Ref != "" {
		w.scalar(indent, "ref", g.Ref)
	}
	if g.Subpath != "" {
		w.scalar(indent, "subpath", g.Subpath)
	}
	if g.Auth != nil {
		w.key(indent, "auth")
		// An inferred scheme is NOT filled in here. render shows what the
		// manifest says; §35.2's export is the operation that emits resolved
		// values, and it works from the ledger, not from this.
		if g.Auth.Scheme != "" {
			w.scalar(indent+1, "scheme", g.Auth.Scheme)
		}
		w.valueFrom(indent+1, g.Auth.ValueFrom)
	}
}

func (w *yw) localSourceBlock(indent int, l *LocalSource) {
	w.scalar(indent, "path", l.Path)
	if l.Subpath != "" {
		w.scalar(indent, "subpath", l.Subpath)
	}
}

func (w *yw) resourcesBlock(indent int, key string, rs map[string]Resource) {
	if len(rs) == 0 {
		return
	}
	w.key(indent, key)
	for _, k := range sortedKeys(rs) {
		w.key(indent+1, k)
		r := rs[k]
		w.boolField(indent+2, r.Enabled)
		switch {
		case r.Ref != "":
			w.scalar(indent+2, "ref", r.Ref)
		case r.Source != nil:
			w.key(indent+2, "source")
			w.sourceBlock(indent+3, r.Source)
		}
	}
}

func (w *yw) mcpsBlock(indent int, key string, mm map[string]MCP) {
	if len(mm) == 0 {
		return
	}
	w.key(indent, key)
	for _, k := range sortedKeys(mm) {
		w.key(indent+1, k)
		m := mm[k]
		w.boolField(indent+2, m.Enabled)
		w.key(indent+2, "transport")
		w.transportBlock(indent+3, m.Transport)
	}
}

func (w *yw) transportBlock(indent int, t Transport) {
	w.scalar(indent, "type", t.Type)
	switch t.Type {
	case "http":
		w.scalar(indent, "url", t.URL)
		if len(t.Headers) > 0 {
			w.headersBlock(indent, "headers", t.Headers)
		}
	case "stdio":
		w.scalar(indent, "command", t.Command)
		if len(t.Args) > 0 {
			w.list(indent, "args", t.Args)
		}
	}
}

func (w *yw) artifactsBlock(indent int, key string, as map[string]Artifact) {
	if len(as) == 0 {
		return
	}
	w.key(indent, key)
	for _, k := range sortedKeys(as) {
		w.key(indent+1, k)
		a := as[k]
		w.boolField(indent+2, a.Enabled)
		if a.Source != nil {
			w.key(indent+2, "source")
			w.sourceBlock(indent+3, a.Source)
		}
		if a.Destination != "" {
			w.scalar(indent+2, "destination", a.Destination)
		}
	}
}

func (w *yw) marketplacesBlock(indent int, key string, ms map[string]Marketplace) {
	if len(ms) == 0 {
		return
	}
	w.key(indent, key)
	for _, k := range sortedKeys(ms) {
		w.key(indent+1, k)
		m := ms[k]
		w.boolField(indent+2, m.Enabled)
		w.scalar(indent+2, "type", m.Type)
		if m.Source != nil {
			w.key(indent+2, "source")
			w.sourceBlock(indent+3, m.Source)
		}
	}
}

func (w *yw) inputsBlock(indent int, in Inputs) {
	if len(in.Variables) == 0 && len(in.Secrets) == 0 {
		return
	}
	w.key(indent, "inputs")
	if len(in.Variables) > 0 {
		w.bindingsBlock(indent+1, "variables", in.Variables)
	}
	if len(in.Secrets) > 0 {
		w.bindingsBlock(indent+1, "secrets", in.Secrets)
	}
}

func (w *yw) bindingsBlock(indent int, key string, bs map[string]Binding) {
	w.key(indent, key)
	for _, k := range sortedKeys(bs) {
		w.key(indent+1, k)
		b := bs[k]
		// b.Env/b.File name WHERE a value comes from, never the value itself —
		// a Profile has no field that could hold a resolved secret.
		if b.Env != "" {
			w.scalar(indent+2, "env", b.Env)
			continue
		}
		w.scalar(indent+2, "file", b.File)
	}
}

func (w *yw) runtimeBlock(indent int, r Runtime) {
	w.resourcesBlock(indent, "skills", r.Skills)
	w.mcpsBlock(indent, "mcps", r.MCPs)
	w.artifactsBlock(indent, "artifacts", r.Artifacts)
	w.marketplacesBlock(indent, "marketplaces", r.Marketplaces)
	if r.Model != nil {
		w.key(indent, "model")
		w.modelBlock(indent+1, r.Model)
	}
	if r.Prompt != nil {
		w.key(indent, "prompt")
		w.promptBlock(indent+1, r.Prompt)
	}
	w.inputsBlock(indent, r.Inputs)
	if len(r.Plugins) > 0 {
		w.key(indent, "plugins")
		for _, k := range sortedKeys(r.Plugins) {
			w.key(indent+1, k)
			w.node(indent+2, r.Plugins[k])
		}
	}
	if len(r.Environment) > 0 {
		w.stringMap(indent, "environment", r.Environment)
	}
	if len(r.Variants) > 0 {
		w.key(indent, "variants")
		for _, k := range sortedKeys(r.Variants) {
			w.list(indent+1, k, r.Variants[k])
		}
	}
}

// node walks a raw *Node — used only for runtimes.*.plugins, which §24 keeps
// adapter-owned and unvalidated. It has no schema to key off, so a mapping's
// keys are sorted rather than trusted for determinism, same as everywhere
// else in Render.
func (w *yw) node(indent int, n *Node) {
	if n == nil {
		return
	}
	switch n.Kind {
	case Mapping:
		keys := append([]string(nil), n.Keys...)
		sort.Strings(keys)
		for _, k := range keys {
			c := n.Map[k]
			if c.Kind == Mapping || c.Kind == Sequence {
				w.key(indent, k)
				w.node(indent+1, c)
				continue
			}
			w.scalar(indent, k, scalarText(c))
		}
	case Sequence:
		for _, e := range n.Seq {
			w.writeln(indent, "- "+quoteIfNeeded(scalarText(e)))
		}
	default:
		w.writeln(indent, quoteIfNeeded(scalarText(n)))
	}
}

func scalarText(n *Node) string {
	if n.Kind == Null {
		return "null"
	}
	return n.Str
}

// quoteIfNeeded quotes a value that would otherwise be misread on a later
// parse — empty, a boolean/null keyword, or something that looks like a
// number — using this subset's own two escapes (\\ and \"), never Go's.
func quoteIfNeeded(s string) string {
	if s != "" && s != "true" && s != "false" && s != "null" && s != "~" && !jsonNumber.MatchString(s) {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' || s[i] == '"' {
			b.WriteByte('\\')
		}
		b.WriteByte(s[i])
	}
	b.WriteByte('"')
	return b.String()
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
