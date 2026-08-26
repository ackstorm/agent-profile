# Walkthrough: a manifest, then a plugin from a private GitLab marketplace

A played-out session, invented but realistic, run against the design as it
currently stands. Findings are flagged **inline where they bite** and collected
at the end. Everything attributed to `ach-cli` was read in its source.

---

## The setup

Acme has a private GitLab at `gitlab.acme.internal`. It hosts a plugin
marketplace at `ai/agent-plugins`. Juan wants his `plan` profile to have the
team's shared skills plus one internal plugin.

## Step 1 — apply the team manifest

```yaml
# team/plan.yaml
version: "1"
name: plan
targets:
  - claude

inputs:
  secrets:
    gitlab-token:
      env: GITLAB_TOKEN

prompt:
  mode: append
  content: |
    Prefer small diffs. Cite file:line.

marketplaces:
  anthropic-skills:
    type: skills
    source:
      git:
        url: https://github.com/anthropics/skills.git
        ref: main
        subpath: skills

skills:
  pdf:
    ref: pdf@anthropic-skills

mcps:
  memory:
    transport:
      type: http
      url: https://memory.acme.internal/mcp
      headers:
        Authorization:
          prefix: "Bearer "
          value_from:
            secret: memory-token
```

```console
$ ap manifest apply claude:plan ./team/plan.yaml --dry-run
error: skill "pdf" references secret "memory-token"
       no binding declared in inputs
```

Wrong error — it names the skill, not the MCP. Fine, the point stands: the
missing binding is caught in the resolution phase, before anything is written.
Juan adds it and re-runs.

```console
$ export GITLAB_TOKEN=glpat-xxxx MEMORY_TOKEN=mt-yyyy
$ ap manifest apply claude:plan ./team/plan.yaml --dry-run
resolving
  ✓ marketplace anthropic-skills   github.com/anthropics/skills.git @ 8a71c2e
  ✓ skill pdf                      skills/pdf
  ✓ mcp memory                     http https://memory.acme.internal/mcp
would write into ~/.local/share/agent-profile/profiles/claude/plan
  + skills/pdf/                    4 files
  + .mcp.json                      1 key: mcpServers.memory
  + CLAUDE.md                      append block
nothing written
```

> **F6 — `--dry-run` is not offline.** §37 makes resolution include source
> fetching, so a dry run against a private manifest still needs the credential
> and still hits the network. That is correct (it is the only way to check the
> `SKILL.md` contract) but it must be said in the help text, because "dry run"
> reads as "does nothing" and here it does authenticate.

```console
$ ap manifest apply claude:plan ./team/plan.yaml
wrote 6 files
```

## Step 2 — add the private GitLab marketplace

The manifest never mentioned it. Juan adds it imperatively.

```console
$ ap install claude:plan marketplace acme-plugins \
    --type plugins \
    --git https://gitlab.acme.internal/ai/agent-plugins.git
error: authentication required for gitlab.acme.internal
       declare a credential with --auth-secret-env or --auth-secret-file
```

> **F2 — imperative install has nowhere to declare a secret binding.** A manifest
> has `inputs.secrets`; `ap install` has no manifest. The flag has to exist.
>
> **And here `ap` must NOT copy `ach-cli`.** `ach-cli repo add --token` reads the
> token and persists it in `~/.config/ach/local/credentials.json` at mode `0600`.
> SPEC v0.5 §34 forbids that outright: resolution-time consumers "MUST NOT
> persist it". The binding — the variable's *name* — is what gets recorded.
>
> The UX cost is real and should be stated rather than discovered: `GITLAB_TOKEN`
> has to be in the shell every time, where `ach-cli` asks once. The benefit is
> that `ap` never holds a credential on disk, which is the same reason
> `.credentials.json` is a symlinked share rather than a copy.

```console
$ ap install claude:plan marketplace acme-plugins \
    --type plugins \
    --git https://gitlab.acme.internal/ai/agent-plugins.git \
    --auth-secret-env GITLAB_TOKEN
added marketplace "acme-plugins"  (plugins)
  gitlab.acme.internal/ai/agent-plugins.git @ c04e772 (main)
  auth: Basic oauth2:$GITLAB_TOKEN   ← inferred from host
  14 plugins in .claude-plugin/marketplace.json
```

> **F1 — GitLab needs `Basic base64("oauth2:"+token)`, and SPEC §17 has no way to
> say so.** `ach`'s own comment records the measurement: on a self-hosted GitLab
> "Bearer → 401, Basic oauth2:<token> → 200". It infers the scheme from the host
> (`strings.Contains(host,"gitlab") || strings.HasPrefix(host,"git.")`) and
> allows `--auth bearer|basic-oauth2` to override.
>
> §17's `auth` block has **only** `value_from`. A self-hosted GitLab at
> `code.acme.internal` matches neither pattern, would get `Bearer`, would 401,
> and the manifest would have no field to fix it with.
>
> Fix: add `auth.scheme` to the git source, inferred from host by default,
> explicit override wins. Inference is acceptable here — it is a protocol
> default with a stated escape hatch, not a guess about where the author's
> argument goes — but the 401 message must name the scheme it chose, or the user
> has no way to know what to override.

## Step 3 — install the plugin

```console
$ ap install claude:plan plugin code-review@acme-plugins
resolving code-review from marketplace acme-plugins
  entry source: git-subdir  gitlab.acme.internal/ai/agent-plugins.git  plugins/code-review
installed plugin "code-review"  c04e772
  + commands/review.md      → .claude/commands/review.md
  + agents/reviewer.md      → .claude/agents/reviewer.md
  + skills/diff-reader/     → .claude/skills/diff-reader/   (3 files)
  ~ .mcp.json                 1 key: mcpServers.acme-lint
```

Worked. Now the surprise, one plugin over.

```console
$ ap install claude:plan plugin doc-writer@acme-plugins
resolving doc-writer from marketplace acme-plugins
  entry source: url  https://github.com/some-vendor/doc-writer.git
```

> **F5 — THE ONE THAT MATTERS. The second hop sends your credential to a host
> you do not control.**
>
> `marketplace.json` lives in Acme's repo, but its *entries* may name any URL.
> `ach`'s `BuildEntrySpec` passes `Token: token, AuthScheme: scheme`
> **unconditionally** for `git-subdir` and `url` entries — it never compares the
> entry's host to the marketplace's. So fetching `doc-writer` sends
> `Authorization: Basic base64("oauth2:glpat-xxxx")` to `github.com`.
>
> SPEC §17 already flags the shape of this hazard for *composition*: "When
> overriding `url` — particularly to a different host — review or redeclare
> `auth`: the referenced credential will be sent to the new host." But this case
> is strictly worse, because **the manifest author does not write
> `marketplace.json` — the marketplace owner does.** Reviewing your own manifest
> cannot protect you. Anyone who can land a commit in a marketplace repo you
> trust can add an entry pointing at a host they control and harvest the token.
>
> §21.1 currently says the second hop is "performed by the runtime's native
> install mechanism and is **outside** agent-profile source resolution", which
> would make it someone else's problem. Decision D1 makes `plugins` a common
> type that **we** materialize, so we inherit it.
>
> **Fix: send the credential only when the entry's host equals the marketplace's
> host.** A different host is fetched anonymously; if that 401s, the error names
> both hosts and says the credential was withheld deliberately. This is a
> security guard, so it gets the repository's guard treatment: revert it, watch
> its test fail, restore.

```console
$ ap install claude:plan plugin doc-writer@acme-plugins
resolving doc-writer from marketplace acme-plugins
  entry source: url  https://github.com/some-vendor/doc-writer.git
  NOTICE: entry host github.com differs from marketplace host
          gitlab.acme.internal — fetching without your credential
installed plugin "doc-writer"  3f9d10b
```

## Step 4 — what does the profile know?

```console
$ ap list claude:plan
claude:plan  ~/.local/share/agent-profile/profiles/claude/plan

  marketplaces
    anthropic-skills   skills    github.com/anthropics/skills.git       8a71c2e
    acme-plugins       plugins   gitlab.acme.internal/ai/…              c04e772
                                 auth: $GITLAB_TOKEN (basic-oauth2)

  skills   pdf (anthropic-skills)
  plugins  code-review (acme-plugins)   doc-writer (acme-plugins)
  mcps     memory
```

> **F3 — applying a manifest has to record marketplace DEFINITIONS, not just
> files.** `acme-plugins` was added imperatively, so it is in the ledger. But
> `anthropic-skills` came from the manifest, which is an *input* and is not kept.
> If applying a manifest only recorded materialized files, `ap install
> claude:plan skill xlsx@anthropic-skills` a week later could not resolve
> `anthropic-skills` — the name would be gone with the manifest.
>
> So the ledger holds two kinds of record: **materialized resources** (files,
> hashes, merge keys) and **resolved definitions** (marketplaces, their source,
> their auth *binding*). A marketplace writes no file into the root and is
> invisible to a file-only ledger. This changes Phase 4's ledger design, which
> was specified as `ach`'s `FileRec` list and needs a second arm.
>
> **F4 — and definitions are per-root, which means repetition.** `ach`'s
> `repos.json` is *global* (`~/.config/ach/local/`), so a repo added once is
> usable from every target. Here `acme-plugins` belongs to `claude:plan` only;
> adding it to four more profiles means four more commands.
>
> Recommend keeping per-root anyway. A global registry is invisible state that no
> manifest can express, which breaks "the manifest is a portable definition" —
> the manifest would work on Juan's machine and fail on a colleague's. The
> repetition has a one-command answer:
>
> ```console
> $ ap manifest export claude:plan > plan.yaml
> $ ap manifest apply claude:exec ./plan.yaml
> ```

## Step 5 — share it back

```console
$ ap manifest export claude:plan
version: "1"
name: plan
targets:
  - claude
inputs:
  secrets:
    gitlab-token:
      env: GITLAB_TOKEN
    memory-token:
      env: MEMORY_TOKEN
marketplaces:
  anthropic-skills:
    type: skills
    source:
      git: {url: https://github.com/anthropics/skills.git, ref: main, subpath: skills}
  acme-plugins:
    type: plugins
    source:
      git:
        url: https://gitlab.acme.internal/ai/agent-plugins.git
        ref: main
        auth:
          scheme: basic-oauth2
          value_from: {secret: gitlab-token}
skills:
  pdf: {ref: pdf@anthropic-skills}
plugins:
  code-review: {ref: code-review@acme-plugins}
  doc-writer:  {ref: doc-writer@acme-plugins}
mcps:
  memory: …
```

> **F7 — export must synthesise the `inputs` block.** The marketplace was added
> with `--auth-secret-env GITLAB_TOKEN`, which is a binding with no name. Export
> has to invent one (`gitlab-token`), emit the `inputs.secrets` entry, and point
> `value_from.secret` at it — otherwise the exported manifest references a secret
> nothing declares and fails its own validation. Name derivation must be stable,
> or two exports of the same root differ.
>
> Export must also emit the **inferred** `auth.scheme`. Juan never typed
> `basic-oauth2`; it was inferred from the host. A colleague on a differently
> named host would infer differently, so the resolved value is written down.

## Step 6 — the colleague

```console
$ ap create claude:plan
$ ap manifest apply claude:plan ./plan.yaml
error: secret "gitlab-token" is not available
       inputs.secrets.gitlab-token reads $GITLAB_TOKEN, which is unset
       referenced by: marketplace "acme-plugins"
```

Correct, and the error names the binding, the variable and the referrer. This is
§12's error with the referrer added, which the spec's example omits and which is
the difference between a two-minute fix and a ten-minute one.

---

# Findings

| # | Finding | Where it lands |
|---|---|---|
| **F5** | **Second hop sends the marketplace credential to whatever host an entry names.** `ach`'s `BuildEntrySpec` passes `Token`+`AuthScheme` unconditionally. The manifest author does not control `marketplace.json`. **Fix: withhold the credential on a host mismatch, notice it, and mutation-test the guard.** | Phase 6, security guard |
| **F1** | GitLab needs `Basic base64("oauth2:"+token)`; self-hosted returns 401 on Bearer. SPEC §17's `auth` has no scheme field. **Fix: `auth.scheme`, inferred from host, explicit override wins, 401 message names the chosen scheme.** | schema (Phase 1), fetch (Phase 3) |
| **F3** | The ledger needs a second arm for **resolved definitions** (marketplaces + auth bindings), not just materialized files. Otherwise a manifest-declared marketplace is unresolvable once the manifest is gone. | Phase 4, ledger design |
| **F2** | `ap install` needs `--auth-secret-env` / `--auth-secret-file`. **Do not copy `ach-cli`'s `credentials.json`** — §34 forbids persisting the value. Record the binding name. State the UX cost. | Phase 7 |
| **F7** | `ap manifest export` must synthesise an `inputs.secrets` block from recorded bindings, with stable derived names, and must emit the **resolved** `auth.scheme`. | Phase 7 |
| **F4** | Marketplaces are per-root, unlike `ach`'s global `repos.json`. Accepted: a global registry is state no manifest can express. Repetition answered by `export` + `apply`. | Phase 4 |
| **F6** | `--dry-run` authenticates and hits the network. Say so in the help text. | Phase 2 |
| **F8** | Error paths must not grow a token. `ach` keeps the credential in `http.extraHeader`, never the URL — copy that, and check `git`'s stderr is not echoed raw. | Phase 3 |
| **F9** | GitLab subgroups (`/ai/sub/group/repo.git`) — nested paths in clone URLs and `subpath` interaction. One test, not a design change. | Phase 3 |
| **F10** | `<item>@<marketplace>` uses the catalog's `name` as the local key. `--as <name>` for renaming is not in v1; note it before someone hits a collision between two marketplaces. | deferred |

## What `ach-cli` already solves, and we copy verbatim

- **Credential in `http.extraHeader`, never in the URL.** Its comment records
  both reasons: `/proc/<pid>/cmdline` and persistence in
  `git config remote.origin.url`.
- **Scheme per provider**, with the self-hosted-GitLab measurement recorded next
  to the constant.
- **In-repo entries are sliced from the already-cloned tar**, so a 14-plugin
  marketplace clones once (`FetchCache`, measured 5.8× on a 17-plugin install).
- **`repos.json` stores `hasToken: true` only** — the value lives elsewhere. Even
  though we do not persist the value at all, that separation of metadata from
  secret is the right shape for the ledger.
