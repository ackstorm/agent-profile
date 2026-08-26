# Phase 2 — Inputs, Secrets, Preflight and `--dry-run` Implementation Plan

> **For agentic workers:** execute with `superpowers:subagent-driven-development`.
> Two batches, one implementer and one review each.

**Goal:** Complete the resolution phase — compute which inputs an effective profile
actually needs, resolve them, run derived preflight, and expose it all through
`ap apply --dry-run`, which touches nothing.

**Architecture:** Phase 1 ends at `schema.Effective(path, runtime) (Profile, []Warning, error)`.
Phase 2 adds a `Resolution` layer on top: required-input discovery walks only the
*active* effective resources, binding resolution reads `env`/`file`, derived
preflight checks the runtime CLI and every active stdio MCP command. Nothing
mutates. `ap schema` and `ap apply --manifest -` exist so `ach` (Go) and
`ach-agent` (Python) can drive this without reimplementing the rules.

**Tech Stack:** Go 1.25, standard library only. `pkg/**` no build tag, must build
for windows; `cmd/ap/**` keeps `//go:build unix`.

## Global Constraints

- **Standard library only.** `go.mod` gains no `require` line.
- **`pkg/**` carries no build tag and must compile for `windows/amd64`;
  `internal/**` and `cmd/ap/**` keep `//go:build unix`.
- **`pkg/` must never import `internal/`.** `TestPkgNeverImportsInternal` enforces it.
- **`pkg/` is a published API** imported by `ackstorm/ach`. Export only what a
  consumer needs.
- **`pkg/` never reads `$HOME` implicitly.** Every root arrives as a parameter.
- **Secret VALUES never reach a manifest, a lockfile, a diagnostic, a log, an
  error message, or rendered output.** Binding names are structure and may appear;
  values may not.
- **Guards get mutation-tested.** Neuter, run the test, confirm red, restore,
  record both transcripts. Two tests in the Phase 1 plan were vacuous and were
  only caught this way.
- **`run` parses no flags after its reference.** New commands with no passthrough
  use `parseAroundRef`.
- Commit scope `schema` for `pkg/` work, `ap` for `cmd/` work. Subject under 72
  chars. Body ends with:
  `Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>`
- Stage by explicit path; never `git add -A`. Nothing under `docs/superpowers/`
  or `docs/specs/`.

---

## File Structure

| File | Responsibility |
|---|---|
| `pkg/schema/inputs.go` | `Binding`, `Inputs` resolution; exactly one of `env`\|`file` |
| `pkg/schema/inputs_test.go` | binding validation, missing-binding errors, redaction |
| `pkg/schema/required.go` | walk active effective resources, collect referenced inputs |
| `pkg/schema/required_test.go` | the §12 disabled-resource case |
| `pkg/schema/preflight.go` | §28 derived preflight: runtime CLI, stdio MCP commands |
| `pkg/schema/preflight_test.go` | missing binary, missing MCP command |
| `pkg/schema/resolve.go` | `Resolve` — the whole resolution phase, one entry point |
| `pkg/schema/resolve_test.go` | ordering, fail-fast, strict mode |
| `pkg/schema/jsonschema.go` | emit the manifest's JSON Schema |
| `pkg/schema/jsonschema_test.go` | accepts §35, rejects each §36 violation |
| `cmd/ap/apply.go` | `ap apply --dry-run`, `--manifest -`, `--strict` |

---

### Batch E — required inputs, bindings, preflight

**Interfaces produced** (Batch F and every later phase consume these):

```go
// Ref is one reference from a resource to an input, kept with the resource that
// made it so an error can name both. §12 requires exactly that.
type Ref struct {
    Resource string // e.g. `skills.company-review`
    Kind     string // "secret" or "variable"
    Name     string // the input name, e.g. "gitlab-token"
    Line     int
}

// Required walks the ACTIVE resources of an effective profile and returns every
// input they reference, in deterministic order. A disabled resource contributes
// nothing — that is the whole point of §12.
func Required(p Profile) []Ref

// Resolved holds input values. It has no exported field and no String method:
// a secret must not be printable by accident.
type Resolved struct{ /* unexported */ }

// Value returns a resolved input, or false. Callers that materialize
// configuration must use the binding NAME instead — see Phase 5.
func (r *Resolved) Value(kind, name string) (string, bool)

// ResolveInputs binds every Ref against the profile's declared inputs.
// A referenced input with no declared binding is an error naming the resource
// and the input. Reading a file binding is allowed here; the value is held in
// memory and never written anywhere.
func ResolveInputs(p Profile, refs []Ref) (*Resolved, error)

// Preflight is §28: the runtime CLI must be executable and every active stdio
// MCP transport.command must resolve on PATH. Derived, never declared.
func Preflight(p Profile, runtime string) error
```

**Steps**

- [ ] **E1.** Write `required_test.go` first, using the spec's §12 example: a
  `company-review` skill whose git source references `secret: gitlab-token`, plus
  a `runtimes.opencode` overlay disabling it. Assert `Required` returns the ref
  for claude and **not** for opencode. Run it, watch it fail.
- [ ] **E2.** Implement `Required`. Walk `Skills`, `MCPs`, `Artifacts`,
  `Marketplaces`, `Model`, `Prompt`. Skip anything with `Enabled == false`.
  Collect from git `auth`, header `value_from`, and `model.auth.value_from`.
  Sort the result so two runs agree.
- [ ] **E3.** Write `inputs_test.go`: a binding with both `env` and `file` is an
  error; a binding with neither is an error; a referenced input with no binding
  is an error naming **both** the resource and the input; an unset `env` var is
  an error naming the variable.
- [ ] **E4.** Implement `ResolveInputs`. `Resolved` keeps values in an unexported
  map. **Do not add a `String()` or `Format()` method**, and add a test that
  `fmt.Sprintf("%v", resolved)` and `%+v` contain no secret value — this is the
  accident that leaks secrets into logs.
- [ ] **E5.** Write `preflight_test.go`: a profile whose runtime binary is absent
  fails naming it; an active stdio MCP whose `command` is absent fails naming the
  MCP and the command; a **disabled** MCP with an absent command does **not**
  fail. Use `exec.LookPath` against a temp `PATH` so the test is hermetic.
- [ ] **E6.** Implement `Preflight`.
- [ ] **E7. Mutation-test the redaction guard.** Add a `String()` method on
  `Resolved` that returns the values, run the redaction test, confirm it FAILS,
  remove it. Record both transcripts.
- [ ] **E8.** `make test`, `make crossbuild`, `make test-one T=TestPkgNeverImportsInternal P=./pkg/`. Commit per step group.

---

### Batch F — resolution phase, `--dry-run`, `ap schema`

**Interfaces produced:**

```go
// Resolution is everything the resolution phase produced. Phase 4 consumes it
// and materializes; --dry-run prints it and stops.
type Resolution struct {
    Profile  Profile
    Runtime  string
    Refs     []Ref
    Warnings []Warning
}

// Resolve runs §37 steps 1-11 in order and stops before any source is fetched.
// strict promotes every Warning to an error (§8).
func Resolve(path, runtime string, strict bool) (*Resolution, *Resolved, error)

// JSONSchema emits the manifest schema, so ach-agent can validate in Python
// without reimplementing these rules.
func JSONSchema() []byte
```

**Steps**

- [ ] **F1.** Write `resolve_test.go` asserting **order**: a profile with both an
  undeclared input and a missing MCP command reports the *input* error, because
  §37 puts input resolution at step 10 and preflight at step 11. This is the test
  that stops someone reordering the phase for convenience.
- [ ] **F2.** Implement `Resolve` by composing `Effective` → `Required` →
  `ResolveInputs` → `Preflight`. It must not fetch anything.
- [ ] **F3.** Test that `strict` turns a §7.2 locally-declared-non-target warning
  into an error, and that without it the same profile resolves with a warning.
- [ ] **F4.** Write `jsonschema_test.go`: the emitted schema accepts the §35 full
  example fixture already in `pkg/schema/testdata/`, and rejects one document per
  §36 rule (unknown key, `Authorization` in `model.headers`, prompt with both
  `content` and `source`, a binding with both `env` and `file`, a bad `mode`).
  Validate using the emitted schema itself — if writing a validator is more than
  a few lines, assert the schema's structure instead and say so in the report.
- [ ] **F5.** Implement `JSONSchema`. Hand-write the document as a Go literal
  marshalled with `encoding/json`; do not build a reflection-based generator.
  Keys sorted, output byte-stable across runs.
- [ ] **F6.** Implement `cmd/ap/apply.go`: `ap apply <path> --target <runtime>
  [--dry-run] [--strict] [--manifest -]`. `--dry-run` runs exactly `Resolve` and
  prints the resolution with secrets redacted, then exits 0. Without `--dry-run`,
  print `materialization is Phase 4` and exit 1 — do not stub a fake apply.
  `--manifest -` reads the manifest from stdin into a temp file inside the
  process's own temp dir, so `ach` can pipe one.
- [ ] **F7.** Test in `cmd/ap/main_test.go`: `apply` without `--target` errors
  naming the flag; `--dry-run` on the §35 fixture exits 0 and writes nothing —
  assert by snapshotting the profile directory before and after.
- [ ] **F8.** `ap schema` prints `JSONSchema()` and exits 0.
- [ ] **F9.** Full gate: `make verify`, `make crossbuild`, `timeout 900 make fuzz`.
- [ ] **F10.** Update `docs/references/DECLARATIVE.md` with the resolution phase
  order and why `--dry-run` is exactly it. Commit.

---

## Deliberately NOT in Phase 2

Source fetching, the cache and the lockfile (Phase 3 — **not being executed in
this run**); materialization of any kind (Phase 4); secret *references* in
materialized config and the `AP_SECRET_<NAME>` launcher export (Phase 5). Phase 2
resolves inputs into memory and stops.
