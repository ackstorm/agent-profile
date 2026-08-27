package source

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/ackstorm/agent-profile/pkg/schema"
)

// ErrSecretUnset is what a Secret function returns when a declared binding has
// no value. Resolve wraps it with the resource that referenced it, because the
// referrer is what turns a ten-minute hunt into a two-minute fix — and §12's
// own worked example omits it.
var ErrSecretUnset = errors.New("secret is not set")

// ErrForeignEntry is a marketplace entry naming a source outside the
// marketplace's own repository.
//
// v1 scopes catalogues to same-repo entries, which is how the real ones are
// built: one clone, N plugins. An external entry is refused by NAME rather than
// fetched, and refusing it is what removed §21.2 from the specification — with
// no cross-repo hop there is no foreign host for a credential to reach, so the
// guard has nothing to guard. Reintroduction trigger: a real catalogue needing
// cross-repo entries.
type ErrForeignEntry struct {
	Marketplace, Entry, EntryURL string
}

func (e ErrForeignEntry) Error() string {
	return fmt.Sprintf(
		"marketplace %q: entry %q names a source outside the marketplace's own repository (%s); "+
			"v1 catalogues are same-repo only",
		e.Marketplace, e.Entry, e.EntryURL)
}

// Opts is everything Resolve needs from its caller. ManifestDir is a parameter
// like every other root in pkg/: nothing here reads $HOME.
type Opts struct {
	Cache       *Cache
	ManifestDir string
	// Secret resolves a binding NAME to a value, transiently (§34). nil means
	// no secret can be resolved, which is correct for a profile that
	// references none and an error for one that does.
	Secret func(name string) (string, error)
}

// Report is §17.1's mandatory disclosure and §21.2's, carried out of a
// successful resolution rather than only out of a failure. An inference that
// is invisible when it works is undebuggable when it stops working.
type Report struct{ Resource, Text string }

// Resolve turns every ACTIVE locator in an effective profile into bytes.
//
// Walk order is sorted by resource name so two runs produce the same reports in
// the same order: a report nobody can diff is a report nobody reads.
//
// A disabled resource is skipped entirely, and its secret is never read — §12
// computes requirements from active resources, and reading a disabled
// resource's token would make it required in practice while the spec says it
// is not.
func Resolve(ctx context.Context, p schema.Profile, o Opts) (map[string]Resolved, []Report, error) {
	out := map[string]Resolved{}
	var reports []Report

	for _, it := range locators(p) {
		res, err := resolveOne(ctx, it, o, p, out)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", it.name, err)
		}
		out[it.name] = res
		if res.ResolvedRef != "" || res.SchemeInferred {
			reports = append(reports, Report{
				Resource: it.name,
				Text:     resolutionText(res),
			})
		}
	}
	return out, reports, nil
}

func resolutionText(r Resolved) string {
	text := r.ResolvedRef
	if r.Anonymous {
		return text + " (anonymous)"
	}
	return text + " auth " + r.SchemeUsed.Report(r.SchemeInferred)
}

// item is one active locator with the name diagnostics will use.
type item struct {
	name   string
	source *schema.Source
	ref    string
}

// locators collects every active locator in a stable order.
//
// A ref-backed resource is collected too, and resolveOne reports it as
// deferred rather than skipping it. Marketplace item resolution is Phase 6
// (§21.1), and a resource that silently produced nothing here would be §8's
// silent drop in the one output telling a user what apply will do.
func locators(p schema.Profile) []item {
	var items []item
	add := func(kind string, names []string, get func(string) (*schema.Source, string, bool)) {
		sort.Strings(names)
		for _, n := range names {
			src, ref, enabled := get(n)
			if !enabled || (src == nil && ref == "") {
				continue
			}
			items = append(items, item{name: kind + " " + n, source: src, ref: ref})
		}
	}
	// Marketplaces first: Phase 6 resolves items against a catalogue, and a
	// catalogue that cannot be fetched should fail before the resources
	// naming it do.
	add("marketplace", keysOf(p.Marketplaces), func(n string) (*schema.Source, string, bool) {
		m := p.Marketplaces[n]
		return m.Source, "", m.Enabled
	})
	add("skill", keysOf(p.Skills), func(n string) (*schema.Source, string, bool) {
		r := p.Skills[n]
		return r.Source, r.Ref, r.Enabled
	})
	add("plugin", keysOf(p.Plugins), func(n string) (*schema.Source, string, bool) {
		r := p.Plugins[n]
		return r.Source, r.Ref, r.Enabled
	})
	add("artifact", keysOf(p.Artifacts), func(n string) (*schema.Source, string, bool) {
		a := p.Artifacts[n]
		return a.Source, "", a.Enabled
	})
	if p.Prompt != nil && p.Prompt.Source != nil {
		items = append(items, item{name: "prompt", source: p.Prompt.Source})
	}
	return items
}

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func resolveOne(ctx context.Context, it item, o Opts, p schema.Profile, done map[string]Resolved) (Resolved, error) {
	switch {
	case it.source == nil:
		return resolveRef(it, p, done)
	case it.source.Local != nil:
		return ResolveLocal(o.ManifestDir, *it.source.Local)
	case it.source.Git != nil:
		g := it.source.Git
		token, scheme, err := credential(g.Auth, o)
		if err != nil {
			return Resolved{}, err
		}
		return FetchGit(ctx, o.Cache, GitSpec{
			URL: g.URL, Ref: g.Ref, Subpath: g.Subpath,
			Token: token, DeclaredScheme: scheme,
		})
	case it.source.Archive != nil:
		a := it.source.Archive
		token, scheme, err := credential(a.Auth, o)
		if err != nil {
			return Resolved{}, err
		}
		return FetchArchive(ctx, o.Cache, ArchiveSpec{
			URL: a.URL, Digest: a.Digest, Subpath: a.Subpath,
			Token: token, DeclaredScheme: scheme,
		})
	}
	return Resolved{}, errors.New("no source branch selected")
}

// resolveRef turns `<item>@<marketplace>` into a directory inside the
// marketplace's already-fetched tree.
//
// Marketplaces are walked FIRST (see locators), so the catalogue is in `done`
// by the time any resource referencing it is reached. That ordering is not a
// convenience: a catalogue that cannot be fetched should fail before the
// resources naming it do, or the user reads three errors about missing items
// when the real answer is one unreachable repository.
//
// Nothing is fetched here. A catalogue's entries live in the catalogue's own
// repository (§21.1), so resolving one is a path join — which is exactly why
// there is no second hop and §21.2 could be removed rather than satisfied.
func resolveRef(it item, p schema.Profile, done map[string]Resolved) (Resolved, error) {
	itemName, marketplace, err := ParseRef(it.ref)
	if err != nil {
		return Resolved{}, err
	}
	m, ok := p.Marketplaces[marketplace]
	if !ok {
		return Resolved{}, fmt.Errorf("references marketplace %q, which this manifest does not declare", marketplace)
	}
	if !m.Enabled {
		return Resolved{}, fmt.Errorf("references marketplace %q, which is disabled", marketplace)
	}
	// §22: a ref must target a catalogue whose type matches the referencing
	// family. Without this a skill could be drawn from a plugin catalogue and
	// fail its SKILL.md contract with an error naming the wrong thing.
	if want := familyType(it.name); want != "" && m.Type != want {
		return Resolved{}, fmt.Errorf("references marketplace %q, whose type is %q, not %q",
			marketplace, m.Type, want)
	}
	fetched, ok := done["marketplace "+marketplace]
	if !ok {
		return Resolved{}, fmt.Errorf("marketplace %q was not resolved", marketplace)
	}
	dir, err := ResolveItem(marketplace, m.Type, fetched.Dir, itemName)
	if err != nil {
		return Resolved{}, err
	}
	// The receipt is the CATALOGUE's resolved ref: the item is a slice of that
	// tree, so that SHA is what a later drift report has to compare.
	return Resolved{Dir: dir, ResolvedRef: fetched.ResolvedRef, Anonymous: fetched.Anonymous}, nil
}

// familyType maps a resource family to the catalogue type it may reference.
func familyType(name string) string {
	switch {
	case strings.HasPrefix(name, "skill "):
		return "skills"
	case strings.HasPrefix(name, "plugin "):
		return "plugins"
	}
	return ""
}

// credential reads a locator's secret, transiently. It is called only for an
// ACTIVE resource, which is what keeps §12's rule true in practice and not
// only on paper.
func credential(auth *schema.GitAuth, o Opts) (token, scheme string, err error) {
	if auth == nil {
		return "", "", nil
	}
	name := auth.ValueFrom.Secret
	if name == "" {
		return "", "", fmt.Errorf("auth references no secret")
	}
	if o.Secret == nil {
		return "", "", fmt.Errorf("references secret %q, but no secret resolver was supplied", name)
	}
	v, err := o.Secret(name)
	if err != nil {
		return "", "", fmt.Errorf("references secret %q: %w", name, err)
	}
	return v, auth.Scheme, nil
}
