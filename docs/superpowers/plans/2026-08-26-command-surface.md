# Command surface — proposed, with use cases

Written against the real `cmd/ap/main.go`, not invented. House style observed
there and kept:

- `ap <command> <agent>:<profile>[:<variant>] [args...]` — the reference is
  positional.
- Flags work on either side of the reference (`parseAroundRef`), except for
  `run`, which parses none after it.
- `--` appears only where argv is captured verbatim (`ap variant … -- <args>`).
- `--yes` / `-y` is the gate. `--dry-run` prints the plan. `--raw` is the
  script-friendly output.
- **"There is no active profile: every command names one explicitly."**

---

## The reference keeps its two forms

```
claude:plan          a named profile      → <profiles>/claude/plan
claude:default       the real config dir  → ~/.claude          (exists today)
```

Both are **one mechanism** — point the agent's config-directory variable at a
directory — which is why v1 has exactly these two. `claude:default` keeps the
`--yes` gate and the resolved-absolute-path display it already has.

### The third form is deferred, and stays available

A project form was designed and cut (2026-08-26). Recording the design so it is
not re-derived: `agentreg.ValidName` rejects a name starting with `.`, containing
`/` or `\`, or using anything outside `[A-Za-z0-9_-]`, so a path can never
collide with a profile name and `claude:./` needs no new flag and introduces no
ambiguity.

```
claude:./            the project here     → ./          DEFERRED
claude:./some/repo   an explicit project  → ./some/repo DEFERRED
```

Why not now: it is a *second* mechanism. Nothing is redirected — the agent reads
its working directory — so it is the only root that needs a containment rule
(write under the agent's dot-dir, plus the one project-root file that agent
genuinely reads) and a per-agent list of what that file is. That list grows every
release, which is the shape `CLAUDE.md` already rejected once for `--from-base`.
`ach-cli` serves this case today. Additive whenever wanted: `ValidName` already
makes the form unambiguous, and bare destinations already mean "the root".

## New commands — WHERE first, then WHAT

Two shapes were considered. **Where-first wins**, and one collision settles it.

```
(A) what first    ap skill   add       claude:plan pdf@anthropic-skills
(B) where first   ap install claude:plan skill     pdf@anthropic-skills   ← chosen
```

Four reasons, in order of weight:

1. **Every existing command is `ap <verb> <ref>`.** `run`, `create`, `which`,
   `env`, `delete`, `link`, `unlink`, `variant` — all of them. Shape (A) would be
   the only command in the tool where the reference is not the first argument.
2. **`main.go` says "There is no active profile: every command names one
   explicitly."** If the root is the thing that must always be stated, it belongs
   in the same column every time. (A) buries it behind a kind token.
3. **Two new commands instead of eight.** (A) needs `skill add|rm`,
   `plugin add|rm`, `mcp add|rm`, `marketplace add|rm`. (B) needs `install` and
   `uninstall`, with the kind as an argument. Smaller dispatch, smaller help,
   less to keep consistent.
4. **`history | grep claude:plan`** shows everything ever done to that profile,
   because the reference is always in the same position.

### The collision that decides the verb

`rm` is already taken: `case "delete", "rm":` deletes a profile. So `add`/`rm` is
not available under (B), and the reason is not cosmetic —

```console
$ ap rm claude:plan skill xlsx    # would remove one skill
$ ap rm claude:plan               # DELETES THE PROFILE
```

Two tokens lost to a typo, a shell history edit or a truncated script line, and
the second command is the first one's blast radius. `install` / `uninstall`
cannot be confused with `delete` / `rm` at any arity, and they are the verbs
`ach-cli` already uses, so the migrating muscle memory transfers on the word even
though the shape differs.

### The surface — three new commands

The architecture has exactly two inputs to a root: **one loose capability**, or
**a whole manifest**. The command surface is that split and nothing else.

```
# one capability at a time
ap install    <ref> <kind> <name> [locator flags]
ap uninstall  <ref> <kind> <name>

# a whole manifest
ap manifest apply  <ref> <path>  [--dry-run] [--manifest -] [--yes]
ap manifest render <path>        [--target <agent>] [--quiet]
ap manifest export <ref>

  <kind> = skill | plugin | mcp | marketplace | artifact
```

Three new top-level commands. `sync` is deleted in Phase 7, so the net change is
**+2**.

Plus one extension, no new command: **`ap list` accepts a qualified reference.**
It takes `[<agent>]` today and would reject `claude:plan`, so there is nothing to
collide with — it is one more level of the tree it already prints.

```console
$ ap list                 every agent, its profiles, their variants   (today)
$ ap list claude          one agent                                   (today)
$ ap list claude:plan     that profile's installed resources          (new)
```

In `ap --help` the two groups read as the architecture:

```
install    Install one capability into a profile
uninstall  Remove one capability
manifest   Apply, render or export a whole manifest
```

### The rules underneath

**Split by mode, not by argument type.** An earlier draft grouped by what the
first argument *is*, which put `apply` at the top level next to `install` and hid
the thing that matters: `install` changes one resource, `apply` changes
everything a file declares.

**The first argument after the verb is the subject.** `install`, `uninstall`,
`manifest apply` and `manifest export` take a reference — a root being inspected
or changed — exactly as `run`, `create`, `which`, `env` and `delete` do.
`manifest render` takes a path because it has no root: it answers what a file
*means*, before any root is chosen.

**The kind is stated, never inferred.** `pdf@anthropic-skills` could be resolved
to a kind by asking the marketplace's `type`, but a bare `--git` source cannot,
and `mcp` takes entirely different arguments. This repository's rule is that the
author states the position rather than the tool guessing it — the same rule that
governs `{}` in a variant.

### What was folded or cut, and why

| Not built | Instead | Reason |
|---|---|---|
| `ap manifest render <path>` | `ap manifest render <path>` | Same pipeline. With no `--target` it composes every target the manifest declares and the **exit code is the answer** — all `ach` and `ach-agent` need. `--quiet` suppresses the profile. A separate command would be the same code behind a second name. |
| `ap manifest prune` / `apply --prune` | *deferred* | Whole-root convergence is a set difference over `ap uninstall`, which is in v1 and is what actually shapes the ledger — prune needs nothing recorded that uninstall does not already record. Its only named consumer is `ach-runtime`'s init container, which is Phase 9 in another repository. Additive: when it lands it is a flag on `apply`, not a second subcommand. Trigger: that container shipping. |
| `ap status <ref>` | `ap list <ref>` | Same question at a deeper level of a tree that already exists. |
| `ap manifest schema` (deferred) | *deferred* | `ach-agent` needs the binary anyway in order to apply, and `ap manifest render` already validates by exit code. Revisit for editor autocomplete (yaml-language-server) — a different user, not a v1 need. |
| `ap outdated <ref>` | *deferred* | The one command that exists purely to inform. Without a lockfile every apply already resolves to latest, so "outdated" only means "your last apply is stale" — and `ap install <same ref>` updates either way. It is **additive**: the ledger already records the `resolvedSHA` it needs, so adding it later breaks nothing, unlike `uninstall`, which shapes the ledger's design and therefore had to be in v1. Trigger: someone asks "am I behind?" twice. |

---

# Use cases

## 1. Empezar de cero, sin manifiesto

The case that killed the "verbs edit a manifest" idea: install one skill into one
profile, and never write a YAML file.

```console
$ ap create claude:plan
$ ap install claude:plan skill pdf --git https://github.com/anthropics/skills.git \
                                    --subpath skills/pdf
added skill "pdf" → ~/.local/share/agent-profile/profiles/claude/plan/skills/pdf
  from github.com/anthropics/skills.git @ 8a71c2e (main)

$ ap run claude:plan
```

No manifest exists anywhere. The ledger records it.

Note what is **not** typed: `--target claude`. `ach-cli` needs that flag because
its verbs address a tool; here the reference already names the agent.

## 2. Un marketplace, y varias cosas de él

Register the catalogue once, then address items by name — the manifest's `ref`
form, typed.

```console
$ ap install claude:plan marketplace anthropic-skills --type skills \
    --git https://github.com/anthropics/skills.git --subpath skills

$ ap install claude:plan skill pdf@anthropic-skills
$ ap install claude:plan skill xlsx@anthropic-skills

$ ap list claude:plan
claude:plan  ~/.local/share/agent-profile/profiles/claude/plan

  marketplaces
    anthropic-skills   skills   github.com/anthropics/skills.git @ 8a71c2e

  skills
    pdf                anthropic-skills   8a71c2e   4 files
    xlsx               anthropic-skills   8a71c2e   6 files
```

`--git`/`--subpath` and `<item>@<marketplace>` are the two locator forms the
schema already has (§22's exclusive group), typed instead of written.

## 3. Compartir lo que montaste a mano

The payoff of "the manifest is an input": you built the profile by hand, and now
you want it to be portable.

```console
$ ap manifest export claude:plan > team-plan.yaml
$ cat team-plan.yaml
version: "1"
name: plan
targets:
  - claude
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
  xlsx:
    ref: xlsx@anthropic-skills

$ git add team-plan.yaml && git commit -m "chore: share the plan profile"
```

A colleague:

```console
$ ap create claude:plan
$ ap manifest apply claude:plan ./team-plan.yaml
```

They get `main` as it is that day. **v1 does not pin** — see "No lockfile in v1".

## 4. El manifiesto como entrada, y `--dry-run`

```console
$ ap manifest render ./team-plan.yaml                  # validate every target, exit code answers
$ ap manifest render ./team-plan.yaml --target claude    # what it WOULD produce, for claude
$ ap manifest apply claude:plan ./team-plan.yaml --dry-run
would materialise into ~/.local/share/agent-profile/profiles/claude/plan
  + skill pdf      github.com/anthropics/skills.git @ 8a71c2e  (4 files)
  ~ skill xlsx     overwrite 6 files
nothing written

$ ap manifest apply claude:plan ./team-plan.yaml
```

`--dry-run` runs the whole resolution phase — sources fetched into the cache,
contracts checked, secrets confirmed present — and stops before writing. That
matters because apply overwrites, and a failure halfway leaves a namespace
half-written.

## 5. Tu configuración real, con la puerta

```console
$ ap install claude:default skill company-review@internal
claude:default is the real configuration claude already uses:
  /home/jcm/.claude
This is not a profile. ap cannot undo it.
Continue? [y/N]
```

Off a terminal it refuses; a pipe is not consent. `--yes` covers it. Same single
gate `ap sync` has today, same display of the resolved absolute path.

## 6. Un repositorio, para el equipo — DEFERRED past v1, shown for the record

The project root is cut from v1 (see "The third form is deferred" above);
`ach-cli` covers this case today. Kept because it is the clearest illustration of
why `plugins` is one common type, which **is** in v1.

```console
$ cd ~/work/payments-api
$ ap manifest apply claude:./ ./agent-profile.yaml       # DEFERRED
materialised into ./
  + .claude/skills/company-review/     (7 files)
  + .claude/commands/deploy.md
  + .mcp.json                          merged 1 key: mcpServers.memory
```

Containment: writes only under `.claude/`, plus `.mcp.json`, the one project-root
file claude actually reads. The user's `README.md` and `CLAUDE.md` are never
touched.

Same manifest, a different agent, same repository:

```console
$ ap manifest apply codex:./ ./agent-profile.yaml
materialised into ./
  + .codex/agents/company-review.toml   (frontmatter rewritten)
  ~ .codex/config.toml                  merged 1 table: mcp_servers.memory
```

One `plugins` resource, routed per runtime. That routing is why `plugins` is a
common type and not four `artifacts` with hand-written destinations.

## 7. Quitar cosas — el pago del ledger

```console
$ ap uninstall claude:plan skill xlsx --dry-run
would remove skill "xlsx"
  remove  skills/xlsx/SKILL.md
  remove  skills/xlsx/scripts/convert.py
  skip    skills/xlsx/notes.md          modified since install
nothing removed

$ ap uninstall claude:plan mcp memory
removed mcp "memory"
  .mcp.json   removed key mcpServers.memory   (3 other keys kept)
```

Two properties, both from `ach`'s `FileRec{relPath, hash, merge, keys}`:

- a file whose **hash** no longer matches was edited by you, so it is reported
  `skip`, never removed;
- a deep-merged file loses **only its recorded dotted keys** — every key you
  added by hand survives.

Which is also why removal works against `claude:default`, the case SPEC v0.5 gave
up on: the ledger can tell ap's writes from yours.

Apply stays additive, and v1 ships no flag that changes that. Whole-root
convergence (`--prune`) is deferred to its first real consumer — see the folded
table above.

## 8. Drift

```console
$ ap outdated claude:plan   # DEFERRED past v1 — shown for the record
REF                        INSTALLED   UPSTREAM   STATUS
pdf@anthropic-skills       8a71c2e     8a71c2e    up to date
company-review@internal    3f9d10b     c04e772    outdated

$ ap install claude:plan skill company-review@internal   # re-resolves, re-installs
```

Read-only: it re-resolves each recorded SHA and discards the result. Exactly what
`ach-cli outdated` does today, and it needs no lockfile — the ledger's recorded
SHA is the "installed" column.

## 9. Sin cabeza: contenedor, CI, `ach-runtime`

```console
$ ap manifest apply claude:default --manifest - --yes < profile.yaml
```

- `--manifest -` reads stdin, so `ach` and `ach-agent` pipe a generated manifest
  with no temp file.
- Inside a container the real config dir *is* the target, so `claude:default` is
  the right root and the `--yes` gate is what makes it non-interactive. No TTY;
  off a terminal the gate refuses without it.
- `--prune`, which would make the root match the input exactly, is deferred to
  Phase 9 — `ach-runtime`'s init container is its only caller, so it gets built
  against a real one.

## 10. Migrar desde `ap sync`

```console
# before
$ ap sync ./agent-profiles --yes

# after
$ ap manifest apply claude:plan ./agent-profiles/plan.yaml
$ ap manifest apply claude:exec ./agent-profiles/exec.yaml
```

What changes: `install:` shell one-liners become declared resources, so nothing
runs `sh -c` on your behalf and the `--yes` gate is only about writing to
`claude:default`. What survives unchanged: variants, `{}`, and the manifest
living in a git repository you clone.

---

## No lockfile in v1

Decided 2026-08-26. `ref: main` means whatever `main` is today, and if tomorrow
it is different, it is different.

- SPEC §32 in full, `--frozen`, append-only lock semantics and the lock-commit
  boundary in §37: **all out of v1.**
- **Kept:** the advisory single-writer mutex (`flock` / `LockFileEx`) — a
  different thing that happens to share the word "lock". Two processes must not
  write one root at once.
- **Kept:** `resolvedSHA` in the ledger. It is a receipt, not a pin, and it is
  what makes `ap outdated` work — `ach-cli` proves the pattern, having shipped
  with no lockfile at all.
- **Consequence, stated rather than discovered:** two machines applying the same
  manifest on different days can get different bytes. That is the accepted
  trade, and it is the same one `ach-cli` makes today.
- Reintroduction trigger, recorded so it is not re-argued from scratch: someone
  needs two machines to agree, or a build to be reproducible from a manifest
  alone. Then §32 comes back as written — it is designed, just not built.

---

## Still to confirm

Nothing. The grammar is settled (where first, `install` / `uninstall`), the
reference keeps its two existing forms, and the project form, `--prune` and
`ap outdated` are recorded above as deferred with their triggers.
