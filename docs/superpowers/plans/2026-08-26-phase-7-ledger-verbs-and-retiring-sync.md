# Phase 7 — the state surface: ledger verbs, imperative install, and `ap sync` dies

**Spec of record:** `docs/specs/agent-profile-declarative-spec-v0.6.3.md`
(§6, §13.1, §33.1, §33.3, §34, §35.1, §35.2, §38).
**Roadmap:** `2026-08-26-declarative-v1-roadmap.md` § "Phase 7".
**Command surface:** `2026-08-26-command-surface.md` §§ 3, 7.

Phases 1–6 built the ledger. Nothing has read it yet except apply, which writes
it. This phase spends it: three verbs that exist only because the ledger does,
one verb that makes the manifest optional, and the deletion of the command this
one replaces.

---

## 0. What Phase 7 is actually deciding

Four things are NOT free consequences of the phases before it. Each is settled
here, with the reason, so a later reader does not re-derive it.

### 0.1 The hash gate does not apply to a merged record — it cannot

Phase 4's rule is "a file whose hash no longer matches was edited by the user,
so it is left alone". That rule is correct for a whole-file record and is
**already always false** for a merged one:

```
applyMCPs merges server "a" into config.toml → records hash H1 for "a"
applyMCPs merges server "b" into config.toml → records hash H2 for "b"
                                                H1 is now stale, on disk
```

Two servers in one file is the ordinary case, not a corner. A hash gate on
merged records would therefore refuse every merged uninstall on any root that
holds more than one server — the ledger's headline feature, dead on arrival.

**Settled:** the hash gates a whole-file record. A merged record is bounded by
its recorded KEYS instead, which is exactly the bound §33.3 states ("its merged
keys are removed from the files that carry them") and exactly what `MergeInto`
returns keys for. `Merge: "composite"` is bounded by neither yet — nothing
writes it (Phase 6 warns instead), so it is classified `skip` naming itself,
never guessed at.

### 0.2 Export cannot reconstruct a declaration it never recorded

`ap manifest export` must "produce a manifest that validates and re-applies
cleanly" (§35.2). Walking today's ledger, three resources cannot be written back:

| Kind | What is missing | Why reversing the materialized file is wrong |
|---|---|---|
| `mcp` | the transport declaration | four native shapes, so four reverse mappings, each re-derived from a file the user may have edited |
| `model` | the model declaration | same, plus `auth` would have to be recovered from a reference |
| `artifact` | `destination` | `ArtifactDest` is many-to-one; the rel path does not name the destination that produced it |

**Settled:** the ledger records the DECLARATION alongside the files. Three new
`omitempty` fields on `ResourceRec` — `MCP`, `Model`, `Destination` — plus
`Secrets`, below. They are `schema` values, and a `schema.Profile` has no field
capable of holding a *resolved* secret (resolution lands in `schema.Resolved`,
which is a different type). §34's ban is therefore structural here, not a rule
someone has to remember; a test asserts it over a real ledger.

No version bump: `encoding/json` ignores unknown fields, so a v1 reader survives
them and `ledgerVersion` stays 1.

### 0.3 A binding needs a name before export can ask for one

§13.1: an imperative install supplies `--auth-secret-env VAR`, which is a binding
with **no logical name**, and §35.2 requires export to synthesise one *stably*.

Deriving the name at export time means deriving it from data spread across
records, and colliding names (two different files, one base name) would have to
be resolved by a rewrite pass over already-emitted sources.

**Settled: the name is derived at INSTALL time, against the ledger, and
recorded.** `ResourceRec.Secrets map[string]schema.Binding` holds it; the
resource's `ValueFrom.Secret` already points at it. Export then just unions the
maps. Derivation: `GITLAB_TOKEN` → `gitlab-token`; a file → its base name,
kebab; an identical binding already in the ledger reuses its name; a collision
with a *different* binding takes the first free `-2`, `-3`. Deterministic given
the ledger, which is the only state there is.

Two manifests applied to one root that disagree about a name is the one case
export cannot fix. It is reported as an error naming both resources, never
silently unioned to whichever sorted last.

### 0.4 What imperative install does NOT take

- **`<item>@<marketplace>`** — §35.1 as amended by D4: v1 keeps no record of a
  marketplace definition, so a bare ref has nothing to resolve against. Refused
  by name, pointing at `ap manifest apply`.
- **`--manifest <path>` to select one resource out of a manifest** — not built.
  `ap manifest apply` already applies a manifest, and apply is additive, so
  "install one thing from this manifest" is a filter over an operation that
  exists. Trigger: someone wanting a subset of a manifest they cannot edit.
- **`mcp` and `model`** — they have no locator. Installing one imperatively means
  spelling a whole transport in flags, which is the manifest with worse syntax.
  Refused by name, pointing at `ap manifest apply`.

---

## 1. Tasks

Tests are batched per task group, per the standing instruction: one `go test`
per package at the end of a group, not per file.

### T1 — removal, one classifier `pkg/hydrate/remove.go`, `merge.go`, `ledger.go`

- `Verdict{Path, Op, Keys, Reason}`; `Op` ∈ `remove` | `keys` | `skip` | `gone`.
- `Remove(root, kind, name string, dryRun bool) (Removal, error)` — ONE entry
  point, taking the root lock, so §33.3's "the preview MUST be produced by the
  same classifier as the action" is structural: there is no second path to
  drift from.
- `MergeOut(path string, keys []string) error` — delete exactly these dotted
  keys from a JSON/TOML document. An unparseable file is an error, never
  overwritten (same refusal `MergeInto` already makes). An emptied container is
  LEFT — the user may have created it, and removing it is a claim the ledger
  cannot support.
- `Ledger.Delete(kind, name)`.
- Directory pruning after a `remove`: empty parents up to, and never including,
  the root.

### T2 — export `pkg/hydrate/export.go`, `apply.go`

- The four new `ResourceRec` fields, populated by `applyArtifacts`,
  `applyMCPs`, `applyModel` and every `applyX` that references a secret.
- `Export(root, name string, targets []string) (schema.Profile, error)`; the
  emitted `auth.scheme` is the RESOLVED one (§17.1), because a reader on a
  differently-named host would infer differently.
- Round trip test: apply → export → `schema.Effective` → apply into a second
  root → the two roots agree.

**Gate for T1+T2:** `go test ./pkg/hydrate/` once.

### T3 — `ap list <ref>`, `ap uninstall`

- `cmdList` gains a reference branch (a positional containing `:`), so it stays
  one command answering the same question one level deeper.
- `ap uninstall <ref> <kind> <name> [--dry-run]`.

### T4 — `ap install <ref> <kind> <name> [locator]`

Builds a one-resource `schema.Profile` in memory and runs Phase 4's pipeline
unchanged: same resolve, same apply, same ledger. Flags: `--git/--git-ref/--subpath`,
`--local`, `--url/--digest`, `--dest`, `--auth-secret-env/--auth-secret-file`.

### T5 — `ap manifest export <ref>`, and `ap sync` dies

Delete `cmd/ap/sync.go`, `cmd/ap/sync_test.go`, `internal/manifest/`, the
Makefile's fuzz target for it, the dispatch and help entries, and
`docs/specs/ap-sync-v1.md` — a spec for a deleted command is an invitation to
re-implement it. Migrate `examples/agent-profiles/` to declarative manifests.
Rewrite CLAUDE.md's `ap sync` section.

**Gate for T3+T4+T5:** `go test ./cmd/... ./internal/...` once, then the full
`verify`, `crossbuild`, `secrets`, `sandbox`, `smoke`, `fuzz`.

---

## 2. Exit criteria (roadmap's, verbatim)

- `ap install` on an empty profile installs with no manifest anywhere on disk
- uninstalling an MCP server from codex's `config.toml` leaves every other key intact
- a user-edited skill file is never removed
- `ap manifest export` of a hand-built root re-applies to a byte-identical result
- `make smoke` green

## 3. Mutation tests this phase owes

1. Remove the hash comparison in the classifier → the user-edited-file test goes red.
2. Record `Merge: "deep"` as a whole-file record → uninstalling one MCP server
   deletes the user's servers; its test goes red.
3. Remove the `<item>@<marketplace>` refusal in install → its test goes red.
4. Remove export's binding-conflict check → two disagreeing records union
   silently; its test goes red.

Each is reverted, confirmed RED, restored **from the scratch copy** — never with
`git checkout`, which restores from HEAD and silently reverts the feature too
(the defect that shipped `dac0f77` in Phase 6).
