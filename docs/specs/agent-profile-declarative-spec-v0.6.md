# Agent Profile Declarative Specification — SPEC v0.6

**Status:** Draft — supersedes v0.5, which was frozen pending implementation evidence
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
- **Three materialization roots (§26.1, §33.2).** v0.5 made workspace
  destinations a non-goal but reserved the mechanism. A root is a parameter with
  three values: the agent's real configuration directory, a named profile's
  namespace, or a project directory.
- **Apply is still additive; convergence is explicit (§33).** A manifest that
  stops declaring a resource does not remove it. `--prune` does, and only over
  what the ledger owns.

**Additions found by walking a private-GitLab scenario.**

- **`auth.scheme` on the git source (§17).** A self-hosted GitLab returns 401 on
  `Bearer` and 200 on `Basic base64("oauth2:"+token)` — measured. v0.5's `auth`
  had only `value_from`, leaving such a manifest unfixable.
- **The second hop withholds the credential on a host mismatch (§21.2).** A
  marketplace's entries may name any URL, and the manifest author does not write
  the catalogue. This is the specification's one mandatory security guard.
- **Imperative secret bindings (§13.1).** Commands that install one capability
  have no `inputs` block. They take a binding, and record its name, never its
  value.

**Removals.** gemini-cli is not a supported runtime: it is deprecated. Four
runtimes — claude, codex, opencode, pi.

**Unchanged and worth restating**, because they were questioned and survived:
`model` and `prompt` remain singular — a capability catalogue is plural, a
hydrated environment is not, and the selection belongs in the producer's
translator. A2A agents need no type: a translator emits them as MCP servers.
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
Convergence is a separate, explicit act (§33.3).

## 1.2 Common where semantics are truly common

The common layer defines:

```text
model
prompt
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

`model` and `prompt` are singular objects, not collections (see their sections).

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
prompt: {}
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
- the prompt locator group: `content | source`.

Composition rule:

> If an overlay declares any member of an exclusive group, all other inherited members of that group are discarded. Declaring the same member composes normally.

Keys outside the group compose normally: an overlay setting only `enabled: false` does not touch the locator; an overlay replacing prompt `content` inherits the parent's `mode`.

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

in a child yields an effective profile without `model` — runtime-native defaults and credentials (the subscription/OAuth case, see §9). The same applies to `prompt: null` and to scalar keys.

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

Detection is filesystem-based only; keys inside structured configuration files are not tracked.

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
prompt: ...
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

- The adapter maps `auth` to the runtime's native mechanism (e.g. an auth token environment variable for Claude Code, a provider entry for OpenCode). Adapters MUST NOT require raw `Authorization` header passthrough to express endpoint authentication.
- `Authorization` as a key inside `model.headers` is a **validation error**. Endpoint authentication has exactly one representation: `auth`.
- `headers` carries request metadata only (`X-User`, `X-Tenant`, ...).

---

# 10. Prompt

`prompt` is a singular, optional resource.

Inline:

```yaml
prompt:
  mode: append
  content: |
    You are a coding agent.
    Produce precise and maintainable changes.
```

Source-backed:

```yaml
prompt:
  mode: replace
  source:
    local:
      path: ./prompts/review.md
```

Rules:

- exactly one of `content` | `source` (an exclusive group, see §3.5.2);
- `mode` is `append` (default) or `replace`, relative to the runtime's native system prompt;
- the adapter maps `mode` to the runtime's native mechanism (e.g. append-system-prompt for Claude Code); an unsupported `mode` follows the normal degradation policy.

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
- secret values MUST NOT appear in manifests, lockfiles or diagnostics;
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

Item lookup is by the entry's `name` field. An entry may declare its own source,
and resolving it is a **second hop**.

> Changed in v0.6. v0.5 placed the second hop outside this specification, on the
> basis that a runtime's native install mechanism performed it. §24 now makes
> `plugins` a common resource type materialized by the adapter, so the second hop
> is ours and §21.2 governs it.

Second-hop resolution:

- An entry whose source resolves to the **marketplace's own repository** is
  sliced out of the already-fetched tree. A 14-entry marketplace fetches once.
- An entry naming a **different repository** is fetched on its own, subject to
  §21.2.

**`skills`** — item `<name>` resolves to directory `<name>` under the marketplace
root/`subpath`. The directory MUST satisfy the skill contract (`SKILL.md` at its
root).

## 21.2 The second hop MUST NOT carry the credential to a foreign host

**Normative, and the one mandatory security guard in this specification.**

> When a marketplace entry's source resolves to a host other than the
> marketplace's own, the implementation MUST NOT send the marketplace's
> credential with that request. The fetch proceeds unauthenticated, and the
> implementation MUST report that it withheld the credential, naming both hosts.

Host comparison is on the resolved URL's host, case-insensitively. Equal hosts
carry the credential; anything else does not. There is no flag to disable this.

### Why this is a rule and not advice

§17.3 tells an author to review `auth` when they change `url`. That is sufficient
when the author controls both. Here they control neither:

- `marketplace.json` lives in the marketplace's repository and is written by the
  marketplace's owner.
- Its entries may name any URL.
- The manifest author writes only `ref: <item>@<marketplace>`.

So reviewing your own manifest cannot protect you. Anyone able to land a commit in
a marketplace you already trust can add an entry pointing at a host they control,
and every subsequent install hands them the credential you use for the
marketplace — which for a private GitLab is a token scoped to your organisation's
source code.

A 401 resulting from the withheld credential is not a failure of this rule. The
error MUST distinguish it from an ordinary authentication failure and say the
credential was withheld deliberately, or the user's first instinct will be to
work around the guard.

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

Literal resources such as inline prompt content are the explicit exception.

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

Two routing rules are normative because they are security properties, not
formatting:

- **An adapter MUST NOT register MCP servers found in a plugin's agent
  frontmatter.** A plugin that can register an MCP server by shipping a
  frontmatter key can add a tool the user never reviewed.
- **In a project root (§26.1), an adapter MUST NOT write outside the runtime's
  own dot-directory**, except for the single project-root file that runtime
  genuinely reads. A plugin must never overwrite a user's `README.md` or
  `CLAUDE.md`.

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
prompt
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
apply operates on, which is a parameter with three values (§33.2).

Path rules are those of `subpath`: relative; MUST NOT escape the root via `..`;
checked after cleaning, because `a/../../b` is only visibly an escape once
cleaned.

> Changed in v0.6. v0.5 made workspace (user repository) destinations a non-goal
> while reserving the mechanism: "a scope prefix can be added
> backward-compatibly: bare paths keep meaning namespace root." A project is now
> one of the three roots, and bare paths still mean the root — so no prefix was
> needed after all.

In a **project root** the containment rule of §24.2 applies to artifacts too:
write under the runtime's dot-directory, plus the single project-root file that
runtime genuinely reads. A manifest cannot use `destination` to overwrite a
user's own files.

Absolute destinations remain a non-goal.

`artifacts` MUST NOT become a replacement for stronger semantic resource types.
If something is clearly a `prompt`, `skill`, `plugin`, `MCP`, etc., it uses that
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
- prompt;
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

The ledger is per root, and it is the **only** state. It has two arms.

**Materialized resources** — one record per resource, one entry per file:

```text
resource   name, kind, source or ref, resolved_ref, installed_at
file       rel_path
           hash
           merge      "" (replace) | "deep" | "composite"
           keys       deep: the dotted keys contributed
                      composite: the marker id
```

**Resolved definitions** — marketplaces, which materialize no file:

```text
marketplace  name, type, source, resolved_ref
             auth: scheme, and the BINDING NAME (never a value)
```

The second arm is not optional, and the reason is the interaction between two
other rules. A manifest is an **input** and is not kept (§1.1). A marketplace
declared in one writes no file into the root. So a file-only ledger loses the
marketplace the moment the manifest is gone, and a later single-capability
install of `<item>@<that-marketplace>` has nothing to resolve the name against.

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

## 33.2 The three roots

A root is a **parameter**. An implementation MUST NOT infer one from the
environment.

```text
the agent's real configuration directory      e.g. ~/.claude
a named profile's isolated namespace          e.g. <profiles>/claude/plan
a project directory                           e.g. ./
```

The first two are the same mechanism: the agent's own configuration-directory
variable is pointed at the chosen directory. The third is not — a project is
read by the agent from its working directory — and it carries the containment
rule of §24.2.

The real configuration root is the user's own environment and cannot be undone by
deleting a profile. An interactive implementation MUST display its **resolved
absolute path**, name it as the real configuration, and gate the operation. Off a
terminal it MUST refuse: a pipe is not consent.

## 33.3 Removal and convergence

Two operations, both bounded by the ledger. Neither may remove anything the
ledger does not own.

- **Remove one resource.** Its files are removed subject to §33.1's verdicts; its
  merged keys are removed from the files that carry them.
- **Converge (`--prune`).** Remove what the ledger owns and the current input no
  longer declares.

Convergence is explicit — a flag, never a side effect of apply. A pod should
match the profile it was given; a developer's machine should not lose what they
installed by hand.

Both MUST offer a preview, and the preview MUST be produced by the same
classifier as the action.

---

# 34. Secret materialization

Two invariants, scoped by consumer:

> **Runtime configuration materialization writes secret references, never secret values.** The sole exception is the explicit `--allow-plaintext-secrets` path below.

> **Resolution-time consumers** (git `auth`, future source resolvers) **MAY read a required secret value transiently** when needed to access a source, and MUST NOT persist it or include it in logs, diagnostics, error messages, the lockfile, the EffectiveProfile, or materialized configuration. Fetch errors MUST NOT embed credentials (e.g. tokens inside URLs).

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


prompt:
  mode: append
  content: |
    You are a coding agent.
    Implement changes carefully and keep the repository healthy.


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
install one capability  one resource, now, with no manifest
```

and the operations over what a root already has:

```text
list       what the ledger holds
remove     one resource                       (§33.3)
converge   --prune: what the input no longer declares   (§33.3)
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
- produce a manifest that validates, and that re-applies to the same result.

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
- `Authorization` absent from `model.headers`.

## Prompt

- exactly one of `content` | `source` (exclusive group);
- `mode` is `append` or `replace`.

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
- MCP servers in agent frontmatter are NOT registered (§24.2);
- in a project root, nothing is written outside the runtime's dot-directory
  except the one project-root file it reads (§24.2).

## Sources

- git `auth.scheme` is `bearer` or `basic-oauth2` when present; the scheme
  actually used is reported, and named in any 401 (§17.1);
- the credential travels as a header, never in the URL (§17.2);
- an archive source declares a `digest`, verified before extraction (§18);
- a marketplace entry resolving to a foreign host is fetched WITHOUT the
  marketplace's credential, and the withholding is reported (§21.2).

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
  true.

## 37.3 Locking

Two levels, acquired in a fixed order to exclude deadlock:

- a **workspace lock**, protecting the cache;
- a **root lock** per materialization root being mutated, held across
  materialization and the ledger write.

Roots are per-user global — the real configuration directory, the shared profile
directory — so a workspace lock alone cannot serialize two workspaces applying
onto one root.

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
- prompt and artifact marketplaces;
- `extends` search paths;
- an `abstract` flag;
- absolute artifact destinations;
- **a lockfile, reproducibility across machines, and signatures/provenance**
  (§32 — removed in v0.6, with its reintroduction trigger recorded);
- **gemini-cli as a supported runtime** (deprecated; four runtimes: claude,
  codex, opencode, pi);
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
8. Schema-marked unions come in two forms: branch-keyed unions and exclusive groups. Same branch/member composes; a different branch/member replaces the union or group wholesale. v1 union points: the `source` branch union (`git | local`); the resource locator group (`source | ref`); the prompt locator group (`content | source`).
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
25. **AMENDED in v0.6.** Marketplaces are typed named resources (`plugins | skills`) with per-type item resolution contracts; refs use `<item>@<marketplace>`. The `plugins` catalog contract is a de-facto standard applicable beyond one runtime, hence declarable at common level. **Its second resolution hop is performed by this specification** (§21.1) and is governed by §21.2's credential guard.
26. `model` is a singular optional object; absent means runtime-native defaults and credentials; `auth` is optional and its absence means runtime-native credentials; `Authorization` is banned inside `model.headers`.
27. `prompt` is a singular optional resource; exactly one of `content` | `source` (an exclusive group); `mode` is `append` (default) or `replace`.
28. `requires` is removed; preflight is derived from the effective profile.
29. **AMENDED in v0.6.** Apply is merge-overwrite and additive; overwritten keys are logged. Deletion exists but is confined to ledger-owned paths and ledger-owned keys, and is never a side effect of apply (§33.3).
30. Secret handling is scoped by consumer: runtime configuration materialization writes references, never values; resolution-time source consumers may read values transiently but never persist, log, or embed them. env-sourced runtime references use the declared binding variable with no launcher re-export; file-sourced runtime references are launcher-exported as `AP_SECRET_<NAME>`. Inability to materialize a reference for an active resource is a resolution-phase error; the sole opt-in to plaintext is the CLI flag `--allow-plaintext-secrets` (no manifest field), which retains the mandatory prominent warning, emphasized in `default`.
31. The resolution phase MUST complete before the atomic lock commit and materialization; `--dry-run` executes exactly the resolution phase and discards pending lock additions.
32. MCP arguments are literal; no builtin placeholder set exists. `{}` in variant args retains its existing meaning.
33. `artifacts` are first-class opaque file/directory resources; stronger semantic types SHOULD be preferred.
34. `null` is reset-to-absent for singular objects and scalars; `null` on a named-collection entry is a validation error (use `enabled: false`).
35. **REMOVED in v0.6** with the lockfile. Re-installing a reference re-resolves it.
36. **REPLACED in v0.6.** The ledger is written after materialization succeeds (§37.2). A failed apply and a dry run leave it untouched.
37. **REMOVED in v0.6** with the lockfile.
38. **REPLACED in v0.6.** Host comparison for §21.2's credential guard is case-insensitive on the resolved URL's host. There is no lock identity to normalize.
39. Two-level locking: a workspace lock (lockfile, cache) plus a target-namespace lock per materialization root, acquired in fixed order.
40. **AMENDED in v0.6.** `artifacts.destination` resolves relative to the materialization root, which is one of three (§33.2); path rules are those of `subpath`; absolute destinations remain a non-goal, and a project root carries §24.2's containment rule.
41. **REVERSED in v0.6.** An ownership ledger exists, per root, with two arms (§33.1). Because it carries per-file hashes and per-file contributed keys, removal is safe in every root including the real configuration directory — the case v0.5 abandoned. Convergence is `--prune` (§33.3), available now rather than in v2.
42. `extends` resolves only in the manifest directory or via an explicit relative path; there are no search paths.
43. There is no `abstract` flag; a profile without `targets` is a base profile and cannot be applied directly.
44. In MCP transport headers, `Authorization` MUST use `value_from`; a literal value is a validation error.

45. A manifest is an INPUT and is never written to; the ledger is the only state (§1.1, §33.1).
46. Git `auth.scheme` is `bearer | basic-oauth2`, inferred from host when absent, explicit value wins, and the scheme used is always reported — mandatorily in a 401 (§17.1). The credential travels as a header, never in the URL (§17.2).
47. `archive` returns as a source family with a REQUIRED digest, verified before extraction (§18).
48. A marketplace entry resolving to a host other than the marketplace's is fetched WITHOUT the marketplace's credential, the withholding is reported naming both hosts, and there is no flag to disable it (§21.2).
49. There are three materialization roots — the agent's real configuration directory, a named profile's namespace, a project directory — and a root is always a parameter, never inferred (§33.2).
50. A single-capability install supplies its secret binding at the call site and records the binding NAME, never the value; the per-run environment cost is accepted (§13.1).
51. A dry run executes exactly the resolution phase, and it authenticates and fetches — an implementation must say so (§37.1).
52. Export emits resolved values for anything inferred, synthesises `inputs` from recorded bindings with stable names, and round-trips (§35.2).
53. gemini-cli is not a supported runtime; the set is claude, codex, opencode, pi.

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
now defines as one of three.

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
                       the LEDGER  ──►  list / remove / prune / export
                              │
                              ▼
                            agent
```

Two inputs reach that pipeline: a manifest, or a single capability. Neither is
state. The ledger is.

The common declarative vocabulary:

```text
model
prompt
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
