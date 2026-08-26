# Open Decisions — blocking the declarative v1 plan

Purpose: stop a plan being written twice. Every item below changes what gets
built. **Settled** items are recorded so they are not re-litigated; **Open** items
each name the conflict, the evidence, and a recommendation to accept or reject.

Nothing in Phases 1 and 2 depends on any open item. Phase 3 depends on D1.
Phases 4, 5 and 6 depend on the rest.

---

## Settled

| # | Decision | Where it lands |
|---|---|---|
| S1 | **`archive` source returns.** It is how ACH serves every context item (`downloadUrl`). §18's recorded reintroduction trigger has fired. Checksum verification mandatory — an archive is not content-addressed the way a git SHA is, so its lock entry carries a digest. | Phase 3 |
| S2 | **`model` stays singular.** ACH is plural because it describes *capabilities*; selection happens at hydration time, **in the ACH→agent-profile translator**, not here. agent-profile receives one model. | no change |
| S3 | **`prompt` stays singular.** Same reasoning as S2, same owner. | no change |
| S4 | **`a2aAgents` needs no new type.** They are being converted into MCP servers; the translator emits them as `mcps` entries. | no change |
| S5 | **`guardrails` is dropped.** It does not exist as a hydratable resource — LiteLLM applies it server-side and no adapter projects it. No asymmetry. | no change |

S2–S4 share one consequence worth stating plainly: **the ACH→agent-profile
translator is a real component with real logic**, and it lives in `ach`, not
here. It selects one model, selects one prompt, and rewrites A2A agents as MCP
servers. This repository's schema stays narrow because that translator absorbs
the width.

---

## Open — must be answered before the phases that depend on them

### D1 — The vocabulary: what a "capability" is at the common level

**Blocks:** Phase 6 planning. **Does not block:** Phases 1–5.

`ach-cli` routes nine source kinds (`ach/internal/cli/adapter/route/kinds.go`):
`rules`, `commands`, `agents`, `skills`, `mcp`, `.mcp.json`, `prompts`,
`AGENTS.md`, `hooks`. SPEC v0.5's common vocabulary covers three. Replacing
`ach-cli` with the v0.5 vocabulary as written means commands, agents and rules
stop being installed.

Noted: plugins are gated in ACH **Server** but not in the **CLI**, so the CLI
path is live and must keep working.

- **(a) Add a common `plugins` resource type**, port `ach`'s `route` engine as
  its materializer. — **RECOMMENDED.** One new type; content already ships this
  way; §25 already calls the plugin marketplace contract "a de-facto standard
  applicable beyond one runtime, hence declarable at common level". Buys the
  per-runtime routing (~3 650 LOC verified against real binaries) that
  `artifacts` + `destination` cannot express.
- (b) Add `commands`, `agents`, `rules` as separate common types. — Five types
  for the same work, each needing its own routing table anyway.
- (c) Keep plugins runtime-native (§24 as written). — Rejected: ACH serves one
  plugin to N runtimes, so its exporter would need every runtime's routing table.

**Answer: (a) / (b) / (c)**

---

### D2 — Uninstall and convergence *(SPEC CONFLICT)*

**Blocks:** Phase 4.

`ach-cli` has convergence today, and SPEC v0.5 explicitly does not:

| `ach-cli` today | SPEC v0.5 |
|---|---|
| `installed.json` — a per-file ledger with hashes | decision #41: "**no ownership state file exists in any version**" |
| `plugin uninstall` / `skill uninstall`, with `--dry-run` sharing one classifier with the real removal | §33: "**Apply never deletes.**" |
| `env hydrate --sync` prunes what is no longer declared, boundary-safe | §33: "`--clean` is v2, **named profiles only**; `default` orphans are permanent by design" |

If this repository replaces `ach-cli` under v0.5 as written, **users lose
uninstall and lose drift correction.** That is not an oversight in the plan — the
spec designed it in deliberately, on the reasoning that ownership tracking is
state that lies when the user edits by hand.

Three ways out:

- **(a) Adopt the ledger.** Port `ach`'s `installed.json` model into `pkg/hydrate`
  and reverse spec decision #41 and the §33 never-delete rule. Buys uninstall and
  `--sync` for every consumer. Costs: a state file that can disagree with disk,
  which is exactly what the spec refused. — **RECOMMENDED**, because "replaces
  `ach-cli`" is not true without it, and because `ach` has already carried this
  design in production and knows its failure modes (its own docs name them).
- (b) Keep never-delete; `ach-cli`'s uninstall verbs stay in `ach`, reading
  `installed.json` that `ach` keeps writing. — Then `ach` does not stop
  hydrating; it keeps half. Contradicts the goal.
- (c) Ledger only for named profiles / non-`default` roots, matching §33's future
  `--clean` scoping. Uninstall works in a project or a profile, never against
  `~/.claude`. — The narrow version of (a).

**Answer: (a) / (b) / (c)**

---

### D3 — Project scope *(SPEC CONFLICT)*

**Blocks:** Phase 4.

`ach-cli`'s primary mode is **project-scoped**: it writes `./.claude/`,
`./.mcp.json` and `./AGENTS.md` into the user's repository, with `--global` as the
alternative. Its containment rule is "write nothing outside the target's dot-dir",
with exactly one allowed project-root file.

SPEC v0.5 §26.1: "Absolute destinations and **workspace (user repository)
destinations are v1 non-goals**." agent-profile's roots are a profile namespace or
`default` (`~/.claude`).

There is no overlap. A hydrator that cannot write into a repository cannot
replace `ach-cli`.

- **(a) Add a third root: `workspace`.** §26.1 already reserves the mechanism —
  "If a second root is ever needed, a scope prefix can be added
  backward-compatibly: bare paths keep meaning namespace root." So this is
  additive, not a reversal. Brings `ach`'s containment rule and the `.gitignore`
  credential block with it. — **RECOMMENDED.**
- (b) Model a project as a profile whose namespace is `./.ap`. — Does not work:
  the tools read `./.claude/`, not `./.ap/`.
- (c) Leave project scope in `ach`. — Contradicts the goal.

**Answer: (a) / (b) / (c)**

If (a): does the `.gitignore` marker block come too? `ach` writes one because
projected config carries bearer tokens in plaintext at project root. Under §34
this repository writes *references*, not values — so the block may be
unnecessary. **Sub-answer: yes / no.**

---

### D4 — gemini-cli, the fifth runtime

**Blocks:** Phase 4 (adapter set), the smoke image, `pkg/agentreg`.

`ach-cli` supports five: claude-code, codex, gemini-cli, opencode, pimono.
`agent-profile` supports four — **there is no gemini row.** Replacing `ach-cli`
means adding it, and this repository's standing rule is that every registry field
is verified by running the real binary, never read from documentation.

Known from `ach` (already paid for, still needs re-verification here): the
variable is `GEMINI_CLI_HOME` and it is a **parent** — config goes to
`$GEMINI_CLI_HOME/.gemini/`, and a `settings.json` written one level up is
silently ignored. `GEMINI_CONFIG_DIR` was proposed upstream and never shipped.
Commands are read **only** as TOML.

- **(a) Add gemini in Phase 4**, with a verified registry row and a smoke check.
  — **RECOMMENDED** if the answer to D3 is (a); a project-scoped hydrator that
  drops a supported tool is a regression.
- (b) Defer gemini to a later phase, accept that `ach-cli` cannot be retired until
  then.

**Answer: (a) / (b)**

---

### D5 — The imperative surface

**Blocks:** Phase 7 (what `ap sync` is replaced *by*).

`ach-cli` has verbs SPEC v0.5 does not model at all:

```
ach-cli repo add <source> --name <n> | list | remove | update
ach-cli plugin install <name@repo>… | uninstall | update | outdated
ach-cli skill  install <name@repo>… | uninstall | update | outdated
```

"Install this one thing, now" is not a declarative manifest. `outdated`
re-resolves each installed ref's SHA and reports drift — which needs D2's ledger.

- **(a) `ap` grows an imperative surface** that edits a manifest and re-applies:
  `ap add skill pdf@anthropic-skills` appends to the manifest, then applies. The
  manifest stays the source of truth and `outdated` becomes lockfile drift. —
  **RECOMMENDED.** It keeps one model and gives the ergonomics people actually
  use.
- (b) Declarative only. Users hand-edit YAML. `ach-cli`'s verbs die.
- (c) `ach` keeps the verbs and generates manifests. — `ach` does not stop
  hydrating.

**Answer: (a) / (b) / (c)**

---

### D6 — `ach-runtime` as a fourth consumer *(confirm, low risk)*

`ackstorm/ach-runtime` (`internal/hydrator/hydrator.go`) injects an init container
named `ach-hydrator` into a pod built from a `CapabilityProfile` CRD, sharing an
EmptyDir `workspace` volume with the main container. The image is configurable via
`--hydrator-image` and the whole thing is disabled when that flag is empty.

Reading: **the `ach-hydrator` image becomes `ap`.** That is a fourth consumer and
it adds requirements:

- `ap` must run headless — no TTY, no terminal gate, no `$HOME` assumption.
  Already true for `--yes`; must stay true and be tested.
- A container image is a release artifact of this repository.
- The materialization root is an EmptyDir given as a parameter — which is exactly
  why `pkg/` must never read `$HOME` implicitly.
- `CapabilityProfile` CRD → manifest is another translator, owned by `ach-runtime`.

**Confirm: is `ach-hydrator` in scope as a consumer? yes / no**

---

## Not replaced — recorded so they are not revisited

| Project | Why not |
|---|---|
| `omnigent` | third-party clone (`omnigent-ai/omnigent`), not ours |
| `hermes-agent` | third-party clone (`NousResearch/hermes-agent`), not ours |
| `ach-spec` | specification documents; they get **updated**, not replaced |
| `ach-agent` harness (channels, limits, memory, cost) | not hydration and not in SPEC v0.5; only `engine/hydrate.py` is replaced |
| `ach-agent.old` | superseded already |

---

## What is replaced, once the above are answered

| Project | Component retired |
|---|---|
| `ach` | `internal/cli/{adapter,hydrate,localpkg,extract,merge,namespace,conflict,gitignore}`, `internal/{gitfetch,contentkit,cachefs}` — kept: platform-API client, auth, keys, and a new Environment→manifest exporter |
| `ach-agent` | `src/ach_agent/engine/hydrate.py` projection — kept: the manifest decode, now feeding `ap apply --manifest -` |
| `ach-runtime` | nothing structural — the `ach-hydrator` image becomes `ap` (pending D6) |
| `ccplugin` | the whole project |
| `agent-profile` | `cmd/ap/sync.go`, `internal/manifest` |
