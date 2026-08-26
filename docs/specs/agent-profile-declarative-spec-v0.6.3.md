# Agent Profile Declarative Specification — SPEC v0.6.1

**Status:** Draft — supersedes v0.5, which was frozen pending implementation evidence
**Revision:** v0.6.3 — four architecture decisions closed, each with its reintroduction trigger in §38: `model` materializes into per-adapter FILES rather than a launcher-carried environment (§9, §15.1); `prompt` is out of v1 (§10); marketplaces are scoped to same-repo entries, which removes §21.2 entirely and the ledger's second arm with it (§21, §33.1, §35.1); Windows is a spawn-semantics target rather than a non-goal. Decisions 54-57 added.
**Date:** 2026-08-26
**Scope:** Declarative definition of agent environments, runtime specialization, reusable capabilities, source resolution, materialization and lifecycle
**Primary reference implementation:** `ackstorm/agent-profile` (`ap`)
**Consumers:** `ap` (CLI), `ackstorm/ach` (Go, imports the library), `ackstorm/ach-agent` (Python, drives the binary), `ackstorm/ach-runtime` (ships it as an init container)
**Implementation baseline:** `ackstorm/ach` (`ach-cli`) packages — single-writer lock, atomic publication and tmp sweep, download verification, hash discipline, JSON/TOML resource-key merge, the install ledger, the projection rule engine.

---

## Why v0.5 unfroze

v0.5's status line required implementation evidence for any further change. Three
things supplied it: `agent-profile` became the single hydrator for four
consumers, `ach-cli`'s shipped behaviour was read rather than assumed, and a
private-GitLab marketplace scenario was walked end to end. Every change below
traces to one of those.

## Changes from v0.5

**Reversals.** Each was a deliberate v0.5 position; each is reversed with its reason.

- **The ledger exists (§33, §33.1).** v0.5 decision #41 said "no ownership state
  file exists in any version" and §33 said "apply never deletes", on the
  reasoning that ownership state drifts from disk and then lies. `ach-cli`'s
  shipped design answers that: a per-file hash means an edited file is reported
  and skipped rather than removed, and recorded dotted keys mean a merged file
  loses only what was contributed. Without a ledger this specification cannot
  describe uninstall, which its consumers already have.
- **`archive` returns (§18).** v0.5 removed it and recorded the exact
  reintroduction trigger: "direct consumption of ACH capability manifests, which
  serve content as archive download URLs". ACH is now a consumer. Checksum
  verification is mandatory — an archive is not content-addressed the way a git
  SHA is.
- **The lockfile is out of v1 (§32).** v0.5 specified a workspace lockfile,
  `--frozen`, append-only consumption and a commit boundary. All removed.
  `ref: main` means whatever `main` is today. `ach-cli` ships with no lockfile and
  its drift reporting works from the ledger's recorded SHA, which is the evidence
  that this is sufficient. Reproducibility across machines is explicitly not a v1
  goal; the reintroduction trigger is recorded in §32.

**Amendments.**

- **`plugins` is a common resource type (§24).** v0.5 said "the common
  specification does not define one universal plugin format", while §25 already
  conceded that the plugin marketplace contract is "a de-facto standard
  applicable beyond one runtime, hence declarable at common level". A hydrator
  that cannot install a plugin cannot replace the tools it is replacing, and
  per-runtime routing is what makes one plugin work on four of them.
- **Two materialization roots (§26.1, §33.2).** A root is a parameter with two
  values: the agent's real configuration directory, or a named profile's
  namespace. Both are the same mechanism. v0.5's non-goal on workspace
  destinations therefore stands, and a project root is deferred rather than
  removed (§38): it is a third mechanism carrying a containment rule of its own.
- **Apply is additive, and nothing in v1 makes it otherwise (§33).** A manifest
  that stops declaring a resource does not remove it. Removing one resource is an
  explicit operation bounded by the ledger (§33.3); whole-root convergence is
  deferred (§38).

**Additions found by walking a private-GitLab scenario.**

- **`auth.scheme` on the git source (§17).** A self-hosted GitLab returns 401 on
  `Bearer` and 200 on `Basic base64("oauth2:"+token)` — measured. v0.5's `auth`
  had only `value_from`, leaving such a manifest unfixable.
- **A marketplace catalogue is same-repo (§21.1).** Its entries may not name
  another repository, and one that does is a resolution error. v0.6 guarded the
  cross-repo hop instead (§21.2); v0.6.3 removes the hop, which removes the
  guard's subject. The reasoning survives as the reason the restriction is an
  error rather than advice: the manifest author does not write the catalogue.
- **Imperative secret bindings (§13.1).** Commands that install one capability
  have no `inputs` block. They take a binding, and record its name, never its
  value.

**Removals.** gemini-cli is not a supported runtime: it is deprecated. Four
runtimes — claude, codex, opencode, pi.

**Unchanged and worth restating**, because they were questioned and survived:
`model` remains singular — a capability catalogue is plural, a hydrated
environment is not, and the selection belongs in the producer's translator.
(`prompt` was singular for the same reason and is out of v1 entirely as of
v0.6.3; see §10.) A2A agents need no type: a translator emits them as MCP servers.
Guardrails are not a hydratable resource.

---

# 0. Goal

`agent-profile` should evolve from YAML that embeds installation commands into a declarative description of an agent environment.

Instead of:

```yaml
install:
  - claude plugin marketplace add ...
  - claude plugin install ...
  - npx ...
```

the profile describes desired state:

```yaml
skills:
  executing-plans:
    source:
      git:
        url: https://github.com/obra/superpowers.git
        subpath: skills/executing-plans

mcps:
  memory:
    transport:
      type: http
      url: https://memory.company.com/mcp
```

Runtime adapters are responsible for turning the effective profile into native runtime configuration.

The design goal is not a universal agent runtime.

The design goal is:

> A declarative, inspectable and reusable description of an agent environment that standardizes genuinely common concepts while keeping runtime-specific concepts explicit.

---

# 1. Core design principles

## 1.1 Declarative desired state

Profiles describe what should exist, not how to install it.

The primary model:

```text
Manifest (an INPUT)
    ↓
resolve inheritance
    ↓
effective profile
    ↓
runtime adapter
    ↓
native materialization  →  the LEDGER (the STATE)
```

Imperative installation scripts are not part of the common declarative model.

**A manifest is an input, not state.** It is a portable definition: written once,
moved between machines, shared, applied, and discarded if you like. It is not the
record of what is installed. That record is the ledger (§33.1), and it is what a
second input — installing one capability at a time (§13.1) — writes to as well.
Nothing ever writes back to a manifest.

Apply is additive: it never removes what a manifest has stopped declaring.
Removing something is a separate, explicit act (§33.3).

## 1.2 Common where semantics are truly common

The common layer defines:

```text
model
skills
plugins
mcps
artifacts
inputs
sources
marketplaces
```

`plugins` is common as of v0.6 — see §24 for why, and for what stays
runtime-specific about it.

Runtime-specific concepts remain under:

```yaml
runtimes:
  claude:
    ...
  codex:
    ...
  opencode:
    ...
```

Examples:

```text
native marketplaces
packages/extensions
runtime environment
variants
```

The specification MUST NOT invent fake portability where runtimes differ materially.

---

## 1.3 Resource identity is the mapping key

Named resource collections are YAML mappings.

Example:

```yaml
skills:
  ponytail:
    source: ...

  superpowers:
    source: ...
```

The mapping key is the logical resource name.

Conceptually:

```text
skills.ponytail.name = "ponytail"
```

There is no redundant:

```yaml
- name: ponytail
```

inside these collections.

This applies to named resource collections:

```text
skills
mcps
artifacts
marketplaces
```

This structure allows ordinary object merge semantics to compose resources naturally.

`model` is a singular object, not a collection (see §9). `prompt` was one and is
out of v1 (§10).

---

# 2. Top-level profile shape

A concrete profile may look like:

```yaml
version: 1
name: execute

extends: coding-base

targets:
  - claude
  - codex
  - opencode

model: {}
inputs: {}
marketplaces: {}
skills: {}
mcps: {}
artifacts: {}

runtimes: {}
```

A reusable base profile is simply a profile without `targets`:

```yaml
version: 1
name: coding-base
```

A profile without `targets` cannot be applied directly (see §5.3).

---

# 3. Composition and merge semantics

The composition model MUST remain small and predictable.

## 3.1 Three rules

For inheritance and runtime overlay composition:

> 1. Mappings merge recursively.
> 2. Non-mapping values replace.
> 3. Schema-marked unions replace as a unit when the overlay selects a different branch or member.

Examples of non-mapping values:

```text
strings
numbers
booleans
null
lists
```

No implicit list append exists.

`null` is a non-mapping value with defined reset semantics — see §3.7.

---

## 3.2 Mapping merge

Base:

```yaml
skills:
  ponytail:
    source:
      git:
        url: https://github.com/DietrichGebert/ponytail.git

  superpowers:
    source:
      git:
        url: https://github.com/obra/superpowers.git
```

Child:

```yaml
skills:
  pdf:
    source:
      local:
        path: ./skills/pdf
```

Effective:

```yaml
skills:
  ponytail:
    source:
      git:
        url: https://github.com/DietrichGebert/ponytail.git

  superpowers:
    source:
      git:
        url: https://github.com/obra/superpowers.git

  pdf:
    source:
      local:
        path: ./skills/pdf
```

Resources are therefore added naturally by mapping key.

---

## 3.3 Partial override

Base:

```yaml
model:
  type: anthropic
  model: claude-opus-5
  parameters:
    effort: high
    temperature: 0.2
```

Child:

```yaml
model:
  parameters:
    effort: xhigh
```

Effective:

```yaml
model:
  type: anthropic
  model: claude-opus-5
  parameters:
    effort: xhigh
    temperature: 0.2
```

---

## 3.4 Lists replace

Base:

```yaml
targets:
  - claude
  - codex
```

Child:

```yaml
targets:
  - opencode
```

Effective:

```yaml
targets:
  - opencode
```

The same rule applies to runtime `args` and any other list.

---

## 3.5 Unions

Some configuration points represent a discriminated choice. v1 marks them in the schema — never hardcoded per type in the merge engine. Two forms exist.

### 3.5.1 Branch-keyed unions

A single key whose value selects one branch. v1 instance: `source` (`git | local`):

```yaml
source:
  git:
    ...
```

or:

```yaml
source:
  local:
    ...
```

but never both.

Composition rule:

- If the overlay selects the **same branch** as the inherited value, ordinary mapping merge applies within that branch (e.g. base declares `git.url`, child overrides `git.ref`).
- If the overlay selects a **different branch**, the entire union object is replaced by the overlay value. Branches never coexist in the effective profile.

Base:

```yaml
source:
  git:
    url: https://github.com/acme/repo.git
```

Child:

```yaml
source:
  local:
    path: ./repo
```

Effective:

```yaml
source:
  local:
    path: ./repo
```

### 3.5.2 Exclusive groups

A set of sibling keys within an object, of which exactly one may exist. v1 instances:

- the resource locator group on named resources: `source | ref`;
(The prompt locator group `content | source` went with §10 in v0.6.3.)

Composition rule:

> If an overlay declares any member of an exclusive group, all other inherited members of that group are discarded. Declaring the same member composes normally.

Keys outside the group compose normally: an overlay setting only `enabled: false` does not touch the locator.

Base:

```yaml
skills:
  pdf:
    ref: pdf@anthropic-skills
```

Child:

```yaml
skills:
  pdf:
    source:
      git:
        url: https://github.com/acme/skills-fork.git
        subpath: pdf
```

Effective: the child's `source` wins; the inherited `ref` is discarded. Without this rule the effective resource would carry both locators and fail locator validation — making it impossible to override a marketplace-backed resource with a direct source.

Schema validation still enforces branch/member exclusivity within a single authored document.

---

## 3.6 No merge mini-language

v1 does not define:

```yaml
merge: append
merge: replace
merge: drop
```

or equivalent generic patch operators.

Deletion from effective behavior is represented through resource state, not merge operations.

---

## 3.7 `null` resets singular values

Post-merge, a `null` value is treated exactly as if the key were absent; schema validation operates on that view.

```yaml
model: null
```

in a child yields an effective profile without `model` — runtime-native defaults and credentials (the subscription/OAuth case, see §9). The same applies to scalar keys.

`null` is valid only for singular objects and scalars. `null` on a named-collection entry is a **validation error**:

```text
skills.x: null is not valid; use `enabled: false`
```

Named resources already have a state mechanism that keeps them visible in diagnostics; a second, subtly different disable semantics is not introduced. There is no deep-null and no special meaning for `null` inside lists.

---

# 4. Resource enablement

Named resources MAY support:

```yaml
enabled: false
```

Default:

```text
enabled = true
```

Example:

Base:

```yaml
skills:
  ponytail:
    source:
      git:
        url: https://github.com/DietrichGebert/ponytail.git

  superpowers:
    source:
      git:
        url: https://github.com/obra/superpowers.git
```

Runtime override:

```yaml
runtimes:
  opencode:
    skills:
      ponytail:
        enabled: false
```

The effective profile retains the resource for diagnostics:

```yaml
skills:
  ponytail:
    enabled: false
    source:
      git:
        url: https://github.com/DietrichGebert/ponytail.git

  superpowers:
    source:
      git:
        url: https://github.com/obra/superpowers.git
```

but `ponytail` is not materialized or updated during this apply.

This supports:

- `render`;
- `explain`;
- debugging inheritance;
- runtime-specific disabling.

`enabled: false` is desired-state semantics, not a merge operation.

> `enabled: false` excludes the resource from the current apply. It does not remove previously materialized state.

This is intentionally consistent with the v1 **merge-overwrite-never-delete** apply policy. A resource materialized by an earlier apply may therefore remain physically present after it becomes disabled.

When a resource declares `enabled: false` and its previously materialized path still exists in the profile namespace, apply MUST warn:

```text
WARNING:
skill "company-review" is disabled but previously materialized state remains
```

Detection is ledger-driven (§33.1): a disabled resource whose ledger record still holds files or contributed keys triggers the warning — merged keys inside structured configuration files included.

---

# 5. `extends`

## 5.1 Single inheritance

v1 supports a single direct parent:

```yaml
extends: coding-base
```

Inheritance chains are allowed.

Multiple inheritance is out of scope.

---

## 5.2 Resolution

Accepted forms:

```yaml
extends: coding-base
```

or:

```yaml
extends: ./bases/coding-base.yaml
```

For name lookup, resolution is:

1. current manifest directory;
2. error.

There are no configured search paths.

Resolution MUST be local and deterministic.

The resolver MUST NOT implicitly search:

- GitHub;
- package registries;
- marketplaces;
- arbitrary remote locations.

---

## 5.3 Base profiles

A base profile is a profile without `targets`. There is no `abstract` flag.

A base participates in inheritance but cannot be applied directly; attempting to apply one fails:

```text
profile "coding-base" declares no targets — it is a base profile
```

---

# 6. `default`

`default` is a first-class profile.

It has exactly the same declarative capabilities and apply semantics as any other profile (see **Apply policy**).

Example:

```yaml
name: default

model: ...
skills: ...
mcps: ...
artifacts: ...

runtimes:
  claude:
    plugins: ...
    variants: ...
```

Its only special behavior is:

> `default` targets the runtime's native/default profile namespace instead of an isolated named profile namespace.

Examples:

```text
claude::default
codex::default
opencode::default
```

A deployment MAY use only `default`.

This is useful for:

- corporate environments;
- managed developer workstations;
- containers;
- shared MCP/plugin/skill configuration;
- standard developer agent environments.

---

# 7. Targets and runtime overlays

Concrete profiles declare target runtimes:

```yaml
targets:
  - claude
  - codex
```

Runtime-specific configuration lives under:

```yaml
runtimes:
  claude:
    ...

  codex:
    ...
```

---

## 7.1 Inherited non-target runtime blocks

If a parent defines:

```yaml
runtimes:
  claude: ...
  opencode: ...
```

and the child targets only:

```yaml
targets:
  - claude
```

the inherited `opencode` block is ignored silently.

This allows broad reusable bases.

---

## 7.2 Locally declared non-target runtime blocks

If a concrete profile declares:

```yaml
targets:
  - claude

runtimes:
  opencode:
    ...
```

the implementation MUST warn.

With `--strict`, the warning becomes an error.

---

# 8. Adapter degradation

Adapters MUST NOT silently discard active concepts.

For an active configuration item the adapter may:

```text
materialize
warn and skip
error
```

Unsupported common concepts:

```text
warning by default
error under --strict
```

Example:

```text
WARNING:
runtime "foo" does not support common skill resources;
skipping skill "security-review"
```

Silent drop is forbidden.

Normal errors remain errors regardless of `--strict`, including:

- invalid profile;
- malformed source;
- missing required secret;
- failed source retrieval;
- invalid skill contract;
- invalid native plugin contract.

---

# 9. Model

`model` is a singular, optional object.

```yaml
model:
  type: anthropic
  base_url: https://llm.company.com
  model: claude-opus-5[1m]

  auth:
    type: bearer
    value_from:
      secret: llm-token

  headers:
    X-User:
      value_from:
        variable: user-email

  parameters:
    effort: high
```

Semantics:

```text
type         provider/protocol family
base_url     endpoint
model        concrete upstream model identifier
auth         endpoint authentication (optional)
headers      provider request metadata headers
parameters   provider/runtime parameters
```

Rules:

- `model` is optional. Absent → the runtime uses its native default model configuration **and** native credentials. This is the subscription/OAuth case: declare nothing, the runtime authenticates itself.
- `auth` is optional. Absent → runtime-native credential mechanism, even when `base_url` or `model` are declared.
- v1 `auth`:

```yaml
auth:
  type: bearer
  value_from:
    secret: llm-token
```

- The adapter maps `auth` to the runtime's native mechanism. Adapters MUST NOT require raw `Authorization` header passthrough to express endpoint authentication.

**Changed in v0.6.3: `model` materializes into FILES, per adapter.** An earlier
draft carried it as an environment the launcher exported, on the reasoning that
§9 describes variables and there is no configuration key to merge them into.
That reasoning was wrong — every runtime has a native destination — and the
mistake had a specific cost: an environment only the launcher exports is unread
wherever the launcher is not in the path, which is exactly the deployment that
needs it most. An init container hydrates, the main container executes the
runtime directly, and the whole block vanishes in silence.

| Runtime | Destination | Shape |
|---|---|---|
| claude | `settings.json` | the `env` map: `ANTHROPIC_BASE_URL`, `ANTHROPIC_MODEL` as literals |
| codex | `config.toml` | `model_providers.<id> = { base_url, env_key }` |
| opencode | `opencode.json` | `provider.<id> = { baseURL, apiKey: "{env:<binding>}" }` |
| pi | none measured | §8 degradation: warn and skip |

The credential is never written. Each destination names the BINDING: codex's
`env_key` and opencode's `{env:…}` are the runtimes' own mechanisms, and claude
reads its token from the environment as it always has. Presence is checked at
resolution; a variable missing at run time is the runtime's own failure, not
this specification's problem.

A runtime with no measured destination is a §8 degradation and not an invention.
A guessed path writes to a name the agent never opens, which is worse than a
warning because nothing reports it.
- `Authorization` as a key inside `model.headers` is a **validation error**. Endpoint authentication has exactly one representation: `auth`.
- `headers` carries request metadata only (`X-User`, `X-Tenant`, ...).

---

# 10. Prompt (removed in v0.6.3)

`prompt` is out of v1. See §38.

It was specified as a singular resource with `content` | `source` and an
`append` | `replace` mode. Nothing about that description was wrong; what was
missing was any measurement of how the four runtimes actually consume a managed
system prompt, and this specification does not describe mechanisms it has not
watched work.

The evidence gathered before removing it is archived with the decision, so it
does not have to be re-gathered: claude takes `--system-prompt` and
`--append-system-prompt` as LAUNCH ARGUMENTS rather than a destination, which
makes it a launcher concern and not a materialized file; codex appends through
`$CODEX_HOME/AGENTS.md`, with a `model_instructions_file` config key whose
semantics were not measured; opencode has an `instructions` array plus an
auto-loaded `AGENTS.md` beside its config; pi was not located.

Reintroduction trigger, recorded so it is not re-argued: **a real consumer
needing a managed system prompt.**

---

# 11. Inputs

Inputs represent externally supplied values required to resolve or apply the effective profile.

Example:

```yaml
inputs:
  variables:
    user-email:
      env: USER_EMAIL

  secrets:
    memory-token:
      env: MEMORY_TOKEN

    github-token:
      file: /run/secrets/github-token
```

Runtime-scoped inputs are allowed:

```yaml
runtimes:
  opencode:
    inputs:
      secrets:
        npm-token:
          env: NPM_TOKEN
```

Input objects compose through ordinary mapping merge.

---

# 12. Required input calculation

Required inputs are computed after the effective profile is known.

Conceptually:

```text
extends
    ↓
profile overlay
    ↓
runtime overlay
    ↓
enabled/disabled state
    ↓
effective active resources
    ↓
discover referenced inputs
```

Only references from active effective resources contribute requirements.

Example:

```yaml
skills:
  company-review:
    source:
      git:
        url: https://gitlab.company.com/skills.git
        auth:
          value_from:
            secret: gitlab-token

runtimes:
  opencode:
    skills:
      company-review:
        enabled: false
```

For OpenCode:

```text
gitlab-token is not required
```

A referenced input with no declared binding fails during input resolution — not at schema validation — with an error naming the referencing resource and the missing input:

```text
skill "company-review" references secret "gitlab-token"
no binding declared in inputs
```

`--dry-run` (see **Apply lifecycle**) executes the full resolution phase and is the sanctioned way to surface unresolved or undeclared inputs before touching anything.

---

# 13. Secrets

v1 secret inputs resolve from exactly one of:

```text
env
file
```

Example:

```yaml
inputs:
  secrets:
    memory-token:
      env: MEMORY_TOKEN

    github-token:
      file: /run/secrets/github-token
```

Rules:

- exactly one of `env` or `file`;
- referenced active secrets MUST resolve before materialization starts;
- missing required secret MUST abort apply;
- disabled resources do not require their secrets;
- secret values MUST NOT appear in manifests, the ledger or diagnostics;
- secret values MUST be redacted from logs, `render`, `explain` and errors.

External secret systems such as Vault may populate an environment variable or file before apply.

The common v1 schema does not model external secret providers.

How resolved secrets reach materialized runtime configuration is defined in **Secret materialization**.

---

## 13.1 Bindings without a manifest

**New in v0.6.** A manifest carries `inputs.secrets`. Installing a single
capability has no manifest, so the binding is supplied at the call site:

```text
--auth-secret-env <VAR>     read from an environment variable
--auth-secret-file <path>   read from a file
```

Two rules, and the first is where this specification deliberately diverges from
its own implementation baseline:

- **The value is never persisted.** `ach-cli` stores tokens in a
  `credentials.json` at mode `0600`. §34 forbids that: a resolution-time consumer
  "MUST NOT persist it". Only the binding — the variable name or the file path —
  is recorded, in the ledger (§33.1).
- **The cost is stated, not discovered.** The variable must be present in the
  environment on every run, where a tool that stores the token asks once. That is
  the trade: the implementation never holds a credential on disk.

An export (§35.2) must synthesise an `inputs.secrets` entry from a recorded
binding, deriving a stable logical name, or the exported manifest references a
secret nothing declares and fails its own validation.

---

# 14. Header values

Headers support literal or typed sourced values.

Example:

```yaml
headers:
  Authorization:
    prefix: "Bearer "
    value_from:
      secret: memory-token

  X-User:
    value_from:
      variable: user-email

  X-Tenant:
    value: acme
```

Rules:

- exactly one of `value` or `value_from`;
- `prefix` is optional;
- `prefix` is valid only with `value_from`;
- `prefix` is prepended to the resolved input;
- no generic string expression language.

The common schema intentionally avoids:

```text
${...} template expressions in the manifest
append expressions
arbitrary templates
```

(`${VAR}` appears in *materialized* runtime configuration as the runtime's own expansion syntax — see **Secret materialization** — never in the manifest.)

Note: the `Authorization` example above applies to MCP transport headers, where it MUST use `value_from` — a literal `value` for `Authorization` is a validation error (a literal there is a secret embedded in the manifest, which §13 prohibits). Inside `model.headers` the key is banned entirely (see **Model**).

---

# 15. Environment

There is no common root-level runtime environment field.

This is not valid common configuration:

```yaml
environment:
  DEBUG: "true"
```

Runtime environment is runtime-specific:

```yaml
runtimes:
  claude:
    environment:
      DEBUG: "true"
      COMPANY_REGION: eu-west-1
```

A runtime adapter may materialize it only when the runtime provides an appropriate profile-scoped/native mechanism.

Unsupported environment configuration:

```text
warning by default
error with --strict
```

## 15.1 Precedence

Adapters inject managed variables derived from declarative configuration (e.g. base URL and auth token derived from `model`).

Precedence:

> Adapter-managed variables are applied first. User-declared `runtimes.<runtime>.environment` is applied last and **wins** on collision.

**Amended in v0.6.3.** Where a runtime has a native, profile-scoped destination
for both — claude's `settings.json` `env` map is the only measured one — the
managed variables and the user's share that block, so this precedence is a
property of ONE merge rather than of two mechanisms that might disagree about
which ran last. A runtime with no such destination materializes no environment
at all, which §15 already required.

Overriding a managed variable is the deliberate escape hatch. The adapter SHOULD log a notice when it happens, so the `model` block does not silently lie:

```text
NOTICE:
runtimes.claude.environment overrides managed variable ANTHROPIC_BASE_URL
```

## 15.2 No global mutation

Agent Profile MUST NOT mutate global shell or OS configuration such as:

```text
~/.bashrc
~/.zshrc
/etc/environment
```

Input resolution from environment variables is separate:

```yaml
inputs:
  secrets:
    token:
      env: TOKEN
```

means:

> read `TOKEN` while applying the profile

not:

> configure `TOKEN` in the target agent environment.

---

# 16. Sources

Sources describe where a common artifact comes from.

v1 source families:

```text
git
local
marketplace (via ref)
```

The specification does not classify them into `core-resolved`, `delegated`, etc.

The relevant resolver or adapter handles them.

---

# 17. Git source

Example:

```yaml
source:
  git:
    url: https://github.com/obra/superpowers.git
    ref: main
    subpath: skills/executing-plans
```

Private source:

```yaml
source:
  git:
    url: https://gitlab.company.com/agents/assets.git
    ref: v2
    subpath: skills/internal

    auth:
      scheme: basic-oauth2
      value_from:
        secret: gitlab-token
```

Fields:

```text
url
ref
subpath
auth.scheme       bearer | basic-oauth2   (optional)
auth.value_from
```

## 17.1 `auth.scheme`

New in v0.6, and it exists because a manifest without it can be unfixable.

```text
bearer         Authorization: Bearer <token>
basic-oauth2   Authorization: Basic base64("oauth2:" + <token>)
```

Honored by github.com, bitbucket.org and gitlab.com (≥15.x): `bearer`. **A
self-hosted GitLab configured without Bearer support honors only
`basic-oauth2`** — measured against a real instance: `Bearer` → 401,
`Basic oauth2:<token>` → 200.

Rules:

- `scheme` is optional. When absent it is **inferred from the host**: a host
  containing `gitlab`, or beginning `git.`, yields `basic-oauth2`; anything else
  yields `bearer`.
- An explicit `scheme` always wins over inference.
- **An implementation MUST report the scheme it used** — in a successful
  resolution's diagnostics and, mandatorily, in a `401` error. Inference that is
  invisible when it succeeds is undebuggable when it fails, and a self-hosted
  GitLab on a host named neither `gitlab*` nor `git.*` is exactly that case.

Inference is admitted here, against this specification's general preference for
stated over inferred, on a narrow basis: it is a **protocol default with a
declared escape hatch and a mandatory disclosure**, not a guess about where an
author's argument belongs.

## 17.2 Credential transport

The credential MUST be sent as an HTTP header and MUST NOT be placed in the URL:

```text
git -c http.extraHeader="Authorization: <scheme> <credential>" <subcommand>
```

Two reasons, both concrete: a credential in the URL position is visible in
`/proc/<pid>/cmdline` to every process on the machine, and git persists it on
disk in `remote.origin.url`.

The credential MUST NOT be sent over non-TLS transport: a source or marketplace
entry whose URL is `http://` and would carry a credential is an error. This
applies wherever a credential travels — §17 and §18 alike. (It applied to §21's
second hop too, until v0.6.3 removed the hop; §17.2 itself is general and
unaffected.)

## 17.3 Composition and host changes

`auth` references follow the source object through composition. When overriding
`url` — particularly to a different host — review or redeclare `auth`: the
referenced credential will be sent to the new host on the next fetch.

Where the host is chosen by someone other than the manifest's author, this is not
sufficient and a mandatory guard applies instead — see §21.2.

---

# 18. Archive source

Reintroduced in v0.6. v0.5 removed it and recorded the trigger: "direct
consumption of ACH capability manifests, which serve content as archive download
URLs". That consumer now exists.

```yaml
source:
  archive:
    url: https://ach.company.com/content/9f2a/skill.tar.gz
    digest: sha256:8a71c2e4f09b3d5c1e7a2b6f4d8c0e3a9b5f7d1c2e4a6b8d0f2c4e6a8b0d2f4c
    subpath: review

    auth:
      scheme: bearer
      value_from:
        secret: ach-key
```

Fields:

```text
url
digest      REQUIRED   <algorithm>:<hex>
subpath
auth
```

Rules:

- **`digest` is REQUIRED.** A git source is content-addressed — fetching by SHA
  is verified by git itself — and an archive is not. The digest is its only
  integrity claim, so it is not optional and there is no flag to skip it.
- v1 algorithm: `sha256`.
- The archive is verified **before** extraction, not after. A digest checked on
  extracted bytes has already run the extractor over untrusted input.
- Extraction MUST reject absolute paths, `..` components, symlinks pointing
  outside the extraction root, and entries exceeding an implementation size cap.
- A digest mismatch is an error naming the URL, the expected digest and the
  computed one. Nothing is written.
- `subpath` has its §20 meaning, relative to the archive root.

Archives are not "downloads with a checksum bolted on": the digest is what makes
the source addressable at all, which is why it participates in cache identity.

---

# 19. Local source

Example:

```yaml
source:
  local:
    path: ./agent-assets
    subpath: skills/company-review
```

Useful for:

- development;
- checked-in workspace assets;
- baked container assets.

---

# 20. `subpath`

`subpath` selects content relative to the resolved source root.

Rules:

- optional;
- relative;
- MUST NOT escape source root using `..`;
- has the same semantic meaning across supported source families where applicable.

---

# 21. Marketplaces

A marketplace is a named catalog of reusable resources.

Marketplaces are typed.

Example:

```yaml
marketplaces:
  anthropic-skills:
    type: skills

    source:
      git:
        url: https://github.com/anthropics/skills.git
        ref: main
        subpath: skills
```

Marketplace mapping keys are unique identities.

A marketplace's `type` determines which resource family may reference it.

Types:

```text
plugins
skills
```

Additional types may be added only when they represent a concrete catalog contract.

## 21.1 Item resolution contracts

Each marketplace type defines how an item name resolves:

**`plugins`** — the catalog contract is the Claude plugin marketplace manifest:

```text
<root>/.claude-plugin/marketplace.json
```

Item lookup is by the entry's `name` field.

**Changed in v0.6.3: a catalogue is SAME-REPO.** Every entry resolves inside the
marketplace's own repository and is sliced out of the already-fetched tree. A
14-entry marketplace fetches once, which is how the real catalogues are built.

An entry naming a source in a **different repository or host is a resolution
error** naming the entry, the marketplace and the offending URL. It is not
fetched, authenticated or otherwise.

That one rule removes §21.2 from this specification. With no cross-repo hop
there is no foreign host for a credential to reach, so there is nothing left to
guard. The credential and the URL now always come from the same author
declaration, which is the condition §17.3 already states is sufficient.

Reintroduction trigger: **a real catalogue needing cross-repo entries.** When it
fires, §21.2 returns as written in v0.6.2 — it is designed, and an
implementation that removed the scope restriction without restoring it would be
handing a marketplace owner every credential its users hold.

**`skills`** — item `<name>` resolves to directory `<name>` under the marketplace
root/`subpath`. The directory MUST satisfy the skill contract (`SKILL.md` at its
root).

## 21.2 (removed in v0.6.3)

The guard that stopped a marketplace's credential travelling to a host named by
one of its entries. Removed with the hop it guarded: §21.1 now scopes a
catalogue to its own repository, so no such request exists.

It is removed, not weakened. Its reasoning — that the manifest's author does not
write `marketplace.json`, so reviewing your own manifest cannot protect you —
survives untouched and is exactly why the scope restriction is an ERROR rather
than a warning. The comparison it specified (host and effective port,
case-insensitive, a subdomain is not the same host) returns with the trigger in
§21.1.

§17.2 is unaffected: a credential still never crosses a non-TLS transport, and
that rule is general rather than a property of the second hop.

---

# 22. Marketplace references

Marketplace-backed resources preserve their logical name through the mapping key.

Example:

```yaml
skills:
  pdf:
    ref: pdf@anthropic-skills
```

Grammar:

```text
<item-name>@<marketplace-name>
```

Semantics:

```text
logical profile identity   mapping key
marketplace item           <item-name>
marketplace                <marketplace-name>
```

Versioning and source pinning belong to the marketplace/source declaration, not to `ref`.

An enabled resource defines exactly one external locator:

```text
source
or
ref
```

Inline literal content is the explicit exception where a resource family
supports one.

A `ref` MUST target a marketplace whose `type` matches the referencing resource family.

---

# 23. Skills

Common `skills` represent standard Agent Skill directories.

Example:

```yaml
skills:
  executing-plans:
    source:
      git:
        url: https://github.com/obra/superpowers.git
        ref: main
        subpath: skills/executing-plans
```

The resolved skill root MUST contain:

```text
SKILL.md
```

Expected example:

```text
executing-plans/
├── SKILL.md
├── scripts/
├── references/
└── ...
```

Missing `SKILL.md` is an error.

A runtime-native concept also called "skill" but not following this contract belongs in runtime-specific configuration instead of common `skills`.

Marketplace example:

```yaml
skills:
  pdf:
    ref: pdf@anthropic-skills
```

---

# 24. Plugins

Changed in v0.6. v0.5 said "the common specification does not define one
universal plugin format" and kept plugins entirely runtime-specific.

**A plugin is now a common named resource.** §25's marketplace note already
conceded the ground, calling the Claude plugin marketplace contract "a de-facto
standard applicable beyond one runtime, hence declarable at common level"; this
completes that position for the resource itself.

```yaml
plugins:
  code-review:
    ref: code-review@acme-plugins

  ponytail:
    source:
      git:
        url: https://github.com/DietrichGebert/ponytail.git
```

## 24.1 What a plugin is

A directory whose top-level entries are **component kinds**:

```text
commands/      agents/       skills/
rules/         prompts/      mcp/        .mcp.json
hooks/         AGENTS.md
.claude-plugin/plugin.json
```

The resolved root MUST satisfy the native plugin contract
(`.claude-plugin/plugin.json`). Entries that are not component kinds — manifests,
`README.md`, `LICENSE` — are skipped silently. A **known** component kind that
the selected runtime has no destination for is reported as dropped, never
silently skipped: that is §8, and it is what tells a user "this runtime does not
support hooks" instead of leaving them to notice.

## 24.2 Routing is adapter-owned

The common layer says a plugin exists and where it comes from. **Where each
component kind lands is the adapter's**, and it differs materially: a `.md`
command is a file for one runtime and a converted `.toml` for another; agent
frontmatter is rewritten for one and passed through by another; an MCP entry is
reshaped per runtime's schema.

This is precisely why `plugins` is one common type rather than five, and why it
cannot be expressed as `artifacts` with hand-written destinations: an artifact
names one destination, and a plugin has a different one per runtime.

One routing rule is normative because it is a security property, not
formatting:

- **An adapter MUST NOT register MCP servers found in a plugin's agent
  frontmatter.** A plugin that can register an MCP server by shipping a
  frontmatter key can add a tool the user never reviewed.

A second rule — never write outside the runtime's own dot-directory — belongs
with the project root that needs it, and returns with it (§38).

## 24.3 What stays runtime-specific

A runtime's own package mechanism, where it has one and it is not this contract:

```yaml
runtimes:
  opencode:
    plugins:
      ponytail:
        package: "@dietrichgebert/ponytail"
```

A runtime block's `plugins` entry overrides the common one for that runtime, the
ordinary §30 mechanism. The exact schema of a runtime-native plugin declaration
is adapter-owned and remains an open question (§40.1).

---

# 25. MCP servers

MCP servers are common capabilities when they can be represented portably.

Remote:

```yaml
mcps:
  memory:
    transport:
      type: http
      url: https://memory.company.com/mcp

      headers:
        Authorization:
          prefix: "Bearer "
          value_from:
            secret: memory-token
```

stdio:

```yaml
mcps:
  filesystem:
    transport:
      type: stdio
      command: npx
      args:
        - -y
        - "@modelcontextprotocol/server-filesystem"
        - "."
```

`args` are literal strings. There is no placeholder or expansion mechanism in MCP arguments.

Runtime adapters materialize these into native configuration.

Unsupported common MCP support follows the normal degradation policy.

---

# 26. Artifacts

Artifacts are first-class named resources for opaque files or directories that must be present in the materialized profile environment.

An artifact is used when the resource has no stronger common semantic type such as:

```text
skill
MCP
model
```

Example:

```yaml
artifacts:
  agents-md:
    source:
      local:
        path: ./AGENTS.md

    destination: AGENTS.md
```

Git-backed:

```yaml
artifacts:
  company-rules:
    source:
      git:
        url: https://github.com/acme/agent-assets.git
        ref: main
        subpath: rules/CODING.md

    destination: references/CODING.md
```

Potential artifact examples include:

```text
AGENTS.md
CLAUDE.md
reference documents
templates
scripts
runtime helper files
certificates
configuration fragments
```

## 26.1 Destination semantics

`destination` resolves relative to the **materialization root** — the root the
apply operates on, which is a parameter with two values (§33.2).

Path rules are those of `subpath`: relative; MUST NOT escape the root via `..`;
checked after cleaning, because `a/../../b` is only visibly an escape once
cleaned.

> Unchanged from v0.5, after v0.6 briefly changed it. Workspace (user
> repository) destinations remain a non-goal (§38), and the mechanism v0.5
> reserved stays reserved: "a scope prefix can be added backward-compatibly:
> bare paths keep meaning namespace root." Bare paths mean the root today, so
> adding a project root later costs a prefix and breaks nothing written now.

Absolute destinations remain a non-goal.

`artifacts` MUST NOT become a replacement for stronger semantic resource types.
If something is clearly a `skill`, `plugin`, `MCP`, etc., it uses that
type.

---

# 27. Dependencies (removed in v0.5)

Dependency declarations are removed from the v1 common vocabulary (see §38, §39). They are reintroduced only when a concrete consumer dictates their design.

---

# 28. Derived preflight

There is no declared-prerequisites field. (`requires` from earlier drafts is removed.)

Preflight checks are **derived** from the effective profile. Before materialization, apply verifies:

- the selected runtime CLI is executable;
- every active stdio MCP `transport.command` resolves.

Failures abort during the resolution phase, before any mutation.

Tools that agent-profile itself needs (e.g. `git` for source resolution) are internal implementation dependencies, not profile concerns.

---

# 29. Variants

Variants are named runtime-specific invocations.

Example:

```yaml
runtimes:
  claude:

    variants:

      brainstorm:
        args:
          - --effort=xhigh
          - "/superpowers:brainstorming {}"

      write:
        args:
          - --effort=xhigh
          - "/superpowers:writing-plans {}"

      opus:
        args:
          - --model=claude-opus-5
          - --effort=xhigh
```

`{}` keeps the current meaning as the insertion point for user input.

---

## 29.1 Variant argv is opaque

Variant arguments are runtime-native invocation data.

They MAY duplicate, override or contradict declarative configuration elsewhere in the profile.

The core and adapters MUST NOT parse argv in order to reconcile it with:

- the model declaration;
- effort;

- permissions;
- other declarative configuration.

This drift is accepted by design.

---

## 29.2 Variant args composition

`args` is a list.

Therefore normal composition applies:

> a redefined `args` list replaces the inherited list wholesale.

---

# 30. Runtime-specific inputs and common-resource overrides

Runtime overlays may override common named resources.

Example:

```yaml
skills:
  ponytail:
    source:
      git:
        url: https://github.com/DietrichGebert/ponytail.git

runtimes:
  opencode:

    skills:
      ponytail:
        enabled: false

    plugins:
      ponytail:
        package: "@dietrichgebert/ponytail"

    inputs:
      secrets:
        npm-token:
          env: NPM_TOKEN
```

This expresses:

```text
common skill exists
        ↓
OpenCode disables common representation
        ↓
OpenCode uses its native plugin/package mechanism
```

without creating a universal plugin abstraction.

---

# 31. Effective profile

The effective profile is runtime-specific.

Conceptual lifecycle:

```text
load profile
    ↓
resolve extends
    ↓
composition (mappings / non-mappings / unions)
    ↓
select runtime
    ↓
merge runtime overlay
    ↓
validate schema
    ↓
evaluate enabled state
    ↓
compute referenced inputs
    ↓
EffectiveProfile
```

`render` or equivalent tooling SHOULD expose the effective profile without materializing it.

Sensitive values MUST remain redacted.

---

# 32. Reproducibility (no lockfile in v1)

**Removed in v0.6.** v0.5 §32 specified a workspace-scoped lockfile keyed by
source identity, `--frozen`, append-only consumption, an atomic commit boundary
and minimal URL normalization. None of it is in v1.

`ref: main` means whatever `main` is today. If tomorrow it is different, it is
different.

## 32.1 What this costs, stated rather than discovered

Two machines applying the same manifest on different days can materialize
different bytes. A manifest is portable; it is not reproducible. Anyone sharing
one is sharing a description, not a build.

This is the same trade `ach-cli` makes today, which is the evidence it is
survivable: it has no lockfile, every install re-resolves its ref, and drift is
still reportable from the ledger's recorded SHA.

## 32.2 What is NOT removed

Two things share the word "lock" and neither goes:

- **The single-writer mutex (§37.3).** Two processes must not write one root at
  once. This is `flock` / `LockFileEx`, not a dependency lockfile.
- **`resolved_ref` in the ledger (§33.1).** It is a **receipt** — what was
  installed — not a **pin**. Nothing re-uses it as a resolution input. It is what
  makes drift reporting possible without a lockfile.

The distinction is the whole of why one was removable and the other was not: a
lockfile *constrains what happens next*; the ledger *records what happened*.

## 32.3 Reintroduction trigger

Recorded so it is not re-argued from first principles: **someone needs two
machines to agree**, or a build to be reproducible from a manifest alone. Then
v0.5 §32 returns as written — it is designed, merely not built.

---

# 33. Apply policy, the ledger, and the roots

Apply semantics are **uniform across all roots**. There is no special apply mode.

- **Structured configuration files** (JSON, TOML and equivalents) merge at
  **resource-key level**: keys owned by the profile are added or overwritten; all
  other keys — including entries the user added by hand — are preserved.
- **Colliding keys are overwritten** without prompting. The profile is the source
  of truth for what it declares.
- **File and directory resources** are overwritten wholesale.
- The adapter MUST **log every key and file it overwrites**.
- **Apply is additive.** A manifest that stops declaring a resource does not
  remove it. Removal is §33.3.

## 33.1 The ledger

**New in v0.6.** v0.5 decision #41 stated "no ownership state file exists in any
version". Reversed.

The ledger is per root, and it is the **only** state.

**Materialized resources** — one record per resource, one entry per file:

```text
resource   name, kind, source or ref, resolved_ref, installed_at
file       rel_path
           hash
           merge      "" (replace) | "deep" | "composite"
           keys       deep: the dotted keys contributed
                      composite: the marker id
```

**Removed in v0.6.3: there is no second arm.**

One was specified — resolved marketplace definitions — because a manifest is an
input that is not kept (§1.1), a marketplace writes no file, and a later ad-hoc
install of `<item>@<that-marketplace>` would have nothing to resolve the name
against.

It is out of v1 with the operation that needed it. §35.1 now requires an install
to name a manifest or a direct source, so a `ref` is only ever resolved inside
the manifest that declares its marketplace, and nothing outlives it.
Reintroduction trigger: **ad-hoc install by ref.**

Secrets never enter the ledger. A binding's **name** is recorded; its value is
not (§13, §34).

### Why an ownership file is honest here

v0.5 refused one on the reasoning that it drifts from disk and then lies. Three
properties answer that:

1. **A hash per file.** On removal, a file whose hash no longer matches was
   edited by the user: the verdict is *modified*, and it is left alone. The
   ledger never claims a file it no longer recognises.
2. **Recorded keys for merged files.** Removing from a deep-merged
   `settings.json` or `config.toml` removes **only the dotted keys that were
   contributed**. The file survives; every hand-added key survives with it.
3. **One classifier for preview and action.** A dry run and a real removal share
   the same function, so the preview cannot drift from what happens.

A consequence, and an improvement over v0.5: because the ledger distinguishes the
implementation's writes from the user's, **removal is safe in the real
configuration root too** — the case v0.5 §33 gave up on when it made `default`
orphans permanent by design.

## 33.2 The two roots

A root is a **parameter**. An implementation MUST NOT infer one from the
environment.

```text
the agent's real configuration directory      e.g. ~/.claude
a named profile's isolated namespace          e.g. <profiles>/claude/plan
```

Both are **one mechanism**: the agent's own configuration-directory variable is
pointed at the chosen directory. That is why v1 has two roots and not three. A
project directory is not that mechanism — the agent reads it from its working
directory, nothing is redirected — so it needs a containment rule no other root
needs, and a per-agent list of which project-root files that runtime genuinely
reads. It is deferred whole, with its rule, in §38.

The real configuration root is the user's own environment and cannot be undone by
deleting a profile. An interactive implementation MUST display its **resolved
absolute path**, name it as the real configuration, and gate the operation. Off a
terminal it MUST refuse: a pipe is not consent.

## 33.3 Removal

One operation, bounded by the ledger. It may not remove anything the ledger does
not own.

- **Remove one resource.** Its files are removed subject to §33.1's verdicts; its
  merged keys are removed from the files that carry them.

It MUST offer a preview, and the preview MUST be produced by the same classifier
as the action.

Removal is never a side effect of apply. Apply is additive: a manifest that stops
declaring a resource leaves it alone, and a developer's machine does not lose what
they installed by hand.

**Whole-root convergence — `--prune`: remove everything the ledger owns that the
current input no longer declares — is deferred (§38).** It is a set difference
over the operation above and over the ledger this specification already requires,
so it adds nothing that must be recorded now. Its trigger is a consumer that
needs a root to match its input exactly, which describes a pod and not a laptop.

---

# 34. Secret materialization

Two invariants, scoped by consumer:

> **Runtime configuration materialization writes secret references, never secret values.** The sole exception is the explicit `--allow-plaintext-secrets` path below.

> **Resolution-time consumers** (git `auth`, future source resolvers) **MAY read a required secret value transiently** when needed to access a source, and MUST NOT persist it or include it in logs, diagnostics, error messages, the ledger, the EffectiveProfile, or materialized configuration. Fetch errors MUST NOT embed credentials (e.g. tokens inside URLs).

Two secret classes, two mechanisms for runtime references:

- **`env`-sourced secrets.** Materialized configuration references the **declared binding variable name** through the runtime's native expansion mechanism. agent-profile verifies the variable's *presence* during resolution (fail-fast when unset). When the runtime is the only consumer, the value is **never read** by agent-profile; when a source resolver consumes the same secret, the transient rule above applies. There is no launcher re-export and no synthesized name for runtime references. Consequence: invoking the runtime CLI directly — without the launcher — works wherever the environment is already populated (containers, CI).
- **`file`-sourced secrets: the only launcher-export case.** Runtimes do not read files, so the launcher reads the file and exports the value as `AP_SECRET_<NAME>`; materialized configuration references that name. The value exists only in the child process environment, never on disk. A source resolver consuming a file-sourced secret reads the file directly during resolution under the transient rule. Profiles using file-sourced secrets in runtime configuration require launch through agent-profile.

Per-adapter expansion mechanisms:

| Runtime  | Expansion in materialized config |
|----------|----------------------------------|
| claude   | `${VAR}`                         |
| opencode | `{env:VAR}`                      |
| pi       | `{env:VAR}`                      |
| codex    | native env indirection (`bearer_token_env_var` for `Bearer` `Authorization` values; `env_http_headers` for other header references) |

If the target runtime cannot materialize a secret reference through native expansion for an **active** resource, apply MUST fail during the resolution phase — the same error class as a missing required secret. Warn-and-skip is not permitted here: it would materialize a resource without its authentication, i.e. silently broken configuration.

The sole opt-in is the CLI flag `--allow-plaintext-secrets`. There is deliberately **no manifest field** — a manifest field in a shared inherited base would pre-consent plaintext for every child invisibly; consent lives with whoever runs the apply, per invocation. Under the flag, the adapter MUST emit a prominent warning listing every file that contains plaintext secret material:

```text
WARNING:
plaintext secret written to:
  ~/.codex/config.toml
```

In the `default` namespace this warning MUST be emphasized, because plaintext lands in the user's global runtime configuration rather than an isolated profile directory.

---

# 35. Full example

```yaml
version: 1
name: execute

extends: coding-base

targets:
  - claude
  - opencode

model:
  type: anthropic
  base_url: https://llm.company.com
  model: claude-opus-5[1m]

  auth:
    type: bearer
    value_from:
      secret: llm-token

  parameters:
    effort: high


inputs:

  secrets:

    llm-token:
      env: LITELLM_TOKEN

    memory-token:
      env: MEMORY_TOKEN

    gitlab-token:
      env: GITLAB_TOKEN


marketplaces:

  anthropic-skills:
    type: skills

    source:
      git:
        url: https://github.com/anthropics/skills.git
        ref: main
        subpath: skills

  acme-plugins:
    type: plugins

    source:
      git:
        url: https://gitlab.company.com/ai/agent-plugins.git
        ref: main

        auth:
          scheme: basic-oauth2
          value_from:
            secret: gitlab-token


plugins:

  code-review:
    ref: code-review@acme-plugins


skills:

  pdf:
    ref: pdf@anthropic-skills

  executing-plans:
    source:
      git:
        url: https://github.com/obra/superpowers.git
        ref: main
        subpath: skills/executing-plans

  company-review:
    source:
      git:
        url: https://gitlab.company.com/ai/skills.git
        ref: main
        subpath: review

        auth:
          value_from:
            secret: gitlab-token


mcps:

  memory:
    transport:
      type: http
      url: https://memory.company.com/mcp

      headers:
        Authorization:
          prefix: "Bearer "
          value_from:
            secret: memory-token


artifacts:

  agents-md:
    source:
      local:
        path: ./AGENTS.md

    destination: AGENTS.md


runtimes:

  claude:

    environment:
      COMPANY_PROFILE: execute

    variants:

      brainstorm:
        args:
          - --effort=xhigh
          - "/superpowers:brainstorming {}"

      opus:
        args:
          - --model=claude-opus-5
          - --effort=xhigh


  opencode:

    skills:

      company-review:
        enabled: false

    plugins:

      ponytail:
        package: "@dietrichgebert/ponytail"

    variants:

      brainstorm:
        args:
          - "/superpowers:brainstorming {}"
```

For OpenCode, `company-review` is inactive; therefore `gitlab-token` is not required for the OpenCode effective profile.

A subscription-based profile omits `model.auth` (or `model` entirely) and the runtime authenticates with its own OAuth/subscription credentials.

---

## 35.1 Operations

**New in v0.6.** v0.5 described apply and nothing else. Four consumers now need
the same vocabulary, so the operations are named here rather than left to each
implementation.

Two inputs to a root, mirroring §1.1:

```text
apply a manifest        the whole declaration, at once
install one capability  one resource, now
```

**Amended in v0.6.3.** An install names a manifest or a **direct source**. It may
not resolve a bare `<item>@<marketplace>` against a root, because v1 keeps no
record of a marketplace definition (§33.1) — a `ref` is meaningful only inside
the manifest that declares its catalogue. Trigger for lifting it: ad-hoc install
by ref.

and the operations over what a root already has:

```text
list       what the ledger holds
remove     one resource                       (§33.3)
export     the ledger, as a manifest          (§35.2)
render     what a manifest MEANS, before any root is chosen
```

`render` is the only operation with no root: its subject is a file. With no
runtime selected it composes **every** target the manifest declares and its exit
status is the answer — that is the contract check a producer runs against a
manifest it generated, and a manifest that composes for one target and not
another is broken.

A reference implementation's spelling of these is not normative. `ap` uses
`install` / `uninstall` for the single-capability pair and groups the
manifest-driven ones under `manifest`, so the surface mirrors the two inputs.

## 35.2 Export

Export serializes a root's ledger into a manifest, closing the loop:

```text
manifest → apply → ledger → export → manifest
```

It is what makes "a manifest is an input" workable in practice: an environment
assembled one capability at a time becomes portable without ever having been
written down.

Export MUST:

- emit **resolved** values where a value was inferred — notably `auth.scheme`
  (§17.1), which a reader on a differently-named host would infer differently;
- **synthesise the `inputs` block** from recorded bindings (§13.1), deriving
  logical names stably, so two exports of an unchanged root are identical;
- emit **binding names only**, never secret values;
- produce a manifest that validates and re-applies cleanly. Refs re-resolve on
  re-apply (§32): the exported manifest reproduces the environment's shape, not
  necessarily its bytes.

---

# 36. Validation summary

A conforming implementation SHOULD validate at least:

## Profile

- valid `version`;
- valid `name`;
- acyclic inheritance;
- deterministic `extends` (manifest directory or explicit relative path);
- valid targets;
- a profile without `targets` is a base and cannot be applied directly.

## Composition

- mappings merge recursively;
- non-mappings replace;
- unions: same branch/member merges, a different branch/member replaces the union or exclusive group wholesale;
- `null` on singular objects and scalars resets to absent; `null` on a named-collection entry is an error;
- effective result satisfies schema.

## Resources

- mapping key is the logical resource identity;
- disabled resources are not materialized;
- active resource locator/content is valid;
- an enabled resource has exactly one locator (`source` or `ref` — an exclusive group), inline content excepted.

## Model

- `auth` absent or valid (`type: bearer` + `value_from`);
- `Authorization` absent from `model.headers`;
- the model block reaches the runtime's measured FILE destination, never a
  launcher-carried environment (§9);
- a runtime with no measured destination warns (§8) rather than receiving a
  guessed one.

## Skills

- resolved root contains `SKILL.md`;
- marketplace references target `type: skills`.

## Plugins

- declaration is valid for the runtime adapter;
- direct plugin artifacts satisfy native runtime contract;
- marketplace references target `type: plugins`.

## Artifacts

- source resolves;
- destination is valid for materialization policy.

## Plugins

- resolved root satisfies the native plugin contract;
- marketplace references target `type: plugins`;
- a known component kind the runtime cannot route is reported as dropped (§8);
- MCP servers in agent frontmatter are NOT registered (§24.2).

## Sources

- git `auth.scheme` is `bearer` or `basic-oauth2` when present; the scheme
  actually used is reported, and named in any 401 (§17.1);
- the credential travels as a header, never in the URL (§17.2);
- an archive source declares a `digest`, verified before extraction (§18);
- a marketplace entry naming a source outside the marketplace's own repository
  is a resolution error naming the entry, the marketplace and the URL (§21.1).

## Ledger

- every materialized file is recorded with its hash and, when merged, its
  contributed keys (§33.1);
- every marketplace is recorded as a definition, with its auth binding NAME;
- no secret value appears anywhere in it;
- removal never touches a path the ledger does not own;
- a file whose hash no longer matches is reported modified and left (§33.1);
- preview and action share one classifier.

## Secrets and inputs

- every referenced active secret resolves (failures occur at resolution, with resource and input named);
- values remain redacted, including from fetch errors;
- materialized configuration contains expansion references, never values, absent `--allow-plaintext-secrets`;
- `Authorization` in MCP transport headers uses `value_from`; a literal value is an error.

## Environment

- adapter-managed variable overrides produce a logged notice.

## Runtime overlays

- inherited non-target blocks are ignored;
- local non-target blocks warn;
- unsupported active concepts warn;
- strict mode promotes degradation warnings.

---

# 37. Apply lifecycle

## 37.1 Resolution phase (no mutation)

```text
 1. load manifest (a file, or a single capability's arguments)
 2. validate basic syntax
 3. resolve extends
 4. compose (mappings / non-mappings / unions and exclusive groups)
 5. select runtime
 6. compose runtime overlay
 7. validate effective schema
 8. evaluate enabled resources
 9. compute referenced inputs
10. resolve variables/secrets                 (§13)
11. derived preflight (runtime CLI, stdio MCP commands)
12. resolve sources                           (§17, §18, §19)
13. resolve marketplace items                 (§21.1, and §21.2's guard)
14. validate skill/plugin/resource contracts
```

## 37.2 Materialization phase

```text
15. materialize common resources
16. materialize runtime-native configuration
17. materialize variants
18. write the ledger                          (§33.1)
19. finish apply
```

Rules:

- The resolution phase MUST complete successfully before any materialization
  mutation begins. Source acquisition into a cache during steps 12–13 is not
  mutation.
- **A dry run executes exactly the resolution phase and stops.** It is the
  sanctioned way to surface unresolved inputs, undeclared references, preflight
  failures and contract violations before anything is touched — which matters
  under an overwriting apply policy, where a mid-materialization failure leaves a
  root partly overwritten.
- **A dry run is not offline.** It resolves secrets, authenticates to private
  sources and fetches content, because that is the only way to check a contract.
  An implementation MUST say so: "dry run" reads as "does nothing", and here it
  authenticates.
- **The ledger is written last**, after materialization succeeds. A ledger
  claiming files that were never written is worse than no ledger, because §33.1's
  verdicts would then remove or skip on the strength of a record that was never
  true. A crash after materialization but before the ledger write leaves files
  unclaimed; re-running the apply repairs it — the same files are overwritten and
  the ledger is written.

## 37.3 Locking

One lock: a **root lock** per materialization root being mutated, held across
materialization and the ledger write. Roots are per-user global — the real
configuration directory, the shared profile directory — so two unrelated
workspaces can apply onto one root, and the root is the only place that can
serialize them.

**The cache is not locked.** Entries are published atomically: fill a temporary
directory, then rename it into place, sweeping whatever a failed fill left
behind. Two processes resolving the same source concurrently waste one fetch;
neither can observe a half-written entry, and a half-written entry served as
verified content is the only failure a cache lock would prevent. A second lock
would buy back that one fetch at the price of a lock-ordering rule and the
deadlock that rule exists to exclude.

This is a mutex (`flock`, `LockFileEx`), not a dependency lockfile; v1 has no
lockfile at all (§32).

---

# 38. Non-goals

v1 intentionally does not define:

- universal runtime semantics;
- a universal runtime-native package format (§24.3 — the *common* plugin contract
  is defined; a runtime's own packaging is not);
- workflow orchestration;
- multi-agent orchestration;
- memory semantics;
- A2A (a producer's translator emits A2A agents as MCP servers);
- guardrails as a hydratable resource;
- RBAC;
- Kubernetes lifecycle;
- multiple inheritance;
- implicit remote `extends`;
- generic patch/merge operators;
- YAML programming constructs;
- generic conditions;
- arbitrary string expressions;
- placeholder expansion in MCP arguments;
- declared prerequisites (`requires`);
- reconciliation between variant argv and declarative fields;
- global shell environment mutation;
- OCI sources;
- dependency declarations;
- artifact marketplaces;
- `extends` search paths;
- an `abstract` flag;
- absolute artifact destinations;
- **a project (user repository) materialization root** (§33.2 — deferred, not
  designed away: the two v1 roots are one mechanism, a project is a second one
  carrying a containment rule and a per-agent project-root-file list. Trigger: a
  consumer that must hydrate a repository and is not already served by
  `ach-cli`);
- **whole-root convergence, `--prune`** (§33.3 — a set difference over
  single-resource removal, requiring nothing extra in the ledger. Trigger: a
  consumer that needs a root to match its input exactly, e.g. an init container);
- **a second, cache-level lock** (§37.3 — atomic publication, not mutual
  exclusion, is what keeps the cache consistent);
- **`prompt`** (§10 — removed in v0.6.3. No runtime's mechanism for a managed
  system prompt was measured, and a guessed destination writes to a name the
  agent never opens. The evidence gathered is archived with the decision.
  Trigger: a real consumer needing a managed system prompt);
- **cross-repo marketplace entries** (§21.1 — a catalogue is same-repo, which is
  how the real ones are built and what removes §21.2 entirely. Trigger: a real
  catalogue needing cross-repo entries. §21.2 returns with it, as written);
- **ad-hoc install by ref** (§35.1 — an install names a manifest or a direct
  source. v1 keeps no marketplace definition in the ledger, so a bare
  `<item>@<marketplace>` has nothing to resolve against. Trigger: someone
  needing it twice);
- **a lockfile, reproducibility across machines, and signatures/provenance**
  (§32 — removed in v0.6, with its reintroduction trigger recorded);
- **gemini-cli as a supported runtime** (deprecated; four runtimes: claude,
  codex, opencode, pi);
- **exec-replacement semantics on Windows** (§39 decision 54 — Windows has no
  exec replacement, so a Windows implementation spawns and proxies the exit
  code. This is a stated difference, not a missing feature);
- a machine-readable schema document (JSON Schema) — `render`'s exit status is
  the contract check; a schema export is for editor autocomplete, a different
  need;
- renaming a marketplace item on install.

The profile format MUST NOT become a programming language.

---

# 39. Closed design decisions

1. No source classification such as `core-resolved` vs `delegated`.
2. `default` is a full first-class profile; only its target namespace is special.
3. Named resource collections are mappings keyed by logical name.
4. Mappings deep-merge recursively.
5. Non-mapping values replace.
6. Lists replace wholesale.
7. There is no generic `merge: append|replace|drop`.
8. Schema-marked unions come in two forms: branch-keyed unions and exclusive groups. Same branch/member composes; a different branch/member replaces the union or group wholesale. v1 union points, after v0.6.3: the `source` branch union (`git | local | archive`) and the resource locator group (`source | ref`). The prompt locator group went with §10.
9. `enabled: false` excludes inherited or local named resources from the current apply without removing previously materialized state; disabled resources remain visible in the effective diagnostic model; apply warns when a disabled resource's previously materialized path still exists (filesystem-detectable only).
10. `extends` is single-parent and locally deterministic.
11. Required inputs are computed from active effective configuration.
12. Runtime-scoped inputs are allowed.
13. Unsupported common concepts warn by default; `--strict` promotes degradation warnings to errors.
14. Silent degradation is forbidden.
15. Header sourced values use `prefix`.
16. Secrets resolve from `env` or `file`.
17. Referenced secrets must resolve before materialization; undeclared references fail during resolution with resource and input named; `--dry-run` surfaces them early.
18. `subpath` replaces `repo_path`.
19. Environment configuration is runtime-specific; user-declared `runtimes.<runtime>.environment` overrides adapter-managed variables, with a logged notice.
20. Inherited non-target runtime blocks are ignored; locally declared ones warn.
21. **REVERSED in v0.6.** There is no lockfile in v1. `ref: main` resolves afresh on every apply; two machines can materialize different bytes, which is stated as the cost (§32). The reintroduction trigger is recorded.
22. Variant args are opaque argv and may contradict declarative configuration.
23. Common skills follow the `SKILL.md` root contract.
24. **AMENDED in v0.6.** `plugins` is a common named resource with a stated component-kind contract; per-runtime routing, and validation of the native artifact, are adapter-owned. A runtime's own packaging mechanism remains runtime-specific (§24.3).
25. **AMENDED in v0.6, rescoped in v0.6.3.** Marketplaces are typed named resources (`plugins | skills`) with per-type item resolution contracts; refs use `<item>@<marketplace>`. The `plugins` catalog contract is a de-facto standard applicable beyond one runtime, hence declarable at common level. **A catalogue is SAME-REPO**: every entry resolves inside the marketplace's own repository, and an entry naming another is a resolution error. That removes the cross-host second hop and §21.2 with it (§21.1).
26. `model` is a singular optional object; absent means runtime-native defaults and credentials; `auth` is optional and its absence means runtime-native credentials; `Authorization` is banned inside `model.headers`.
27. **REMOVED in v0.6.3.** `prompt` is out of v1 (§10, §38). No runtime's mechanism for a managed system prompt was measured, and this specification does not describe a mechanism it has not watched work. The evidence gathered is archived with the decision; the trigger is a real consumer needing one.
28. `requires` is removed; preflight is derived from the effective profile.
29. **AMENDED in v0.6.** Apply is merge-overwrite and additive; overwritten keys are logged. Deletion exists but is confined to ledger-owned paths and ledger-owned keys, and is never a side effect of apply (§33.3).
30. Secret handling is scoped by consumer: runtime configuration materialization writes references, never values; resolution-time source consumers may read values transiently but never persist, log, or embed them. env-sourced runtime references use the declared binding variable with no launcher re-export; file-sourced runtime references are launcher-exported as `AP_SECRET_<NAME>`. Inability to materialize a reference for an active resource is a resolution-phase error; the sole opt-in to plaintext is the CLI flag `--allow-plaintext-secrets` (no manifest field), which retains the mandatory prominent warning, emphasized in `default`.
31. The resolution phase MUST complete before materialization; the ledger is written last, after materialization succeeds; a dry run executes exactly the resolution phase and stops.
32. MCP arguments are literal; no builtin placeholder set exists. `{}` in variant args retains its existing meaning.
33. `artifacts` are first-class opaque file/directory resources; stronger semantic types SHOULD be preferred.
34. `null` is reset-to-absent for singular objects and scalars; `null` on a named-collection entry is a validation error (use `enabled: false`).
35. **REMOVED in v0.6** with the lockfile. Re-installing a reference re-resolves it.
36. **REPLACED in v0.6.** The ledger is written after materialization succeeds (§37.2). A failed apply and a dry run leave it untouched.
37. **REMOVED in v0.6** with the lockfile.
38. **REPLACED in v0.6.** Host comparison for §21.2's credential guard is on the resolved URL's host and effective port (explicit or the scheme's default), case-insensitive on the host. There is no lock identity to normalize.
39. **AMENDED in v0.6.2.** One lock: a root lock per materialization root, held across materialization and the ledger write. The cache is unlocked — atomic publication is what keeps it consistent, so a second lock would add a lock-ordering rule to save one duplicated fetch (§37.3).
40. **AMENDED in v0.6.2.** `artifacts.destination` resolves relative to the materialization root, which is one of two (§33.2); path rules are those of `subpath`; absolute and workspace destinations remain non-goals, and bare paths mean the root — which is what keeps a project root addable later without a break.
41. **REVERSED in v0.6, amended in v0.6.2 and v0.6.3.** An ownership ledger exists, per root, with ONE arm — materialized resources (§33.1). Because it carries per-file hashes and per-file contributed keys, removal is safe in every root including the real configuration directory — the case v0.5 abandoned. Single-resource removal is v1 and is what the ledger's design must support; whole-root convergence (`--prune`) is deferred (§33.3, §38) and needs nothing added to the ledger when it arrives.
42. `extends` resolves only in the manifest directory or via an explicit relative path; there are no search paths.
43. There is no `abstract` flag; a profile without `targets` is a base profile and cannot be applied directly.
44. In MCP transport headers, `Authorization` MUST use `value_from`; a literal value is a validation error.

45. A manifest is an INPUT and is never written to; the ledger is the only state (§1.1, §33.1).
46. Git `auth.scheme` is `bearer | basic-oauth2`, inferred from host when absent, explicit value wins, and the scheme used is always reported — mandatorily in a 401 (§17.1). The credential travels as a header, never in the URL, and never over non-TLS transport (§17.2).
47. `archive` returns as a source family with a REQUIRED digest, verified before extraction (§18).
48. **REPLACED in v0.6.3.** A marketplace entry naming a source outside the marketplace's own repository is a RESOLUTION ERROR, not a fetch to guard (§21.1). The withholding rule it replaces is recorded in §21.2 and returns with the cross-repo trigger — removing the scope restriction without restoring the guard would hand a marketplace owner every credential its users hold.
49. **AMENDED in v0.6.2.** There are two materialization roots — the agent's real configuration directory and a named profile's namespace — and a root is always a parameter, never inferred (§33.2). They are one mechanism; a project directory is a second one and is deferred with its containment rule (§38).
50. A single-capability install supplies its secret binding at the call site and records the binding NAME, never the value; the per-run environment cost is accepted (§13.1).
51. A dry run executes exactly the resolution phase, and it authenticates and fetches — an implementation must say so (§37.1).
52. Export emits resolved values for anything inferred, synthesises `inputs` from recorded bindings with stable names, and round-trips (§35.2).
53. gemini-cli is not a supported runtime; the set is claude, codex, opencode, pi.
54. **NEW in v0.6.3.** Windows is a supported target with the FULL command surface, not a hydration-only one. `run` uses spawn semantics there — start the child, proxy its exit code — because Windows has no exec replacement; the signal difference is documented rather than papered over. The portable half must keep building and vetting for Windows whether or not binaries ship, because a consumer imports it.
55. **NEW in v0.6.3.** `model` materializes into per-adapter FILES, never a launcher-carried environment (§9). An environment only the launcher exports is unread wherever the launcher is not in the path — an init container hydrating for a main container that executes the runtime directly loses the whole block in silence. A runtime with no measured destination warns (§8) rather than being given a guessed one.
56. **NEW in v0.6.3.** A marketplace catalogue is same-repo (§21.1); an external entry is a resolution error. This removes §21.2 and the ledger's second arm (§33.1), and both return together with the cross-repo trigger.
57. **NEW in v0.6.3.** An install names a manifest or a direct source (§35.1). Ad-hoc install by a bare ref is deferred with the ledger arm that would have made it resolvable.

---

# 40. Open questions

## 40.1 Runtime-native package schemas

Adapters still need explicit schemas for a runtime's own packaging mechanism,
where it has one and it is not §24's contract:

```text
runtimes.opencode.plugins   (package: "@scope/name")
runtimes.pi.packages
```

These remain adapter-owned. §24 settles the *common* plugin contract; this is
what is left.

## 40.2 Artifact destination semantics (closed in v0.5, amended in v0.6)

Closed. `destination` resolves relative to the materialization root, which §33.2
now defines as one of two.

## 40.3 Dependency materialization (closed in v0.5)

Closed by removal.

## 40.4 Stable name derivation on export

§35.2 requires export to synthesise `inputs.secrets` names from recorded bindings
"stably". The derivation rule itself is not specified. `GITLAB_TOKEN` →
`gitlab-token` is obvious; two bindings that collide after derivation are not.
Closable with an implementation and one test.

---

# 41. Final architecture

```text
                         Profile YAML
                              │
                           extends
                              │
                              ▼
                     structural merge
                              │
                      runtime overlay
                              │
                              ▼
                      schema validation
                              │
                         enabled state
                              │
                              ▼
                       EffectiveProfile
                              │
                     resolve inputs/secrets
                              │
                        derived preflight
                              │
                       resolve sources
                              │
                              ▼
                       runtime adapter
                              │
                     native materialization
                              │
                              ▼
                       the LEDGER  ──►  list / remove / export
                              │
                              ▼
                            agent
```

Two inputs reach that pipeline: a manifest, or a single capability. Neither is
state. The ledger is.

The common declarative vocabulary:

```text
model
skills
plugins
mcps
artifacts
inputs
sources
marketplaces
```

Runtime adapters retain ownership of truly native concepts:

```text
component routing (where each plugin kind lands)
packages/extensions
runtime environment
variants
native configuration shapes
```

The core rule remains deliberately small:

> Mappings merge recursively. Non-mappings replace. Unions and exclusive groups replace as a unit across branches. `null` resets a singular to absent. Named resources are mappings. `enabled: false` disables a resource. The effective result must satisfy the schema. Resolution completes in full, then materialization mutates the root, then the ledger records what it did. A manifest is an input; the ledger is the state. Apply adds; only the ledger's own entries are ever removed.

This gives composition without introducing a patch language.
