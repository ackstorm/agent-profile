# `ap sync` — SPEC v1

**Status:** Implemented (2026-08-24)
**Amended (2026-08-24):** `--allow-default` was removed after implementation, on
the author's decision — `--yes` now covers a `name: default` manifest too. §6.1
and §11 below describe the two-gate design as specified; the reasoning there is
kept as the record of why it existed, and what replaced it is the display: a
default target is printed with its resolved absolute path and named as the real
config in both `--dry-run` and the prompt. Everything else in §6.1 still holds —
nothing is created for the sentinel, and variants are still refused under it.
**Version:** 1
**Scope:** Declarative agent profiles, reproduced from a Git repository.
**Principle:** YAGNI. Two lists of shell commands do 95% of the work.

---

## 1. Goal

`ap` already gives isolated profiles per agent:

```text
claude:plan     codex:plan
claude:execute  codex:execute
claude:review   codex:review
```

v1 adds one file format and one command, so a team can keep those profiles in
Git and a new developer can materialise them:

```bash
git clone git@github.com:company/agent-profiles.git
ap sync ./agent-profiles
```

That is the whole feature. Everything below exists to make that command
predictable, re-runnable, and safe to point at a repository somebody else wrote.

---

## 2. What v1 deliberately does not have

An earlier draft had `skills:` and `plugins:` alongside `install:`. Both are cut.

`install` subsumes them. A skill is `git clone` plus a copy; a plugin is a key
in the agent's own settings file, written by the agent's own subcommand
(`claude plugin install`, `codex plugin add`). Shipping three mechanisms for one
job on day one is the thing YAGNI exists to stop.

Cutting them also removes a contradiction the draft could not resolve: it said
`ap` implements plugin installation directly **and** that `ap` must never
maintain a compatibility matrix. Those cannot both hold. Measured, in
`internal/agent/agent.go`:

- **claude** keeps plugin intent in `settings.json` (`extraKnownMarketplaces`,
  `enabledPlugins`) and re-materialises the content itself over the next couple
  of starts.
- **codex** keeps it in `config.toml` and **never** reconciles a declaration
  against its cache. Verified: a profile cloned with only `config.toml` reported
  the plugin as not installed and stayed that way through a full session. One
  `codex plugin add <p>@<m>` fixes it, idempotently.
- **pi** has no plugin concept and no skills path at all — its `CloneAllow` is
  `settings.json` and nothing else.

One artifact, three mechanisms, one of which does not exist. That knowledge is a
compatibility matrix whatever it is called. `install` hands it back to the agent
that owns it, which is the only party that can be right about it.

Our sister project already owns that matrix, k8s-free and behind a binary. See
§17.2: the v2 plan for skills and plugins is to call `ach-cli`, not to grow
`ap`.

Also out of v1, and out for the same reason — nothing in the goal needs them:

```text
MCP declarations          lockfiles                 profile inheritance
skills:/plugins: keys     package versions          includes
binary/package management marketplaces              cross-manifest merge
dependency resolution     compatibility database    uninstall / prune
RBAC                      policy / governance       remote profile registry
workflow orchestration    ACH dependency
```

---

## 3. Example

```yaml
version: 1
name: execute

bootstrap:
  - "curl -fsSL https://raw.githubusercontent.com/rtk-ai/rtk/refs/heads/master/install.sh | sh"
  - "ach-cli repo add https://github.com/anthropics/skills --name anthropics"

platforms:
  claude:
    install:
      - "ach-cli skill install pdf@anthropics --global"
      - "npx get-shit-done-cc@latest --claude --global"
      - "rtk init -g"
    variants:
      opus:
        args: --model=claude-opus-5 --effort=xhigh
      execute-plan:
        args: --effort=xhigh "/superpowers:executing-plans {}"

  codex:
    install:
      - "rtk init -g --codex"
      - "npx get-shit-done-cc@latest --codex --global"
    variants:
      execute-plan:
        args: -s danger-full-access -a never "/superpowers:executing-plans {}"
```

Materialises:

```text
claude:execute          codex:execute
claude:execute:opus     codex:execute:execute-plan
claude:execute:execute-plan
```

### 3.1 Why `repo add` is in `bootstrap` and `skill install` is not

The two `ach-cli` lines look like one operation and are not, and the split is
the clearest illustration of §8's two levels.

Read from ACH's source, `internal/cli/localpkg/store/store.go`: the local
package manager persists its repository registry under `~/.config/ach/local/`,
resolved through `XDG_CONFIG_HOME` when set. That is **machine state**. It is
the same registry whichever profile is active, `repo add` is idempotent, and
registering it once per platform would just do the same write twice.

`ach-cli skill install --global`, by contrast, resolves through
`CLAUDE_CONFIG_DIR` (§17.3) and therefore lands in whichever profile `ap`
pointed it at. That is **profile state**, so it belongs under the platform, and
it is written once per platform on purpose.

There is a second, sharper reason, and it only shows up on opencode. `ap` gives
an opencode profile `XDG_CONFIG_HOME=<profile>/xdg`, so an `ach-cli` invoked
there looks for `<profile>/xdg/ach/`. If `~/.config/ach` already exists, the
shim's passthrough link resolves that to the real directory and everything
works — the passthrough earning its keep exactly as `profile.Shim` intends. If
it does **not** exist yet, there is no passthrough entry for a directory that
was not there when the shim was built, so `ach-cli` creates a real
`<profile>/xdg/ach/` inside the shim: a registry invisible from everywhere else,
and one that `ap run` reports as *"real files inside the profile
XDG_CONFIG_HOME shim"*.

`bootstrap` runs before any profile or shim exists (§10), against the plain
environment, so it creates `~/.config/ach` on the machine where it belongs. The
ordering is not a convention — it is what makes a tool's own state land outside
the profiles that use it.

---

## 4. Manifest format

YAML, restricted to a subset `ap` parses itself.

The repository takes no dependencies, and the standard library has no YAML
decoder. The three ways out are: take `yaml.v3` and end the zero-dependency
claim; use JSON and make the manifests unpleasant to write; or parse a subset.
v1 parses a subset, the same call already made for TOML in
`internal/profile/settings.go`, which slices blocks without a decoder.

**The parser rejects everything it does not understand. It never guesses.** A
manifest carrying an anchor, a flow collection or a block scalar is an error
naming the construct and the line, not a best-effort parse. Silently misreading
a file somebody else wrote is worse than refusing it.

### 4.1 Grammar

Accepted:

- One document per file. No `---`, no `...`, no multi-document files.
- Block mappings: `key: value` or `key:` followed by a more-indented block.
- Block sequences: `- item`, one per line.
- Scalars: bare, or double-quoted with `\\` and `\"` as the only escapes.
  Everything else inside quotes is literal.
- Indentation with spaces. A child is strictly more indented than its parent;
  siblings match exactly.
- Blank lines. Full-line comments (`#` first after the indent) and trailing
  comments (` #` outside a quoted scalar).

Rejected, each with its own error:

- tabs in indentation
- flow collections (`[a, b]`, `{a: b}`)
- block scalars (`|`, `>`)
- anchors, aliases, merge keys, tags (`&`, `*`, `<<`, `!!`)
- multi-document markers
- single-quoted scalars — one quoting style, so there is one escaping rule
- duplicate keys in one mapping

### 4.2 Typing

Every scalar is a string. There are no booleans, numbers or nulls: nothing in
the schema needs one. `version` is compared as the string `"1"`.

A key with nothing after the colon and no indented block is *empty*. The parser
reports it as empty and does not decide whether that means an empty mapping or
an empty string; the schema layer decides, per key. That is what lets
`claude:` under `platforms` mean "this platform, no configuration".

### 4.3 When a scalar must be quoted

Bare scalars cover most of a manifest, including the ones that look alarming:
`--model=claude-opus-5`, `--effort=xhigh`, `-s`, `-a` and `never` all parse
bare, because the `- ` of a sequence entry is consumed before the value is read.

Double quotes are required when the value:

- contains `: ` (colon then space), or ends with `:` — otherwise it is a mapping
- contains ` #` — otherwise the rest of the line is a comment
- begins with any of ``{ [ & * ! | > % @ ` `` — each opens a construct §4.1
  rejects, so the error would name the construct rather than the quoting
- begins with `- ` (dash then space) — otherwise it is a sequence entry
- has leading or trailing spaces that matter

A bare scalar keeps any quotes **inside** it verbatim; YAML only treats a quote
as special at the start of a scalar. That is what lets
`args: --effort=xhigh "/superpowers:executing-plans {}"` be a bare scalar whose
inner quotes survive for §7's tokenizer to read. When the value needs YAML
quoting as well, alternate the two styles —
`args: "--system '/role: helper'"` — or escape with `\"`.

Which is why `"/superpowers:executing-plans {}"` is quoted twice over — a colon
and a brace — while `--effort=xhigh` beside it is not. Both forms in one list is
normal and correct.

---

## 5. Schema

```text
version   required, must be "1"
name      required, the logical profile name
bootstrap optional, list of shell command strings — run once, no agent env
platforms required, at least one entry
  <platform>          a name ap supports: claude, codex, opencode, pi
    install           optional, list of shell command strings — run in that
                      platform's agent environment
    variants          optional, mapping of variant name to:
      args            required, a string (tokenized) or a non-empty list of
                      strings (verbatim) — §7
```

Unknown keys at any level are an error. A schema that ignores what it does not
recognise turns a typo (`varients:`) into a silently missing variant.

`name: default` is accepted and does not mean a profile at all; see §6.1.

Conceptually:

```text
Profile
├── name
├── bootstrap[]        once, machine-level, no agent variable set
└── platforms
     ├── claude
     │    ├── install[]
     │    └── variants
     └── codex
          ├── install[]
          └── variants
```

---

## 6. Identity

`name` is the logical name. The real identity is the reference `ap` already
parses:

```text
<platform>:<name>            claude:execute
<platform>:<name>:<variant>  claude:execute:opus
```

`name` and every variant name **MUST** be validated with `profile.ValidName`
before any path is built from them.

This is not a formality and it is stricter than it looks. `--from` skipped this
check once and became a path traversal that copied the user's real `~/.claude`
into a profile — and `--from` at least came from the user's own keyboard. A
manifest comes from a repository somebody else wrote, so `name: ../../../.ssh`
is the same bug with an attacker on the other end. `profile.Dir` joins and
cleans; `..` escapes.

### 6.1 `name: default` — provisioning the agent you already had

`default` is not a profile. It is the sentinel `profile.Default`, which
`profile.Dir` resolves to the agent's real, machine-wide configuration
directory: `~/.claude`, `~/.codex`, `~/.pi/agent`,
`${XDG_CONFIG_HOME:-~/.config}/opencode`.

A manifest may name it:

```yaml
version: 1
name: default

platforms:
  claude:
    install:
      - "npx get-shit-done-cc@latest --claude --global"
```

so one repository can provision both the team's isolated profiles and the plain
agent a developer runs with no profile at all. Without it, the base agent is the
one environment a profiles repository cannot describe — and it is the one every
developer actually uses first.

**Rules, all of them consequences of Default being read-only everywhere else:**

- **`profile.ValidName` still rejects `default`, and must not be changed.** The
  loader compares the literal string first and routes it to the Default path;
  every other name goes to `ValidName` exactly as before. This is the shape
  `ParseRefAllowDefault` already uses, for the same reason: "is this a safe path
  component" and "is this the sentinel" are two different questions, and merging
  them is how `--from` became a traversal (§6).
- **Nothing is created.** No `profile.Create`, no `Link`, no `Shim`, no
  `seedFirstRun`, no wrapper script. The directory exists or that platform fails
  with the message `prepare` already gives: *"claude's real config directory does
  not exist"*.
- **`Link` must never run against it.** The shared credential is not linked
  *into* the real config directory — it *is* the file in the real config
  directory. Pointing a symlink at itself is a no-op at best and destroys the
  credential at worst.
- **`install` runs with no override at all**, exactly as `ap run claude:default`
  does: `run.Env` strips the agent's config variable and sets nothing in its
  place. The command sees the environment the agent sees when `ap` is not
  involved, which is the entire point of naming it.
- **`variants` is not accepted under a default platform** — §15.
- `claude:default` takes part in duplicate detection like any other identity.

**It gets its own gate, separate from `--yes`.**

Everywhere else, a bad manifest ruins a directory under
`~/.local/share/agent-profile/profiles`, and `ap delete` removes it. Here, a
third-party repository's shell commands run against the configuration of the
agent the developer uses every day, and `ap` has no undo for that.

- On a terminal, `ap sync` names the resolved absolute directory it is about to
  provision and asks about it **separately** from the general install prompt
  (§11). Bundling them would let one `y` cover both an isolated profile and the
  real home.
- Off a terminal, `--allow-default` is required. **`--yes` alone does not cover
  it.** `--yes` means "do not ask me"; this is not a question about convenience.
- `--dry-run` prints the resolved absolute path of every default target, so what
  is at stake is visible before anything runs.

---

## 7. Variants

A variant is written exactly as `ap variant` writes one, through
`profile.WriteVariant`, and is stored in the same place. Whichever form `args`
takes, what lands on disk is one argv list, so `ap list` and `ap run` cannot tell
which was typed.

### 7.1 `args` takes a string or a list

```yaml
args: --model=claude-opus-5 --effort=xhigh              # string, tokenized
args: -s danger-full-access -a never "/plan:run {}"     # quotes group a token

args:                                                    # list, verbatim
  - --model=claude-opus-5
  - --effort=xhigh
```

The string is the normal form. Five lines of YAML to say four flags is a tax on
every variant anybody writes, and most variants have no argument that needs
grouping at all.

The list stays, and is the canonical form, for the cases where it is worth being
explicit: an argument containing quotes of both kinds, an argument that is
literally a quote character, or any time the author wants no tokenizer between
what they wrote and what the agent receives. When the two forms disagree, the
list is right by definition — it is the storage format.

### 7.2 The string is tokenized, never evaluated

This is the whole safety of the feature and it is one sentence: `args` is split
into tokens, and **nothing is expanded**.

| | recognised | |
|---|---|---|
| whitespace | separates tokens | runs of spaces and tabs collapse |
| `"…"` `'…'` | group one token | the quotes are removed; the other style inside is literal |
| `\` | escapes the next character | outside single quotes only |

Everything else is a literal character. **No** variable expansion, **no** command
substitution, **no** globbing, **no** tilde, **no** `;`, `&&`, `|` or
redirection. `--prompt $HOME` reaches the agent as the two characters `$H`… — as
typed, unexpanded.

That is deliberate and it is the opposite of `install` (§8.4), which really does
go through `sh -c`. The two fields are now both strings and look alike, so the
difference has to be stated rather than inferred:

| | `install` | `args` |
|---|---|---|
| Goes to | `sh -c` | a tokenizer |
| `$VAR`, `*`, `` ` `` | expanded by the shell | literal characters |
| Purpose | run a command on this machine | hand argv to the agent |

An agent's arguments are data. A prompt is the most likely thing to contain `$`,
a backtick or an asterisk, and having `ap` interpret any of them would corrupt
the prompt in a way that is invisible until the agent answers the wrong question.

### 7.3 Tokenize first, substitute second

`{}` keeps the meaning it already has: `runArgs` substitutes the caller's
arguments, joined with a space, into every occurrence, and does not also append
them. A variant with no `{}` composes as before — variant args first, caller's
after. There is no escape for a literal `{}`.

The order is load-bearing. The string is tokenized **before** `{}` is
substituted, so a caller argument can never change how many tokens a variant has:

```text
args: --effort=xhigh "/plan:run {}"
ap run claude:x:execute-plan 'fix the parser'

tokenize  →  ["--effort=xhigh", "/plan:run {}"]
substitute →  ["--effort=xhigh", "/plan:run fix the parser"]
```

Substituting first would have produced four tokens, silently turning one prompt
into three arguments — and claude takes one trailing positional and drops a
second without a word (measured: `claude -p "say FIRST" "say SECOND"` answers
FIRST, exit 0). A caller argument containing a quote character would be worse
still: it would re-open quoting and re-cut every token after it. Tokenizing
first makes that class of bug unreachable rather than unlikely.

### 7.4 Re-syncing overwrites a variant of the same name

Sync writes variants with `replace = true`, so re-syncing a manifest converges.
**This overwrites a hand-made variant of the same name**, and the report says so
with `updated` rather than `created`. That is the only place v1 destroys
anything a user typed, and it is why it is visible in the report and in
`--dry-run`.

---

## 8. `bootstrap` and `install` — the 95%

Two lists of shell commands, at two levels, because there are two jobs and they
need two different environments:

| | `bootstrap` | `install` |
|---|---|---|
| Where | the logical profile | one platform |
| Runs | once per manifest, before any platform | once per platform |
| Environment | no agent variable set at all | that platform's, profile and shim included |
| For | putting a tool on the machine — `ach-cli`, `ripgrep`, `npm` packages | putting content in the profile |
| Failure | fails **every** platform in that manifest | fails **that** platform |

`ap` does not interpret what either one does. Between them they may install
tools, commands, hooks, MCP servers, skills, plugins, or anything else. That is
the point: the schema never has to grow a concept per artifact type.

### 8.1 Why `bootstrap` is not just `install` at the top

An earlier draft had one list at the profile level and ran each command **once
per platform**. That leaks into the real home, and the draft's own example was
the case that leaked: `npx … --claude --global` run during **codex's** turn sees
no `CLAUDE_CONFIG_DIR` at all — `run.Env` strips it — so `--global` resolves to
the user's real `~/.claude` and writes there.

The leak came from the *semantics*, not the location. A profile-level list is
right; running it per platform was wrong. `bootstrap` runs **once**, with **no
agent variable set**, so there is no platform whose environment it could be
mistaken for and nothing for `--global` to resolve into. A command that names a
platform belongs under that platform, where it gets that platform's environment
and no other.

This is also what makes the two-phase shape useful rather than redundant:

```yaml
bootstrap:
  - "npm install -g @ackstorm/ach-cli"       # once, on the machine

platforms:
  claude:
    install:
      - "ach-cli skill install pdf@anthropics --global"   # in the profile
  codex:
    install:
      - "ach-cli skill install pdf@anthropics --global"
```

`bootstrap` gets the tool; `install` uses it, twice, each time pointed at a
different profile by the variable `ap` sets. See §17.3 for why that
`--global` lands where it should.

### 8.2 `bootstrap` writes to the machine, and cannot write to a profile

There is no profile at all in scope. `bootstrap` is step 8a of §10 and runs before step 8b —
before any profile is created — precisely so a manifest cannot express "install
this into all my profiles at once", which is the thing that leaked.

Its environment is the one `ap` inherited, with one subtraction: **any variable
whose value resolves inside `profile.Root()` is removed.** That covers the case
where `ap sync` is run from a shell that already has `CLAUDE_CONFIG_DIR` or a
shimmed `XDG_CONFIG_HOME` pointing at some profile — bootstrap must not
accidentally write there.

The rule is "points inside the profile root", not "is one of the four config
variables", and the difference is load-bearing. opencode's variable is
`XDG_CONFIG_HOME`, which every freedesktop-following program reads. Stripping it
by name would break a perfectly legitimate `XDG_CONFIG_HOME=~/myconfig` for
`npm`, `cargo` and everything else a bootstrap command might invoke. Stripping
it by *value* removes exactly what `ap` itself could have set and nothing else.

### 8.3 `install` runs in its platform's environment

Each command runs with the environment `run.Env(a, dir, os.Environ())` builds
for that platform — the same one the agent will see, shim and all. A tool that
populates a profile has to resolve the same config root the agent resolves, or
it writes somewhere the agent never reads.

`run.Env` already strips and re-sets that agent's own variables. On top of that,
the same value-based rule as §8.2 applies to everything else: a variable
pointing inside `profile.Root()` that this agent did not set is removed, so an
inherited `CODEX_HOME` cannot redirect claude's install into another profile.

An earlier version of this section said "every **other** agent's `ConfigEnv` and
shim variables are removed". That was wrong for the same reason as above: for a
claude install it would delete `XDG_CONFIG_HOME` outright, because that string
is opencode's config variable, breaking any generic tool the command shells out
to. One value-based rule covers both levels and neither special case.

### 8.4 Execution, common to both

Every command in either list runs:

- through `sh -c`, so `npx …@latest --flag` works as typed. This is the
  deliberate asymmetry with `args`: an install command is a shell one-liner by
  nature, an agent's argv must arrive verbatim. It is also the escape hatch for
  anything this spec does not provide — `PATH=$HOME/.local/bin:$PATH ach-cli …`
  is a command, not a feature request.
- in a fresh empty temporary directory, removed afterwards. Not the manifest's directory and not the user's cwd: `scripts/smoke.sh` learned this one the hard
  way, where a command that quietly resolved against the wrong cwd was
  indistinguishable from a command that ran correctly.
- with stdout and stderr streamed through to the user's terminal. A five-minute
  silent `npx` is indistinguishable from a hang, and silencing output is what
  made a failure to run indistinguishable from a failure to pass in `smoke.sh`.
- under a wall-clock timeout of 10 minutes. Unbounded waits are banned in this
  repository; a hung install must fail the manifest, not the afternoon. No flag
  for it in v1 — add one when 10 minutes is measurably wrong.

Commands run in file order and stop at the first failure within their own list.
A failed `bootstrap` fails every platform in that manifest and none of them is
attempted, because a platform install that needed the tool would fail anyway and
would fail with a worse message. A failed `install` fails only its platform;
other platforms and other manifests still run (§12).

### 8.5 A tool that ignores the variable writes to the real home, silently

This is the sharpest edge in the design and it deserves stating plainly rather
than being discovered.

`ap` sets one variable and execs. It cannot make a third-party tool read it.
`ach-cli` does (§17.3), and `npx get-shit-done-cc --claude --global` was written
against the same convention — but a tool that resolves `~/.claude` directly
instead of consulting `CLAUDE_CONFIG_DIR` writes into the developer's real
configuration, reports success, and leaves the profile empty. Nothing in `ap`
fails, because from `ap`'s side nothing did.

`rtk init -g` in §3 is exactly this case: unverified here, therefore unknown. So
is any tool an author adds later.

Verify a new tool once, the way every claim in this repository is verified — by
running it and looking:

```bash
ap create claude:probe
ap env claude:probe <the install command>

find "$(ap which claude:probe)" -mmin -2      # should be non-empty
find ~/.claude -mmin -2                       # should be empty
```

Non-empty on the last line means the tool ignored the variable. Put it in
`bootstrap` if what it writes is genuinely machine-level; otherwise do not call
it from a manifest until it grows a flag.

`ap` does not check this on the author's behalf. Watching where another program
writes means sandboxing it, and half-doing that would be worse than the honest
limit. It is also usually temporary: `rtk` has an open upstream issue asking for
`CLAUDE_CONFIG_DIR` support, which is where the fix belongs.

### 8.6 The shell's `PATH` is inherited, never managed

Separate from the config *path* the sections above are about: `ap` does not
touch the shell's `PATH` either. §3's bootstrap pipes an installer into `sh`,
and where that installer puts `rtk` is between the author and the installer. If
it lands somewhere not on `PATH` — `~/.local/bin`, `$GOPATH/bin`, an npm
prefix — the `rtk init -g` that follows fails with `command not found`, and the
report says exactly that.

Guessing the install location of npm, go, cargo and pip is a per-tool table, and
this specification does not add tables it can avoid. The author who knows where
their bootstrap put things says so in the command (§8.4).

### 8.7 Idempotency is the author's problem

`ap` re-runs every command on every sync. It cannot know which are idempotent.
Manifests should use commands that are safe to repeat — which the agents' own
subcommands are, `codex plugin add` included.

`bootstrap` runs **once per manifest, not once per sync**. Three manifests in a
directory that each bootstrap `ach-cli` run it three times. Deduplicating
identical command strings across manifests is not in v1: two manifests asking
for the same tool at different versions is a real case, and "identical string"
is the wrong test for it.

---

## 9. Loading and conflicts

`ap sync <file-or-directory>`

- A file is loaded directly, whatever its name.
- A directory is scanned **non-recursively** for `*.yaml` and `*.yml`, sorted by
  filename so a run is deterministic. Recursion is not in v1: a `node_modules`
  under a profiles repo would be walked for no reason.

All manifests are parsed and validated **before anything is created**. Any parse
or schema error aborts the whole run with nothing done.

Two manifests **MAY** share a `name` when they target different platforms:

```text
execute-claude.yaml  name: execute, platforms: claude   → claude:execute
execute-codex.yaml   name: execute, platforms: codex    → codex:execute
```

Two manifests **MUST NOT** produce the same `<platform>:<name>`. That is a
duplicate-profile error naming both files, raised before any change. There are
no merge or override semantics in v1.

---

## 10. Sync algorithm

```text
1. Load every manifest                      (abort on any parse error)
2. Validate schema and every name           (abort on any invalid name)
3. Resolve <platform>:<name> identities
4. Detect duplicates                        (abort, naming both files)
5. Build the plan
6. --dry-run? print the plan and exit 0, having changed nothing
7. Confirm bootstrap and install commands   (§11)
8. Per manifest, in sorted order:
     a. run bootstrap commands              (once, no agent variable set)
     b. per platform, in sorted order:
          i.   ensure the profile exists    (never reset an existing one)
          ii.  run that platform's install commands
          iii. create or update variants
9. Print the report, exit non-zero if anything failed
```

Step 8a runs before any profile exists, which is what stops a bootstrap command
from being able to write into one (§8.2).

Step 8b.i creates a profile exactly the way `ap create` does — `profile.Create`,
`Link`, `Shim`, `seedFirstRun`, and the wrapper script — so a synced profile is
indistinguishable from a hand-made one. For `name: default` step 8b.i is skipped
entirely and 8b.iii is a schema error, so the only thing that runs is 8b.ii,
with no config override at all (§6.1). An existing profile is reused untouched:
its sessions, credentials, settings and hand-made variants all survive.
`ap sync` **MUST NOT** reset or recreate an existing profile.

Variants come last so a profile whose install failed still gets a legible
report rather than half a set of variants and no explanation.

---

## 11. Safety

`git clone` a repository, then `ap sync` runs the shell commands inside it as
you. That is arbitrary code execution by design and it cannot be engineered
away — it is the feature. What can be done is make it impossible to happen by
surprise.

- **`--dry-run` prints the whole plan and changes nothing**: every bootstrap
  command, every profile that would be created or reused, every install command
  verbatim, every variant that would be created or updated.
- **On a terminal, `ap sync` prints the commands and asks before running any of
  them.** One prompt for the whole run, with the two lists shown separately and
  labelled by what they can reach — `bootstrap` says *this machine*, `install`
  says the profile directory it will write into. Two blast radii on one screen
  that looked alike would make the prompt worse than no prompt.
- **Off a terminal it does not ask and does not run them**, unless `--yes` is
  given. It refuses with a message naming `--yes`. This mirrors `askToPromote`,
  which checks `os.Stdin.Stat` for `ModeCharDevice` — stdlib only, no `x/term`.
  A pipe is not consent.
- **A manifest with no `bootstrap` and no `install` never prompts.** Creating
  profiles and writing variants runs no third-party code.
- **`bootstrap` is covered by `--yes`, and `name: default` is not.** The
  distinction is what each can destroy: a bootstrap command adds something to
  the machine, where a mistake is a stray package; `name: default` mutates the
  agent configuration the developer already depends on, where a mistake is their
  working setup. Additive and machine-wide is a lower bar than destructive and
  in-place.
- **`name: default` is gated on its own** (§6.1): asked about separately on a
  terminal, and requiring `--allow-default` off one. Its blast radius is the
  user's real agent configuration, not a directory `ap delete` can remove.

`--yes` skips the general install prompt and nothing else. It does not imply
`--allow-default`, and there is no flag that skips validation.

---

## 12. Errors and reporting

Independent work continues so the user gets one complete picture rather than
discovering failures one sync at a time. A failed `install` fails **that platform's
profile** and does not stop the others; a failed `bootstrap` fails every platform
in its manifest and no other manifest (§8.4).

```text
execute.yaml
  ✔ bootstrap    npm install -g @ackstorm/ach-cli

claude:execute
  ✔ profile      reused
  ✔ install      npx get-shit-done-cc@latest --claude --global
  ✔ variant      opus (created)
  ✔ variant      execute-plan (updated)

codex:execute
  ✔ profile      created
  ✗ install      npx get-shit-done-cc@latest --codex --global
                 exit status 1
  – variant      execute-plan (skipped: install failed)

1 of 2 profiles failed
```

`ap sync` exits non-zero if any operation failed.

---

## 13. Local modifications and removal

A synced profile is a normal profile. `ap run`, `ap env`, `ap variant`,
`ap sessions` and `ap resume` all work on it unchanged. The manifest describes a
baseline, not the complete and exclusive state of the profile.

v1 is **additive**. Removing a line from a manifest does not undo it: a variant
dropped from the YAML stays on disk, and nothing an install command did is
reversed. There is no ownership tracking and no prune. `ap delete` remains the
way to remove things, one reference at a time, asking first.

---

## 14. Known limits, stated rather than hidden

**A manifest is only as reproducible as its install commands.**
`npx get-shit-done-cc@latest` resolves to whatever `latest` is that day, so two
developers syncing a week apart can get different environments. `ap` does not
pin, does not lock, and does not record what was installed. Authors who need
reproducibility pin their own commands. Calling this "reproduce the team's
environment" is true of the *declaration*, not of the bytes.

**`ap sync` operates on many profiles from a file**, where every other command
names exactly one profile explicitly. That is a real departure from "there is no
active profile", and it is why `--dry-run` exists.

**Nothing verifies the manifest's origin.** There is no signing, no allowlist,
no checksum. Trust in the repository is the user's, expressed by cloning it.

---

## 15. Rejected, with reasons

- **`skills:` and `plugins:` keys** — §2. Add in v2 only if `install` proves
  clumsy; by then it will be known *how* it was clumsy, and §17.2 says what the
  v2 shape is likely to be (sugar over `ach-cli`, never a fetcher in `ap`).
- **Profile-level `install` that runs once per platform** — §8.1. It leaks into
  the real home in the draft's own example. Note what was rejected: the
  semantics, not the level. `bootstrap` is a profile-level list and is fine,
  because it runs once and no agent variable is set while it does.
- **Running unscoped install commands under a throwaway config dir for the other
  platforms** — considered as the way to keep a per-platform profile-level
  `install`. It works and it is safe, and it silently discards half the
  commands, which produces "why is my codex setup empty" instead of an error.
- **Stripping the four config variables by name** — §8.3. `XDG_CONFIG_HOME` is
  opencode's config variable *and* the variable every other program on the
  machine reads. Strip by value, or break `npm` for everyone.
- **Managing the shell's `PATH`** — §8.6.
- **Deduplicating `bootstrap` across manifests** — §8.7.
- **Verifying that an install command honoured the variable** — §8.5. `ap`
  states the limit and shows how to check it; policing another program's
  filesystem writes is a different tool, with a different threat model.
- **Bind-mounting the profile over the agent's real config directory during
  install** — a mount namespace (`CLONE_NEWNS` + `mount --bind`, or `bwrap`) or
  a FUSE overlay would make a tool that ignores `CLAUDE_CONFIG_DIR` land in the
  profile anyway, and it catches strictly more than a `$HOME` override does: a
  path derived from `getpwuid` rather than the environment is still captured.
  Rejected for v1 on four counts, the first of which is disqualifying on its
  own:

  1. **macOS has no equivalent.** `ap` supports Linux and macOS — the build tags
     say `unix`, and CI gates both. macFUSE is an out-of-tree kernel extension
     needing an administrator install. So a manifest would isolate the install
     on Linux and silently write into the real home on a Mac: the same failure
     as today, but now platform-dependent and invisible, which is worse than
     the honest limit.
  2. **Unprivileged user namespaces are not reliably available.** Several
     distributions ship them off or AppArmor-restricted, and `ap`'s own
     `make sandbox` and `make smoke` run inside containers whose default seccomp
     profile blocks `mount`. The gates would need `--cap-add SYS_ADMIN`.
  3. **It is a second execution model.** `ap` today is `syscall.Exec` after one
     variable. Go's `SysProcAttr.Unshareflags` unshares between fork and exec but
     cannot run the `mount` there, so `ap` would have to re-exec itself as a
     mount helper. That is the same objection that keeps Windows out of scope.
  4. **It would be a sandbox, and would be read as a security boundary.** It is
     not one — the tool still has the whole filesystem — and a mechanism people
     trust for more than it does is a liability.

- **Redirecting `$HOME` for install commands** — in every form: a shim over the
  home directory, a scratch home holding one symlink, a generated `HOME=…`
  one-liner. It is a trick, and it has a sharp edge on every side: two of the
  four agents keep their config more than one directory deep, `ap run` must never
  inherit it, `bootstrap` has no profile to point it at, and whatever it is
  called it reads as a sandbox while containing nothing. `ap` sets the config
  variable and stops. A tool that ignores that variable has a bug, and the fix
  belongs upstream — `rtk`'s is already an open issue.

  `CLAUDE.md`'s "`HOME` is never redirected" stands unamended.
- **A full YAML parser or `yaml.v3`** — §4. Zero dependencies is load-bearing
  here: `install.sh` ships one static binary, and `make vulncheck` gates a
  dependency set that is currently the standard library.
- **`variants` under `name: default`** — `parseVariantRef` refuses a variant
  over Default today, deliberately and with a test. Variants live in
  `VariantsRoot`, not inside a profile, so allowing it would write nothing into
  the real home and is defensible — but it is a change to core reference
  parsing, with its own tests and its own paragraph in `CLAUDE.md`, and the case
  `name: default` exists for is install-shaped. If it is wanted, change
  `ap variant` first and let `ap sync` inherit it.
- **A string-only `args`** — §7.1. Dropping the list would leave no escape when
  the tokenizer is the wrong tool: an argument holding both quote styles, or one
  that is a bare quote character. `ap`'s `{}` design exists because a subtle argv
  mistake in claude is *silent*, which is exactly when an escape hatch earns its
  keep.
- **Shell evaluation of `args`** — §7.2. `sh -c` would expand `$HOME`, globs and
  backticks inside a prompt. Agent arguments are data.
- **Recursive directory scan** — §9.
- **Prune / uninstall** — §13. Deleting things a manifest no longer mentions
  needs ownership tracking, which needs state in the profile, which is a
  different feature.
- **Pinning and lockfiles** — §14. Out of scope, and named as a limit rather
  than left to be discovered.

---

## 16. Test obligations

Every guard below gets mutation-tested the way the existing ones were: revert
the guard, run its test, confirm it fails, restore. A green test that would pass
with the guard removed is worse than no test.

- `TestSyncRejectsANameThatEscapesTheProfileRoot` — `name: ../../x` and
  `variants: {../x: …}` both fail validation, before anything is created. Fuzz
  the manifest name fields alongside the existing path-validation fuzz targets.
- `TestSyncAbortsBeforeAnyChangeOnDuplicateProfile` — two manifests, same
  `<platform>:<name>`; assert no profile directory appeared.
- `TestSyncDoesNotRunInstallOffATerminal` — feed a literal `1` down a **pipe**,
  not `</dev/null`, and assert the command did not run. Written with `</dev/null`
  it is vacuous, because an empty answer also means no.
- `TestSyncInstallSeesOnlyItsOwnAgentVariable` — set `CODEX_HOME` to a path
  inside `profile.Root()` in the parent environment, sync a claude platform,
  assert the child did not see it.
- `TestSyncKeepsAConfigVariableThatPointsOutsideTheProfileRoot` — set
  `XDG_CONFIG_HOME=~/myconfig`, sync a **claude** platform, assert the child
  still saw it. Mutation: strip by name instead of by value and this fails. This
  is the test that stops §8.3's bug coming back.
- `TestSyncBootstrapRunsBeforeAnyProfileExists` — a bootstrap command that lists
  `profile.Root()` sees nothing the manifest would create.
- `TestSyncBootstrapSetsNoAgentVariable` — a bootstrap command printing
  `CLAUDE_CONFIG_DIR`, `CODEX_HOME`, `PI_CODING_AGENT_DIR` gets nothing, even
  though the manifest declares all three platforms.
- `TestSyncBootstrapFailureSkipsEveryPlatform` — assert no profile was created
  and no install ran.
- `TestSyncReusesAnExistingProfileWithoutResetting` — seed a file into an
  existing profile, sync, assert the file is still there.
- `TestValidNameStillRejectsDefault` — `name: default` must be handled above
  `ValidName`, never by loosening it. Mutation: make `ValidName` accept
  `default` and this test fails.
- `TestSyncDefaultRequiresAllowDefault` — off a terminal, `--yes` alone leaves
  the real config directory untouched and the run non-zero.
- `TestSyncDefaultNeverCreatesLinksOrShims` — point the agent's real config at a
  throwaway directory, sync `name: default`, assert no symlink, no `xdg/`, no
  `.claude.json` seeded, and no wrapper written.
- `TestSyncDryRunChangesNothing` — full manifest, `--dry-run`, assert an empty
  profile root and that no install command ran.
- `TestParserRejects…` — one per rejected construct in §4.1. Each asserts an
  error, not a wrong value.
- `TestVariantArgsTokenizeBeforeSubstituting` — `args: --effort=xhigh "/plan:run
  {}"` with a caller argument of `fix the parser` yields exactly **two** tokens.
  Mutation: substitute into the string before tokenizing and this fails with
  four. The most important test in §7.
- `TestVariantArgsStringExpandsNothing` — `args: --p $HOME *.go` reaches the stub
  as the literal characters, three tokens, no expansion and no glob.
- `TestVariantArgsStringAndListAgree` — the two forms of §7.1's first example
  produce byte-identical variant files.
- `TestSyncVariantArgsReachTheAgentVerbatim` — sandbox check asserted on
  `arg:[…]`, never on `argv:`. The stub's `"$*"` joins with a space, so a check
  written against `argv:` cannot tell one argument from two, which is the whole
  property.

---

## 17. Relationship to ACH, and where skills and plugins go in v2

### 17.1 v1 stays independent

`ap` does not import, call, or require ACH. v1 fetches nothing, so there is
nothing to bring over: with `skills:` and `plugins:` cut, the pieces of ACH that
would have been adapted are exactly the pieces no longer needed.

ACH may later generate valid `ap` manifests:

```text
ACH environment → resolve/govern → export ap manifest → ap sync
```

That arrow points one way and stays that way.

### 17.2 v2: `ach-cli` already is the skills and plugins implementation

§2 cut `skills:` and `plugins:` because implementing them means owning a
per-agent materialisation matrix. `ach-cli` — the second binary in `../ach`,
built from `cmd/ach-cli` — already owns one, and owns it under a hard rule that
it **must not import `k8s.io/*` or `controller-runtime`**. That rule is why the
relevant packages are usable outside a cluster at all:

| ACH package | What it already does |
|---|---|
| `internal/gitfetch` | git clone / checkout / subtree / auth, shared with the operator |
| `internal/contentkit` | marketplace parse, skill discovery, plugin and skill verify, tar safety, size caps (`SkillRawIngressCap`, the `SKILL.md` gate) |
| `internal/cli/skillstage` | nests an extracted skill under `skills/<name>/`, stripping the one wrapper directory a repo archive adds |
| `internal/cli/adapter` | per-agent projection tables (`skills/**/* → .claude/skills/**/*`) with typed merge kinds — `MergeReplace`, `MergeDeep`, `MergeComposite` |
| `internal/cli/localpkg/*` | the local-first, serverless install / uninstall / update / outdated engine |
| `internal/cli/{lock,state,conflict,hash}` | what makes re-installing converge instead of duplicating |

Its user-facing surface is already the shape v2 wants:

```bash
ach-cli repo add <source> --name <n>
ach-cli skill  install <name@repo>… --target … [--global] [--dry-run]
ach-cli plugin install <name@repo>… --target … [--global] [--conflict …] [--dry-run]
```

### 17.3 The two projects already agree on the interface, by accident

`internal/cli/adapter/globalpath.go` holds a closed table, `globalScopes`, that
says how `--global` resolves per agent. Read from source — not yet run, which
matters, see below:

| ACH adapter ID | variable it reads | suffix |
|---|---|---|
| `claude-code` | `CLAUDE_CONFIG_DIR` | — |
| `codex` | `CODEX_HOME` | — |
| `pimono` | `PI_CODING_AGENT_DIR` | — |
| `opencode` | `XDG_CONFIG_HOME` | `opencode` |

That is `internal/agent/agent.go`'s `ConfigEnv` column, agent for agent,
including opencode's suffix — which is precisely the `Entry: "opencode"` name
`ap`'s shim links back to the profile.

Neither project was built against the other. They line up because both read the
same four upstream CLIs and wrote down what they found.

**If that holds when run, v2 needs no code in `ap` at all.** It is already
expressible in v1:

```yaml
platforms:
  claude:
    install:
      - "ach-cli repo add https://github.com/anthropics/skills --name anthropics"
      - "ach-cli skill install pdf@anthropics --global"
```

`ap` sets `CLAUDE_CONFIG_DIR` to the profile (§8.3); `ach-cli --global` reads it
and installs there. No import, no dependency, no shared module, no coupling —
a process boundary, which is the only kind of boundary a zero-dependency program
can have with a Kubernetes-adjacent one.

### 17.4 So v2 opens with a measurement, not a branch

This repository's rule is that a claim about another binary is verified by
running it. The table above was read, which is stronger than documentation and
weaker than a run. Two checks decide the whole feature:

```bash
# 1. the simple case
ap create claude:t
ap env claude:t ach-cli skill install pdf@anthropics --global
ls "$(ap which claude:t)/skills"

# 2. the interesting case — two indirections must meet in the middle
ap create opencode:t
ap env opencode:t ach-cli skill install pdf@anthropics --global
#   ap sets     XDG_CONFIG_HOME=<profile>/xdg
#   ach appends /opencode
#   <profile>/xdg/opencode is ap's shim Entry, symlinked back to <profile>
ls "$(ap which opencode:t)/skills"
```

- Both pass → **v2 is a paragraph in the README, not a feature.** Close it.
- They fail → the fix belongs in `ach-cli` (a flag, or a scope entry), not in
  `ap`. `ap` acquiring a fetcher is the last resort, not the first.
- They pass but the manifests read badly → add sugar, and only sugar:

  ```yaml
  skills:
    - pdf@anthropics
  ```

  desugaring to the two `install` lines above. `ap` still never fetches, never
  reads a `SKILL.md`, never learns what a marketplace is.

  Note what even that costs: ACH's adapter IDs are `claude-code`, `pimono`,
  `gemini-cli`; `ap`'s platforms are `claude`, `pi`, and no gemini at all. Sugar
  needs a name-mapping table between the two registries — a small compatibility
  matrix, which is the thing §2 refused. That is a reason to stay at `install:`.

### 17.5 What `ap` must not take from ACH, whatever v2 decides

- **No importing ACH packages.** `go.mod` has no dependencies and that is
  load-bearing: `install.sh` ships one static binary and `make vulncheck` gates
  a dependency set that is currently the standard library. `internal/contentkit`
  alone pulls `sigs.k8s.io/yaml`.
- **No repository registry.** `ach-cli repo add` keeps state. Two tools owning
  one registry is two sources of truth and a reconciliation nobody asked for.
- **No lockfile, no conflict engine, no `--conflict` semantics.** ACH has
  `internal/cli/{lock,conflict,state}` because it governs shared environments.
  `ap`'s answer to "which version" stays §14: whatever the install command pins,
  and the limit is stated rather than hidden.
- **No adapter or projection table.** That is the compatibility matrix §2
  refuses, and §17.3 is the argument that `ap` never needs its own copy.
- **A manifest calling `ach-cli` is not integration.** It is a manifest calling
  a program, exactly as it calls `npx`. `ap` does not know the difference
  between them and must not learn it — the moment `ap` special-cases `ach-cli`,
  §17.1's one-way arrow is gone.

---

## 18. Definition of done

A repository:

```text
agent-profiles/
├── plan.yaml
├── execute.yaml
└── review.yaml
```

synchronised with:

```bash
ap sync ./agent-profiles
```

produces working isolated profiles:

```text
claude:plan     codex:plan
claude:execute  codex:execute
claude:review   codex:review
```

with bootstrap commands executed once against the plain environment, declared
install commands executed under the right platform's environment, declared
variants created or updated, existing sessions and configuration
preserved, manual modifications preserved, duplicate profile definitions
rejected before any change, install commands shown and confirmed before they
run, and failures reported per profile with a non-zero exit.

The purpose, in one line:

> **Define specialized coding-agent profiles in Git and reproduce them locally
> with one command.**
