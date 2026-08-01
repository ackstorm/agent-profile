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
ap variant claude:review:on -- '/code-review {}'    # {} = your argument, at run time
ap run claude:review:on src/auth.go                 # runs "/code-review src/auth.go"
ap env claude:plan npx skills add <src> --skill <s> -g -a claude-code
ap env codex:plan env | grep CODEX                  # what a command inherits
ap list                                             # everything, as a tree
ap list --raw | cut -f1                             # the same, for scripts
```

There is **no active profile**. Every command names one. A bare `claude` in any
shell still uses your normal `~/.claude` — that boundary is the point: you can
never install something into a profile you only thought you were in.
`<agent>:default` is the one deliberate exception, and it stays read-only for
exactly that reason — see "`default`" below.

## Commands

| Command | What it does |
|---|---|
| `ap list [--raw] [agent]` | your profiles and their variants, as a tree — always includes `default`, and every supported agent. `--raw` prints the same listing tab-separated, for scripts |
| `ap create [--from <profile>] [--only-settings <key>]... [--copy-instructions] <agent>:<profile>` | create it and a wrapper so it is a command you can type, optionally cloning one (`--from default` clones your real config, `--only-settings` narrows that to a few keys of one file) and seeding it with your global instructions file |
| `ap variant <agent>:<profile>:<variant> -- <args...>` | name a set of launch arguments over an existing profile — same configuration, a different way to start it. May leave `{}` where your run-time arguments should be substituted, which is how a variant becomes a prompt prefix |
| `ap which <agent>:<profile>[:<variant>]` | the profile directory, for editing by hand — a variant has none of its own, so it answers for the parent |
| `ap env <agent>:<profile>[:<variant>]` | exactly which variable would be set (for reading, not for `eval`) |
| `ap env <agent>:<profile>[:<variant>] <cmd> [args...]` | set it and run `cmd` — `env(1)`, for tools that install into the agent's config directory. `cmd` never receives a variant's arguments: those are the agent's flags |
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

`ap list` prints each one under the profile it varies, arguments included, so a
name that disables every permission prompt never becomes invisible:

```
claude
├─ claude:default             (the agent's own config: read-only)
├─ claude:finops
├─ claude:plan
└─ claude:review
   ├─ claude:review:ci        --dangerously-skip-permissions --model=claude-opus-5[1m] -p
   └─ claude:review:opus      --dangerously-skip-permissions --model=claude-opus-5[1m] --effort=xhigh
codex
└─ codex:default              (the agent's own config: read-only)
opencode
└─ opencode:default           (the agent's own config: read-only)
pi
└─ pi:default                 (the agent's own config: read-only)
```

Every reference is qualified, including a variant's, so any line is exactly what
you type after `ap run` — the tree could get away with printing the leaf name
alone, and does not, because the listing is read to answer "which one was it?"
and the answer has to be copyable where it is read. The arguments are not
pasteable: they are printed unquoted, and `--model=claude-opus-5[1m]` in zsh is
`no matches found`. They are the last column and are allowed to overflow, the
deal `ps aux` makes with `CMD`.

For scripts there is `--raw`, so nothing has to parse the format meant for
reading: one line per reference, the reference in field 1 and one argument per
field after it, tab-separated, no tree and no padding.

```console
$ ap list --raw | cut -f1                      # every reference ap run accepts
$ ap list --raw | awk -F: 'NF==3'              # just the variants
$ ap list --raw | grep -c dangerously          # how many skip permissions
```

One argument per field is the shape the store already has — one argument per
line — so there is no quoting to invent and none to get wrong. It costs one more
stated limit: **an argument may not contain a tab**, alongside the newline the
store already refuses, and `ap variant` says so at write time.

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
two limits it buys: **an argument may not contain a newline**, which is the
store's own separator, or **a tab**, which is `ap list --raw`'s. `ap variant`
says so at write time rather than inventing an escape syntax for either.

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

**A variant that ends in a prompt is terminal, unless it leaves `{}`.**
`claude`'s grammar takes one trailing positional, so a variant ending in
`"/code-review"` composes with flags but not with a second prompt — and the
second one is **dropped in silence**, not refused: `claude -p "say FIRST" "say
SECOND"` answers `FIRST` and exits 0.

To make a variant a prompt *prefix* you complete at run time, leave a hole where
your arguments go, spelled the way `xargs -I{}` spells it:

```bash
ap variant claude:plan:exec -- --effort=xhigh '/superpowers:executing-plans {}'

ap run claude:plan:exec docs/plans/some-plan.md
claude:plan:exec        docs/plans/some-plan.md   # the wrapper, unchanged
```

Your arguments are joined with a space and substituted there — reaching the
agent as **one** element of `argv`, which is what appending can never produce —
and are not also appended at the end. Every placeholder in the variant fills,
like `xargs -I`. Running it with nothing to add leaves the prefix alone, so the
slash command asks you itself.

A variant that never mentions `{}` composes exactly as it always did: arguments
first, yours after. The hole is opt-in by typing it.

This is not `ap` guessing which of your baked arguments is the prompt — that
would mean deciding that `/code-review` is a positional while the `opus` in
`--model opus` is not, which is a claim about four external CLIs that would need
re-checking every release. You state the position; `ap` substitutes text and
infers nothing. There is **no escape for a literal `{}`**, the same kind of
stated limit as the newline and the tab: `claude --agents '{"reviewer":{}}'`
baked into a variant would be substituted. `ap variant` prints the composed line
when it creates one, so that shows up where you write it rather than at run
time.

### Flag order matters for `run`

Everything after `ap run`'s reference is passed to the agent untouched — that is
what lets you write `ap run claude:plan --effort xhigh` without `ap` trying to
interpret `--effort`.

`ap run` parses no flags of its own at all, so there is nothing to collide with:
`ap run opencode:review --pure` passes opencode's own `--pure` to opencode.

`ap create` is different, because it has nothing to pass through: `--from`,
`--only-settings` and `--copy-instructions` all work on either side of the
reference.

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

### `--only-settings` — a few keys, not a config

A fresh profile is empty on purpose, and that is right for skills, commands,
hooks, agents and MCP servers. It is wrong for the two or three settings that
make a terminal feel like yours. `--only-settings` narrows a clone to the named
keys of the agent's settings file and skips every other file:

```bash
ap create claude:new --from default --only-settings statusLine --only-settings theme
ap create codex:new  --from default --only-settings tui
ap create claude:new --from otro    --only-settings mcpServers.linear
```

The flag repeats, needs `--from`, and composes with `--copy-instructions`, which
is a different flag over a different file.

Naming a parent takes its children — `tui` brings `[tui]` and `[tui.*]`,
`mcpServers` brings every server. A key that is not in the source is a warning
naming it, not a failure. Paths split on `.`, so a key holding a literal dot
cannot be named; it is reported as not found.

JSON files (claude, pi, opencode) are decoded and re-encoded with two-space
indent. `codex`'s `config.toml` is never parsed — whole `[table]` blocks are
copied verbatim, so comments and formatting survive exactly and every byte that
comes out went in. A `config.toml` containing a multiline string (`"""` or
`'''`) is refused, because a `[` at the start of a line inside one cannot be
told from a table header without a TOML parser, and this program has no
dependencies.

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
make sandbox  # home-safety checks against a throwaway home, with stub agents
make smoke    # the four real agents, in their own image
make fuzz     # 60s against the path validation
make hooks    # install a pre-push hook that runs verify
make shell    # a shell inside the devtools image
```

Everything above is containerised, including `smoke`. It used to need claude,
codex, opencode and pi installed and logged in on the host, and wrote to the
real home to do its work — which made the release gate something exactly one
machine could run. The four agents now live in `Dockerfile.smoke`, unpinned on
purpose: smoke exists to catch the day one of them changes what it does with the
variable `ap` hands it, and a pinned agent freezes the thing under observation.

It needs **no credentials**. That was measured, not assumed, and it was not the
first answer:

| Check | Needs |
|---|---|
| profile `settings.json` changes the resolved model | nothing — the debug log records the resolved model before authentication fails |
| `codex doctor`, `pi list`, `opencode debug paths` | nothing |
| plugin marketplace add / install / list | network, no model |
| `codex login status` through the shared `auth.json` | nothing — `login status` reads that file and masks what it finds without validating it, so a synthesised `auth.json` answers the question, which is whether the profile reaches the file through `ap`'s symlink |
| claude reaching its credential through the shared link | nothing — the two answers differ where it matters: a profile that cannot get to the credential says `Not logged in · Please run /login`, one that read it and had it rejected says `Failed to authenticate`. Only the first is a broken link, and only the first is `ap`'s business |
| a clone materialising its declared plugin | `ANTHROPIC_API_KEY` — it happens during a session. Skipped without one, and already a `warn` because it is claude's asynchronous work |

So the only key that buys anything buys a `warn`, and CI needs no secret. Both
synthesised credentials were built from the **field names** of a real one, never
a value.

Two orderings in there are load-bearing, and both were found by reverting a
guard rather than by reading the code. The symlink assertion runs *before* the
agent does: given a credential it cannot refresh, claude replaces the file with
one of its own, which is exactly why `Link` re-asserts the symlink on every `ap
run` — asserted afterwards it goes red because claude did its job. And the
authentication message goes to stdout, never to `--debug-file`, so grepping the
debug log for it is a check that cannot fail.

The seeded home is load-bearing. Every "shared state survived" assertion is
vacuous against an empty one, and adding a check means seeding whatever would
let it fail — a `[user]` git section with no keys under it made the shim's
passthrough check compare zero settings against zero and call that a pass.

`sandbox` is the half of that which never needed a real agent. It builds a home
that has been used — configuration, credentials, transcripts, and a `~/.config`
holding four other programs — inside the container, puts a stub on `PATH` in
place of each agent, and asks whether `ap` keeps its hands off any of it: what
`delete` removes, what `--from` copies, what the shim links, what argv and what
environment `run` execs with. Every one of its checks was confirmed to go red
with its guard reverted. It is not a substitute for `smoke`: the registry's
claims are about what the real binaries do with the variable they are handed,
and a stub cannot answer that. Running `smoke` inside a container is worse than
not running it — every block is gated on `command -v <agent>`, so all twelve
skip and it exits 0 announcing "all checks passed".

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
