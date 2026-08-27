# Terminal session — private GitLab marketplace

A simulated transcript, formatted the way `cmd/ap` actually prints: report lines
are `  ✓ <label> <value>`, skips are `  – `, failures are `  ✗ `, the tree uses
`├─ └─ │`, prompts are `  <q> [y/N] `, and errors are `ap: …`.

Everything below is a design artefact. No code exists yet.

```console
jcm@laptop ~/work $ ap create claude:plan

claude:plan
  ✓ created    ~/.local/share/agent-profile/profiles/claude/plan
  ✓ shared     .credentials.json
  ✓ wrapper    ~/.local/bin/claude-plan

  Next: ap run claude:plan plugin install <plugin>

jcm@laptop ~/work $ cat team/plan.yaml
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

jcm@laptop ~/work $ ap manifest apply claude:plan ./team/plan.yaml --dry-run
ap: ./team/plan.yaml:34: mcp "memory" references secret "memory-token"
    no binding declared in inputs.secrets
$ echo $?
1

jcm@laptop ~/work $ vim team/plan.yaml     # add memory-token: {env: MEMORY_TOKEN}
jcm@laptop ~/work $ export GITLAB_TOKEN=glpat-REDACTED MEMORY_TOKEN=mt-REDACTED

jcm@laptop ~/work $ ap manifest apply claude:plan ./team/plan.yaml --dry-run

resolving
  ✓ secret     gitlab-token   $GITLAB_TOKEN     present
  ✓ secret     memory-token   $MEMORY_TOKEN     present
  ✓ preflight  claude         /usr/local/bin/claude
  ✓ market…    anthropic-skills   github.com/anthropics/skills.git @ 8a71c2e
  ✓ skill      pdf                skills/pdf                 SKILL.md ok
  ✓ mcp        memory             http  memory.acme.internal

would write into ~/.local/share/agent-profile/profiles/claude/plan
  + skills/pdf/SKILL.md
  + skills/pdf/scripts/extract.py
  + skills/pdf/references/spec.md
  + skills/pdf/references/examples.md
  + .mcp.json                    merge  mcpServers.memory
  + CLAUDE.md                    append ap block

  --dry-run authenticated and fetched; nothing was written.

jcm@laptop ~/work $ ap manifest apply claude:plan ./team/plan.yaml

claude:plan
  ✓ skill      pdf                4 files
  ✓ mcp        memory             .mcp.json  +mcpServers.memory
  ✓ prompt     append             CLAUDE.md
  ✓ ledger     6 files, 1 definition

jcm@laptop ~/work $ ap install claude:plan marketplace acme-plugins \
>     --type plugins \
>     --git https://gitlab.acme.internal/ai/agent-plugins.git
ap: gitlab.acme.internal: authentication required
    declare a credential with --auth-secret-env <VAR> or --auth-secret-file <path>
    ap never stores a token; only the binding name is recorded
$ echo $?
1

jcm@laptop ~/work $ ap install claude:plan marketplace acme-plugins \
>     --type plugins \
>     --git https://gitlab.acme.internal/ai/agent-plugins.git \
>     --auth-secret-env GITLAB_TOKEN

claude:plan
  ✓ market…    acme-plugins       plugins
               gitlab.acme.internal/ai/agent-plugins.git @ c04e772 (main)
               auth  basic-oauth2  $GITLAB_TOKEN     (scheme inferred from host)
               14 plugins in .claude-plugin/marketplace.json

jcm@laptop ~/work $ ap install claude:plan plugin code-review@acme-plugins

claude:plan
  ✓ plugin     code-review        c04e772
               entry  git-subdir  gitlab.acme.internal/…  plugins/code-review
               + commands/review.md        → .claude/commands/review.md
               + agents/reviewer.md        → .claude/agents/reviewer.md
               + skills/diff-reader/       → .claude/skills/diff-reader/  (3)
               ~ .mcp.json                   merge  mcpServers.acme-lint
               – hooks/                      claude adapter drops "hooks"

jcm@laptop ~/work $ ap install claude:plan plugin doc-writer@acme-plugins

ap: doc-writer@acme-plugins: entry host differs from marketplace host
      marketplace  gitlab.acme.internal
      entry        github.com   (url  https://github.com/some-vendor/doc-writer.git)
    your credential for gitlab.acme.internal was NOT sent to github.com.
    fetching anonymously.

claude:plan
  ✓ plugin     doc-writer         3f9d10b   anonymous
               + commands/docs.md          → .claude/commands/docs.md

jcm@laptop ~/work $ ap list claude:plan

claude:plan  ~/.local/share/agent-profile/profiles/claude/plan

├─ marketplaces
│  ├─ anthropic-skills   skills    github.com/anthropics/skills.git    8a71c2e
│  └─ acme-plugins       plugins   gitlab.acme.internal/ai/…           c04e772
│                                  auth  basic-oauth2  $GITLAB_TOKEN
├─ skills
│  └─ pdf                anthropic-skills   4 files
├─ plugins
│  ├─ code-review        acme-plugins       5 files
│  └─ doc-writer         acme-plugins       1 file
├─ mcps
│  └─ memory             http  memory.acme.internal
└─ prompt                append  CLAUDE.md

jcm@laptop ~/work $ ap manifest export claude:plan > plan.yaml
jcm@laptop ~/work $ cat plan.yaml
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
prompt:
  mode: append
  content: |
    Prefer small diffs. Cite file:line.
marketplaces:
  acme-plugins:
    type: plugins
    source:
      git:
        url: https://gitlab.acme.internal/ai/agent-plugins.git
        ref: main
        auth:
          scheme: basic-oauth2
          value_from:
            secret: gitlab-token
  anthropic-skills:
    type: skills
    source:
      git:
        url: https://github.com/anthropics/skills.git
        ref: main
        subpath: skills
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
plugins:
  code-review:
    ref: code-review@acme-plugins
  doc-writer:
    ref: doc-writer@acme-plugins
skills:
  pdf:
    ref: pdf@anthropic-skills
```

The colleague, on a fresh machine:

```console
ana@laptop ~/work $ ap create claude:plan
ana@laptop ~/work $ ap manifest apply claude:plan ./plan.yaml
ap: secret "gitlab-token" is not available
    inputs.secrets.gitlab-token reads $GITLAB_TOKEN, which is unset
    referenced by: marketplace "acme-plugins"
$ echo $?
1

ana@laptop ~/work $ export GITLAB_TOKEN=glpat-REDACTED MEMORY_TOKEN=mt-REDACTED
ana@laptop ~/work $ ap manifest apply claude:plan ./plan.yaml

claude:plan
  ✓ skill      pdf                4 files
  ✓ plugin     code-review        5 files
  ✓ plugin     doc-writer         1 file      anonymous
  ✓ mcp        memory             .mcp.json  +mcpServers.memory
  ✓ prompt     append             CLAUDE.md
  ✓ ledger     16 files, 2 definitions

ana@laptop ~/work $ ap run claude:plan
```

And the self-hosted GitLab that does not have `gitlab` in its hostname:

```console
jcm@laptop ~/work $ ap install claude:plan marketplace acme-int \
>     --type plugins \
>     --git https://code.acme.internal/ai/plugins.git \
>     --auth-secret-env GITLAB_TOKEN
ap: code.acme.internal: 401 Unauthorized
    tried  bearer      (scheme inferred from host)
    a GitLab instance needs basic-oauth2; override with --auth basic-oauth2
    or set source.git.auth.scheme in the manifest
$ echo $?
1

jcm@laptop ~/work $ ap install claude:plan marketplace acme-int \
>     --type plugins --auth basic-oauth2 \
>     --git https://code.acme.internal/ai/plugins.git \
>     --auth-secret-env GITLAB_TOKEN

claude:plan
  ✓ market…    acme-int           plugins
               code.acme.internal/ai/plugins.git @ 7b2e903 (main)
               auth  basic-oauth2  $GITLAB_TOKEN     (explicit)
```

---

## What each moment is testing

| Moment | Finding |
|---|---|
| missing `memory-token` binding, exit 1 before anything runs | §12 resolution-phase failure, with the **referrer named** |
| `--dry-run authenticated and fetched` | **F6** — dry run is not offline, and says so |
| `authentication required … ap never stores a token` | **F2** — the binding is recorded, never the value; the divergence from `ach-cli`'s `credentials.json` is visible in the message |
| `auth basic-oauth2 (scheme inferred from host)` | **F1** — the inference is printed, not silent |
| `– hooks/  claude adapter drops "hooks"` | §8 — no silent degradation |
| `entry host differs … was NOT sent to github.com` | **F5** — the guard, and it announces itself |
| `ap list claude:plan` showing marketplaces | **F3** — definitions are in the ledger, not just files |
| exported `auth.scheme: basic-oauth2` + synthesised `inputs` | **F7** — inference resolved and written down; binding named |
| Ana's run: `doc-writer  1 file  anonymous` | the guard is a property of the resolution, so it reproduces |
| `tried bearer (scheme inferred from host)` on 401 | **F1** — a 401 that cannot be debugged is the actual bug |
