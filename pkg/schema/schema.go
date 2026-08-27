package schema

import "slices"

// Schema marks the points where composition stops being an ordinary mapping
// merge. §3.5 requires exactly this: the marking lives here, and compose.go
// asks. Nothing in compose.go may name a resource type.
type Schema struct {
	// unions are branch-keyed union NODES: the value under this path selects
	// exactly one branch, and an overlay choosing a different branch replaces
	// the whole node.
	unions [][]string
	// groups are exclusive groups, keyed by the CONTAINER path. An overlay
	// declaring any member discards every other inherited member.
	groups []group
}

type group struct {
	at      []string
	members []string
}

// V1Schema is §3.5's three union points and no others. Adding a row is a spec
// change, not an implementation detail.
func V1Schema() Schema {
	return Schema{
		unions: [][]string{
			{"skills", "*", "source"},
			{"artifacts", "*", "source"},
			{"marketplaces", "*", "source"},
			{"prompt", "source"},
			{"runtimes", "*", "skills", "*", "source"},
			{"runtimes", "*", "artifacts", "*", "source"},
			{"runtimes", "*", "marketplaces", "*", "source"},
			{"runtimes", "*", "plugins", "*", "source"},
		},
		// mcps.*.transport is deliberately NOT a marked union, even though it too
		// picks one of two shapes (http vs stdio). Every union above is
		// BRANCH-keyed: the overlay selects a shape by which nested key is
		// present (source: {git: {...}} vs source: {local: {...}}), so an
		// ordinary mapping merge would blend git.url with local.path into a
		// value that names nothing. transport is shaped differently: `type:
		// http | stdio` plus type-conditional keys sit directly alongside it in
		// the same flat mapping — url/headers for http, command/args for
		// stdio — not nested under a branch key. decodeTransport enforces the
		// type/key pairing at decode time, so there is nothing for the merge
		// engine to disambiguate: an ordinary mapping merge is the WANTED
		// behavior here, letting an overlay that supplies only headers compose
		// onto an inherited url instead of discarding the whole node the
		// moment a single key is overlaid.
		groups: []group{
			// Members sorted, so Group's result is stable and comparable.
			{at: []string{"skills", "*"}, members: []string{"ref", "source"}},
			{at: []string{"runtimes", "*", "skills", "*"}, members: []string{"ref", "source"}},
			{at: []string{"prompt"}, members: []string{"content", "source"}},
			// runtimes.*.plugins.* deliberately has no exclusive group here.
			// `package:` (opencode) is a third locator form and §24 makes the
			// plugin schema adapter-owned, so Phase 6 decides its group. A
			// group added now would refuse `package` before anything can
			// materialize it.
		},
	}
}

func (s Schema) IsUnion(path []string) bool {
	for _, p := range s.unions {
		if matchPath(p, path) {
			return true
		}
	}
	return false
}

// Group returns the exclusive-group members at this container path, or nil.
func (s Schema) Group(path []string) []string {
	for _, g := range s.groups {
		if matchPath(g.at, path) {
			return slices.Clone(g.members)
		}
	}
	return nil
}

// matchPath compares segment by segment; "*" in the pattern matches exactly one
// segment. There is no "**": every marked point in v1 is at a known depth, and
// a wildcard that swallowed depth would mark paths nobody wrote down.
func matchPath(pattern, path []string) bool {
	if len(pattern) != len(path) {
		return false
	}
	for i, seg := range pattern {
		if seg != "*" && seg != path[i] {
			return false
		}
	}
	return true
}

// collections are the named-resource collections: their ENTRIES are the things
// `null` may not reset. §3.7.
var collections = [][]string{
	{"skills", "*"}, {"mcps", "*"}, {"artifacts", "*"}, {"marketplaces", "*"},
	{"runtimes", "*", "skills", "*"}, {"runtimes", "*", "mcps", "*"},
	{"runtimes", "*", "artifacts", "*"}, {"runtimes", "*", "marketplaces", "*"},
	{"runtimes", "*", "plugins", "*"}, {"runtimes", "*", "variants", "*"},
}

// IsCollectionEntry reports whether path names an entry of a named resource
// collection.
func (s Schema) IsCollectionEntry(path []string) bool {
	for _, p := range collections {
		if matchPath(p, path) {
			return true
		}
	}
	return false
}
