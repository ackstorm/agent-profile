# CLAUDE.md

Instructions for AI agents working on this repository. `AGENTS.md` is a symlink
to this file so every agent reads the same thing.

## What this is

`ap` launches `claude`, `codex`, `opencode` or `pi` with a named per-agent
profile. Sessions, credentials and workspace trust are symlinked back into the
user's real home, so profiles never fork them.

Go 1.25, **standard library only**. Unix only (`//go:build unix`).

```
ap run claude:plan:review --effort xhigh
 │
 ├─ dispatch          cmd/ap/main.go       run parses no flags after the ref
 ├─ ParseVariantRef   internal/profile/    agent + profile + variant,
 │                                         every name through ValidName
 ├─ prepare
 │    ├─ Exists?      missing profile names the create command
 │    ├─ :default     the real config: nothing linked, no shim, no override
 │    ├─ Link         internal/profile/share.go
 │    │                 shares → symlinks into the real home, re-asserted
 │    │                 every run; a real file found there is healed, and a
 │    │                 newer credential may be promoted (a human decides)
 │    └─ Shim         internal/profile/shim.go
 │                      <profile>/xdg + one passthrough link per entry of
 │                      the real config base (opencode also gets xdg-data)
 ├─ runArgs           internal/profile/variant.go    {} substitution
 └─ run.Exec          internal/run/
      ├─ Env            exactly ONE config variable, pointing inside the
      │                 profile; inherited shim vars stripped
      └─ syscall.Exec   the agent owns the TTY from here

profile dir   ~/.local/share/agent-profile/profiles/<agent>/<profile>
the registry  internal/agent/agent.go — four external CLIs, every field
              verified by running the binary, never read from docs
```

## MANDATORY reading

Read the file before touching the code. Not suggestions.

| Working on…                                                   | MUST read first                  |
|---------------------------------------------------------------|----------------------------------|
| `Dockerfile.smoke`, `Dockerfile.devtools`, `scripts/smoke.sh`   | `docs/references/SMOKE.md`       |
| `internal/profile/share.go`, share conflicts, promotion         | `docs/references/CREDENTIALS.md` |
| "the profile behaves oddly", `Agent.FirstRun`, onboarding flags | `docs/references/CLAUDE-JSON.md` |
| `pkg/schema/*`, composition, the YAML subset                    | `docs/references/DECLARATIVE.md` |

Everything else in this file is a standing rule: it applies before you know
which file you are about to touch.

## Never run the toolchain on the host

There is no host toolchain and you must not add one. Every Go, lint, vuln and
release command goes through `scripts/dev.sh`, which runs it inside the pinned
`Dockerfile.devtools` image. Public make targets wrap themselves via the
`in_container` macro and delegate to a private `_name` half; add both when you
add a gate, and never wrap a target that must touch the real home.

Host-only targets, deliberately: `install` and the housekeeping ones. `smoke` is
not one of them — it runs in its own image, like everything else.

`AP_IN_DEVTOOLS=1` skips the container. It exists for CI's macOS runner, which
has no docker. Do not reach for it to avoid a slow first image build.

Do not add tool bootstrapping back into the Makefile — no `go install` into
`./bin`. Tool versions belong in `Dockerfile.devtools`, pinned, so `make verify`
means one thing everywhere.

## Three images, and only one of them pins anything

`Dockerfile.devtools` pins every tool, because `make verify` has to mean the same
thing on every machine. `Dockerfile.smoke` pins **nothing**, on purpose: smoke
exists to catch the day an agent changes what it does with the variable ap hands
it, and a pinned agent freezes the very thing under observation. Do not
"stabilise" it with versions.

`make smoke` runs there, not on the host, against a seeded home it builds and
throws away. A missing agent is a broken image and a red run, never a skip.

**MUST read `docs/references/SMOKE.md`** before editing either image or
`scripts/smoke.sh`: why the seeded home is load-bearing, the checks caught
passing vacuously, and the two orderings that are.

## Share conflicts are healed, and the user picks the survivor

A share's symlink gets replaced by a real file during ordinary use: claude
writes its credential with a temp-file-plus-rename, which lands on top of the
link. `Link` must cope with that on its own — never refuse and tell the user to
move the file by hand.

Two rules, and neither may be weakened:

- **A real file is healed, not refused.** Rename to `<rel>.ap-orphan` through
  the same `os.Root`, relink, and say so. Renamed and not removed: it is a
  credential and may hold the newer token.
- **Which credential survives is the user's call, not `Link`'s.** `Link` takes
  a `resolve func(Conflict) Resolution` and asks. A nil resolver means `Orphan`;
  identical files are not a conflict; off a terminal ap never asks and never
  promotes; a symlink at the shared path is refused, not replaced. `Promote`
  always keeps what it replaced at `<shared>.ap-previous` — that backup is the
  only way back, and is not optional.

Promotion is the only thing in this program that writes outside a profile. Do
not widen it into ap syncing credentials on its own: it moves one file, once,
because a human at a terminal said so.

**MUST read `docs/references/CREDENTIALS.md`** before editing
`internal/profile/share.go` or anything that resolves a share conflict. Every
rule above is mutation-tested, and the file names the test for each.

## Three tests that must never be deleted or weakened

**`TestDeleteDoesNotFollowTheConfigShim`** (`internal/profile/shim_test.go`). The
shim links to every entry of the user's real config directory, so a `Delete` that
followed those links would erase the configuration of every application on the
machine. This is the worst thing this program could do.

**`TestDeleteDoesNotFollowSymlinks`** (`internal/profile/share_test.go`) and
`TestDeleteDoesNotFollowNestedSymlinks` (`security_test.go`). A `Delete` that
descended into the `projects` symlink would erase every Claude Code transcript
the user has.

**`TestEnvOnlySetsPathsInsideTheProfile`** (`internal/run/run_test.go`). Whatever
variable is set must point inside the profile. It replaced a blanket "never set
XDG_CONFIG_HOME", and it is strictly stronger: the old rule would have allowed
pointing a private variable at some unrelated place.

If a change makes any of them fail, the change is wrong. Do not adjust the test.

## A shared environment variable may only be set through a shim

Exactly one variable is set per run. Three agents have a private one. opencode
does not: its config root is `(XDG_CONFIG_HOME || ~/.config)/opencode` and nothing
else feeds into it — verified by reading the function that computes it, not the
docs — so isolating opencode means setting a variable every other program reads.

Setting it at the profile directly would send `git`, `gh`, `npm` and every
language server looking for *their* config inside the profile. So it is pointed at
`<profile>/xdg`, which contains a link to the profile under the agent's own name
plus one passthrough link per entry of the real config base. `profile.Shim` builds
it and re-asserts it on every run.

Rules that follow:

- One variable per declared shim, and nothing else. Three agents have a private
  variable and declare no shim. opencode declares two: XDG_CONFIG_HOME for its
  config and XDG_DATA_HOME for its sessions, neither of which it has a private
  alternative for.
- `XDG_STATE_HOME`, `XDG_CACHE_HOME` and `HOME` are never redirected. opencode's
  prompt history, selected model and locks stay shared, deliberately: a run was
  measured never to write there, so isolating them is cost with no benefit.
- Never point a shared variable at a raw profile directory, with or without a
  shim. `TestOnlySharedConfigVarsAreShimmed` fails if an agent sets one without a
  matching shim, and `TestEnvOnlySetsPathsInsideTheProfile` fails if any variable
  lands outside the profile.
- Never shim a private variable: pointless indirection, same test catches it.
- The passthrough is not optional. Without it this is the bug the old blanket ban
  existed to prevent.

Levers already ruled out for opencode, each by running the binary — do not
re-litigate without new measurements: `OPENCODE_CONFIG_DIR`, `OPENCODE_CONFIG` and
`OPENCODE_CONFIG_CONTENT` are all additive; `OPENCODE_TEST_HOME` moves `home` but
not `config`; there is no config-level switch. All 82 `OPENCODE_*` variables in
the binary were enumerated.

opencode keeps its session database (`opencode.db`) under `XDG_DATA_HOME/opencode`.
`XDG_DATA_HOME` is shimmed for opencode to `<profile>/xdg-data` so profiles get
isolated sessions. The three credentials under data (`auth.json`, `account.json`,
`mcp-auth.json`) are linked back as `Shared`. Both shims point `Entry` at the
profile itself (`<profile>/xdg` and `<profile>/xdg-data`), resolving config and
data into one directory with no name collisions (`opencode.jsonc` vs `opencode.db`).
Existing sessions in the global db are not migrated.

## The registry describes other people's software

`internal/agent/agent.go` asserts how four external CLIs behave. Every field was
verified by running the real binary — not read from documentation, and not
recalled from training data.

If you change a row, prove it first:

```bash
CODEX_HOME=/tmp/x codex doctor
PI_CODING_AGENT_DIR=/tmp/x pi list
XDG_CONFIG_HOME=/tmp/shim opencode debug paths   # config must be /tmp/shim/opencode
CLAUDE_CONFIG_DIR=/tmp/x claude -p --debug-file /tmp/x.log "ok"
```

When `scripts/smoke.sh` fails, the registry row is usually what is wrong — but
check whether the *check* is lying first, because three of them were: two
grepped output that could never match, and one asserted claude's asynchronous
background work and went red for reasons nobody controls. Before adding a check,
ask what it would take to make it go red when nothing is wrong. The three
write-ups are in `docs/references/SMOKE.md`.

## Guards get mutation-tested, not just green tests

Every security guard in this codebase was verified by reverting it and confirming
its test fails. A green test that would still pass with the guard removed is
worse than no test, because it advertises safety it does not provide.

After adding a guard: revert it, run its test, confirm it fails, restore. If the
test still passes, the test is wrong.

This is not ceremony — it is how the three real bugs here were found, along with
two tests that passed vacuously.

## `run` parses no flags of its own

Everything after its reference goes to the agent verbatim. Do not add flags to
`run`, and do not add GNU-style intermixed parsing: passthrough is the feature,
and `ap run claude:plan --effort xhigh` has to reach claude untouched.
`TestDispatchRunDoesNotParseFlagsAfterTheRef` fails if someone "makes run
consistent with create".

`create` is the exception and uses `parseAroundRef`, because it has no
passthrough at all, so `ap create claude:review --from plan` is unambiguous. Only
extend that helper to commands that pass nothing to the agent.

`--from` takes a bare profile name, never a qualified reference: a profile is
only ever cloned within its own agent, which the destination already names.

`{}` in a variant is the one exception to "the store goes to `syscall.Exec`
untouched", and it is narrow on purpose. `runArgs` substitutes the caller's
arguments — joined with a space — into every `{}` and does **not** also append
them; a variant that never types `{}` composes exactly as before. It exists
because appending cannot express a prompt prefix: claude's grammar takes one
trailing positional and **drops a second in silence** (measured: `claude -p "say
FIRST" "say SECOND"` answers FIRST, exit 0), so the prefix and the caller's
argument have to arrive as *one* element of argv.

Do not "generalise" this into ap deciding where the caller's arguments go on its
own. That was rejected, and the reason still stands: it would mean deciding that
`/code-review` is a positional while the `opus` in `--model opus` is not, which
is a claim about four external CLIs needing re-verification every release. The
placeholder infers nothing — the author states the position. There is no escape
for a literal `{}`, the same class of stated limit as the newline and the tab.

The sandbox check for it is asserted on `arg:[…]`, never on `argv:`. The stub's
`"$*"` joins with a space, so a check written against that line cannot tell one
argument from two — which is the entire property under test. That check was
written against `argv:` first and would have been vacuous.

## `ap sync` runs other people's shell commands, on purpose

`git clone` a repository and `ap sync` runs the commands inside it as you. That
is arbitrary code execution by design and cannot be engineered away — it is the
feature. Everything in `cmd/ap/sync.go` exists to stop it happening by surprise,
and four rules hold it together. The reasoning is in
`docs/specs/ap-sync-v1.md`; these are the parts that must not drift.

- **`install` goes to `sh -c`; a variant's `args` goes to a tokenizer.** The two
  fields are both strings and look alike, so the difference is stated rather
  than inferred. An install command is a shell one-liner by nature; an agent's
  argv is data. `--prompt $HOME` reaches the agent as those characters. Never
  give `args` a shell, and never take one away from `install`.
- **`args` is tokenized BEFORE `{}` is substituted.** Substituting first would
  let a caller's argument change how many tokens a variant has — one prompt
  silently becoming three arguments, which claude then drops without a word.
  `TestSyncVariantArgsTokenizeBeforeSubstituting` fails with four tokens if
  anyone reverses it.
- **An inherited variable is stripped by VALUE, never by name.** A value that
  resolves inside `profile.Root()` goes; everything else stays. Stripping the
  four config variables by name would delete `XDG_CONFIG_HOME` for a claude
  install — that string is opencode's config variable *and* the one every other
  program on the machine reads — and break `npm` for everyone.
  `TestSyncKeepsAConfigVariableThatPointsOutsideTheProfileRoot` is the guard.
- **`bootstrap` runs before any profile exists, with no agent variable set.**
  Not a convention: it is what makes "install this into all my profiles at once"
  inexpressible. An earlier draft ran a profile-level list once per platform,
  and its own example leaked into the real home — `npx … --claude --global`
  during codex's turn sees no `CLAUDE_CONFIG_DIR` and writes to `~/.claude`.

`name: default` runs against the configuration the developer uses every day,
which ap cannot undo. It used to be gated **separately** from `--yes`; that
second flag, `--allow-default`, was removed on request — one run, one question.
`--yes` now covers it. What must not be lost with it is the display: a default
target is printed with its resolved absolute path and named as the real config,
in `--dry-run` and in the prompt, because that display is now the only thing
distinguishing the two blast radii. Off a terminal the single gate still
refuses; a pipe is not consent, checked with `stdinIsTerminal` and not with
`answered()`. Nothing is ever created for the sentinel — no directory, no
links, no shim, no wrapper — and `TestSyncDefaultNeverCreatesLinksOrShims`
holds that line.

Two limits are stated in the spec rather than defended here, and neither is a
bug to be fixed: **ap cannot tell whether an install command honoured the
variable** (§8.5 — a tool that resolves `~/.claude` directly writes to the real
home and reports success; the fix belongs upstream, and sandboxing it was
rejected on four counts), and **a manifest is only as reproducible as its
install commands** (§14 — `@latest` is whatever it was that day; ap does not pin
and does not lock).

`ap sync` is additive. Nothing is pruned, nothing is uninstalled, and there is
no ownership tracking. The one exception is a variant of the same name, which is
overwritten — reported as `updated`, in both the report and `--dry-run`, because
it is the only place v1 destroys something a user typed.

## install.sh is a `curl | bash` target, so treat it as one

Two couplings that no compiler checks:

- The archive name it builds must match `name_template` in `.goreleaser.yml`.
  Change one, change both, then confirm with `make snapshot` and compare
  `dist/` against what the script asks for.
- It must **never** write to PREFIX before verifying the checksum against the
  release's `checksums.txt`. That guard is mutation-tested the same way the Go
  ones are: append a byte to a served archive and the install must abort with
  nothing installed.

`make shellcheck` gates it and runs inside `verify`. Keep it POSIX-ish bash with
no dependency beyond curl, tar and sha256sum/shasum.

## `.claude.json` is where the surprises live

Claude Code keeps far more in it than its name suggests: onboarding flags,
per-project trust, user-scope MCP servers, UI preferences, cached feature flags,
prompt history. Before treating "the profile behaves oddly" as an ap bug, check
whether a key in that file drives it — then run the bare agent, which has
settled more than one such report.

It is **not** shared, since `57f545f`: user-scope MCP servers live in it and
sharing it made a per-profile MCP server impossible. Do not link it back, and do
not sync it per key — that would fight the agent on every write. `Agent.FirstRun`
is not that and must not grow into it.

**MUST read `docs/references/CLAUDE-JSON.md`** before acting on any of this.

## Anything that turns user input into a path must call `profile.ValidName`

`--from` once skipped it and became a path traversal that copied the user's real
`~/.claude` into a profile. `profile.Dir` joins and cleans, so `..` escapes.

## Session storage and `ap resume`

- **`ap resume` chdirs before exec, for all four agents.** Measured: `claude` hard-scopes
  session ids to directory (`No conversation found` from elsewhere); `codex` is not
  scoped and silently resumes against the wrong tree; `pi` prompts to fork into current
  dir; `opencode` groups by git project. Chdiring first is required for all four.
- **Never decode claude's directory names.** Both `/` and `.` encode to `-`, making
  paths ambiguous (e.g. `-home-jcm--claude` vs `-home-jcm-Projects-agent-profile`).
  Read `cwd` from inside the transcript file instead.
- **Bounded scan for session metadata.** `readClaude` bounds line scan to 50 lines /
  64 KB because transcripts can be huge (e.g. 24.9 MB with no `ai-title` at all).
- **No positional index survives an invocation.** `ap sessions` prints session ids and
  `ap resume <id>` takes an id prefix or full id. Numbering in `ap sessions` is for
  display only. The interactive picker in `ap resume` (when run without arguments on a
  terminal) is the exception because listing and selection occur in the same invocation.
- **opencode's listing is project-scoped and costs a subprocess.** opencode sessions live
  in sqlite (`opencode.db`) and are read by shelling out to `opencode session list --format json`
  under the profile environment. `ap sessions` output includes a caveat note that
  opencode listings are project-scoped.
- **`ap sessions` and `ap resume` parse their own flags; `run` still does not.**
  `ap resume <id> --model opus` passes extra flags through to the agent, maintaining
  passthrough discipline.

## Deliberately absent — do not add

- **`ap use` / `ap shell` / an active profile.** A "current profile" that a bare
  `claude` would ignore is hidden state that lies to the user.
- **A separate `--from-base` flag.** Copying out of `~/.claude` needed a
  per-agent allowlist of a directory that grows every release — that allowlist
  now exists (`Agent.CloneAllow` in `internal/agent/agent.go`), and `--from
  default` (the `default` sentinel — see `profile.Default`) reaches the real
  config through the existing `--from` flag. A second flag would be
  redundant, not missing.
- **Windows.** It would need a second execution model and a second sharing
  mechanism. The build tags say so.
- **`--pure`.** It set `OPENCODE_PURE` (identical to opencode's own `--pure`),
  `OPENCODE_DISABLE_PROJECT_CONFIG` and `OPENCODE_DISABLE_DEFAULT_PLUGINS`. It did
  not isolate anything — the global config still loaded — and the project-config
  half suppressed the user's own repo, which is not this tool's business. The shim
  made it obsolete. Its name also collided with opencode's flag, so misplacing it
  failed silently.
- **Dependencies.** Standard library only.

## The Go version floor is a security floor

`go.mod` requires 1.25 for a reason, not for a language feature. Two stdlib
advisories hit code paths this program actually executes:

- **GO-2026-4602**, "FileInfo can escape from a Root in os", fixed in 1.25.8 —
  `Link` uses `os.Root` specifically to stop a symlinked ancestor from letting a
  remove-and-relink escape into the real home. A Root escape defeats that guard.
- **GO-2025-3956**, "unexpected paths returned from LookPath", fixed in 1.24.6 —
  `Exec` calls `exec.LookPath` and then `syscall.Exec`s the result.

Do not lower the floor, and do not add either to an acknowledged list.
`make vulncheck` is the gate.

The devtools image floats on `golang:1.26-bookworm` — above the floor, and
floating on purpose so patch-level Go fixes arrive without anyone editing a pin.
`GOTOOLCHAIN=local` in that image turns a base that drifted *below* go.mod into a
build failure instead of a silent toolchain download.

## Before claiming done

```bash
make verify        # fmt-check, shellcheck, vet, lint, test (race + shuffle), vulncheck
make secrets       # gitleaks over the full history
make sandbox       # ap's own side, against a throwaway home, with stub agents
make smoke         # the four real agents, in their own image
```

`make doctor` is the fast preflight when something looks wrong with the
container itself rather than the code.

`make fuzz` exercises the path validation, which is where the traversal bug was.

`sandbox` and `smoke` overlap on purpose and answer different questions.
`sandbox` uses stubs, so it is deterministic, needs no network and no
credential, and can assert argv exactly; `smoke` drives the real binaries, so it
is the only thing that can catch an upstream change. When both could cover an
assertion, the sandbox is where it belongs — smoke's version of the variant
check is now the weaker of the two and says so.

Neither touches the real home. Both build their own, seeded, inside a container,
and throw it away.

## Releasing

```bash
make release VERSION=v0.1.0
```

Do not create the tag by hand. That target refuses unless the version is semver
with a leading `v`, HEAD is a clean `main` in sync with `origin/main`, and the tag
does not already exist — then it runs `verify`, `secrets`, `snapshot` and
`require-green-ci`, and only after all of them pass does it tag and push.
**Every gate passes before the tag exists**, so a failure leaves origin with no
orphan tag and the fix is simply another `make release`.

`require-green-ci` exists because `make verify` runs in the devtools container and
therefore covers Linux only. macOS is the other supported platform and a
macOS-only defect has already shipped this way. CI has already run on HEAD — the
in-sync check guarantees it — so the release refuses unless that run is green.
Without gh installed it warns loudly and continues; do not make it silent.

`release.yml` fires on the pushed tag and gates again on both platforms
(`verify` on ubuntu, `test` on macos) before `make release-publish` runs.
`release-publish` is CI-facing; it needs `GITHUB_TOKEN` and is the only target
that publishes anything.

`make snapshot` alone is the dry run: the same four archives and `checksums.txt`,
nothing published.
