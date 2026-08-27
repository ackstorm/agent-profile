# Phase 5 — MCP, model, prompt and secret references

> **For agentic workers:** execute task by task. Steps use `- [x]` for tracking.

**Goal:** Materialize the three things that are not files-in-a-directory — MCP
servers, `model`, and `prompt` — into each runtime's own configuration, with
secrets present as REFERENCES and never as values.

**Architecture:** `pkg/hydrate` grows a structured-merge layer. Phase 4 wrote
whole files; this phase writes *keys inside files someone else owns*, which is
why it needs the ledger's `Merge`/`Keys` fields that Phase 4 recorded and never
used. One new dependency, `github.com/BurntSushi/toml`, and it is the only one
v1 gets.

**Spec of record:** `docs/specs/agent-profile-declarative-spec-v0.6.2.md`

**Status: Tasks 1-4 COMPLETE. Task 5 (prompt) is OUT OF V1 (spec v0.6.3 §10, §38).** Gates
green. Eleven mutation tests. One real bug found by a test rather than by
reading: an absent MCP container was written whole and recorded as
`mcpServers`, which would have made Phase 7's uninstall delete every server in
the file, including the user's.

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

- [x] **Step 1** Write the test, and these three assertions are the task:
  - **a key the user added by hand survives** a merge that writes a sibling.
    This is Phase 4's hand-added-file assertion one level down, and it is the
    same property: apply is additive;
  - the returned keys are exactly what was written, dotted
    (`mcpServers.memory`), because Phase 7 removes precisely these and nothing
    else;
  - a merge is **idempotent**: merging the same contribution twice produces
    byte-identical output, or every apply would show a spurious diff in the
    user's version control.
- [x] **Step 2** Run, watch fail.
- [x] **Step 3** Implement. JSON via `encoding/json`; TOML via
  `github.com/BurntSushi/toml`, chosen by extension. Port the shape of
  `ach/internal/cli/merge`.

  **A file that does not parse is an ERROR, never a file to overwrite.** The
  user broke it, or it was never ours; replacing it would destroy work this
  program did not create and cannot restore.
- [x] **Step 4** Run, watch pass. `make crossbuild`.
- [x] **Step 5** **Mutation-test the additive property**: replace the merge with
  a whole-document write. The hand-added-key assertion must go red.
- [x] **Step 6** Commit: `feat(hydrate): deep merge reporting the keys it wrote`

---

### Task 2: Secret references, and the refusal

**Files:** Create `pkg/hydrate/secretref.go`, `secretref_test.go`

**Interfaces:**
```go
// SecretRef renders a reference to a binding for one runtime, or reports that
// the runtime cannot express one.
func SecretRef(runtime, bindingVar string) (string, bool)
```

- [x] **Step 1** Write the test: claude `${VAR}`, opencode and pi `{env:VAR}`,
  codex `false` — it has no generic syntax, only the two specific keys — and
  **the rendered reference never contains the value**, asserted with a value in
  the environment.
- [x] **Step 2** Run, watch fail.
- [x] **Step 3** Implement §34's table. `env`-sourced secrets reference the
  DECLARED binding variable: no launcher re-export and no synthesized name, so
  invoking the runtime CLI directly works wherever the environment is already
  populated — containers, CI.
- [x] **Step 4** Implement the `file`-sourced case: the launcher reads the file
  and exports `AP_SECRET_<NAME>`, and materialized configuration references
  THAT name. Wire it into `internal/run`. State the consequence in its doc
  comment: a profile using file-sourced secrets in runtime configuration
  requires launching through ap.
- [x] **Step 5** **The refusal.** If a runtime cannot express a reference for an
  ACTIVE resource, apply fails in the RESOLUTION phase — the same error class as
  a missing secret. Warn-and-skip is forbidden (§34): it would materialize a
  resource without its authentication.
- [x] **Step 6** `--allow-plaintext-secrets` is the sole opt-in, a CLI flag with
  **no manifest field** — a field in a shared inherited base would pre-consent
  plaintext for every child invisibly. Under it, the adapter emits a prominent
  warning listing every file that now contains plaintext, emphasized when the
  root is the real configuration.
- [x] **Step 7** **Mutation-test the refusal**: make the unexpressable case a
  warning. Confirm red. Restore.
- [x] **Step 8** Commit: `feat(hydrate): secret references, never values`

---

### Task 3: MCP servers, four native shapes

**Files:** Create `pkg/hydrate/mcp.go`, `mcp_test.go`

- [x] **Step 1** Write one golden test per runtime from SPEC §35's `memory`
  server (http transport, `Authorization: Bearer` from `memory-token`) plus the
  `filesystem` stdio server. Assert the exact document, not a substring: a
  substring check passes for a document the runtime cannot load.
- [x] **Step 2** Run, watch fail.
- [x] **Step 3** Implement the table above. `args` are literal strings — §25 has
  no placeholder mechanism, and adding one here would be inventing syntax the
  spec refuses.
- [x] **Step 4** Record every merged file in the ledger with `Merge: "deep"` and
  its contributed `Keys`. **This is the field Phase 4 recorded and never used,
  and Phase 7 cannot work without it.**
- [x] **Step 5** **Mutation-test the ledger keys**: write `Merge: ""` instead.
  The test must go red on the recorded shape, not on the file's contents.
- [x] **Step 6** Commit: `feat(hydrate): MCP servers in each runtime's own shape`

---

### Task 4: `model`, and who wins

**Files:** Create `pkg/hydrate/model.go`, `model_test.go`

- [x] **Step 1** Write the test: `model` becomes adapter-managed environment
  variables; a user-declared `runtimes.<r>.environment` entry for the same
  variable WINS (§15.1) and produces a NOTICE naming the variable — without the
  notice the `model` block silently lies about what the agent will use.
- [x] **Step 2** Run, watch fail.
- [x] **Step 3** Implement. Absent `model` means runtime-native defaults AND
  native credentials — the subscription case — so nothing is written at all.
  Absent `model.auth` means native credentials even when `base_url` is declared.
- [x] **Step 4** **Mutation-test the notice**: drop it, confirm red, restore.
- [x] **Step 5** Commit: `feat(hydrate): model as adapter-managed environment`

---

### Task 5: `prompt` — DEFERRED, and this is why

Not built, and deliberately not attempted. Every other table in this phase came
from `ach`, which measured it against the real binaries. There is no such
measurement for `prompt`, for any of the four runtimes.

`CLAUDE.md`'s rule is explicit: *"Only fill it in once you have run the binary
and watched it read the file… A guessed path is worse than none: the flag would
silently copy nothing, or copy to a name the agent never opens."* That is
exactly the situation, and shipping a guess would have been the same mistake as
guessing codex's skills directory — the one this phase avoided by leaving it
empty.

What is known and not enough:

- claude takes `--append-system-prompt`, which is a LAUNCH ARGUMENT and not a
  destination, so its shape is `model`'s (a variant/env concern) rather than a
  materialized file;
- what `mode: replace` maps to for claude is not known;
- codex, opencode and pi's mechanisms are not known at all.

**To close it**, in the smoke image, against each real binary:

```bash
claude --help | grep -i 'system-prompt'
codex --help  | grep -i prompt
opencode --help | grep -i prompt
pi --help     | grep -i prompt
```

Then one row per runtime, with a smoke check that the binary READS what was
written. Until then §8 is the honest answer: a declared `prompt` produces a
degradation warning naming the runtime, which is a true statement, where a
guessed path is a false one.

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

---

## What Phase 5 turned up that the plan did not predict

### The merge test passed while the merge was broken

`TestMergeIsAdditiveReportsItsKeysAndIsIdempotent` seeded a document that
already contained `mcpServers`. With the container present, the merge descended
correctly and recorded `mcpServers.memory`. With it **absent** — the first apply
into a fresh profile, the common case — it wrote the container as a unit and
recorded the key `mcpServers`.

Phase 7 removes exactly the recorded keys, so uninstalling one of our servers
would have removed **every** server in the file, including ones the user added
by hand. Reporting keys at all exists to prevent precisely that.

It was caught by the apply-level test, which started from an empty root, and
only after the merge-level test was green. The lesson is not "write more tests":
it is that a fixture which pre-creates the structure under test hides the
creation path, and the creation path is the one every new user takes.

### `model` needed a mechanism — and the first one I picked was wrong

§9 and §15.1 describe variables; §34 describes expansion inside materialized
configuration. Neither says where a derived `ANTHROPIC_BASE_URL` lives between
`apply` and `run`, which are separate invocations.

I concluded there was no configuration key to merge into and wrote
`<root>/.ap-env`, exported by `internal/run`. Architecture measured the real
binaries and every runtime has one: claude's `settings.json` `env` map, codex's
`model_providers.<id>.env_key`, opencode's `provider.<id>.apiKey` with `{env:}`.

The failure mode of the version I shipped is the part worth remembering. An
environment only the launcher exports is unread wherever the launcher is not in
the path — an init container hydrating for a main container that execs the
runtime directly loses the entire model block, silently. I flagged that exact
topology as an open question in the hand-off note **while shipping the design
that breaks in it**. Naming a risk is not the same as letting it change the
design.

Corrected in v0.6.3; `.ap-env` deleted.

### codex having no generic secret syntax is a design constraint, not a quirk

It shapes the code: `SecretRef` returns `false` for codex, and `codexMCP` is
routed away from `renderHeaders` entirely rather than being given a placeholder
to substitute. A made-up placeholder would be written into `config.toml`
verbatim and codex would send those literal characters AS the credential. The
test asserts codex's document contains neither `${` nor `{env:` anywhere.
