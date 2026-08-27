# Open Decisions — blocking the declarative v1 plan

Purpose: stop a plan being written twice. Every item below changes what gets
built. **Settled** items are recorded so they are not re-litigated; **Open** items
each name the conflict, the evidence, and a recommendation to accept or reject.

**All blocking items are answered.** D6 is deferred by choice and blocks only
Phase 8, whose requirements are identical under either outcome.

---

## Settled

| # | Decision | Where it lands |
|---|---|---|
| S1 | **`archive` source returns.** It is how ACH serves every context item (`downloadUrl`). §18's recorded reintroduction trigger has fired. Checksum verification mandatory — an archive is not content-addressed the way a git SHA is, so its lock entry carries a digest. | Phase 3 |
| S2 | **`model` stays singular.** ACH is plural because it describes *capabilities*; selection happens at hydration time, **in the ACH→agent-profile translator**, not here. agent-profile receives one model. | no change |
| S3 | **`prompt` stays singular.** Same reasoning as S2, same owner. | no change |
| S4 | **`a2aAgents` needs no new type.** They are being converted into MCP servers; the translator emits them as `mcps` entries. | no change |
| S5 | **`guardrails` is dropped.** It does not exist as a hydratable resource — LiteLLM applies it server-side and no adapter projects it. No asymmetry. | no change |

| S6 | **D1 answered: `plugins` becomes a common resource type**, and `ach`'s `route` engine is ported as its materializer. ACH Server does not emit the type today and simply will not declare it — a type nobody uses costs nothing. The `ach-cli` hydration path for it is live and keeps working. | Phase 6 |
| S7 | **D2 answered: adopt the install ledger.** `ap` supports install/uninstall the way `ach-cli` already does. This **reverses SPEC v0.5 decision #41 and the §33 never-delete rule** — see "Spec amendments" below. | Phase 4 writes it, Phase 7 spends it |
| S8 | **D3 answered: a root is just a parameter — and v1 has two.** `default` → the agent's real config dir; a named profile → its namespace. Both are one mechanism, the one `ach` calls `--global`: point the agent's config variable at a directory. **Amended 2026-08-26:** the project root is deferred. It is a *second* mechanism (the agent reads its working directory; nothing is redirected) and the only root carrying a containment rule and a per-agent project-root-file list. `ach-cli` serves that case today. Additive later — bare destinations already mean "the root". | Phase 4 |
| S10 | **D5 answered: both surfaces, and NEITHER is state.** A manifest is an *input* — a portable definition you apply, share and may not even keep. An imperative verb is the same input, typed instead of written. **The ledger is the only state.** `ap install claude:plan skill pdf@anthropic-skills` needs no manifest and creates none. `ap manifest export` turns a root's ledger back into a manifest. | Phase 7 |
| S11 | **D6 deferred**, leaning binary-only. It does not block: the portability requirement is identical either way, because `ach-cli` ships windows and would need a windows `ap` whether it imports `pkg/` or shells out to it. The zero-logic-CLI rule is adopted now regardless — free from the start, expensive to retrofit. | Phase 8 |
| S9 | **D4 answered: no gemini.** `gemini-cli` is deprecated. It is not added here, and `ach`'s `gemini` adapter is **deleted** rather than ported — including its `commands/**/*.md → .gemini/commands/**/*.toml` transform. Four runtimes: claude, codex, opencode, pi. | scope reduction |

S2–S4 share one consequence worth stating plainly: **the ACH→agent-profile
translator is a real component with real logic**, and it lives in `ach`, not
here. It selects one model, selects one prompt, and rewrites A2A agents as MCP
servers. This repository's schema stays narrow because that translator absorbs
the width.

---

---

## Spec amendment from Phase 8 (2026-08-26): a root may be named literally

SPEC v0.6.3 §33.2 says a root is a **parameter** and MUST NOT be inferred from
the environment, and lists two: the agent's real configuration directory, and a
named profile's namespace. Both are addressed by a reference.

Phase 8's exit criterion — the hydrator image materializing into an empty
directory with **no `$HOME` set** — cannot be met that way. A named profile
resolves through `XDG_DATA_HOME` or `$HOME`, and an init container arranging
`XDG_DATA_HOME` so that `<it>/agent-profile/profiles/claude/hydrated` lands on
the volume the main container reads is inferring a root from the environment
through two layers of indirection — with no `$HOME` to infer from in the first
place.

**`--root <dir>` names the directory literally, and the subject is then a bare
agent name.** This is not a third root in §33.2's sense: it is the same one
mechanism — point the agent's configuration-directory variable at a directory —
with the directory stated instead of derived. The section's own reasoning for
excluding a project root (it is a *different* mechanism, and needs a containment
rule and a per-agent file list) does not apply.

Consequences, each settled with the flag:

- **A reference and `--root` together is an error.** They answer the same
  question, and a silent precedence rule is how the wrong directory gets
  written.
- **A literal root is not gated.** It is neither the user's real configuration
  nor a profile ap manages, so there is nothing ap could claim to undo, and the
  resolved absolute path the gate exists to display is the argument the user
  just typed.
- **Preflight's executable checks become conditional (§28).** Both of them —
  the runtime CLI and every active stdio MCP command — belong to the runtime's
  process, not to ap. An init container has neither, and must not: that
  separation is the topology. `Preflight` takes `runtimeIsLocal`, true whenever
  the root came from a reference, because a profile exists to be launched.

For v0.6.4.

---

## Spec amendments these answers require

S7 and S8 are not gaps in SPEC v0.5 — they contradict it. Recording them so the
next revision carries them and nobody re-derives the old rule from the frozen
document.

| Spec text | Status |
|---|---|
| §33 "**Apply never deletes.** … Orphaned entries are an accepted v1 limitation" | **Reversed.** Apply writes a ledger; `uninstall` removes what the ledger owns. (Convergence — `--prune` — is deferred; see the deferral table in the roadmap.) |
| decision #41 "no ownership state file exists in any version" | **Reversed.** The ledger is that file. |
| decision #29 "Apply is merge-overwrite-never-delete, uniformly for all profiles including `default`" | **Amended** to merge-overwrite, with deletion confined to ledger-owned paths and ledger-owned keys. |
| §26.1 "Absolute destinations and workspace (user repository) destinations are v1 non-goals" | **Amended.** A `workspace` root is added. §26.1 already reserved the mechanism: "a scope prefix can be added backward-compatibly: bare paths keep meaning namespace root." |
| §24 "The common specification does not define one universal plugin format" | **Amended.** `plugins` is a common resource type with adapter-owned routing. §25 already conceded the ground, calling the plugin marketplace contract "a de-facto standard applicable beyond one runtime, hence declarable at common level." |
| §38 non-goal "archive sources" | **Reversed** (S1). Its own recorded trigger fired. |
| §38 non-goal "an ownership state file" | **Reversed** (S7). |

### Why the ledger is safe, and why the spec's fear was misplaced

The spec refused an ownership file on the reasoning that it drifts from disk and
then lies. `ach`'s design answers that, and it is the design to port
(`ach/internal/cli/localpkg/store/store.go`):

```go
type FileRec struct {
	RelPath string   `json:"relPath"`
	Hash    string   `json:"hash"`
	Merge   string   `json:"merge,omitempty"` // "deep"|"composite"; empty = replace
	Keys    []string `json:"keys,omitempty"`  // deep: dotted keys removed on uninstall
}
```

Three properties make it honest rather than authoritative:

1. **Hash per file.** On uninstall a file whose hash no longer matches was edited
   by the user; the verdict is `modify`/`skip`, not `remove`. The ledger never
   claims a file it no longer recognises.
2. **`Keys` for deep merges.** Uninstalling from a merged `settings.json` or
   `config.toml` removes **only the dotted keys it added** — the file survives,
   and every key the user added by hand survives with it. This is the property
   that makes `default`-scope pruning safe, which §33 assumed was impossible.
3. **One classifier for act and preview.** `--dry-run` and the real removal share
   the same function, so the preview cannot drift from the action.

Consequence, and it is an improvement over the spec: **removal works in `default`
too.** SPEC v0.5 made `default` orphans permanent by design because it had no way
to tell its own writes from the user's. The ledger has one.

## Resolved — recorded in full, because the reasoning is not re-derivable from the outcome

### D5 — SETTLED: the manifest is an input, the ledger is the state

The first framing of this question was wrong, and the correction changes the
design. Recorded in full because getting it backwards is easy.

**Wrong model** (proposed, rejected): the manifest is the source of truth, and
`ap install` edits it. That makes the manifest state.

**Right model:** the manifest is a *definition* — something you write once, move
between machines, share with a team, apply, and are free to throw away. It is not
a record of what is installed. Two ways in, one record out:

```
  a manifest file            ─┐
  (portable, shareable,       │
   optional, never written    ├──►  apply  ──►  the LEDGER  ◄── the only state
   to by ap)                  │                 (per root)
                              │
  ap install <ref> skill <name>         ─┘
  (no manifest, none created)
```

Consequences, each of which makes the build smaller:

- **No manifest-writing code.** The rejected model needed `ap install` to edit
  a YAML file the user wrote — preserving their comments, key order and
  formatting. Comment-preserving YAML round-trip is hard, the hand-rolled subset
  parser in `pkg/schema` cannot do it, and it would have forced a real YAML
  dependency. Avoided entirely.
- **No "where does the manifest live" question.** There is no default manifest
  location because nothing creates one.
- **`ach-cli`'s imperative verbs port unchanged.** They already write only
  `installed.json`. That is exactly this model.
- **`ap export <root>` closes the loop**: ledger → manifest, so a profile built
  by hand becomes something portable. `ach`'s own docs already sketch this as
  "a future `ach-cli env export` can SERIALIZE local state → CR YAML"; the ledger
  is a superset of what it needs.
- **Seeing what is installed is `ap list <ref>`**, reading the ledger —
  not `render`, which renders a manifest.

**Apply stays additive, and v1 ships nothing that changes it.** Applying a
manifest that no longer mentions a resource does not remove it — §33's rule
survives. Removal is per-resource (`ap uninstall`) and explicit.

**Amended 2026-08-26: whole-root convergence (`--prune`) is deferred.** It is a
set difference over that removal and needs nothing extra in the ledger, so it is
purely additive. Its only named consumer is `ach-runtime`'s init container — a
pod should match its `CapabilityProfile` exactly, a developer's laptop should not
— and that container is Phase 9 work in another repository. Build it against a
real caller.

### Lockfile and ledger: one pins, the other records

Corrected after reading `ach`. The first version of this note said "they
overlap", which was the wrong framing.

**What `ach-cli` actually does today**, verified:

- `internal/cli/lock` is an **advisory single-writer mutex** — `flock(LOCK_EX)`
  on POSIX, `LockFileEx` on Windows. It is not a dependency lockfile, despite
  the name.
- **`ach` has no dependency lockfile anywhere.** `installed.json` is the only
  record.
- Each entry carries `resolvedSHA`, and **install re-resolves every time** — it
  does not pin to the recorded SHA. `manager.ResolveWithCache` resolves the ref
  fresh; the stored SHA is never used as an input.
- `outdated` re-resolves upstream and compares:
  `if rr.ResolvedSHA != e.ResolvedSHA { status = "outdated" }`.

So the ledger **records what happened**. It does not **constrain what happens
next**. That is the whole distinction:

| | lockfile | ledger |
|---|---|---|
| direction | **input** to resolution | **output** of materialization |
| claim | "this ref MUST resolve to this SHA" (§32.3: "the ref is not re-resolved over the network") | "this is what landed here" |
| scope | the workspace, beside the manifests | one materialization root |
| keyed by | source identity | resource → files |
| carries | requested ref → resolved SHA | `{relPath, hash, merge, keys}` + resolvedSHA |
| lives | in git, shared with the team | on the machine, never shared |
| answers | "what will this manifest resolve to, **for everyone**" | "what did **this machine** install, and can I remove it" |

They share exactly one field — a SHA — for opposite reasons: a constraint versus
a receipt.

**Why `ach` gets away without a lockfile:** it has no manifest. Nothing is
shared, so nothing claims two machines should agree. `ach-cli skill install X`
on two laptops gets whatever `main` was that day and nobody notices, because no
artifact ever promised otherwise.

**Why this repository cannot:** a portable manifest is the entire point. Sharing
a manifest with no lockfile means "everyone gets a different environment", and
the manifest is the thing making the false promise.

**Neither can do the other's job**, in both directions:

- The ledger cannot pin. It is machine-local; machine B cannot see what machine
  A recorded.
- The lockfile cannot uninstall. It is keyed by source identity, not by files on
  disk, and carries no hashes and no dotted keys — it cannot say which keys to
  remove from a merged `settings.json`.

**The rule that falls out: a lockfile is a property of a MANIFEST, not of a
ROOT.** No manifest, no lockfile. An imperative `ap install` therefore produces
no lock entry — there is nothing to pin for anyone else, and the ledger already
carries the SHA that `outdated` needs. Consistent with §32's workspace scoping
and with §32.7 leaving local sources unlocked.

---

### Command grammar — SETTLED, see the command-surface document

Superseded. The full surface, the use cases and the reasoning live in
`2026-08-26-command-surface.md`. In one line:

```
ap install    <ref> <kind> <name>       one capability
ap uninstall  <ref> <kind> <name>
ap manifest   apply | render | export   a whole manifest
ap list       <ref>                     extended to accept a qualified reference
```

Split by **mode of operation** — one loose capability versus a whole manifest —
so the command surface mirrors the two inputs the architecture has. The first
argument after the verb is always the subject, which is a reference everywhere
except `manifest render`, whose subject is a file and which has no root.

Deferred past v1, additive whenever wanted: `ap outdated`, `ap manifest schema`.

### D6 — DEFERRED: binary, SDK, or both

Not blocking, and deliberately left open. Leaning **binary and nothing else**.

What matters now is that the choice stays cheap, which needs two things adopted
from the start:

- **`cmd/ap` holds zero logic.** Argument parsing and exit codes; everything else
  in `pkg/`. Free if done from the first commit, expensive to retrofit, and it is
  what makes "add an SDK later" a no-op.
- **`pkg/` stays portable and root-parameterised.** The requirement is identical
  under either answer: `ach-cli` ships windows/amd64 and windows/arm64, so a
  windows `ap` is needed whether `ach` imports the library or shells out to the
  binary. The `make crossbuild` gate stands either way.

Recommendation when it is time to decide: **the binary is the contract, an SDK is
an optimization**, with a golden test asserting the CLI's result is byte-identical
to the library's. And for `ach-runtime`, run `ap`'s own image rather than a
per-project hydrator wrapper — the materialization path is where a tar, traversal
or archive-verification CVE would land, and N images means N things to rebuild
with no way to tell which are patched. Its `--hydrator-image` flag is already the
seam.

## Not replaced — recorded so they are not revisited

| Project | Why not |
|---|---|
| `omnigent` | third-party clone (`omnigent-ai/omnigent`), not ours |
| `hermes-agent` | third-party clone (`NousResearch/hermes-agent`), not ours |
| `ach-spec` | specification documents; they get **updated**, not replaced |
| `ach-agent` harness (channels, limits, memory, cost) | not hydration and not in SPEC v0.5; only `engine/hydrate.py` is replaced |
| `ach-agent.old` | superseded already |

---

## What is replaced

| Project | Component retired |
|---|---|
| `ach` | `internal/cli/{adapter,hydrate,localpkg,extract,merge,namespace,conflict,gitignore}`, `internal/{gitfetch,contentkit,cachefs}` — kept: platform-API client, auth, keys, and a new Environment→manifest exporter |
| `ach-agent` | `src/ach_agent/engine/hydrate.py` projection — kept: the manifest decode, now feeding `ap manifest apply --manifest -` |
| `ach-runtime` | nothing structural — the `ach-hydrator` image becomes `ap` (pending D6) |
| `ccplugin` | the whole project |
| `agent-profile` | `cmd/ap/sync.go`, `internal/manifest` |
