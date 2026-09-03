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
| `pkg/source/*`, the credential guards, the cache, fetching       | `docs/references/DECLARATIVE.md` |
| `pkg/hydrate/*`, the ledger, the root lock, materialization      | `docs/references/DECLARATIVE.md` |
| `internal/run/handoff_*.go`, anything Windows                    | `docs/references/WINDOWS.md`     |

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

A row that was right when measured goes stale, and it does so silently.
`Agent.Skills` for codex was empty because codex read skills only from
`~/.agents/skills`, outside `CODEX_HOME`; on codex-cli 0.149.1 it reads
`$CODEX_HOME/skills` too, so ap materializes them and the §8 warning is gone.
Verified the only way that counts — a marker skill planted in a throwaway
`CODEX_HOME`, `codex exec` asked to list its skills, the marker came back — and
the tests that encoded the absence were updated rather than deleted, except
`TestARuntimeWithNoSkillsDestinationWarnsRatherThanDropping`, which now rides on
a fake adapter. That is the lesson worth keeping: a §8 guard riding on a real
runtime's gap disappears the day upstream fills it.

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

## The ledger is the only state, and it bounds every removal

`ap sync` is gone, and with it the one thing in this program that ran other
people's shell commands. A capability is now DECLARED and ap materializes it,
which is what lets one declaration work on four runtimes and what lets
`ap uninstall` know precisely what to take back. Running a tool's own installer
is no longer expressible — `ap env <ref> -- <installer>` is the honest
replacement, and it is the user's command, not ap's.

Four rules hold the state surface together. The reasoning is in
`docs/specs/agent-profile-declarative-spec-v0.6.3.md` §33 and §35.

- **A manifest is an INPUT; the ledger is the STATE.** Nothing writes back to a
  manifest, and applying one does not make it authoritative. `ap list <ref>`
  reads the ledger, never a manifest, because a manifest says nothing about what
  is installed. `ap manifest export <ref>` closes the loop the other way.
- **The preview and the action share ONE classifier.** `hydrate.Remove` is the
  only entry point and calls `classify` exactly once, so `--dry-run` cannot
  drift from what happens. Do not add a second path that "just previews".
- **The hash gates a whole-file record. It CANNOT gate a merged one.** Apply
  merges each MCP server into one document in turn, so installing a second
  server invalidates the first one's recorded hash the moment it lands. On a
  root with two servers every recorded hash but the last is already stale.
  Gating on it would refuse every merged uninstall on any root holding more
  than one server — the ledger's headline feature, dead on arrival. A merged
  record is bounded by its recorded KEYS, which is what `MergeInto` returns
  them for. `TestUninstallingOneMCPServerLeavesEveryOtherKeyIntact` is the
  guard, and it goes red if anyone "makes removal consistent".
- **The credential value is never written down.** `--auth-secret-env VAR` and
  `--auth-secret-file <path>` record the BINDING only (§13.1, §34) — this is a
  deliberate divergence from `ach-cli`, which persists tokens in a
  `credentials.json`. The cost is stated rather than discovered: the variable
  must be present on every run, where a tool that stores the token asks once.

Removal is safe against `<agent>:default` — the agent's real configuration
directory, the case SPEC v0.5 gave up on — precisely because the ledger can tell
ap's writes from the user's. There is no gate on `ap uninstall`: it is bounded
by a record, not by a question.

Apply stays **additive**, and v1 ships no flag that changes it. A manifest that
stops declaring a resource leaves it alone. Whole-root convergence (`--prune`)
is deferred to its first real consumer, `ach-runtime`'s init container.

`ap install` takes a direct source, never a bare `<item>@<marketplace>`: v1
records no marketplace definition, so there is nothing on the root to resolve
the catalogue name against. It is refused BY NAME — without that refusal the ref
is treated as a literal resource name and the user gets a contract error naming
a cache directory.

## A runtime-native plugin overrides the common one, and ap declares it

`runtimes.<rt>.plugins.<name>.package` is the runtime's OWN packaging mechanism
(§24.3), which shares only a word with §24's common plugin contract. The locator
is in the runtime's syntax — `git:github.com/owner/repo` for pi, `@scope/name`
for opencode — and ap never parses it.

- **A native entry suppresses the common plugin of that name, for that runtime,
  INCLUDING when it is disabled.** That is what makes "ponytail everywhere except
  pi" expressible at all: `enabled: false` with no package is a runtime opting
  out, and the common plugin must not come back to fill the hole. Materializing
  both would install one plugin twice by two mechanisms.
- **ap writes the declaration and then RUNS the command that materializes it**,
  by default, under the profile's own environment. Measured: `pi update <source>`
  clones a package that exists only in `settings.json`, with nothing on disk, so
  the declaration alone is enough to act on.

  This is not the thing `ap sync` did. That line is **ap never runs a command a
  MANIFEST chose** — an `install:` string, arbitrary, from a repository somebody
  else wrote. `ReconcileArgv` comes from ap's own registry and only the package
  locator comes from the manifest, and applying a manifest is the consent, the
  same way running a script you downloaded is. Declaring a package and then
  refusing to finish installing it protects nobody.

  It returns **argv**, not a command line. A locator holding a space must not
  become two arguments, which is what splitting a rendered string downstream
  would do. The sandbox check asserts `arg:[…]`, never `argv:`, for the reason
  the `{}` check does: the stub's `"$*"` cannot tell one argument from two.

  A literal `--root` runs nothing — an init container has no runtime binary,
  the same separation that makes it provision nothing. A failure is a WARNING,
  not an error: the declaration is written and the ledger records it, so what
  failed is a command the user can run again. `--no-reconcile` turns off the
  running and never the recording.
- **The record is bounded by the ELEMENT.** A package list is an ARRAY the user
  also writes to, so `AppendInto`/`RemoveFrom` record `<key>.<package>` and never
  the bare container key. `MergeInto` is wrong here for the reason its own
  `mergeMap` comment gives for `mcpServers`: it replaces a non-map value whole and
  records the container, so uninstalling ours would delete the user's packages.
  `TestUninstallingANativePluginLeavesTheUsersPackagesIntact` is the guard.
- **claude and codex have no package list and warn.** Both declare plugins
  through a marketplace, which is a second mechanism with its own reconcile
  story; §40.1 leaves it open. Inventing a `packages` key for them would write a
  file neither reads — §8 says warn, never invent.

## A manifest addresses its own profile

`ap manifest apply <manifest>` takes the manifest and nothing else. A manifest
IS a profile's definition: `name` is that profile's name and `targets` are the
runtimes it can be materialized for, so neither is restated on the command line.
`--profile` overrides the name.

**Naming the runtimes is REQUIRED** — `--target`, repeatable, or `--all-targets`
— and that holds even when the manifest declares exactly one. It is not
ceremony: `targets` says what a manifest CAN be materialized for, not what the
caller wants today, and a manifest that targets claude now can gain three later.
A script that never named its runtime would quietly start building four profiles
on somebody's laptop. Do not add a default here.

An earlier version took `<agent>:<profile>` here and ignored both fields. Do not
restore it. The justification was §33.2's "a root is a parameter, never inferred
from the environment" — misapplied, because a manifest is the INPUT the user
named, not the environment, and `ap sync` addressed profiles exactly this way
before it (`name` was "the logical profile name"; `platforms` chose the
runtimes). What §33.2 still forbids is intact: no active profile, no default
manifest location, nothing reading a root out of the environment.

Two guards come with it, and both are load-bearing:

- **`agentreg.ValidNameAllowDefault` runs on the manifest's `name`.** A manifest
  can come from a repository somebody else wrote, so `name: ../../../.ssh` is a
  path traversal with an author behind it — the same class as the `--from` bug.
  The sentinel is permitted here and only here, because a manifest legitimately
  provisions the configuration the agent already uses.
- **`name: default` is gated ONCE for the whole run**, naming each agent with
  its resolved absolute path, and refuses off a terminal. Per-target gating
  would ask four times for one decision.

`--root` still names a directory outright, and on apply it needs exactly one
`--target`: one directory holds one runtime's configuration, and writing two
into it would have them overwrite each other with no way to say so.

`install`, `uninstall`, `list` and `export` keep `<agent>:<profile>`. They have
no manifest to read a name from — that asymmetry is the reason, not an
inconsistency to tidy away.

## Materializing into a profile PROVISIONS it, exactly as `create` does

A profile that a manifest built is a profile, and one that cannot log in is not.
Applying used to write resources into a bare directory and stop: no shared
credential, no shim, no first-run flags, no wrapper — so the first thing anyone
typed was an unauthenticated agent in a profile whose name was not a command.
`target.provision` closes it, and both `manifest apply` and `install` call it,
because they are the two commands that materialize into a root.

- **It calls `finishCreate`, it does not reimplement it.** §10's rule is that a
  materialized profile is indistinguishable from a hand-made one, and one shared
  function is the only way to keep that true.
- **It runs BEFORE materialization, and the order is load-bearing.**
  `seedFirstRun` opens the first-run file with `O_EXCL`, and for claude that file
  is `.claude.json` — which is also where a manifest's MCP servers merge.
  Materializing first leaves the seed refusing a file that already exists, and
  the profile opens on the theme picker.
- **A literal `--root` provisions NOTHING.** An init container has no `$HOME` to
  link a credential out of and no PATH directory to write a wrapper into.
  `TestApplyingToALiteralRootProvisionsNothing` is the guard; with it removed the
  wrapper is written to a file literally named `claude:`.
- **`<agent>:default` provisions nothing either.** It IS the real configuration:
  its credential is already the one the agent reads, and shimming it would point
  the agent's configuration directory at itself.

Variants are materialized here too, by `applyVariants`, and NOT by `pkg/hydrate`.
The variant store is a sibling of the profiles root and deliberately outside the
configuration directory, so a literal root has nowhere to put one. They replace
without asking: a manifest is a declaration the caller named, applying it twice
has to converge, and `--all-targets` would otherwise stop to ask once per variant
per runtime. `ap variant` keeps its confirmation, because there the arguments are
typed and what is being overwritten is not on screen.

## A root may be named literally, and then nothing is inferred

`--root <dir>` names the materialization directory outright, and the subject is
then a bare agent name. It is the artifact `ach-runtime` runs as an init
container: hydrate onto a volume, exit, and let the main container exec the
runtime against what was left behind.

It is **not** a third root. SPEC §33.2's two roots and this are one mechanism —
point the agent's configuration-directory variable at a directory — with the
directory stated instead of derived. The project root stays deferred because it
is a *different* mechanism.

- **A reference and `--root` together is an error.** They answer the same
  question, and a silent precedence rule is how the wrong directory gets
  written. `TestARootIsNamedOnce` is the guard.
- **Neither is not a default.** A bare agent name with no `--root` is refused
  with the way forward, never resolved to somewhere nobody named.
- **A literal root is not gated.** It is neither the user's real configuration
  nor a profile ap manages, so there is nothing ap could claim to undo — and
  the resolved absolute path the gate exists to display is the argument the
  user just typed.
- **Preflight's executable checks are conditional on `runtimeIsLocal`.** Both
  of them — the runtime CLI and every active stdio MCP command — belong to the
  runtime's process, not to ap. An init container has neither and must not:
  that separation is the topology. The runtime is local whenever the root came
  from a reference, because a profile exists to be launched.

`make hydrate` builds `Dockerfile.hydrate` and runs it headlessly — no TTY, no
`$HOME`, non-root — against a git repository mounted in. Keep that image to one
binary plus git and CA certificates; it pins nothing, because freezing the trust
store is not a reproducibility win.

## `stdinIsTerminal` asks the kernel, and it must keep doing so

It used to test `os.ModeCharDevice`. `/dev/null` is a character device — so are
`/dev/zero` and `/dev/urandom` — and systemd, cron and every container runtime
hand a process `/dev/null` on stdin by default. Every one of them was reported
as a terminal, and the real-configuration gate **printed its question and read
the answer off a pipe**, where the rule is that it refuses without asking.
`docker run` with no `-t` is what found it, after the check had been wrong since
the gate was written.

It is a `TCGETS` ioctl now (`TIOCGETA` on the BSDs and macOS), which is what a
terminal actually is and what `x/term` does — spelled out here because the
standard library is the only dependency. Do not "simplify" it back to a mode
test: a check that accepts any character device accepts one that can deliver a
`y`.

`GOOS=darwin go vet ./...` is in `crossbuild` for exactly this: the constant
exists under one name on linux and another on darwin, `verify` runs in a linux
container, and a macOS-only defect has shipped that way before.

## `ls-remote <ref>` is a PATTERN, and it lied about a race

`git ls-remote <url> main` matches the tail of every ref name, so it also
answers with `refs/heads/daisy/caffeinate/main` — which sorts first. `lsRemote`
took the first line, `cloneAt` then fetched `main` and got the real branch, and
the SHA check between them reported:

```
main moved from 95380b3c to b819188d while fetching; re-run
```

Nothing moved. Re-running could never help, and the repository it was found on
is `anthropics/claude-plugins-official`, which anyone may declare as a
marketplace. Two rules came out of it:

- **The ref is resolved ONCE, and the fetch uses the full name that came back.**
  `lsRemote` returns `(sha, full)` and `cloneAt` fetches `full`, so the two
  cannot disagree about what `main` meant. Resolving a shorthand twice, against
  two commands with different matching rules, is what created a race that did
  not exist.
- **Candidates are asked for by exact name, and selected by precedence** —
  `refs/heads/<ref>`, then `refs/tags/<ref>`, then `refs/<ref>`. A branch beats
  a tag of the same name; that is stated here because it is what the old
  first-line-wins did by accident, not because either is obviously right.

Annotated tags come with the same trap one layer down: `ls-remote` emits the
peeled `^{}` line only when a pattern asks for it, and the checkout lands on the
COMMIT, not the tag object. The peeled forms are requested and never selected.
`TestAnAnnotatedTagResolvesToTheCommitItPointsAt` is the guard, and it fails the
same way the branch bug did — as a phantom "moved while fetching".

## Render's output must parse

Two defects lived in `pkg/schema/render.go` because its tests compared emitted
text to expected text, so a renderer and a parser that disagreed about the
grammar both stayed green:

- a git or archive `auth` block emitted `secret:` where the decoder requires a
  nested `value_from:`, so the manifest did not parse at all;
- a header `prefix` of `"Bearer "` came back as `"Bearer"`, because a bare
  scalar is right-trimmed — silent, and it materialized `Bearer${TOKEN}` with no
  separator.

`TestRenderRoundTripsThroughTheParser` feeds render's output to `Effective` and
asserts a fixed point. Any new emitter belongs in that fixture, and
`quoteIfNeeded`'s list is read off `scalarNode`, not guessed at.

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

## A variant over `<agent>:default` is allowed, and the guards moved to fit

`ParseVariantRef` accepts the sentinel as the profile, with or without a
variant. It used to refuse a variant over `default` outright, on the grounds
that nothing is ever created for the sentinel — which confused WHERE a variant
lives with WHAT it names. A variant has no directory, no shim and no links: it
is one file under `VariantsRoot`, a sibling of the profiles root, and `prepare`
still returns an empty directory for `default`, so `Exec` gets no override.
Nothing lands inside the agent's real configuration directory, which is what
being read-only there actually means.

`ap variant codex:default:yolo -- <args>` is the case that motivated it: name a
launch mode over the configuration you already use, without cloning it into a
profile that then drifts from it.

What follows, and none of it is optional:

- **There is no strict sibling parser any more.** `ParseVariantRefAllowDefault`
  is gone and every caller uses `ParseVariantRef`. The strict one hid a decision
  rather than making it — three of the four writing commands needed a guard of
  their own regardless, because the sentinel is fine as the PARENT of a variant
  and never fine on its own.
- **Each command refuses the BARE form itself, with its own sentence.**
  `ap delete <agent>:default` and `ap link <agent>:default` refuse in
  `cmdDelete` and `cmdLink`; `ap variant <agent>:default` falls into the
  existing "names no variant" branch. `which`, `env` and `run` answer for it,
  as before.
- **`cmdDelete`'s refusal is not redundant with `profile.Delete`'s.** Delete
  refuses the sentinel on its own and always will, but only after `cmdDelete`
  has already printed "delete ~/.claude?" and read the answer. Refusing early is
  what keeps that question off the screen, so
  `TestDeleteRefusesTheRealConfigButNotItsVariants` asserts on the resolved
  PATH — the one thing only the early message contains. Written against the
  shared phrase first, where it passed with the guard removed.
- **`default` is still refused as a variant NAME**, by `agentreg.ValidName` on
  the third segment. That name would be a file ap creates, which is the thing
  the sentinel promises does not exist.

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
- **Windows for the LAUNCHER, for now.** No longer a non-goal: `run` on Windows
  is **spawn semantics** — start the child, proxy its exit code — because
  Windows has no exec replacement (`syscall.Exec` exists there as a stub that
  always returns `EWINDOWS`, which is why the unix code compiles for Windows and
  fails at the one moment that matters). `internal/run/handoff_windows.go` is
  that path, and `make crossbuild` vets it so it cannot rot while unshipped.

  Binaries are **not** published yet, and the four blockers are specific rather
  than a general reluctance — see `docs/references/WINDOWS.md`. The first is
  silent: a wrapper named `claude:plan` on NTFS creates an alternate data stream
  on a file called `claude` and reports success. Full command surface or
  nothing; a hydration-only Windows binary is explicitly not the answer.
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
make verify        # fmt-check, shellcheck, vet, lint, test (race + shuffle), examples, vulncheck
make secrets       # gitleaks over the full history
make sandbox       # ap's own side, against a throwaway home, with stub agents
make walkthrough   # the two sequences a person types, newcomer and expert
make smoke         # the four real agents, in their own image
make hydrate       # the hydrator image, headless: no TTY, no $HOME, non-root
```

`walkthrough` is not a second sandbox. `sandbox` asserts properties one at a
time; `walkthrough` runs a SEQUENCE in the order somebody meets it and prints
what they would see. It exists because three defects shipped past a green
sandbox and were found by typing commands by hand — `ap list <ref> --raw`
ignoring `--raw`, `ap list <agent> --root <dir>` ignoring `--root` entirely, and
`ap list` calling `default` read-only long after the ledger made that false.
None of those is visible to a per-property assertion; all three are obvious in a
transcript. Read its output when you change a command's surface, do not just
check that it is green.

`examples` composes every shipped example manifest for every target it declares,
and it is inside `verify` because it costs milliseconds and because a shipped
example that does not parse has already happened — the README's own example
manifest used flow syntax this subset refuses.

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
