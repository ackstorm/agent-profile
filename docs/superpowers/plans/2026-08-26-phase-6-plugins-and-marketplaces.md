# Phase 6 — plugins as a common type, and same-repo marketplaces

> **For agentic workers:** execute task by task. Steps use `- [x]` for tracking.

**Goal:** One plugin declaration installs correctly on four runtimes, and
`ref: pdf@anthropic-skills` resolves inside the manifest that declares its
catalogue.

**Spec of record:** `docs/specs/agent-profile-declarative-spec-v0.6.3.md`

**Status: routing COMPLETE, three format conversions OUTSTANDING.** Gates
green. Seven mutation tests. What the plan did not predict is at the end,
including a defect I shipped and had to fix in the next commit.

**Scope, set by decision D4 and narrower than the roadmap's version:** the route
engine, four adapter tables, `KnownComponentKinds`, and `marketplace.json`
parsing for **same-repo entries only**. There is no cross-repo fetch path, and
building one would not be scope creep — it would be re-introducing the thing
§21.2 existed to guard, without the guard.

## What changed since the roadmap wrote this phase

- **§21.2 is gone from the spec.** Nothing to apply. `SameEndpoint` stays in
  `pkg/source`, tested and unused, as day-one machinery for the cross-repo
  trigger. Do not delete it and do not wire it up.
- **The ledger has no second arm.** A marketplace records nothing; its definition
  lives in the manifest that declares it, and an install names a manifest or a
  direct source (§35.1).
- **An external catalogue entry is a resolution ERROR.** `source.ErrForeignEntry`
  already exists with the message shape; this phase raises it.
- **`plugins` still has no top-level schema field.** `Decode`'s `onlyKeys` does
  not admit the block, and `pkg/source`'s locator walk has a comment marking
  where its `add()` call goes. Both land here, together.

---

### Task 1: `plugins` as a schema type

- [x] `Profile.Plugins map[string]Resource`, admitted by `onlyKeys`, rendered by
  `Render`, and walked by `source.Resolve` — the four places a union or a
  collection has to be touched together. Phase 1's catch-up found that adding a
  source branch and forgetting `Required`/`Render` is silent; the same shape
  applies here.
- [x] The §35 fixture regains its `code-review` plugin block, which was dropped
  precisely because this did not exist. **Diff it against §35's bytes**, not
  against what the parser accepts: that fixture agreed with a decoder bug once.

### Task 2: `marketplace.json`, same-repo

- [x] Parse the catalogue from the already-fetched marketplace tree.
- [x] An entry whose source stays inside the marketplace's repository is sliced
  out of that tree — one clone, N plugins.
- [x] An entry naming another repository or host raises `ErrForeignEntry`. Test
  it, and mutation-test it: an implementation that "helpfully" fetches it is the
  regression this whole scope exists to prevent.
- [x] `<item>@<marketplace>` type matching: a `ref` must target a catalogue whose
  `type` matches the referencing family.

### Task 3: the route engine

- [x] Port `ach/internal/cli/adapter/route`: `Rule{FromGlob, ToGlob, Merge,
  Transform}`, `route.Project`, `KnownComponentKinds`.
- [x] Its invariant travels with it: every `FromGlob`'s first segment appears in
  `KnownComponentKinds`, and an unrouted KNOWN kind is reported as dropped, never
  silently skipped (§8).

### Task 4: four adapter tables

- [x] Port the tables for claude, codex, opencode and pi. gemini's is deleted,
  not ported.
- [x] Carry `ach`'s measured divergences **verbatim, with their comments**:
  codex agent frontmatter drops `mcp_servers`; opencode gets colour-to-hex, a
  command-frontmatter allowlist and lowercased tool names. These were measured
  against real binaries — re-deriving them costs the same bugs twice.
- [x] **An adapter MUST NOT register MCP servers found in a plugin's agent
  frontmatter** (§24.2). A plugin that can register an MCP server by shipping a
  frontmatter key adds a tool the user never reviewed. Mutation-test it.

### Task 5: gates and docs

- [x] `make verify`, `crossbuild`, `secrets`, `fuzz`, `sandbox`, `smoke`.
- [x] Smoke gains: one plugin installs on all four, and its skill is on disk in
  the profile.
- [x] Write up what the plan did not predict.

## Self-Review

**Cannot be retrofitted:** nothing. The ledger format is frozen and this phase
writes through the existing `Merge`/`Keys` machinery.

**Deliberately absent:** cross-repo entries (§38, trigger recorded), `--as`
renaming on install (§38), ad-hoc install by ref (§35.1, trigger recorded).

---

## What Phase 6 turned up that the plan did not predict

### I shipped a broken commit, and the cause is mechanical

The commit for Task 2 had three red tests in its own verification output and
went in anyway. Cause, exactly: I applied the third mutation to `resolve.go` and
restored it with `git checkout -- pkg/source/resolve.go` instead of from the
scratch copy used for every other mutation this session. `checkout` restores
from HEAD, and HEAD was the commit *before* `resolveRef` existed — so it
reverted the mutation and the feature together.

Two things turned it from caught into shipped:

- the restore used a **different command** from the one every other mutation
  used (`cp` from a `.bak`), so the habit that makes this safe was not applied;
- the `make quick` guarding the commit ran on the **same shell line** as
  `git commit`, so its failures scrolled past above the word "committed".

Rules for the rest: restore a mutation only from its scratch copy, never with
`git checkout`; and never put a gate and a commit on one line.

### Three format conversions are outstanding, and they are named rather than faked

`codex agents/**.md → .toml`, opencode's command-frontmatter allowlist, and
opencode's agent tools/colour rewriting. Each rule carries its `Transform` name,
routes **nothing**, and warns saying which conversion is missing.

That is deliberate. A half-correct TOML conversion is worse than a warning: the
runtime loads it and fails somewhere else, with an error naming neither the
plugin nor ap. Same reasoning as `prompt` and as codex's skills directory —
this repository's recurring rule is that a guessed or approximate artefact is
worse than an honest absence.

`ach`'s implementations are the reference when they land:
`internal/cli/adapter/codex/codex.go` (`codexAgentTOML`) and
`internal/cli/adapter/opencode/opencode.go` (`opencodeCommandFrontmatter`,
`opencodeAgentTools`). Carry their comments — those divergences were measured
against real binaries.

### A plugin's structured destinations are routed but not merged yet

A plugin's `mcp/` and `.mcp.json` route to the runtime's MCP file, and claude's
`AGENTS.md` to `CLAUDE.md` as a composite. Phase 5 built the deep-merge
machinery those need; wiring a plugin's own MCP declarations through it is the
remaining piece, and it warns rather than half-writing.

The composite merge is genuinely new work: `CLAUDE.md` belongs to the user, so a
plugin contributes a **marked region** that uninstall can strip — which is what
the ledger's `Merge: "composite"` and its marker id in `Keys[0]` exist for.

### The same-repo rule made this phase smaller than the roadmap's version

No cross-repo fetch path, no second-hop credential handling, no §21.2 to apply.
`ResolveItem` is a path join inside a tree already fetched. The roadmap's
version of this phase was the largest in the plan; D4 cut roughly half of it,
and the half it cut was the half carrying the security surface.
