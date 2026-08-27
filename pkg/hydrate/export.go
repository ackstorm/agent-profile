package hydrate

import (
	"fmt"
	"net/url"
	"sort"

	"github.com/ackstorm/agent-profile/pkg/schema"
	"github.com/ackstorm/agent-profile/pkg/source"
)

// Export serializes a root's ledger back into a manifest (§35.2), closing the
// loop:
//
//	manifest → apply → ledger → export → manifest
//
// It is what makes "a manifest is an INPUT" workable in practice: a root
// assembled one `ap install` at a time becomes portable without ever having
// been written down.
//
// What it reproduces is the environment's SHAPE, not its bytes. Refs re-resolve
// on re-apply (§32), so a `main` that has moved brings back something newer —
// which is the same promise the original manifest made.
func Export(root, name string, targets []string) (schema.Profile, error) {
	ledger, err := LoadLedger(root)
	if err != nil {
		return schema.Profile{}, err
	}
	p := schema.Profile{Version: "1", Name: name, Targets: targets}

	// Sorted, so two exports of an unchanged root are identical. The ledger
	// stores resources in the order they were installed, which is history, not
	// meaning — emitting that order would make every export diff noise.
	recs := append([]ResourceRec(nil), ledger.Resources...)
	sort.Slice(recs, func(i, j int) bool {
		if recs[i].Kind != recs[j].Kind {
			return recs[i].Kind < recs[j].Kind
		}
		return recs[i].Name < recs[j].Name
	})

	secrets := map[string]schema.Binding{}
	owner := map[string]string{}
	for _, rec := range recs {
		if err := collectSecrets(rec, secrets, owner); err != nil {
			return schema.Profile{}, err
		}
		if err := emit(&p, rec, targets); err != nil {
			return schema.Profile{}, err
		}
	}
	if len(secrets) > 0 {
		p.Inputs.Secrets = secrets
	}
	return p, nil
}

// collectSecrets unions every record's bindings and refuses to guess when two
// disagree.
//
// The disagreement is real: two manifests applied to one root may both declare
// `gitlab-token` and bind it to different variables. Whichever sorted last would
// win silently, and the loser's source would authenticate with someone else's
// credential. There is no correct merge — only a report naming both.
func collectSecrets(rec ResourceRec, into map[string]schema.Binding, owner map[string]string) error {
	for _, n := range sortedKeys(rec.Secrets) {
		b := rec.Secrets[n]
		prev, seen := into[n]
		if seen && prev != b {
			return fmt.Errorf(
				"the ledger binds the secret %q two ways: %s (from %s) and %s (from %s %s); "+
					"export cannot choose, so re-install one of them with a different binding",
				n, describe(prev), owner[n], describe(b), rec.Kind, rec.Name)
		}
		into[n] = b
		if !seen {
			owner[n] = rec.Kind + " " + rec.Name
		}
	}
	return nil
}

// describe names a binding the way the flag that created it did, so the error
// reads back as something the user can act on.
func describe(b schema.Binding) string {
	if b.File != "" {
		return "file " + b.File
	}
	return "env " + b.Env
}

func emit(p *schema.Profile, rec ResourceRec, targets []string) error {
	switch rec.Kind {
	case "skill":
		set(&p.Skills, rec.Name, schema.Resource{Enabled: true, Ref: rec.Ref, Source: exportSource(rec.Source)})
	case "plugin":
		set(&p.Plugins, rec.Name, schema.Resource{Enabled: true, Ref: rec.Ref, Source: exportSource(rec.Source)})
	case "native-plugin":
		// Back under the runtime block it came from: a native plugin is not a
		// common one and round-tripping it to the root would change what the
		// manifest means. A root is materialized for exactly one runtime, so
		// targets — always that runtime here (§33.2) — names it.
		if len(targets) == 0 {
			return fmt.Errorf("native-plugin %q has no runtime to export under", rec.Name)
		}
		pkg, err := packageOf(rec)
		if err != nil {
			return err
		}
		setNativePlugin(&p.Runtimes, targets[0], rec.Name, schema.NativePlugin{Enabled: true, Package: pkg})
	case "artifact":
		set(&p.Artifacts, rec.Name, schema.Artifact{
			Enabled: true, Source: exportSource(rec.Source), Destination: rec.Destination,
		})
	case "mcp":
		if rec.MCP == nil {
			// Recorded before the declaration was kept. Reported, never
			// emitted as an empty server the reader would have to debug.
			return fmt.Errorf("mcp %q was recorded without its declaration and cannot be exported; re-apply the manifest that installed it", rec.Name)
		}
		m := *rec.MCP
		m.Enabled = true
		set(&p.MCPs, rec.Name, m)
	case "model":
		if rec.Model == nil {
			return fmt.Errorf("the model block was recorded without its declaration and cannot be exported; re-apply the manifest that installed it")
		}
		p.Model = rec.Model
	case "marketplace":
		// A marketplace materializes no file and is not recorded (§33.1). If
		// one ever is, saying so beats emitting a resource nothing can resolve.
		return fmt.Errorf("the ledger holds a marketplace record, which v1 does not write; the ledger was not produced by this version of ap")
	default:
		return fmt.Errorf("the ledger holds a resource of unknown kind %q", rec.Kind)
	}
	return nil
}

func set[V any](m *map[string]V, k string, v V) {
	if *m == nil {
		*m = map[string]V{}
	}
	(*m)[k] = v
}

// setNativePlugin writes one runtime's plugin, creating both maps as needed —
// Runtime is a struct, not a pointer, so the entry has to be read, mutated and
// written back rather than reached into directly.
func setNativePlugin(m *map[string]schema.Runtime, runtime, name string, np schema.NativePlugin) {
	if *m == nil {
		*m = map[string]schema.Runtime{}
	}
	rt := (*m)[runtime]
	set(&rt.Plugins, name, np)
	(*m)[runtime] = rt
}

// packageOf reads the package locator back out of a native-plugin record's
// single recorded key — the same key splitListKey parses when execute removes
// it — so there is one parser for that string and not two.
func packageOf(rec ResourceRec) (string, error) {
	if len(rec.Files) != 1 || len(rec.Files[0].Keys) != 1 {
		return "", fmt.Errorf("native-plugin %q was recorded with an unexpected shape and cannot be exported", rec.Name)
	}
	_, pkg, ok := splitListKey(rec.Files[0].Keys[0])
	if !ok {
		return "", fmt.Errorf("native-plugin %q's recorded key %q has no container.element form", rec.Name, rec.Files[0].Keys[0])
	}
	return pkg, nil
}

// exportSource emits the RESOLVED auth scheme (§35.2).
//
// The recorded scheme may be empty because §17.1 let it be inferred from the
// host. A reader on a differently-named host — the same repository behind a
// vanity domain, a mirror — would infer the other one and get a 401 whose cause
// is invisible. Writing down what this machine actually used costs one field
// and removes the whole class.
func exportSource(s *schema.Source) *schema.Source {
	if s == nil {
		return nil
	}
	out := *s
	if out.Git != nil {
		g := *out.Git
		g.Auth = resolvedAuth(g.Auth, g.URL)
		out.Git = &g
	}
	if out.Archive != nil {
		a := *out.Archive
		a.Auth = resolvedAuth(a.Auth, a.URL)
		out.Archive = &a
	}
	return &out
}

func resolvedAuth(a *schema.GitAuth, rawURL string) *schema.GitAuth {
	if a == nil || a.Scheme != "" {
		return a
	}
	out := *a
	host := ""
	if u, err := url.Parse(rawURL); err == nil {
		host = u.Hostname()
	}
	scheme, _ := source.ResolveScheme("", host)
	out.Scheme = string(scheme)
	return &out
}
