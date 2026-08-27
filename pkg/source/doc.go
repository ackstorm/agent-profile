// Package source turns a locator from an effective profile into verified bytes
// in a content-addressed cache. It fetches; it does not materialize. Nothing
// here writes to a materialization root — that is Phase 4 — and nothing here
// pins: a ref re-resolves on every run, because v1 has no lockfile (SPEC §32).
//
// The package carries no build tag and must compile for windows: ach imports
// it and ach-cli ships windows.
//
// Two rules govern every credential that leaves this package, and they live
// together in transport.go because they are one question — may this credential
// go to this endpoint? — asked at two moments:
//
//   - §17.2: never over non-TLS, and never in a URL. A credential in the URL
//     position is visible in /proc/<pid>/cmdline to every process on the
//     machine, and git persists it in remote.origin.url.
//   - §21.2: never to an endpoint other than the one the credential was
//     declared for. The comparison is host and effective port.
//
// The cache is deliberately NOT locked (SPEC §37.3). Publish is atomic — fill a
// temporary directory, rename it into place — so a reader sees a complete entry
// or none. Two processes resolving one source waste a fetch and cannot corrupt
// anything, which is the only failure a cache lock would prevent.
package source
