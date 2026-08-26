package schema

import (
	"fmt"
	"os"
	"strings"
)

// Resolved holds resolved input values. It has no exported field, and its
// String/GoString methods redact rather than expose it: a secret must not be
// printable by accident. Callers that need a value ask for it explicitly with
// Value; anything that formats or logs a Resolved gets the type name and
// nothing else.
type Resolved struct {
	values map[string]string // "<kind>:<name>" -> value
}

// Value returns a resolved input, or false. Callers that materialize
// configuration must use the binding NAME instead — see Phase 5.
func (r *Resolved) Value(kind, name string) (string, bool) {
	v, ok := r.values[kind+":"+name]
	return v, ok
}

// String redacts. An unexported field alone does not hide it: Go's fmt
// reaches into unexported struct fields by reflection with no Stringer in
// the way, so %v and %+v on a bare Resolved print every value in the values
// map. This method exists only to take that path away.
//
// The receiver is a VALUE, not a pointer: a pointer receiver is not in the
// value type's method set, so fmt silently falls back to reflection for a
// plain Resolved (as opposed to a *Resolved) — the exact case a value copy
// held by a cross-module caller produces. GoString covers %#v, which fmt
// never routes through Stringer at all.
func (r Resolved) String() string {
	return fmt.Sprintf("schema.Resolved{%d input(s), redacted}", len(r.values))
}

// GoString redacts %#v the same way String redacts %v/%+v/%s.
func (r Resolved) GoString() string { return r.String() }

// ResolveInputs binds every Ref against the profile's declared inputs. A
// referenced input with no declared binding is an error naming the
// referencing resource and the input, per §12. Reading a file binding is
// allowed here; the value is held in memory and never written anywhere.
func ResolveInputs(p Profile, refs []Ref) (*Resolved, error) {
	values := make(map[string]string, len(refs))
	for _, ref := range refs {
		bindings := p.Inputs.Variables
		if ref.Kind == "secret" {
			bindings = p.Inputs.Secrets
		}
		b, ok := bindings[ref.Name]
		if !ok {
			return nil, fmt.Errorf("%s references %s %q: no binding declared in inputs",
				resourceLabel(ref.Resource), ref.Kind, ref.Name)
		}
		v, err := resolveBinding(b, ref)
		if err != nil {
			return nil, err
		}
		values[ref.Kind+":"+ref.Name] = v
	}
	return &Resolved{values: values}, nil
}

// resolveBinding reads one binding's value. §13/§13's common rule: exactly
// one of env or file.
func resolveBinding(b Binding, ref Ref) (string, error) {
	switch {
	case b.Env != "" && b.File != "":
		return "", fmt.Errorf("%s %q: binding declares both env and file; use exactly one", ref.Kind, ref.Name)
	case b.Env == "" && b.File == "":
		return "", fmt.Errorf("%s %q: binding declares neither env nor file; use exactly one", ref.Kind, ref.Name)
	}
	if b.Env != "" {
		v, ok := os.LookupEnv(b.Env)
		if !ok {
			return "", fmt.Errorf("%s %q: environment variable %s is not set", ref.Kind, ref.Name, b.Env)
		}
		return v, rejectCRLF(v, ref)
	}
	data, err := os.ReadFile(b.File)
	if err != nil {
		return "", fmt.Errorf("%s %q: reading %s: %w", ref.Kind, ref.Name, b.File, err)
	}
	// A token file made with `echo T > f` ends in a newline that is not part
	// of the value — left in, it lands in a materialized header as
	// "Bearer T\n". Only the trailing run is trimmed; an embedded CR/LF is
	// caught below instead of silently stripped.
	v := strings.TrimRight(string(data), "\r\n")
	return v, rejectCRLF(v, ref)
}

// rejectCRLF stops a resolved value from carrying CR or LF outright, naming
// the input. A resolved value is materialized straight into places like a
// header value (§13); an embedded CR/LF there is header injection, and this
// is the layer that reads the raw bytes, so it is where that gets stopped.
func rejectCRLF(v string, ref Ref) error {
	if strings.ContainsAny(v, "\r\n") {
		return fmt.Errorf("%s %q: resolved value contains a CR or LF", ref.Kind, ref.Name)
	}
	return nil
}

// resourceLabel turns a Ref.Resource (e.g. "skills.company-review", or the
// bare "model"/"prompt") into the singular, human form an error names it
// with: `skill "company-review"`.
func resourceLabel(resource string) string {
	kind, name, found := strings.Cut(resource, ".")
	if !found {
		return resource
	}
	if singular, ok := resourceKindLabels[kind]; ok {
		kind = singular
	}
	return fmt.Sprintf("%s %q", kind, name)
}

var resourceKindLabels = map[string]string{
	"skills":       "skill",
	"mcps":         "mcp",
	"artifacts":    "artifact",
	"marketplaces": "marketplace",
}
