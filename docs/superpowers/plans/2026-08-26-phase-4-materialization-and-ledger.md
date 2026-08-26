# Phase 4 — Materialization and the Ledger

> **For agentic workers:** execute task by task. Steps use `- [ ]` for tracking.

**Goal:** Write a resolved profile into a root, record exactly what was written,
and make `ap manifest apply` real — additive, logged, and with the ledger as the
only state.

**Architecture:** One new package `pkg/hydrate`. It takes an effective profile,
the map `pkg/source.Resolve` returned, and a **root** — always a parameter — and
writes. An `Adapter` per runtime owns where each kind of thing lands; the ledger
records what landed. `pkg/schema` and `pkg/source` are consumed, never modified.

**Spec of record:** `docs/specs/agent-profile-declarative-spec-v0.6.2.md`

**Tech Stack:** Go 1.25, standard library only. `pkg/hydrate` carries **no build
tag** except the two lock halves, and must compile for windows.

## Global Constraints

See `CLAUDE.md`. The additions that are specific to `pkg/`:

- `pkg/**` carries no build tag (the lock's two halves excepted) and must compile
  for `windows/amd64`; `make crossbuild` is the gate.
- `pkg/` never imports `internal/`, never reads `$HOME`, and is a published API.
- Standard library only. Phase 4 adds no dependency — TOML is Phase 5.
- Guards get mutation-tested: revert, run, confirm red, restore. **A mutation
  that fails to COMPILE proves nothing** — keep the variable live and re-run.

## What Phase 4 does NOT do

No `--prune` (deferred past v1, SPEC §38). No project root (deferred, §38). No
MCP or model materialization and no secret references — Phase 5, and they need
TOML. No plugin routing or marketplace items — Phase 6. No `uninstall`, `list`
or `export` — Phase 7. **But the ledger must record enough for all three**, which
is the one thing here that cannot be retrofitted.

---

## File Structure

| File | Responsibility |
|---|---|
| `pkg/hydrate/doc.go` | What a root is, why the ledger is the only state |
| `pkg/hydrate/lock.go` | Root lock: acquire, release. ONE lock — the cache is unlocked |
| `pkg/hydrate/lock_unix.go` / `lock_windows.go` | `flock` / `LockFileEx` |
| `pkg/hydrate/ledger.go` | Two arms, atomic write, per root |
| `pkg/hydrate/ledger_test.go` | Round-trip, atomicity, no secret ever in it |
| `pkg/hydrate/adapter.go` | `Adapter` interface + the four registry-driven implementations |
| `pkg/hydrate/adapter_test.go` | Destination table per runtime, and the unsupported path |
| `pkg/hydrate/contract.go` | §23's `SKILL.md` check, run in the RESOLUTION phase |
| `pkg/hydrate/apply.go` | `Apply(ctx, Plan) (Result, error)` — copy, hash, record |
| `pkg/hydrate/apply_test.go` | Overwrite logged, hand-added file survives, ledger matches disk |

---

### Task 1: The root lock, and only the root lock

**Files:** Create `pkg/hydrate/doc.go`, `lock.go`, `lock_unix.go`,
`lock_windows.go`, `lock_test.go`

**Interfaces:** `LockRoot(root string) (release func() error, err error)`.

- [ ] **Step 1** Write `lock_test.go`: a second `LockRoot` on the same root while
  the first is held fails or blocks (assert with a timeout, never an unbounded
  wait); after release it succeeds; two DIFFERENT roots lock independently.
- [ ] **Step 2** Run it, watch it fail.
- [ ] **Step 3** Implement. Port `ach/internal/cli/lock`, **both halves**. The
  lock file lives inside the root at `.ap-lock`, created `0600`.

  **There is exactly one lock.** SPEC §37.3: the cache is unlocked because
  `source.Cache.Publish` is atomic, so two processes resolving one source waste a
  fetch and cannot corrupt. Do not port `ach`'s workspace lock. A second lock
  would buy that fetch back for the price of a lock-ordering rule and the
  deadlock the rule exists to exclude.
- [ ] **Step 4** Run it, watch it pass. `make crossbuild`.
- [ ] **Step 5** Commit: `feat(hydrate): one root lock, held across the write`

---

### Task 2: The ledger

**Files:** Create `pkg/hydrate/ledger.go`, `ledger_test.go`

**Interfaces:**
```go
type FileRec struct {
    RelPath string   `json:"relPath"`
    Hash    string   `json:"hash"`
    Merge   string   `json:"merge,omitempty"` // "deep"|"composite"; empty = replace
    Keys    []string `json:"keys,omitempty"`
}
type ResourceRec struct {
    Name, Kind, Ref string
    Source          *schema.Source
    ResolvedRef     string
    InstalledAt     string
    Files           []FileRec
}
type DefinitionRec struct {
    Name, Kind, ResolvedRef string
    Source                  *schema.Source
    AuthScheme              string
    AuthBinding             string // the binding NAME. NEVER a value.
}
type Ledger struct {
    Version    int
    Resources  []ResourceRec
    Definitions []DefinitionRec
}
func LoadLedger(root string) (*Ledger, error)
func (l *Ledger) Save(root string) error
```

- [ ] **Step 1** Write `ledger_test.go`:
  - an absent ledger loads as empty, not as an error — a root nothing has been
    applied to is the normal first case;
  - round-trip preserves both arms;
  - `Save` is atomic: a temp file plus rename, and a concurrent reader sees the
    old ledger or the new one, never a truncated one;
  - **no secret value can be stored**: `DefinitionRec` has no field for one, and
    a test asserts the marshalled JSON of a ledger built from §35's fixture
    contains no value from the environment.
- [ ] **Step 2** Run, watch fail.
- [ ] **Step 3** Implement. `<root>/.ap-ledger.json`, mode `0600`, written with
  the same temp-plus-rename shape `source.Cache.Publish` uses and for the same
  reason: a half-written ledger claims files that may not exist.

  **The second arm is not optional (F3).** A marketplace writes no file into the
  root, and a manifest is an INPUT that is not kept, so a file-only ledger loses
  the marketplace the moment the manifest is gone — and a later
  `ap install claude:plan skill xlsx@anthropic-skills` would have nothing to
  resolve the name against.

  **A binding's NAME is recorded, never its value** (§13, §34). `ach-cli` stores
  tokens in a `credentials.json` at `0600`; this deliberately does not, and the
  UX cost — the variable must be present on every run — is stated, not
  discovered.
- [ ] **Step 4** Run, watch pass.
- [ ] **Step 5** **Mutation-test the atomic write**: replace it with a plain
  `os.WriteFile`, confirm the concurrency test goes red, restore.
- [ ] **Step 6** Commit: `feat(hydrate): the ledger, two arms, per root`

---

### Task 3: The `SKILL.md` contract, checked before anything is written

**Files:** Create `pkg/hydrate/contract.go`, `contract_test.go`

**Interfaces:** `CheckContracts(p schema.Profile, fetched map[string]source.Resolved) error`

- [ ] **Step 1** Write the test: a resolved skill root with no `SKILL.md` fails,
  naming the skill AND the resolved path; one with it passes; an **artifact** is
  not subject to the check, because §26 is deliberately the opaque type.
- [ ] **Step 2** Run, watch fail.
- [ ] **Step 3** Implement. §23: the resolved skill root MUST contain `SKILL.md`.
- [ ] **Step 4** Wire it into `manifestApply` **after** `source.Resolve` and
  **before** materialization, so `--dry-run` reports it too. §37.1 step 14 puts
  contract validation in the resolution phase for exactly this reason: a
  contract violation found halfway through an overwriting apply leaves a root
  partly written.
- [ ] **Step 5** Test that `--dry-run` fails on a contract violation. That is the
  assertion that proves the check is in the resolution phase and not in the
  materialization one.
- [ ] **Step 6** Commit: `feat(hydrate): the SKILL.md contract, in the resolution phase`

---

### Task 4: The adapter, and the two roots

**Files:** Create `pkg/hydrate/adapter.go`, `adapter_test.go`

**Interfaces:**
```go
// Adapter says WHERE each kind of thing lands for one runtime. It says nothing
// about how: copying, hashing and recording are the same for every runtime.
type Adapter interface {
    Name() string
    SkillDir(name string) (string, bool)   // false = this runtime has no destination
    PromptFile() (string, bool)
    ArtifactDest(destination string) (string, error)
}
func AdapterFor(a agentreg.Agent) (Adapter, error)
```

- [ ] **Step 1** Write `adapter_test.go`: a table of the four runtimes' skill
  destinations, asserted against `pkg/agentreg`, not against a hand-written
  duplicate. A runtime with no destination for a kind returns `false`, and the
  caller's job is to WARN, never to skip silently (§8).
- [ ] **Step 2** Run, watch fail.
- [ ] **Step 3** Implement. The destinations come from `agentreg`, which is
  already the single copy of "what does this agent read". Do not add a second
  table.

  **Two roots, both a parameter** (§33.2): the agent's real config directory, or
  a named profile's namespace. They are ONE mechanism — point the agent's
  config-directory variable at a directory — which is why there are two and not
  three. Do not infer a root from the environment; `pkg/` cannot see one anyway.
- [ ] **Step 4** Run, watch pass.
- [ ] **Step 5** Commit: `feat(hydrate): per-runtime destinations from the registry`

---

### Task 5: Apply

**Files:** Create `pkg/hydrate/apply.go`, `apply_test.go`

**Interfaces:**
```go
type Plan struct {
    Root    string
    Adapter Adapter
    Profile schema.Profile
    Fetched map[string]source.Resolved
}
type Change struct{ Path, Op string } // "create" | "overwrite" | "skip"
type Result struct{ Changes []Change; Warnings []string }
func Apply(ctx context.Context, p Plan) (Result, error)
```

- [ ] **Step 1** Write `apply_test.go`, and these five assertions are the phase:
  - a skill's files land under the adapter's destination, with content intact;
  - a **hand-added file survives** an apply that overwrites its siblings — apply
    is additive (§33), and this is the assertion that proves it;
  - an overwrite is **logged** as `overwrite`, a new file as `create`. §33 says
    the adapter MUST log every file it overwrites; a silent overwrite is the one
    thing an additive policy cannot afford;
  - the ledger's recorded hash equals the hash of what is actually on disk —
    §33.1's honesty property, and the thing every later verdict rests on;
  - **the ledger is written LAST** (§37.2). A failure during materialization
    leaves the ledger untouched, because a ledger claiming files that were never
    written is worse than no ledger: Phase 7's verdicts would remove or skip on
    the strength of a record that was never true.
- [ ] **Step 2** Run, watch fail.
- [ ] **Step 3** Implement. Acquire the root lock, materialize, write the ledger,
  release. A disabled resource is not materialized (§4) — and if its ledger
  record still holds files, apply MUST warn, ledger-driven (v0.6.1's change to
  §4), because merged keys are invisible to the filesystem.
- [ ] **Step 4** Run, watch pass.
- [ ] **Step 5** **Mutation-test the ledger's position**: write it before
  materialization, confirm the crash test goes red, restore. Then mutation-test
  the overwrite log: drop the `overwrite` op, confirm red, restore.
- [ ] **Step 6** Commit: `feat(hydrate): apply into a root, ledger written last`

---

### Task 6: `ap manifest apply` for real

**Files:** Modify `cmd/ap/apply.go`, `cmd/ap/main.go`, `cmd/ap/main_test.go`

- [ ] **Step 1** Write the test: applying into a named profile writes the skill
  and the ledger; applying twice is idempotent and the second run reports
  `overwrite`; `--dry-run` still writes nothing.
- [ ] **Step 2** Run, watch fail.
- [ ] **Step 3** Implement. Resolve the reference to a root **without inferring
  one**. `claude:default` is the user's real configuration and cannot be undone
  by deleting a profile: display its **resolved absolute path**, name it as the
  real configuration, and gate it behind `--yes`. Off a terminal it MUST refuse —
  a pipe is not consent, checked with `stdinIsTerminal` and never with
  `answered()`, which is the rule `ap sync` already follows.
- [ ] **Step 4** Run, watch pass.
- [ ] **Step 5** Sandbox check: apply into a throwaway home with stub agents, and
  assert nothing outside the root is touched.
- [ ] **Step 6** Commit: `feat(ap): ap manifest apply materializes into a root`

---

### Task 7: Gates and docs

- [ ] **Step 1** `make verify`, `crossbuild`, `secrets`, `fuzz`, `sandbox`,
  `smoke`. A red gate is reported with its output, never worked around.
- [ ] **Step 2** Document in `docs/references/DECLARATIVE.md`: why the ledger is
  written last, why there is one lock and not two, why a hand-added file survives
  and a declared one does not, and what the second ledger arm exists for.
- [ ] **Step 3** Update `CLAUDE.md`'s MANDATORY reading table with
  `pkg/hydrate/*`.
- [ ] **Step 4** Mark this plan complete and write up what it did not predict.
- [ ] **Step 5** Commit.

## Self-Review

**Spec coverage:** §4's ledger-driven disabled warning (Task 5), §10 prompt
(Tasks 4–5), §23 (Task 3), §26 artifacts (Tasks 4–5), §33 apply policy and
§33.1 the ledger and §33.2 the two roots (Tasks 2, 4, 5), §37.2 materialization
order (Task 5), §37.3 locking (Task 1).

**Cannot be retrofitted, so it is here:** the ledger's per-file hash and per-file
contributed keys. Phase 7's `uninstall` verdicts rest entirely on them, and a
ledger written without them would need a migration.

**Deferred with owners:** `--prune` and the project root — past v1 (§38); MCP,
model and secret references — Phase 5; plugins and marketplace items — Phase 6;
`list`, `uninstall`, `export` — Phase 7.
