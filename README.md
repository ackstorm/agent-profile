# agent-profile

`ap` launches `claude`, `codex`, `opencode` or `pi` with a named profile, so each
profile carries its own plugins, skills and MCP servers — while your sessions,
your login and your workspace trust stay shared with your normal setup.

Linux and macOS only, by design - see "Deliberately out of scope".

## Why

Every MCP server you connect and every skill and plugin you install is paid for
on **every request**, not on the requests that use it. Tool schemas and skill
descriptions live in the system prompt: they are re-sent each turn, they compete
for the model's attention, and they push your actual work closer to the context
limit.

So the maximal setup is the wrong default. Planning does not need the deployment
MCP server. A code review does not need the scaffolding skills. But keeping one
configuration that suits all of them means it suits none of them, and trimming it
by hand before each task is not something anyone actually does.

`ap` gives each kind of work its own config directory, so `claude:plan` loads what
planning needs and nothing else. What it deliberately does *not* fork is the part
that is expensive to duplicate: you log in once, your session history stays in one
place, and the workspace-trust prompt does not come back.

## Use

```bash
ap create claude:plan                               # new, empty profile
ap run claude:plan plugin install caveman@caveman   # populate it
ap run claude:plan                                  # work in it
ap create claude:review --from plan                 # clone it
ap list
```

There is **no active profile**. Every command names one. A bare `claude` in any
shell still uses your normal `~/.claude` — that boundary is the point: you can
never install something into a profile you only thought you were in.

## Commands

| Command | What it does |
|---|---|
| `ap agents` | supported agents, their variable and their mode |
| `ap list [agent]` | your profiles |
| `ap create [--from <profile>] <agent>:<profile>` | create, optionally cloning |
| `ap which <agent>:<profile>` | the profile directory, for editing by hand |
| `ap env <agent>:<profile>` | exactly which variable would be set (for reading, not for `eval`) |
| `ap run <agent>:<profile> [args...]` | run it; args pass through verbatim |
| `ap delete <agent>:<profile>` | remove the profile, never the shared state |

Profiles live in `${XDG_DATA_HOME:-~/.local/share}/agent-profile/profiles/<agent>/<profile>/`.

### Flag order matters for `run`

Everything after `ap run`'s reference is passed to the agent untouched — that is
what lets you write `ap run claude:plan --effort xhigh` without `ap` trying to
interpret `--effort`.

`ap run` parses no flags of its own at all, so there is nothing to collide with:
`ap run opencode:review --pure` passes opencode's own `--pure` to opencode.

`ap create` is different, because it has nothing to pass through: `--from` works
on either side of the reference.

```bash
ap create claude:review --from plan        # clones claude:plan
ap create --from plan claude:review        # the same thing
```

`--from` takes a bare profile name, never `<agent>:<profile>`, because a profile
can only be cloned within its own agent — the source and the destination always
share one. Putting the flag after the reference makes that read correctly: the
agent is already stated to the left of the name.

## How it works

`ap run` sets one environment variable and `exec`s the real binary. The process
is replaced, so the agent owns the terminal directly: signals, the TUI and `ps`
behave exactly as if you had typed the agent's name.

| Agent | Variable | Value |
|---|---|---|
| claude | `CLAUDE_CONFIG_DIR` | the profile |
| codex | `CODEX_HOME` | the profile |
| pi | `PI_CODING_AGENT_DIR` | the profile |
| opencode | `XDG_CONFIG_HOME` | the profile's config shim |

All four replace their config root, so a profile loads exactly what you put in
it. Nothing under `XDG_DATA_HOME`, `XDG_STATE_HOME` or `XDG_CACHE_HOME` is ever
redirected, which is what keeps sessions, logins and caches shared.

### opencode needs a config shim

opencode has no private config-dir variable. `OPENCODE_CONFIG_DIR` only *appends*
to its search path, and so do `OPENCODE_CONFIG` and `OPENCODE_CONFIG_CONTENT`. Its
config root is computed as

```
(XDG_CONFIG_HOME || ~/.config) / opencode
```

and nothing else feeds into it, so `XDG_CONFIG_HOME` is the only lever there is.

Setting it naively would be indefensible: it is a freedesktop-wide variable, so
every process opencode spawns — git, gh, npm, language servers — would start
looking for *their* config inside the profile. So the profile carries a shim
directory, and that is what the variable points at:

```
<profile>/xdg/opencode  ->  <profile>          the only thing opencode finds
<profile>/xdg/git       ->  ~/.config/git      passthrough
<profile>/xdg/gh        ->  ~/.config/gh       passthrough
<profile>/xdg/…         ->  one per entry of your real ~/.config
```

opencode finds only the profile under its own name — that single omission is the
isolation — while everything else follows a link to its own real config. The shim
is rebuilt on every `ap run`, because `~/.config` gains entries over time and a
profile created last month must not hide a tool installed yesterday.

**The one cost.** A program run inside opencode that creates a *brand-new* config
directory writes it into the shim, where it is invisible from outside the profile.
`ap` detects that on the next run and tells you which one and where to move it. It
is never deleted.

### What every profile shares

Created as symlinks by `ap create` and re-asserted on every `ap run`:

| Agent | Shared |
|---|---|
| claude | `projects/`, `.credentials.json`, `.claude.json`, `CLAUDE.md`, `plugins/cache/` |
| codex | `sessions/`, `auth.json`, `history.jsonl` |
| pi | `sessions/`, `auth.json` |
| opencode | nothing — its sessions and auth live under `XDG_DATA_HOME`, which we never touch |

So: one login per agent works in every profile, `-r` sees your whole history from
any profile, and Claude Code's workspace-trust prompt does not come back
(`.claude.json` holds `hasTrustDialogAccepted`).

The links are re-created on every run because agents rewrite their credential
files — codex refreshes OAuth tokens into `auth.json` — and a write via
temp-file-plus-rename would silently replace the symlink with a regular file. If
`ap` finds real data where a link belongs, it stops and says so instead of
overwriting it.

`plugins/cache/` is shared because it is content-addressed by
`marketplace/name/version`: no point re-downloading every plugin per profile.

**Accepted cost of sharing `.claude.json`:** MCP servers added with
`claude mcp add` are recorded per project in that file, so they show up in every
profile. Put per-profile MCP servers in the profile's own `settings.json` under
`mcpServers`.

### Cloning

`--from <profile>` copies a profile of the same agent, skipping symlinks and
anything at a shared path. There is no `--from-base`: copying out of `~/.claude`
would need a per-agent list of what to take from a directory that grows with
every release, and a stale list copies caches silently. Set the first profile up
by hand — a curated minimal set is the point — then clone it. For a one-off,
`cp ~/.claude/settings.json $(ap which claude:plan)/`.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/ackstorm/agent-profile/main/install.sh | bash
```

That picks the archive for your OS and architecture, **verifies it against the
release's `checksums.txt` before writing anything**, and installs to
`~/.local/bin`. Override either end:

```bash
curl -fsSL .../install.sh | PREFIX=/usr/local/bin VERSION=v0.1.0 bash
```

Piping a script into a shell deserves a look first — `install.sh` is at the root
of this repository, it is a hundred lines, and `make shellcheck` gates it.

Prebuilt archives for linux and macOS (amd64 and arm64) are attached to each
release if you would rather do it by hand:

```bash
sha256sum -c checksums.txt
./ap version
```

From source — **docker is the only requirement**. There is no Go toolchain to
install: the build runs inside a pinned devtools image (`Dockerfile.devtools`),
cross-compiled for your host, and the binary lands in your working directory
owned by you.

```bash
make build          # ./ap, stamped with version, commit and build date
make install        # copy it into ~/.local/bin (override with PREFIX=)
make doctor         # check docker, the image, and what it contains
```

The image is built on first use and pins Go along with golangci-lint,
govulncheck, goreleaser and gitleaks, so `make verify` means the same thing on
your machine as in CI. If you would rather use a host toolchain, set
`AP_IN_DEVTOOLS=1` and every target runs directly — but mind the Go version
floor, which is a security floor, not a language requirement. See CLAUDE.md.

## Verifying

```bash
make verify   # fmt-check, shellcheck, vet, lint (incl. gosec), test -race, vulncheck
make secrets  # gitleaks over the full history
make smoke    # drives the four real binaries; needs them installed and logged in
make fuzz     # 60s against the path validation
make hooks    # install a pre-push hook that runs verify
make shell    # a shell inside the devtools image
```

Everything above is containerised except `smoke`, which has to stay on the host:
it drives the real agent binaries and asserts your real session count is
unchanged afterwards.

Releases are cut with `make release VERSION=vX.Y.Z`. It gates first and tags
last, so a failed release never leaves a tag behind on origin.

`make test` runs 57 tests with `-race -shuffle=on`, all against fakes.
`make fuzz` targets `ValidName`, because that is the boundary a traversal bug got
through once: it asserts the property (an accepted name never resolves outside
the profile root) rather than a list of known-bad inputs.

The registry makes claims about other people's software. `scripts/smoke.sh` is
what catches an upstream change. If it fails, the registry row is usually what
needs fixing — but check first whether the *check* is lying, because two of them
originally were:

- `codex doctor` pretty-prints paths, collapsing `$HOME` to `~` and eliding the
  middle with an ellipsis, so grepping the full profile path can never match.
- `opencode debug config` emits ~730 KB but exits without waiting for the pipe to
  drain. Piping it into `grep` loses everything past 64 KiB — one pipe buffer —
  and truncates the JSON mid-string. The script captures to a file for that
  reason; do not turn it back into a pipeline.

### The one test never to delete

`TestDeleteDoesNotFollowSymlinks` in `internal/profile/share_test.go`. A `Delete`
that descended into the `projects` symlink would erase every Claude Code
transcript you have — the only irreversible bug this program can have. The test
plants a canary file in a fake real-home and asserts it survives.

It is mutation-tested, not just green: replacing `os.RemoveAll` with a variant
that calls `filepath.EvalSymlinks` first makes it fail with
`DELETE FOLLOWED THE SYMLINK AND ATE REAL DATA`. A green test that passes
vacuously would be worse than no test.

## Adding an agent

One row in `internal/agent/agent.go` plus one check in `scripts/smoke.sh`. But
the agent has to pass one test first:

> **It must have its own config-directory variable.** Not `HOME`, not
> `XDG_CONFIG_HOME`, not anything else a different program also reads.

That is not a style preference. `ap` runs the agent as its own process, and every
process the agent spawns inherits the environment — so redirecting a shared
variable would send `git`, `gh`, `npm` and every language server looking for
*their* config inside the profile directory.

To find out, set the candidate variable by hand and ask the agent where it thinks
its config lives:

```bash
CODEX_HOME=/tmp/x codex doctor
PI_CODING_AGENT_DIR=/tmp/x pi list
OPENCODE_CONFIG_DIR=/tmp/x opencode debug config > /tmp/cfg.json
CLAUDE_CONFIG_DIR=/tmp/x claude -p --debug-file /tmp/x.log "ok"
```

If the answer is "only when I set `HOME`", the agent cannot be supported. That is
a dead end, not a to-do: stop there rather than reaching for a wrapper.

## Deliberately out of scope

- **Windows.** A non-goal, not a gap: `syscall.Exec` has no equivalent there and
  symlinks need privileges, so supporting it would mean a second execution model
  and a second sharing mechanism to keep correct. The `//go:build unix` tags say
  so at compile time.
- **`ap use` / `ap shell` / an active profile.** A "current profile" that a bare
  `claude` would ignore is hidden state that lies to you.
- **`--from-base`.** See Cloning above.
