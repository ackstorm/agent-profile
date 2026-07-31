# agent-profile

`ap` launches `claude`, `codex`, `opencode` or `pi` with a named profile, so each
profile is a genuinely separate environment — its own plugins, skills, MCP
servers, config and session history — while your login stays shared with your
normal setup, so there is exactly one thing to sign in to per agent.

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
planning needs and nothing else. The one thing it deliberately does *not* fork is
the one thing that cannot be recreated locally: your login. Everything else —
plugins, skills, MCP servers, config, session history, workspace trust — is the
profile's own, which is also why a `plan` session's transcript never shows up
inside an `exec` profile that never installed the tools it used.

## Use

```bash
ap create claude:plan                               # new, empty profile
ap run claude:plan plugin marketplace add owner/repo # a marketplace of its own
ap run claude:plan plugin install caveman@caveman   # populate it
ap run claude:plan                                  # work in it
claude:plan                                         # same thing, typed directly
ap create claude:review --from plan                 # clone it
ap create claude:work --copy-instructions           # seed it with your CLAUDE.md
ap variant claude:review:opus -- --model='claude-opus-5[1m]' --effort=xhigh
ap run claude:review:opus                           # those arguments, then yours
ap env claude:plan npx skills add <src> --skill <s> -g -a claude-code
ap env codex:plan env | grep CODEX                  # what a command inherits
ap list
```

There is **no active profile**. Every command names one. A bare `claude` in any
shell still uses your normal `~/.claude` — that boundary is the point: you can
never install something into a profile you only thought you were in.
`<agent>:default` is the one deliberate exception, and it stays read-only for
exactly that reason — see "`default`" below.

## Commands

| Command | What it does |
|---|---|
| `ap list [agent]` | your profiles — always includes `default`, and every supported agent |
| `ap create [--from <profile>] [--copy-instructions] <agent>:<profile>` | create it and a wrapper so it is a command you can type, optionally cloning one (`--from default` clones your real config) and seeding it with your global instructions file |
| `ap variant <agent>:<profile>:<variant> -- <args...>` | name a set of launch arguments over an existing profile — same configuration, a different way to start it |
| `ap which <agent>:<profile>` | the profile directory, for editing by hand |
| `ap env <agent>:<profile>` | exactly which variable would be set (for reading, not for `eval`) |
| `ap env <agent>:<profile> <cmd> [args...]` | set it and run `cmd` — `env(1)`, for tools that install into the agent's config directory |
| `ap run <agent>:<profile>[:<variant>] [args...]` | run it; a variant's arguments come first, then yours, both passed through verbatim |
| `ap delete [--yes] <agent>:<profile>[:<variant>]` | remove the profile, its variants and their wrappers — including its own session history; see "What every profile shares" below. Asks first, and `--yes` is how a script answers. A variant on its own is removed without asking: it is two lines of text |
| `ap unlink <agent>:<profile>[:<variant>]` | remove the wrapper, keep the profile or variant |
| `ap link <agent>:<profile>[:<variant>]` | write the wrapper back |

Profiles live in `${XDG_DATA_HOME:-~/.local/share}/agent-profile/profiles/<agent>/<profile>/`.

### Installing into a profile with something that is not the agent

Plugins go in through the agent itself, marketplace first, and both steps are
ordinary passthrough — `ap run` hands everything after the reference to the
agent verbatim:

```bash
ap run claude:plan plugin marketplace add DietrichGebert/ponytail
ap run claude:plan plugin install ponytail@ponytail
```

The marketplace is the profile's, not yours: its clone, the plugin cache and the
`enabledPlugins` entry all land under `ap which claude:plan`, so the same
marketplace can be absent from `claude:review` and from a bare `claude`.

Skills and most third-party installers are separate tools, though. They find
their target by reading the same variable `ap` sets, so `ap env` with a command
is all they need:

```bash
ap env claude:plan npx skills add vercel-labs/agent-skills \
  --skill web-design-guidelines -g -a claude-code
```

The source is not limited to `org/repo` on GitHub — a git URL works, so a
private or self-hosted collection installs the same way:

```bash
ap env claude:finops npx skills add git@github.com:aws/agent-toolkit-for-aws.git \
  --skill aws-billing-and-cost-management --agent claude-code --yes --global
```

Verified against [vercel-labs/skills](https://github.com/vercel-labs/skills),
whose `src/agents.ts` resolves claude's home as `CLAUDE_CONFIG_DIR || ~/.claude`
and installs global skills into `<that>/skills`. `-g` is not optional: without
it the skill lands in `./.claude/skills` in the current directory, which is
project scope and has nothing to do with the profile.

The same installer then reports where it put it:

```console
$ ap env claude:plan npx skills list -g
web-design-guidelines  ~/.local/share/agent-profile/profiles/claude/plan/skills/web-design-guidelines
  Agents: Claude Code  Source: vercel-labs/agent-skills
```

This is `env(1)`'s second form and it behaves like it — the variable is set for
that one command and nothing outlives it. `ap env <agent>:<profile>` on its own
still just prints. Everything after the reference belongs to the command, so
its own flags arrive untouched.

Which makes `env` itself the shortest way to see what a command would inherit:

```console
$ ap env codex:plan env | grep CODEX
CODEX_HOME=/home/user/.local/share/agent-profile/profiles/codex/plan

$ ap env opencode:plan env | grep XDG_CONFIG_HOME
XDG_CONFIG_HOME=/home/user/.local/share/agent-profile/profiles/opencode/plan/xdg

$ ap env claude:default env | grep -c CLAUDE_CONFIG_DIR
0
```

Three things in one view: codex gets its own private variable; opencode gets the
shim directory rather than the profile itself, which is what keeps `git` and
`npm` out of it (see "The opencode asymmetry"); and `default` sets nothing at
all, because your real config is already where the agent looks.

Piping works the same way, since `ap` execs rather than wrapping:

```bash
npx skills use vercel-labs/agent-skills@web-design-guidelines | claude:plan
```

### `default` — your real config, read-only

`<agent>:default` is not a profile `ap` made; it names whatever config the agent
already uses when `ap` is not involved (`~/.claude`, `~/.codex`, `~/.pi/agent`,
`~/.config/opencode`). Nothing is created for it, nothing is linked, no shim is
built — `ap run codex:default` sets no config variable at all, so codex behaves
exactly as if you had typed `codex` yourself.

It exists for two things: reaching your normal setup through the same command as
every profile (`ap run codex:default mcp`), and starting a new profile from the
configuration you already have (`ap create codex:work --from default`).

**Read-only, always.** `ap create claude:default`, `ap delete claude:default`,
and `ap link claude:default` all refuse — the last because there is nothing to
link, `ap run codex:default` already reaches the real thing directly.

### A profile is a command you can type

`ap create claude:plan` writes a small wrapper to `~/.local/bin/claude:plan`
(`exec ap run claude:plan "$@"`), so once that directory is on your `PATH`,
`claude:plan --effort xhigh` works the same as `ap run claude:plan --effort
xhigh`. `ap delete` removes it again, so a deleted profile never leaves behind
a command that fails confusingly.

It is part of `create` rather than a second step because a profile you cannot
type is a profile you do not use. `ap unlink claude:plan` opts out and keeps the
profile; `ap link claude:plan` writes it back, which is also what profiles
created before this behaviour existed need.

Nothing here touches a file `ap` did not write — the wrapper carries a marker
line for exactly that check. If the name is already taken by something else,
`create` says `not linked: ...` and carries on: the profile is fine and `ap run`
reaches it.

The wrapper always goes to `~/.local/bin`, regardless of where the `ap` binary
itself is installed, and it names `ap` by `PATH` lookup rather than its own
location — see "Install" for why that directory gets a `PATH` warning
unconditionally. (`AP_LINK_DIR` overrides the location — an escape hatch for
tests, not something to reach for day to day.)

**Tab completion, if you want it.** The colon in these names predates variants
and behaves differently per shell. zsh 5.9 completes them as-is. bash treats `:`
as a word separator (`COMP_WORDBREAKS`), so after `claude:` it falls back to
completing filenames; one line in your `~/.bashrc` fixes it globally:

```bash
COMP_WORDBREAKS=${COMP_WORDBREAKS//:}
```

`ap` does not do this for you: it would be a global mutation of your shell from
a tool whose whole thesis is not touching what belongs to someone else.

### Launch variants — one configuration, several ways to start it

A profile is expensive: plugins, skills, marketplace caches, session
transcripts. How you *launch* it is a handful of flags. The same
`claude:review` usually wants at least two launch modes — interactive, and `-p`
for pipes — and having two profiles to get them means duplicating the expensive
half to vary the cheap one, then keeping the two configs in sync by hand
forever.

A third segment names a launch mode over an existing profile:

```bash
ap variant claude:review:opus -- --dangerously-skip-permissions --model='claude-opus-5[1m]' --effort=xhigh
ap variant claude:review:ci   -- --dangerously-skip-permissions --model='claude-opus-5[1m]' -p

ap run claude:review:opus                 # those arguments
ap run claude:review:opus --effort=high   # those arguments, then yours — later wins
claude:review:opus                        # the wrapper, same as any profile
```

`ap list` prints them under their profile, arguments included, so a name that
disables every permission prompt never becomes invisible:

```
claude:    default review finops plan
             review:opus   --dangerously-skip-permissions --model=claude-opus-5[1m] --effort=xhigh
             review:ci     --dangerously-skip-permissions --model=claude-opus-5[1m] -p
```

Those argument lines are for reading, not for pasting — they are printed
unquoted, and `--model=claude-opus-5[1m]` in zsh is `no matches found`. The
thing to type is the name: `claude:review:opus`.

**Why not a shell alias?** Two reasons. An alias does not appear in `ap list`,
and a name that carries `--dangerously-skip-permissions` needs to be
enumerable. An alias also does not exist outside an interactive shell, so it is
unreachable from a script, from CI, from a Makefile, or from another agent
shelling out — a wrapper in `~/.local/bin` is reachable from all four, and `ap
run claude:review:opus` needs no shell at all.

The arguments live in
`${XDG_DATA_HOME:-~/.local/share}/agent-profile/variants/<agent>/<profile>/<variant>`,
one per line — outside the profile directory, because that directory belongs to
the agent. Not in the wrapper: a name is invocable only if `ap run` can resolve
it, and arguments baked into a wrapper would either be invisible to `ap run` or
make it read back the file it wrote. The store is also why there is no quoting
to get wrong anywhere in this feature — arguments go from the file to
`syscall.Exec` as a list, so `[1m]` cannot glob and `$(…)` cannot execute. The
one limit it buys: **an argument may not contain a newline**, and `ap variant`
says so rather than inventing an escape syntax.

A few things a variant deliberately is not:

- **Not a profile.** No directory, no shim, no links, no first-run seeding.
  `ap which claude:review:opus` and `ap env claude:review:opus` answer for the
  parent, because that is literally its configuration.
- **Not editable in place.** `ap variant` refuses one that exists, exactly as
  `ap create` refuses a profile that exists. `ap delete` then `ap variant` is
  how arguments change; there is no `--force` for a two-line file.
- **Not cloned by `--from`.** A clone is a different profile, and inheriting
  `--dangerously-skip-permissions` without being asked is the opposite of what a
  new profile should do.
- **Not deeper than three.** `<agent>:<profile>:<variant>` and no further.
- **Not applied by `ap env`.** `ap env claude:review:opus npx skills add …` runs
  `npx` with the profile's variable set and *without* the variant's arguments:
  those are the agent's flags.
- **Not reconciled.** If you remove a profile directory by hand, its variants
  stay in the store and `ap list` will not show them, because it lists the
  profiles that exist. `ap run` still fails correctly, naming the missing
  profile.

Deleting a profile takes its variants with it, and names them while asking —
a variant without its parent is a command that fails confusingly:

```console
$ ap delete claude:review
? remove ~/.local/share/agent-profile/profiles/claude/review
  and its 2 variants: opus, ci [y/N]
```

**A variant that ends in a prompt is terminal.** `claude`'s grammar takes one
trailing positional, so a variant ending in `"/code-review"` composes with flags
but not with a second prompt. `ap variant` prints the composed line when it
creates one, so you can see it then rather than the first time it fails.

### Flag order matters for `run`

Everything after `ap run`'s reference is passed to the agent untouched — that is
what lets you write `ap run claude:plan --effort xhigh` without `ap` trying to
interpret `--effort`.

`ap run` parses no flags of its own at all, so there is nothing to collide with:
`ap run opencode:review --pure` passes opencode's own `--pure` to opencode.

`ap create` is different, because it has nothing to pass through: `--from` and
`--copy-instructions` both work on either side of the reference.

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
it — including, for claude, codex and pi, their session history: it lives inside
that same config root, so replacing it is what makes each profile's history its
own. `XDG_DATA_HOME`, `XDG_STATE_HOME` and `XDG_CACHE_HOME` are never redirected
at all; that is what keeps opencode's sessions, auth and caches global across
profiles, since its own data lives under those instead of under its config root.

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

**The credential, and nothing else.** A profile is a different environment —
that is what the word means. Everything a session depends on (config, plugins,
skills, MCP servers, instructions) lives in the profile; anything that records a
session belongs there too.

Created as a symlink by `ap create` and re-asserted on every `ap run`:

| Agent | Shared |
|---|---|
| claude | `.credentials.json` |
| codex | `auth.json` |
| pi | `auth.json` |
| opencode | nothing — see the asymmetry below |

The link is re-created on every run because agents rewrite their credential
files — codex refreshes OAuth tokens into `auth.json` — and a write via
temp-file-plus-rename would silently replace the symlink with a regular file. If
`ap` finds real data where a link belongs, it stops and says so instead of
overwriting it.

**History is not shared, and `--from` never copies it either** — `projects/` for
claude, `sessions/` and `history.jsonl` for codex, `sessions/` for pi. A `plan`
session resumed inside an `exec` profile would replay a transcript full of tool
calls to MCP servers and skills that are not installed there; the transcript
describes an environment that no longer exists once it is opened somewhere else.

Costs, stated plainly:

1. **One workspace-trust prompt per profile per project.** `.claude.json` — where
   `hasTrustDialogAccepted` lives — is not shared either.
2. **Plugin content is downloaded once per profile**, not once per machine.
3. **`ap delete` removes that profile's session transcripts.** If you want a
   profile's history, copy it out first. It names the directory and asks before
   removing anything, and the default answer is no; `--yes` skips the question,
   which is also the only way it works with no terminal to ask.

Login survives on the credential alone. Onboarding does not: a profile holding
nothing but the credential is logged in, but claude still opens on its theme
picker, because the flag that gets past it lives in `.claude.json` and a new
profile has none. So `ap create` copies that one flag —
`hasCompletedOnboarding`, and nothing else in that file — from your real config
into the new profile, once, at create. Everything else in there is session
state, per-project trust and prompt history, and stays where it is.

**The opencode asymmetry.** opencode's sessions, auth and account state all live
under `XDG_DATA_HOME`, which `ap` deliberately never redirects — redirecting it is
exactly what the config shim exists to avoid doing to every other program in the
process tree (see above). So opencode gets auth and account sharing **for free**,
with no code for it, but its *sessions* stay global across profiles too, which
the one-credential rule would rather they were not. Known, not worth a second
shim.

A direct consequence: **a new profile starts completely empty**, and populating
it is real work, different per agent. `ap create` prints the next step for a
fresh (non-cloned) profile, which is different for each of the four.

**`--copy-instructions`** seeds a fresh profile with your global instructions
file (`CLAUDE.md` for claude — the only agent with a verified one today; the flag
errors by name for the others rather than guessing a path). It is a **copy taken
once, at create** — not a share. Nothing re-asserts it afterwards, so your real
file and the profile's copy are free to drift apart from the moment `ap create`
returns; that is the point of copying rather than linking.

### Cloning

`--from <profile>` copies **configuration only** — an explicit allowlist per
agent (`CloneAllow` in `internal/agent/agent.go`: for claude, `settings.json`,
`CLAUDE.md`, `skills`, `commands`, `hooks`, `agents`; similar short lists for
codex, pi and opencode). Everything else — accumulated runtime, caches, session
history, the config shim — is left behind simply by never being named.

An allowlist rather than a list of exclusions, and the difference matters. A
real config directory is mostly accumulated runtime — 1.9 GB of it on the
machine this was measured on, between transcripts, plugin content and tool
caches — and that set grows with every upstream release of the agent. An
exclusion list would start copying each new cache directory silently the day
it appears; an allowlist instead simply does not clone a new config file until
someone adds it, which is visible and fixable rather than a slow leak.

Hardcoded in the registry for now, and headed for a per-machine config file —
the known gap in the meantime is a hook script, statusline or plugin config
living somewhere the list does not name, which is silently not cloned rather
than copied on a guess.

`--from default` clones straight out of the agent's real config using the same
allowlist — see "`default`" above. Set the first profile up by hand — a curated
minimal set is the point — then clone it from there. For a one-off,
`cp ~/.claude/settings.json $(ap which claude:plan)/`.

Two things a clone carries over without being fixed, because `ap` places files
rather than reading them: a hook command that names its script by absolute path
into the real home keeps running that real script from inside the profile —
point it at `$CLAUDE_CONFIG_DIR` (or the agent's own config variable) instead,
which resolves to whichever profile is running. And a cloned codex profile can
end up with `config.toml` saying a plugin is enabled while `codex plugin list`
reports it as not installed, because codex does not reconcile that declaration
against its own cache on its own — fix it with `codex plugin add <plugin>@<marketplace>`.

**Claude has the same gap, in one specific stage.** A clone's `settings.json`
carries both `enabledPlugins` and `extraKnownMarketplaces`, but claude reads
marketplaces from `plugins/known_marketplaces.json`, which is state and is
therefore not cloned — it records an absolute `installLocation` inside the source
profile, so carrying it would point a clone at another profile's directory.
Converting the declaration into a registration is background work at session
start, and measured on claude v2.1.220 it is reliable only for the official
marketplace: a third-party one took 3 session starts once, 5 the next, and had
not happened after 4 starts on two consecutive runs, with the session log saying
`Skipping orphaned enabledPlugins entry <plugin>@<marketplace>: marketplace not
registered`.

Only that one stage is unreliable. Register the marketplace by hand and the rest
follows in a single start:

```bash
ap run claude:review plugin marketplace add owner/repo   # the stage that stalls
ap run claude:review -p ok                               # installs, deterministically
```

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

`install.sh` warns if `PREFIX` ends up off your `PATH` — and, **regardless of
`PREFIX`**, if `~/.local/bin` is too, since `ap create` always writes its
wrappers there: installing `ap` itself to `/usr/local/bin` does not change
where `claude:plan` ends up. Each warning names the exact line to add to your
shell profile.

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

`make test` runs 84 tests with `-race -shuffle=on`, all against fakes.
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
- **A separate `--from-base` flag.** `--from default` covers the same ground
  through the existing flag — see Cloning above — so a second one would be
  redundant, not missing.
