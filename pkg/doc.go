// Package pkg is not a package — this file documents what lives beneath it.
//
// pkg/ is the PORTABLE hydrator: everything needed to turn a declared agent
// environment into native configuration for claude, codex, opencode and pi. It
// is imported by ackstorm/ach as a library, driven by ackstorm/ach-agent
// through the ap binary, and shipped inside ackstorm/ach-runtime as an init
// container. Those are three ways into one implementation, and the parity test
// in cmd/ap asserts the first two produce the same bytes.
//
// # What is here
//
//	pkg/schema     parse, compose, validate → a Profile. The CONTRACT.
//	pkg/source     resolve git, local and archive sources into a cache.
//	pkg/hydrate    lock, materialize, merge, record; and take it back out.
//	pkg/agentreg   the four runtimes: config directories, variables, modes.
//
// The launcher is deliberately NOT here. internal/profile and internal/run
// exist because ap runs an agent with a per-profile home; a consumer that
// hydrates a directory and never launches anything imports pkg/ and nothing
// else. That boundary is enforced by TestPkgNeverImportsInternal, because Go
// enforces it for ach and would report it in the wrong repository.
//
// # Portability
//
// pkg/** carries NO build tag and must compile for windows/amd64. internal/**
// and cmd/ap/** keep //go:build unix. `make crossbuild` builds and vets both
// windows and darwin, so the claim cannot rot while unshipped.
//
// # What "version 1" promises
//
// The manifest's `version: "1"` is a compatibility promise across four
// repositories, one of which is Python and reads no Go types at all. What it
// covers is the FORMAT: a manifest that composes today composes tomorrow, and
// a field's meaning does not change under it. Breaking it is a coordinated
// release, not a commit.
//
// The Go API is versioned by the module, and the two are not the same promise.
// Adding an exported symbol is cheap. Renaming or removing one is a change in
// four repositories, so it is a deliberate act with a migration, not a tidy-up.
//
// The ledger has its own version, bumped only for a change no older reader
// could survive. Adding an omitempty field is not one: encoding/json ignores
// what it does not know.
package pkg
