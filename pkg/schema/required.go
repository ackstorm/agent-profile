package schema

import (
	"fmt"
	"sort"
)

// Ref is one reference from a resource to an input, kept with the resource
// that made it so an error can name both. §12 requires exactly that.
type Ref struct {
	Resource string // e.g. `skills.company-review`, or `model`/`prompt`
	Kind     string // "secret" or "variable"
	Name     string // the input name, e.g. "gitlab-token"
	Line     int
}

// Required walks the ACTIVE resources of an effective profile and returns
// every input they reference, in deterministic order. A disabled resource
// contributes nothing — that is the whole point of §12: run this only on a
// Profile that already went through Effective, so `Enabled` reflects the
// runtime overlay.
func Required(p Profile) []Ref {
	var refs []Ref

	for _, name := range sortedKeys(p.Skills) {
		r := p.Skills[name]
		if !r.Enabled {
			continue
		}
		refs = append(refs, gitSourceRefs(fmt.Sprintf("skills.%s", name), r.Source)...)
	}

	for _, name := range sortedKeys(p.MCPs) {
		m := p.MCPs[name]
		if !m.Enabled {
			continue
		}
		resource := fmt.Sprintf("mcps.%s", name)
		for _, h := range sortedKeys(m.Transport.Headers) {
			refs = append(refs, headerRefs(resource, m.Transport.Headers[h])...)
		}
	}

	for _, name := range sortedKeys(p.Artifacts) {
		a := p.Artifacts[name]
		if !a.Enabled {
			continue
		}
		refs = append(refs, gitSourceRefs(fmt.Sprintf("artifacts.%s", name), a.Source)...)
	}

	for _, name := range sortedKeys(p.Marketplaces) {
		mk := p.Marketplaces[name]
		if !mk.Enabled {
			continue
		}
		refs = append(refs, gitSourceRefs(fmt.Sprintf("marketplaces.%s", name), mk.Source)...)
	}

	if p.Model != nil {
		if p.Model.Auth != nil {
			refs = append(refs, refFromValueFrom("model", p.Model.Auth.ValueFrom))
		}
		for _, h := range sortedKeys(p.Model.Headers) {
			refs = append(refs, headerRefs("model", p.Model.Headers[h])...)
		}
	}

	if p.Prompt != nil {
		refs = append(refs, gitSourceRefs("prompt", p.Prompt.Source)...)
	}

	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Resource != refs[j].Resource {
			return refs[i].Resource < refs[j].Resource
		}
		if refs[i].Kind != refs[j].Kind {
			return refs[i].Kind < refs[j].Kind
		}
		return refs[i].Name < refs[j].Name
	})
	return refs
}

// gitSourceRefs collects the one reference a git source's auth can carry.
// A local source, or a git source with no auth, contributes nothing.
func gitSourceRefs(resource string, src *Source) []Ref {
	if src == nil || src.Git == nil || src.Git.Auth == nil {
		return nil
	}
	return []Ref{refFromValueFrom(resource, src.Git.Auth.ValueFrom)}
}

// headerRefs collects the one reference a sourced header carries. A literal
// value has nothing to resolve.
func headerRefs(resource string, h HeaderValue) []Ref {
	if h.ValueFrom == nil {
		return nil
	}
	return []Ref{refFromValueFrom(resource, *h.ValueFrom)}
}

func refFromValueFrom(resource string, vf ValueFrom) Ref {
	if vf.Secret != "" {
		return Ref{Resource: resource, Kind: "secret", Name: vf.Secret}
	}
	return Ref{Resource: resource, Kind: "variable", Name: vf.Variable}
}
