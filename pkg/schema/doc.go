// Package schema parses, composes and resolves the declarative ap manifest:
// a restricted YAML subset (yaml.go) into typed nodes, a schema-driven merge
// for extends (compose.go, extends.go), a typed Profile decode and effective-
// profile computation for one runtime (profile.go, effective.go), input
// resolution and preflight (inputs.go, preflight.go), and a deterministic,
// secret-redacting renderer back to that same subset (render.go). Resolve
// (resolve.go) is the package's top-level entry point: it runs SPEC §37
// steps 1–11 in order and returns the effective Profile, the Refs it needs,
// and a Resolved holding what got read for them.
//
// The YAML subset is parsed here rather than decoded by a library because
// this repository takes no dependencies and the standard library has no YAML
// decoder — the same call internal/profile/settings.go already made for
// TOML. The rule that makes a subset safe: it REJECTS everything it does not
// understand — flow collections, anchors, aliases, tags, merge keys, tabs in
// indentation, multiple documents, and anything on a block scalar beyond the
// bare `|`/`|-` forms — naming the construct and the line. A manifest comes
// from a repository somebody else wrote, and silently misreading one — an
// anchor read as a scalar, a flow collection read as a string — is worse
// than refusing it.
//
// This package is a published API: it is imported by another Go module
// (ackstorm/ach), not just by this repository. Its exported surface has
// grown well past parsing alone — the manifest's typed shape (Profile and
// its resource types), the composition and resolution entry points
// (Merge, Effective, Resolve, ResolveInputs, Required, Preflight, Render,
// JSONSchema), and the YAML value model they are all built on (Kind, Node,
// ParseYAML) — and changing any of it is changing someone else's build.
//
// docs/references/DECLARATIVE.md, in this repository, records this
// package's deviations from the spec and the reasoning behind them; that
// file is invisible from the other side of the module boundary, so read it
// here rather than assume the spec's prose is what shipped.
package schema
