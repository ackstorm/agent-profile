# Phase 5 — MCP, model, prompt and secret references

> **For agentic workers:** execute task by task. Steps use `- [ ]` for tracking.

**Goal:** Materialize the three things that are not files-in-a-directory — MCP
servers, `model`, and `prompt` — into each runtime's own configuration, with
secrets present as REFERENCES and never as values.

**Architecture:** `pkg/hydrate` grows a structured-merge layer. Phase 4 wrote
whole files; this phase writes *keys inside files someone else owns*, which is
why it needs the ledger's `Merge`/`Keys` fields that Phase 4 recorded and never
used. One new dependency, `github.com/BurntSushi/toml`, and it is the only one
v1 gets.

**Spec of record:** `docs/specs/agent-profile-declarative-spec-v0.6.2.md`

## Global Constraints

See `CLAUDE.md`, plus:

- `pkg/**` no build tag, must compile for `windows/amd64`.
- **One new dependency, `github.com/BurntSushi/toml`, and no others.** `pkg/` is
  imported by `ach`, so a dependency added here is added there too. It exists
  because codex's configuration is TOML and editing TOML by hand is how you
  destroy someone's comments and key order.
- Guards get mutation-tested. **A mutation that fails to COMPILE proves nothing.**
- Secret VALUES never reach a manifest, the ledger, a diagnostic, a log, an error
  message, or materialized configuration — the last one absent
  `--allow-plaintext-secrets`.

## What Phase 5 does NOT do

No plugin routing or marketplace items (Phase 6). No `list`, `uninstall` or
`export` (Phase 7) — but every deep-merged write must record its dotted keys,
because that is what lets Phase 7 remove a server from `config.toml` without
touching the rest of it, and it cannot be retrofitted.

---

## The four runtimes are four different answers, and that is the phase

Nothing here generalises. Each row was measured by `ach` against the real binary
and is carried verbatim; `smoke` is what re-verifies it.

| Runtime | MCP lands in | Under key | Secret reference |
|---|---|---|---|
| claude | `.claude.json` (config dir) | `mcpServers.<id>` | `${VAR}` |
| codex | `config.toml` | `[mcp_servers.<id>]` | `bearer_token_env_var`, `env_http_headers` |
| opencode | `opencode.jsonc` | `mcp.<id>` | `{env:VAR}` |
| pi | `mcp.json` | `mcpServers.<id>` | `{env:VAR}` |

Two things about that table are load-bearing:

- **claude's user-scope MCP servers live in `.claude.json`**, not in a
  `.mcp.json`. `.mcp.json` is project scope, and this phase has no project root.
  `CLAUDE.md` already records why `.claude.json` is not shared: sharing it made
  a per-profile MCP server impossible, which is precisely the feature this
  phase ships.
- **codex does not have a generic expansion syntax.** It has two specific keys.
  A `Bearer` `Authorization` header becomes `bearer_token_env_var`; anything
  else becomes an `env_http_headers` entry. A runtime that can express neither,
  for an ACTIVE resource, is a resolution-phase ERROR (§34) — warn-and-skip is
  forbidden here, because it would materialize a resource without its
  authentication, which is silently broken configuration.

---

## File Structure

| File | Responsibility |
|---|---|
| `pkg/hydrate/merge.go` | Deep-merge into JSON and TOML, returning the dotted keys contributed |
| `pkg/hydrate/merge_test.go` | Hand-added keys survive; ordering is stable; keys are reported |
| `pkg/hydrate/mcp.go` | §25 → each runtime's native shape |
| `pkg/hydrate/mcp_test.go` | One golden document per runtime |
| `pkg/hydrate/secretref.go` | §34's expansion table, and the error when a runtime cannot express one |
| `pkg/hydrate/secretref_test.go` | Each syntax; the plaintext refusal; the flag |
| `pkg/hydrate/model.go` | §9 → adapter-managed environment, §15.1 precedence and its notice |
| `pkg/hydrate/prompt.go` | §10, moved here from Phase 4 |

---

### Task 1: Deep merge, and the keys it contributed

**Files:** Create `pkg/hydrate/merge.go`, `merge_test.go`; modify `go.mod`

**Interfaces:**
```go
// MergeInto deep-merges contribution into the document at path, creating it if
// absent, and returns the dotted keys it wrote.
func MergeInto(path string, contribution map[string]any) (keys []string, err error)
```

- [ ] **Step 1** Write the test, and these three assertions are the task:
  - **a key the user added by hand survives** a merge that writes a sibling.
    This is Phase 4's hand-added-file assertion one level down, and it is the
    same property: apply is additive;
  - the returned keys are exactly what was written, dotted
    (`mcpServers.memory`), because Phase 7 removes precisely these and nothing
    else;
  - a merge is **idempotent**: merging the same contribution twice produces
    byte-identical output, or every apply would show a spurious diff in the
    user's version control.
- [ ] **Step 2** Run, watch fail.
- [ ] **Step 3** Implement. JSON via `encoding/json`; TOML via
  `github.com/BurntSushi/toml`, chosen by extension. Port the shape of
  `ach/internal/cli/merge`.

  **A file that does not parse is an ERROR, never a file to overwrite.** The
  user broke it, or it was never ours; replacing it would destroy work this
  program did not create and cannot restore.
- [ ] **Step 4** Run, watch pass. `make crossbuild`.
- [ ] **Step 5** **Mutation-test the additive property**: replace the merge with
  a whole-document write. The hand-added-key assertion must go red.
- [ ] **Step 6** Commit: `feat(hydrate): deep merge reporting the keys it wrote`

---

### Task 2: Secret references, and the refusal

**Files:** Create `pkg/hydrate/secretref.go`, `secretref_test.go`

**Interfaces:**
```go
// SecretRef renders a reference to a binding for one runtime, or reports that
// the runtime cannot express one.
func SecretRef(runtime, bindingVar string) (string, bool)
```

- [ ] **Step 1** Write the test: claude `${VAR}`, opencode and pi `{env:VAR}`,
  codex `false` — it has no generic syntax, only the two specific keys — and
  **the rendered reference never contains the value**, asserted with a value in
  the environment.
- [ ] **Step 2** Run, watch fail.
- [ ] **Step 3** Implement §34's table. `env`-sourced secrets reference the
  DECLARED binding variable: no launcher re-export and no synthesized name, so
  invoking the runtime CLI directly works wherever the environment is already
  populated — containers, CI.
- [ ] **Step 4** Implement the `file`-sourced case: the launcher reads the file
  and exports `AP_SECRET_<NAME>`, and materialized configuration references
  THAT name. Wire it into `internal/run`. State the consequence in its doc
  comment: a profile using file-sourced secrets in runtime configuration
  requires launching through ap.
- [ ] **Step 5** **The refusal.** If a runtime cannot express a reference for an
  ACTIVE resource, apply fails in the RESOLUTION phase — the same error class as
  a missing secret. Warn-and-skip is forbidden (§34): it would materialize a
  resource without its authentication.
- [ ] **Step 6** `--allow-plaintext-secrets` is the sole opt-in, a CLI flag with
  **no manifest field** — a field in a shared inherited base would pre-consent
  plaintext for every child invisibly. Under it, the adapter emits a prominent
  warning listing every file that now contains plaintext, emphasized when the
  root is the real configuration.
- [ ] **Step 7** **Mutation-test the refusal**: make the unexpressable case a
  warning. Confirm red. Restore.
- [ ] **Step 8** Commit: `feat(hydrate): secret references, never values`

---

### Task 3: MCP servers, four native shapes

**Files:** Create `pkg/hydrate/mcp.go`, `mcp_test.go`

- [ ] **Step 1** Write one golden test per runtime from SPEC §35's `memory`
  server (http transport, `Authorization: Bearer` from `memory-token`) plus the
  `filesystem` stdio server. Assert the exact document, not a substring: a
  substring check passes for a document the runtime cannot load.
- [ ] **Step 2** Run, watch fail.
- [ ] **Step 3** Implement the table above. `args` are literal strings — §25 has
  no placeholder mechanism, and adding one here would be inventing syntax the
  spec refuses.
- [ ] **Step 4** Record every merged file in the ledger with `Merge: "deep"` and
  its contributed `Keys`. **This is the field Phase 4 recorded and never used,
  and Phase 7 cannot work without it.**
- [ ] **Step 5** **Mutation-test the ledger keys**: write `Merge: ""` instead.
  The test must go red on the recorded shape, not on the file's contents.
- [ ] **Step 6** Commit: `feat(hydrate): MCP servers in each runtime's own shape`

---

### Task 4: `model`, and who wins

**Files:** Create `pkg/hydrate/model.go`, `model_test.go`

- [ ] **Step 1** Write the test: `model` becomes adapter-managed environment
  variables; a user-declared `runtimes.<r>.environment` entry for the same
  variable WINS (§15.1) and produces a NOTICE naming the variable — without the
  notice the `model` block silently lies about what the agent will use.
- [ ] **Step 2** Run, watch fail.
- [ ] **Step 3** Implement. Absent `model` means runtime-native defaults AND
  native credentials — the subscription case — so nothing is written at all.
  Absent `model.auth` means native credentials even when `base_url` is declared.
- [ ] **Step 4** **Mutation-test the notice**: drop it, confirm red, restore.
- [ ] **Step 5** Commit: `feat(hydrate): model as adapter-managed environment`

---

### Task 5: `prompt`

**Files:** Create `pkg/hydrate/prompt.go`, `prompt_test.go`

Moved here from Phase 4, and the reason is worth keeping: a prompt is not a FILE
for every runtime. claude takes `--append-system-prompt` at launch, which is a
launch-argument concern, not a destination — the same shape as `model`, which is
why it belongs beside it.

- [ ] **Step 1** Write the test per runtime: `mode: append` and `mode: replace`
  where the runtime supports both, and a §8 degradation warning where it does
  not. **Do not invent a destination for a runtime whose mechanism you have not
  verified** — an unverified path silently writes nothing, or writes to a name
  the agent never opens, which is worse than a warning.
- [ ] **Step 2–4** Implement, run, commit:
  `feat(hydrate): prompt append and replace`

---

### Task 6: Gates and docs

- [ ] **Step 1** `make verify`, `crossbuild`, `secrets`, `fuzz`, `sandbox`,
  `smoke`. Smoke must gain a check per runtime: the binary READS what was
  written. That is the only thing that can catch a wrong row in the table above,
  and `docs/references/SMOKE.md` records three checks that could never go red —
  before adding one, ask what would make it fail when nothing is wrong.
- [ ] **Step 2** Document: why the four runtimes do not generalise, why codex's
  two keys are not a generic syntax, why `.claude.json` and not `.mcp.json`, and
  why an unexpressable secret is an error rather than a warning.
- [ ] **Step 3** Mark complete and write up what the plan did not predict.

## Self-Review

**Spec coverage:** §9 (Task 4), §10 (Task 5), §15.1 (Task 4), §25 (Task 3),
§34 in full (Tasks 2–3).

**Cannot be retrofitted:** the ledger's `Merge`/`Keys` on every deep-merged
write. Phase 7 removes exactly those keys and nothing else; a write recorded as
`replace` would make uninstall delete the user's whole `config.toml`.

**The dependency:** `github.com/BurntSushi/toml`, and it is the only one v1
gets. Adding it to `pkg/` adds it to `ach`.
