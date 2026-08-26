# Phase 6 — plugins as a common type, and same-repo marketplaces

> **For agentic workers:** execute task by task. Steps use `- [ ]` for tracking.

**Goal:** One plugin declaration installs correctly on four runtimes, and
`ref: pdf@anthropic-skills` resolves inside the manifest that declares its
catalogue.

**Spec of record:** `docs/specs/agent-profile-declarative-spec-v0.6.3.md`

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

- [ ] `Profile.Plugins map[string]Resource`, admitted by `onlyKeys`, rendered by
  `Render`, and walked by `source.Resolve` — the four places a union or a
  collection has to be touched together. Phase 1's catch-up found that adding a
  source branch and forgetting `Required`/`Render` is silent; the same shape
  applies here.
- [ ] The §35 fixture regains its `code-review` plugin block, which was dropped
  precisely because this did not exist. **Diff it against §35's bytes**, not
  against what the parser accepts: that fixture agreed with a decoder bug once.

### Task 2: `marketplace.json`, same-repo

- [ ] Parse the catalogue from the already-fetched marketplace tree.
- [ ] An entry whose source stays inside the marketplace's repository is sliced
  out of that tree — one clone, N plugins.
- [ ] An entry naming another repository or host raises `ErrForeignEntry`. Test
  it, and mutation-test it: an implementation that "helpfully" fetches it is the
  regression this whole scope exists to prevent.
- [ ] `<item>@<marketplace>` type matching: a `ref` must target a catalogue whose
  `type` matches the referencing family.

### Task 3: the route engine

- [ ] Port `ach/internal/cli/adapter/route`: `Rule{FromGlob, ToGlob, Merge,
  Transform}`, `route.Project`, `KnownComponentKinds`.
- [ ] Its invariant travels with it: every `FromGlob`'s first segment appears in
  `KnownComponentKinds`, and an unrouted KNOWN kind is reported as dropped, never
  silently skipped (§8).

### Task 4: four adapter tables

- [ ] Port the tables for claude, codex, opencode and pi. gemini's is deleted,
  not ported.
- [ ] Carry `ach`'s measured divergences **verbatim, with their comments**:
  codex agent frontmatter drops `mcp_servers`; opencode gets colour-to-hex, a
  command-frontmatter allowlist and lowercased tool names. These were measured
  against real binaries — re-deriving them costs the same bugs twice.
- [ ] **An adapter MUST NOT register MCP servers found in a plugin's agent
  frontmatter** (§24.2). A plugin that can register an MCP server by shipping a
  frontmatter key adds a tool the user never reviewed. Mutation-test it.

### Task 5: gates and docs

- [ ] `make verify`, `crossbuild`, `secrets`, `fuzz`, `sandbox`, `smoke`.
- [ ] Smoke gains: one plugin installs on all four, and its skill is on disk in
  the profile.
- [ ] Write up what the plan did not predict.

## Self-Review

**Cannot be retrofitted:** nothing. The ledger format is frozen and this phase
writes through the existing `Merge`/`Keys` machinery.

**Deliberately absent:** cross-repo entries (§38, trigger recorded), `--as`
renaming on install (§38), ad-hoc install by ref (§35.1, trigger recorded).
