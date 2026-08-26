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
| 1 | `Node`/parsing (`yaml.go`), `Merge` (`compose.go`), `extends` folding (`extends.go`), `Decode`/`Profile` (`profile.go`), `Effective` (`effective.go`), `Render` (`render.go`), `ap render`/`ap validate` |
| 2 | Input *resolution* (reading a bound secret/variable's value), `--strict` promoting `Warning` to error, `ap apply --dry-run` |
| 3 | Source *fetching* (git/local), the lockfile-free reproducibility model (§32) |
| 4 | `SKILL.md` contract checks (§23) — needs a resolved source |
| 5 | Materialization, the ledger, apply lifecycle (§33, §37) |
| 6 | Marketplace item resolution (§21–§22), the `ref` grammar, top-level `plugins:` as a common resource type, runtime-native plugin schemas (`Runtime.Plugins` stays an unvalidated `*Node` tree until then, open question §40.1) |

`Effective` (`effective.go`) is Phase 1's single entry point: it is what
Phase 2 onward composes against, and `ap validate`/`ap render` are the only
consumers of it that exist yet.

## `spec-35-execute.yaml` is SPEC §35 with three Phase-1 scope gaps closed, not silently glossed

`pkg/schema/testdata/spec-35-execute.yaml` is the acceptance fixture for
`TestTheSpecsFullExampleComposesForBothOfItsTargets` — it has to compose for
both `claude` and `opencode`, the way the spec's own closing note about
`company-review` claims. It is **not** a byte-for-byte copy of §35's YAML,
because three constructs in that example are outside what Phase 1 decodes.
Each is a deliberate, scoped omission, not a bug found late:

- **No top-level `plugins:` block.** §35 lists a `code-review` plugin
  resource at the manifest root. `Decode`'s `onlyKeys` call does not admit
  `plugins` as a top-level key — it becomes a common resource type in Phase 6
  (`docs/superpowers/plans/2026-08-26-open-decisions.md`, decision S6). The
  fixture drops the block entirely rather than inventing a decoder for it.
  The `acme-plugins` **marketplace** entry (`type: plugins`) stays, because
  `Marketplace.Type` already accepts `"plugins"` — it is the plugin *resource*
  under `plugins:`, not the marketplace that lists it, that is deferred.
- **Git-source `auth` is flat, not `{scheme, value_from}`.** §35 writes
  `auth: {scheme: basic-oauth2, value_from: {secret: gitlab-token}}` under a
  git source. `GitSource.Auth` is `*ValueFrom`, decoded by `decodeGitSource`
  straight off the `auth` key with no `scheme` field and no nested
  `value_from` wrapper — confirmed by reading `decodeGitSource` and
  `ValueFrom`'s fields (`Secret`, `Variable`, nothing else). The fixture
  writes `auth: {secret: gitlab-token}`. `auth.scheme` is real spec text
  (decision 46) but was never wired into `profile.go`; closing that gap is
  future work, not something this fixture should paper over by inventing a
  shape the decoder does not accept.
- **`extends: ./spec-35-coding-base.yaml`, not the bare `extends: coding-base`
  from the spec text.** `resolveExtends` (`extends.go`) turns a bare name into
  `<manifest dir>/<name>.yaml`, so the literal spec text would look for
  `coding-base.yaml`. The fixture's parent is named
  `spec-35-coding-base.yaml` instead, to keep every fixture for this test
  prefixed and grouped, so the `extends` value takes the relative-path branch
  (it contains a `/`) rather than the bare-name branch — both branches are
  exercised elsewhere by `pkg/schema/extends_test.go`, which this fixture does
  not need to re-prove.

`variants` entries also use the bare-list shape (`brainstorm: - --effort=xhigh`)
rather than §35's nested `variants.brainstorm.args: [...]` — `decodeVariants`
passes a variant's value straight to `decodeStringList`, which requires a
`Sequence`, not a mapping with an `args` key. This is the same gap Task 11's
own test fixture hit first; `docs/superpowers/plans/*` §35 text has not been
reconciled with `decodeVariants`'s real shape, so treat the nested `args:`
form in the spec as aspirational until that reconciliation happens.
