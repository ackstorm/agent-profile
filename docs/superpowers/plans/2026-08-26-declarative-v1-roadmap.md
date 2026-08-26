# Declarative Agent Profiles — Phased Roadmap

**Spec of record:** `docs/specs/agent-profile-declarative-spec-v0.6.2.md`. Section
numbers below are that document's.

> **For agentic workers:** this is a ROADMAP, not an executable plan. Each phase
> gets its own bite-sized plan file before it is executed. Phase 1's plan is
> `2026-08-26-phase-1-manifest-and-composition.md`. Do not execute this file.

**Goal:** Make this repository the universal hydrator — one implementation of
SPEC v0.5 that `ap`, `ackstorm/ach` and `ackstorm/ach-agent` all drive, replacing
`ap sync`'s imperative `install:` commands and `ach`'s own hydration path.

**Architecture:** two layers, split by who consumes them. `internal/` is the
*launcher* — profile namespaces, symlinked shares, the XDG shim, `syscall.Exec`,
sessions — unix-only and `ap`-only, present today and untouched. `pkg/` is the
*hydrator* — manifest → composition → effective profile → resolution →
materialization — exported, portable, and the thing `ach` imports and `ach-agent`
drives through the binary. `ap sync` is the hydrator's v0 and is deleted in
Phase 7.

**Tech Stack:** Go 1.25 (floor). `pkg/` is portable and must build for windows;
`internal/` and `cmd/ap` stay unix-only. Standard library plus exactly one new
dependency (`github.com/BurntSushi/toml`, Phase 5 — justified below). Selected
packages are **copied** from `github.com/ackstorm/ach` (Apache-2.0, same
organisation) because Go's `internal/` rule forbids importing them across modules
— which is precisely the mistake `pkg/` exists here to avoid repeating.

---

## 0. What this repository becomes

`agent-profile` is the **universal hydrator**: the one implementation that turns a
declared agent environment into native configuration for claude, codex, opencode
and pi. It has three consumers, and they do not consume it the same way.

| Consumer | Language | How it consumes | What it needs |
|---|---|---|---|
| `ap` (this repo) | Go | the CLI | everything, including the launcher |
| `ackstorm/ach` | Go | **imports the library** | schema + hydrate, no launcher |
| `ackstorm/ach-agent` | **Python** | runs the binary, feeds it a manifest | a stable manifest format + `ap manifest apply --manifest -` |
| `ackstorm/ach-runtime` | Go (operator) | ships it as the `ach-hydrator` init container | a headless container image, root as a parameter — pending D6 |

`ach` stops hydrating. It exports what an Environment resolves to, in this
repository's manifest format, and this repository materializes it. `ach-agent`
does the same from Python, over a file or a pipe. `ach-runtime` stops shipping a
separate hydrator image and ships this one. `ccplugin` is retired whole.

Not replaced, recorded so it is not revisited: `omnigent` and `hermes-agent` are
third-party clones (`omnigent-ai/omnigent`, `NousResearch/hermes-agent`), and
`ach-agent`'s harness — channels, limits, memory, cost — is not hydration and
stays in Python.

Three consequences follow, and each is a change to the plan as first written.

### The reusable half must be EXPORTED, not `internal/`

This is the one mistake `ach` made and this repository must not repeat: every
package worth reusing there lives under `internal/`, so Go forbids importing it
across modules and the only way in is to copy. If `ach` is to import this
repository, the library cannot be `internal/`.

```
pkg/schema/     parse, compose, validate → EffectiveProfile   ← the CONTRACT
pkg/hydrate/    resolve sources, lock, materialize, adapters  ← the ENGINE
pkg/agentreg/   the agent registry: config dirs, env vars, modes
internal/profile/  namespaces, symlinked shares, the XDG shim ← ap only
internal/run/      Env + syscall.Exec                          ← ap only
cmd/ap/
```

The `internal/` half is the *launcher*: it exists because `ap` runs an agent with
a per-profile home. `ach` hydrates into a workspace and never launches anything,
so it imports `pkg/` and nothing else. That boundary is the same Layer 1 / Layer
2 split this roadmap already had — the reframe only moves where the line is
written down.

`internal/agent` moves to `pkg/agentreg` because `ach` needs exactly that table:
it maintains its own copy today in `internal/cli/adapter/globalpath.go`, and two
copies of "what does `GEMINI_CLI_HOME` mean" is how they drift.

### "Unix only" narrows to "the LAUNCHER is unix only"

`ach-cli` ships for windows — verified in `ach/.goreleaser.yml`: `goos: [linux,
darwin, windows]` × `[amd64, arm64]`, with `internal/cli/lock/lock_windows.go`
using `LockFileEx` for exactly that build. If `ach` imports `pkg/`, then `pkg/`
cannot carry `//go:build unix`.

This does not reopen the Windows non-goal in `CLAUDE.md`, which is about the
launcher: "a second execution model and a second sharing mechanism". Both live in
`internal/`. `pkg/` has neither — it parses, merges and writes files.

- `pkg/**` carries **no build tag** and must compile for `windows/amd64`.
- `internal/**` and `cmd/ap/**` keep `//go:build unix`, unchanged.
- CI gains a `GOOS=windows go build ./pkg/...` gate. Build only; `ap` is still
  not shipped for windows and nothing there is tested on it.
- The one platform-specific thing `pkg/` needs is the §37 file lock, and `ach`
  already has both halves — copy `lock_unix.go` *and* `lock_windows.go`.

### The schema is a product, not an implementation detail

Three repositories will agree on one manifest format, one of them from Python.
That makes the format a versioned contract and adds deliverables the CLI-only
plan did not have:

- `ap manifest render <path>` — with no `--target` it composes every declared
  target and the exit code is the contract check any producer runs.
- `ap manifest apply --manifest -` — read a manifest from stdin, so `ach` can pipe a
  generated one with no temp file.
- `version: "1"` becomes a compatibility promise across three repositories.
  Breaking it is a coordinated release, not a commit.

---

## 0.1 SPEC v0.5 unfreezes — decisions and open items

The spec's status line says "further changes require implementation evidence".
Making `ach`, `ach-agent` and `ach-runtime` consumers supplies it.

**Settled** (2026-08-26): `archive` returns as a source family with mandatory
checksum verification, because it is how ACH serves every context item and §18
records that exact reintroduction trigger. `model` and `prompt` stay **singular** —
ACH is plural because it describes capabilities, and the selection happens in the
ACH→agent-profile translator, which lives in `ach`. `a2aAgents` need no new type;
they are converted into MCP servers by that same translator. `guardrails` is
dropped: LiteLLM applies it server-side and no adapter projects it.

One consequence worth stating: **the ACH→agent-profile translator is a real
component with real logic**, owned by `ach`. It selects one model, selects one
prompt, and rewrites A2A agents as MCP servers. This repository's schema stays
narrow because that translator absorbs the width.

**Also settled** (2026-08-26): `plugins` becomes a **common resource type** and
`ach`'s `route` engine is ported as its materializer — ACH Server simply does not
declare a type it does not use. The **install ledger is adopted**, so `ap`
supports uninstall. There are **two roots** — the agent's real config dir and a
named profile's namespace — and a root is just a parameter. A project root and
whole-root `--prune` are deferred (SPEC §38); see "Deferred out of v1" below.
**gemini is dropped**: it is deprecated, so it is not added here and `ach`'s
gemini adapter is deleted rather than ported.

Four of those reverse or amend frozen spec text — never-delete, the ownership
file, workspace destinations, the universal plugin format. Each is recorded with
its replacement in `2026-08-26-open-decisions.md` § "Spec amendments".

**Settled, and it shapes everything downstream:** a **manifest is an input**, not
state — a portable definition you apply, share, and may not keep. `ap` also has
imperative verbs (`ap install <ref> skill <name>`) which need no manifest and
create none. **The ledger is the only state.** `ap manifest export` turns a root's ledger
back into a manifest, closing the loop. Apply stays additive, and nothing in v1
makes it otherwise — removal is per-resource and explicit.

That correction makes the build *smaller*: nothing writes a manifest, so there is
no comment-preserving YAML round-trip to implement and no default manifest
location to define.

**Nothing is blocking.** D6 (binary vs SDK) is deferred by choice and touches only
Phase 8; the portability requirement is identical either way, because `ach-cli`
ships windows and needs a windows `ap` whether it imports `pkg/` or shells out.

---

## Global Constraints

Copied verbatim from `CLAUDE.md` and the spec; every phase's requirements
implicitly include this section.

- **Go 1.25 floor is a security floor.** `go.mod` stays at `1.25.8` or above.
  GO-2026-4602 (`os.Root` escape — `Link` depends on it) and GO-2025-3956
  (`LookPath` — `Exec` depends on it). Never lower it, never acknowledge either.
- **Build tags split by layer.** `pkg/**` carries **no** build tag and must
  compile for `windows/amd64` (`ach-cli` ships windows). `internal/**` and
  `cmd/ap/**` carry `//go:build unix`, unchanged. A `pkg/` file that needs a
  platform split ships both halves, the way `ach/internal/cli/lock` does.
- **No host toolchain.** Every Go/lint/vuln command goes through `scripts/dev.sh`
  via a `make` target. Add both halves (`name` + `_name`) for any new gate.
- **Standard library only, except `github.com/BurntSushi/toml` from Phase 5.**
  No cobra, no yaml library, no other additions. `pkg/` is imported by another
  module now, so every dependency added here is added to `ach` too.
- **`pkg/` is a published API.** An exported symbol is a compatibility promise
  across three repositories. Do not export what a consumer does not need; do not
  rename what one already uses without a coordinated release.
- **`pkg/` never reads `$HOME` implicitly and never writes outside the root it is
  given.** `ach` hydrates into a workspace, `ap` into a profile namespace. Every
  root arrives as a parameter.
- **Copied ach code keeps attribution.** Every file copied from `ackstorm/ach`
  starts with a comment naming the source path and commit, and keeps its
  `// SPDX-License-Identifier: Apache-2.0` header.
- **Three tests must never fail or be adjusted:**
  `TestDeleteDoesNotFollowTheConfigShim`, `TestDeleteDoesNotFollowSymlinks` /
  `TestDeleteDoesNotFollowNestedSymlinks`, `TestEnvOnlySetsPathsInsideTheProfile`.
- **Anything turning user input into a path calls `profile.ValidName`.**
  `--from` once skipped it and became a path traversal.
- **Guards get mutation-tested.** After adding a guard: revert it, run its test,
  confirm the test fails, restore. A guard whose test still passes without it is
  worse than no guard.
- **`run` parses no flags after its reference.** New commands with no
  passthrough use `parseAroundRef`; never extend flag parsing into `run`.
- **Before claiming a phase done:** `make verify`, `make secrets`, `make sandbox`,
  `make smoke`, `make crossbuild` (`GOOS=windows GOARCH=amd64 go build ./pkg/...`),
  and `make fuzz` for any phase touching path validation.
- **Commits are frequent and conventional.** Short imperative subject, <72 chars.

---

## Phase map

Each phase ships working, independently testable software. Spec sections in
brackets.

### Phase 1 — Manifest and composition *(no I/O, no network)*
`[§2 §3 §4 §5 §6 §7 §31 §36-profile/composition]`

Typed YAML subset parser (evolved from `internal/manifest/yaml.go`: `null`,
booleans, numbers, block scalars, quoted-vs-plain style). Document tree.
The three merge rules plus branch-keyed unions, exclusive groups and `null`
reset — driven by a schema-supplied union oracle, never hardcoded per type.
`extends` chain with cycle detection. Runtime overlay selection. Typed
`Profile` + validation. Base profiles (no `targets`).

Lands in `pkg/schema`, which compiles for windows and carries no build tag.
Also moves `internal/agent` to `pkg/agentreg` unchanged — `ach` needs that table
and maintains a second copy of it today in `internal/cli/adapter/globalpath.go`.

**Also from the walkthrough (F1), and still open at Task 11:** the git source gains `auth.scheme`
(`bearer` | `basic-oauth2`). SPEC §17 has only `value_from`, and a self-hosted
GitLab returns **401 on Bearer** — measured by `ach` against a real instance. The
schema needs the field or such a manifest is unfixable.

**Ships:** `ap manifest render <path> --target claude` prints the effective profile with
secrets redacted. With no `--target` it composes **every** target the manifest
declares and the exit code is the answer — that is the contract check other
repositories run, so there is no separate `validate` command. Zero network, zero
mutation.
**Exit:** every §3 composition example in the spec is a passing table test, and
`GOOS=windows go build ./pkg/...` succeeds.
**Plan file:** `2026-08-26-phase-1-manifest-and-composition.md`

### Phase 2 — Inputs, secrets, preflight, `--dry-run`
`[§11 §12 §13 §14 §28 §37-resolution]`

Input binding (`env` | `file`), required-input calculation over *active* effective
resources only, header `value`/`value_from`/`prefix`, the `Authorization` bans
(§9 in `model.headers`, §14 literal in MCP headers). Derived preflight: runtime
CLI executable, every active stdio MCP `transport.command` resolves. Redaction
everywhere.

Also ships `ap manifest apply --manifest -`, so `ach` pipes a generated manifest with no
temp file.

**`ap manifest schema` (deferred) (JSON Schema emission) is NOT in v1.** `ach-agent` needs the binary
anyway in order to apply, and `ap manifest render` already validates by exit code — so a
second validation mechanism in a second format would be redundant for the only
consumer that asked for it. Worth revisiting for editor autocomplete
(yaml-language-server), which is a different user and not a v1 need.

**F6:** `--dry-run` runs the whole resolution phase, so it **authenticates and
hits the network**. Say so in the help text — "dry run" reads as "does nothing"
and here it does not.

**Ships:** `ap manifest apply --dry-run` — full resolution phase, nothing touched.
**Exit:** a manifest referencing an undeclared secret fails naming both the
resource and the input; a disabled resource's secret is not required; the emitted
`ap manifest render` exits non-zero on each §36 violation and zero on SPEC §35's full
example.

### Phase 3 — Sources and cache *(no lockfile in v1)*
`[§16 §17 §18-reintroduced §19 §20]`

Copy `ach/internal/gitfetch`, `ach/internal/cachefs`, `ach/internal/cli/extract`
— all dependency-free. `source.archive` returns (S1), with **mandatory checksum
verification**: an archive is not content-addressed the way a git SHA is, so the
digest is the only integrity claim it has.

**SPEC §32 is out of v1 entirely**, decided 2026-08-26. `ref: main` means whatever
`main` is today. Dropped with it: `--frozen`, append-only lock consumption, the
lock-commit boundary in §37, git URL normalization for lock identity.

Kept, and not to be confused with it: the advisory single-writer **mutex**
(`flock` / `LockFileEx`) — `ach`'s package is called `lock` and is this, not a
lockfile. And `resolvedSHA` in the ledger, which is a receipt rather than a pin
and is what makes `ap outdated` work. `ach-cli` ships with no lockfile at all and
`outdated` works there, which is the evidence this is sufficient.

Accepted trade, stated rather than discovered: two machines applying the same
manifest on different days can get different bytes.

**v0.6.1 adds two normative rules to this phase.** A credential MUST NOT travel
over non-TLS transport: an `http://` URL that would carry one is an error, and it
applies to §17, §18 and §21's second hop alike. And host comparison for the §21.2
guard is on host **and effective port** — the explicit port or the scheme's
default, so `https://h` and `https://h:443` are one endpoint while a different
port on the same host is a different service.

**From the walkthrough:** `auth.scheme` inferred from host when unset
(`ach`'s rule: host contains `gitlab`, or starts with `git.` → `basic-oauth2`),
explicit value wins, and **a 401 names the scheme that was chosen** or the user
cannot know what to override (F1). Credential goes in `http.extraHeader`, never
the URL — `ach`'s comment gives both reasons, `/proc/<pid>/cmdline` and
persistence in `git config remote.origin.url` — and no error path may grow a
token (F8). One test for GitLab subgroup clone URLs (F9).

**Ships:** git and archive sources resolve into a content-addressed cache;
`--dry-run` fetches and verifies without writing to any root.
**Exit:** a second run with a warm cache performs no network I/O for an unchanged
`ref`; a tampered archive fails the digest check and nothing is written.

### Phase 4 — Materialization core: two roots, the adapter interface, the ledger
`[§10 §23 §26 §33-amended §37-materialization]`

Copy `ach/internal/cli/lock` (**both** `lock_unix.go` and `lock_windows.go`) for
**one** lock: a root lock per materialization root, held across materialization
and the ledger write. Adapter interface over four runtimes. `SKILL.md` contract
check (`contentkit.VerifySkillContents`). Prompt `append`/`replace`. Artifact
`destination` under the resolved root.

**The cache is not locked** (SPEC §37.3). Phase 3's `Publish` fills a temp dir
and renames it into place, sweeping a failed fill — its own test asserts that. Two
processes resolving one source waste a fetch and cannot corrupt anything, so a
second lock buys that fetch back for the price of an ordering rule and the
deadlock the rule exists to exclude. Do not port `ach`'s workspace lock.

**The two roots**, each a parameter, never inferred from `$HOME` inside `pkg/`:

| Root | Resolves to | Mechanism |
|---|---|---|
| `default` | the agent's real config dir | `agentreg.Agent.Config` |
| a named profile | `<profiles>/<agent>/<name>` | `ap` points the agent's config variable at it |

Both are **one mechanism**, the one `ach` calls `--global` (its per-adapter remap
table) — port that. Do **not** port its project scope: a project root is a second
mechanism, and it is the only root needing a containment rule plus a per-agent
list of which project-root files an agent actually reads. Deferred whole.

**The ledger** has **two arms**, and the second one was found by walking the
private-GitLab scenario (F3):

- **materialized resources** — `ach`'s model: per resource, one `FileRec` per
  file with `{relPath, hash, merge, keys}`;
- **resolved definitions** — marketplaces: source, ref, resolved SHA, and the
  auth **binding name** (never a value).

The second arm is not optional. A marketplace writes no file into the root, so a
file-only ledger loses it — and because a manifest is an *input* that is not
kept, `ap install claude:plan skill xlsx@anthropic-skills` a week after applying
a manifest that declared `anthropic-skills` would have nothing left to resolve
the name against.

**Definitions are per-root** (F4), unlike `ach`'s global
`~/.config/ach/local/repos.json`. Accepted deliberately: a global registry is
state no manifest can express, so a manifest would work on its author's machine
and fail on a colleague's. The repetition has a one-command answer —
`ap manifest export` then `apply`.

There is no lockfile in v1 (see Phase 3), so the ledger is the only record. It
**records** rather than **pins**: nothing re-uses `resolvedSHA` as a resolution
input, exactly as `ach-cli` behaves today.

**Ships:** `ap manifest apply` materializes skills, artifacts and the prompt into
either root, and records what it wrote.
**Exit:** sandbox test — a hand-added file survives apply; a declared file is
overwritten and logged; the ledger's hashes match what landed on disk.

### Phase 5 — MCP, model, secret references *(adds the TOML dependency)*
`[§9 §25 §34]`

Copy `ach/internal/cli/merge` (brings `github.com/BurntSushi/toml`). Per-runtime
MCP materialization and §34's expansion table, ported from
`ach/internal/cli/adapter/{codex,opencode}`: claude `${VAR}`, opencode
`{env:VAR}`, pi `{env:VAR}`, codex `bearer_token_env_var` + `env_http_headers`.
`model` → adapter-managed environment variables, §15.1 precedence, override
notice. `AP_SECRET_<NAME>` launcher export for `file`-sourced secrets, wired into
`internal/run`. Plaintext is an error; `--allow-plaintext-secrets` is the only
opt-in and has no manifest field.

Deep-merged writes record their dotted `keys` in the ledger — that field is what
lets Phase 7 remove a server from `config.toml` without touching the rest of it.

**Ships:** MCP servers and model configuration land natively per runtime with
secrets as references, never values.
**Exit:** mutation test — remove the plaintext guard, confirm the test goes red.
Smoke asserts each of the four binaries reads what was written.

### Phase 6 — `plugins` as a common type, and marketplaces
`[§21 §22 §24-amended]`

Port `ach/internal/cli/adapter/route`: the `Rule{FromGlob, ToGlob, Merge,
Transform}` model, `route.Project`, `KnownComponentKinds`, and the rule tables
for **four** adapters — gemini's is deleted, not ported. Its invariant travels
with it: every FromGlob first segment appears in `KnownComponentKinds`, and an
unrouted known kind is reported as dropped, never silently skipped (§8).

Port `contentkit.ParseClaudeCodeMarketplace` and `SliceSubtree`. Typed
marketplaces (`plugins` | `skills`), `<item>@<marketplace>` refs, type matching.
The `plugins` catalog contract stays adapter-owned and its second hop stays
outside the lockfile (§32.8).

Carry `ach`'s hard-won divergences verbatim, each with the comment explaining it:
codex agent frontmatter drops `mcp_servers` (no plugin-injected MCP
registrations); opencode gets colour→hex, a command-frontmatter allowlist and
lowercase tool names; project scope drops OpenPackage's `root/**/*` fallback.
These were measured against the real binaries — re-deriving them costs the same
bugs twice.

### F5 — the second hop must withhold the credential on a host mismatch

The security guard this phase exists to get right. `marketplace.json` lives in
the marketplace's repository, but its **entries may name any URL**, and `ach`'s
`BuildEntrySpec` passes `Token` and `AuthScheme` **unconditionally** for
`git-subdir` and `url` entries — it never compares the entry's host to the
marketplace's. Fetching such an entry sends your private-GitLab PAT to whatever
host it names.

SPEC §17 flags the shape of this for *composition* — "the referenced credential
will be sent to the new host" — but this case is strictly worse: **the manifest
author does not write `marketplace.json`, the marketplace owner does.** Reviewing
your own manifest cannot protect you. §21.1 currently calls the second hop
"outside agent-profile source resolution"; decision D1 makes `plugins` a common
type we materialize, so we inherit it.

**Send the credential only when the entry's host equals the marketplace's host.**
A different host is fetched anonymously; a resulting 401 names both hosts and
says the credential was withheld on purpose. Guard treatment applies: revert it,
watch its test fail, restore.

**Ships:** one plugin installs correctly on four tools; `ref: pdf@anthropic-skills`
resolves.
**Exit:** a ref whose marketplace type mismatches is a validation error; the
`KnownComponentKinds` invariant test passes; a marketplace entry pointing at a
foreign host is fetched with no `Authorization` header, and reverting that check
turns its test red.

### Phase 7 — The state surface: ledger verbs, imperative install, and `ap sync` dies
`[§6 §33-amended §38]`

The ledger's payoff, and the phase that makes the manifest optional.

**Reading state.** `ap list <ref>` reports what the ledger
owns. This is distinct from `ap manifest render`, which renders a *manifest* — a manifest
is an input and says nothing about what is installed.

**Removing.** `ap uninstall <ref> <kind> <name>` removes what the ledger owns: a
file whose hash still matches is removed; one the user edited is reported
`modify` and left; a deep-merged file loses only its recorded dotted keys, so
every hand-added key survives. `--dry-run` and the real removal share one
classifier, so the preview cannot drift from the action. Because the ledger tells
ap's writes from the user's, **removal is safe in `default` too** — the case SPEC
v0.5 gave up on.

**Installing without a manifest.** `ap install <ref> <kind> <name>` takes one
resource, resolves it, materializes it, and records it. Private sources need
`--auth-secret-env <VAR>` / `--auth-secret-file <path>`, because there is no
manifest and therefore no `inputs.secrets` block to carry the binding (F2).

**Here `ap` deliberately diverges from `ach-cli`.** `ach-cli repo add --token`
persists the token in `~/.config/ach/local/credentials.json` at `0600`; §34
forbids that — resolution-time consumers "MUST NOT persist it". Only the binding
**name** is recorded. The UX cost is stated rather than discovered: the variable
must be in the environment on every run, where `ach-cli` asks once.

They
write **no manifest and create none**; this is exactly what `ach-cli`'s verbs do
today, so they port rather than get invented. `ap outdated` re-resolves each
recorded `resolvedSHA` and reports drift.

**Exporting.** `ap manifest export <ref>` serializes the ledger back into a
manifest, so an environment assembled by hand becomes portable. Round trip:
manifest → apply → ledger → export → manifest.

Two things export must synthesise, found by walking the scenario (F7): an
`inputs.secrets` block, because `--auth-secret-env GITLAB_TOKEN` is a binding
with no logical name and the emitted manifest must declare one (derivation must
be stable, or two exports of one root differ); and the **resolved** `auth.scheme`,
because it may have been inferred from a host the reader does not share.

**Apply stays additive, full stop.** A manifest that stops mentioning a resource
does not remove it, and v1 ships no flag that changes this. Whole-root
convergence (`--prune`) is deferred: it is a set difference over the removal
above, needs nothing extra in the ledger, and its consumer is `ach-runtime`'s
init container in Phase 9 — a pod should match its `CapabilityProfile`, a laptop
should not. Build it when that container exists.

**Retiring `ap sync`.** `name: default` targets the real config with the resolved
absolute path displayed and named as such, in `--dry-run` and in the prompt; the
single `--yes` gate; off a terminal it refuses (`stdinIsTerminal`, never
`answered()`). Delete `cmd/ap/sync.go` and `internal/manifest`; migrate
`examples/`; update `CLAUDE.md`, `docs/specs/`, `README`.

**Ships:** the full lifecycle on both roots — apply, install, list, uninstall,
export — with or without a manifest.
**Exit:** `ap install` on an empty profile installs with no manifest anywhere on
disk; uninstalling an MCP server from codex's `config.toml` leaves every other key
intact; a user-edited skill file is never removed; `ap manifest export` of a hand-built
root re-applies to a byte-identical result; `make smoke` green.

### Phase 8 — Distribution: one implementation, three ways in
**Blocked on D6 confirmation**

- **Zero-logic CLI.** `cmd/ap` is argument parsing and exit codes; everything else
  is `pkg/`. A golden test asserts the CLI's result for a manifest is
  byte-identical to the library's, so binary and SDK cannot drift.
- **Container image**, the artifact `ach-runtime` runs as its init container in
  place of `ach-hydrator`. Headless: no TTY, no terminal gate, no `$HOME`
  assumption, root supplied as a parameter.
- **`ap manifest apply --manifest -`** as the language-agnostic contract for `ach-agent`,
  with `ap manifest render` as its validator.
- Published `pkg/` API, versioned. Adding an exported symbol is cheap; renaming
  one is a coordinated release across four repositories.

**Ships:** one hydrator, reachable as a Go import, a binary and an image.
**Exit:** the parity test passes; the image materializes SPEC §35's example into
an empty directory with no `$HOME` set.

### Phase 9 — Downstream adoption *(other repositories)*

Not work in this repository, listed so the contract handoff is not forgotten.

- **`ach`**: `env hydrate` and `plugin/skill install` call `pkg/hydrate` instead
  of `route.Project`; `internal/cli/{adapter,hydrate,localpkg,extract,merge,
  namespace,conflict,gitignore}` and `internal/{gitfetch,contentkit,cachefs}` are
  deleted, gemini with them. `ach` keeps the platform-API client, auth and keys,
  and gains the Environment→manifest translator: it selects one model, selects
  one prompt, and rewrites A2A agents as MCP servers.
- **`ach-agent`**: `src/ach_agent/engine/hydrate.py` stops projecting; it writes a
  manifest and runs `ap manifest apply --manifest -`, validating with
  `ap manifest render`. The
  harness — channels, limits, memory, cost — stays in Python and stays out of
  SPEC v0.5.
- **`ach-runtime`**: `--hydrator-image` points at `ap`'s image; the wrapper goes.
  This is the phase that supplies `--prune`'s trigger — a pod must match its
  `CapabilityProfile` exactly. Build convergence here, against a real caller, not
  in Phase 7 against none.
- **`ccplugin`**: archived.

**Exit:** one implementation of "install these capabilities into this agent",
imported by `ach`, driven by `ach-agent`, shipped inside `ach-runtime`, and used
directly as `ap`.

---

## Explicitly out of scope for v1

Carried from SPEC §38 so no phase quietly grows one: universal runtime semantics,
a universal runtime-native package format, multiple inheritance, remote
`extends`, generic patch/merge operators, `${...}` in the manifest, placeholder
expansion in MCP args, `requires`, OCI sources, `dependencies`, prompt/artifact
marketplaces, `extends` search paths, an `abstract` flag, absolute artifact
destinations, a lockfile and reproducibility across machines, gemini-cli, a JSON
Schema document, renaming a marketplace item on install, and Windows for the
launcher.

(Corrected 2026-08-26: this list previously carried "archive sources" and "an
ownership state file", both of which v0.6 reversed and Phases 3 and 4 build. A
scope list that contradicts the phases above it protects nothing.)

### Deferred out of v1 — designed, not built

Three cuts taken 2026-08-26 after a scope audit. Each is **additive later**:
none changes the schema or the ledger, so none needs a migration when it lands.
SPEC §38 carries the same three with the same triggers.

| Deferred | Why not now | Trigger to build it |
|---|---|---|
| **a project (user repository) root** — the `claude:./` reference form | The two v1 roots are one mechanism: point the agent's config variable at a directory. A project is a *second* mechanism — the agent reads its working directory, nothing is redirected — and it is the only root needing a containment rule plus a per-agent list of which project-root files each agent genuinely reads. That list grows every release, which is the shape `CLAUDE.md` already rejected once for `--from-base`. `ach-cli` covers this case today. | A consumer that must hydrate a repository and is not already served by `ach-cli`. |
| **`--prune` / whole-root convergence** | A set difference over `ap uninstall`, which is in v1 and is what actually shapes the ledger. Prune needs nothing recorded that single-resource removal does not already record. Its only named consumer is Phase 9's init container. | `ach-runtime` shipping `ap`'s image (Phase 9). |
| **a second, cache-level lock** | Phase 3 publishes cache entries atomically — temp dir, rename, sweep — so a half-written entry cannot be observed, which is the only failure a cache lock prevents. Concurrent resolvers waste one fetch. A second lock costs a lock-ordering rule and the deadlock that rule exists to exclude. | Measured contention worth an ordering rule. |

Two limits are stated, not fixed: ap cannot tell whether a resolved source
honoured the namespace (§8.5), and a manifest is only as reproducible as its
sources — `@latest` is whatever it was that day (§14).
