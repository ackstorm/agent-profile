// Package schema parses a restricted YAML subset into typed nodes: strings,
// bare booleans, bare numbers, an explicit null, block mappings and block
// sequences. It is the value model declarative ap manifests are decoded from.
//
// The subset is parsed here rather than decoded by a library because this
// repository takes no dependencies and the standard library has no YAML
// decoder. The same call was already made for TOML in
// internal/profile/settings.go, which slices blocks without a parser.
//
// The rule that makes a subset safe: it REJECTS everything it does not
// understand — flow collections, anchors, aliases, tags, block scalars, merge
// keys, tabs in indentation, multiple documents — naming the construct and the
// line. A manifest comes from a repository somebody else wrote, and silently
// misreading one — an anchor read as a scalar, a flow collection read as a
// string — is worse than refusing it.
//
// This package is a published API: it is imported by another Go module
// (ackstorm/ach), not just by this repository. Its exported surface is
// deliberately small — Kind, Node, ParseYAML, and the three typed readers on
// *Node — and changing it is changing someone else's build.
package schema
