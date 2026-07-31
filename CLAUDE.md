# CLAUDE.md

Instructions for AI agents working on this repository. `AGENTS.md` is a symlink
to this file so every agent reads the same thing.

## What this is

`ap` launches `claude`, `codex`, `opencode` or `pi` with a named per-agent
profile. Pure exec: resolve a profile directory, set **one** environment
variable, `syscall.Exec` the real binary. Sessions, credentials and workspace
trust are symlinked back into the user's real home so profiles never fork them.

Go 1.25, **standard library only**. Unix only (`//go:build unix`).

## Never run the toolchain on the host

There is no host toolchain and you must not add one. Every Go, lint, vuln and
release command goes through `scripts/dev.sh`, which runs it inside the pinned
`Dockerfile.devtools` image. Public make targets wrap themselves via the
`in_container` macro and delegate to a private `_name` half; add both when you
add a gate, and never wrap a target that must touch the real home.

Host-only targets, deliberately: `install`, and the housekeeping ones. That list
used to include `smoke`; it does not any more — see below.

`AP_IN_DEVTOOLS=1` skips the container. It exists for CI's macOS runner, which
has no docker. Do not reach for it to avoid a slow first image build.

Do not add tool bootstrapping back into the Makefile — no `go install` into
`./bin`. Tool versions belong in `Dockerfile.devtools`, pinned, so `make verify`
means one thing everywhere.

## Three images, and only one of them pins anything

`Dockerfile.devtools` pins every tool, because `make verify` has to mean the same
thing on every machine. `Dockerfile.smoke` installs the four agents from npm and
pins **nothing**, on purpose: smoke exists to catch the day an agent changes what
it does with the variable ap hands it, and a pinned agent freezes the very thing
under observation. Do not "stabilise" it with versions.

`make smoke` runs there now, not on the host. Two things follow:

- Do not reintroduce a `command -v <agent>` guard as a reason to skip on the
  host. The agents are in the image; if one is missing, that is a broken image
  and a red run, not a skip.
- The seeded home is load-bearing. Every "shared state survived" assertion is
  vacuous against an empty one, and three checks were caught passing that way:
  a `[user]` git section with no keys made the shim passthrough compare 0 against
  0, and the credential and transcript assertions had nothing to lose. When you
  add a check, seed what it needs to be able to fail.

Both credentials in the seed are synthesised, from the **field names** of a real
one and never a value. Neither needs to be accepted, because neither check is
about acceptance: what is asserted is that the profile REACHED the file through
ap's symlink. claude distinguishes the two cases itself — "Not logged in ·
Please run /login" when it cannot get to the credential, "Failed to
authenticate" when it read one and the token was rejected. Only the first is
ap's business. With `ANTHROPIC_API_KEY` set claude does not open the credential
at all, so that check skips rather than passing with the link severed.

Two orderings there are load-bearing, and both were found by reverting a guard:

- The symlink assertion runs **before** the agent does. Given a credential it
  cannot refresh, claude replaces the file with a real one of its own — which is
  the exact reason `Link` re-asserts the symlink on every run. Asserted
  afterwards, it goes red because claude did its job, not because ap failed.
- The authentication message goes to **stdout**, never to `--debug-file`. This
  grepped the debug log for it and therefore could not fail; measured with the
  link severed, it stayed green.

codex's equivalent does work, and needs no key: `codex login status` reads
`auth.json` and masks what it finds without validating it, so the seed
synthesises one and the profile can still only report "logged in" by reaching
that file through the link ap made. Whether the token would work is OpenAI's
business, not ap's. It briefly used `printenv OPENAI_API_KEY | codex login
--with-api-key`, which also works and is worth knowing — `--api-key` was removed
upstream — but demanding a secret for something a literal could do is how a gate
ends up unrunnable in CI.

That check was also **vacuous for as long as it existed**, on the host too:
`grep -qi "logged in"` matches "Not logged in". The mutation that found it —
`Link` skipping codex's `Shared` entry, so the profile has no `auth.json` — left
it green. The pattern is anchored now. When you write a check whose negative
answer is the positive one with a word in front, anchor it.

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

- Never point a shared variable at a raw profile directory.
  `TestOnlySharedConfigVarsAreShimmed` fails if an agent has one without a shim.
- Never shim a private variable: pointless indirection, same test catches it.
- `XDG_DATA_HOME`, `XDG_STATE_HOME`, `XDG_CACHE_HOME` and `HOME` are never
  redirected at all. That is what keeps sessions, logins and caches shared, which
  is the entire point of the tool.
- The passthrough is not optional. Without it this is the bug the old blanket ban
  existed to prevent.

Levers already ruled out for opencode, each by running the binary — do not
re-litigate without new measurements: `OPENCODE_CONFIG_DIR`, `OPENCODE_CONFIG` and
`OPENCODE_CONFIG_CONTENT` are all additive; `OPENCODE_TEST_HOME` moves `home` but
not `config`; there is no config-level switch. All 82 `OPENCODE_*` variables in
the binary were enumerated.

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

When `scripts/smoke.sh` fails, the registry row is usually what is wrong. But
check whether the *check* is lying first — two of them originally were:

- `codex doctor` pretty-prints paths, collapsing `$HOME` to `~` and eliding the
  middle, so grepping a full path never matches.
- `opencode debug config` emits ~730 KB but exits without waiting for the pipe to
  drain, losing everything past 64 KiB. Capture to a file, never a pipe.
- `AP=${AP:-./ap}` was relative, and the plugin block runs its agent calls from a
  neutral empty directory, so `./ap` resolved to nothing there and the commands
  never ran. Six checks failed, each blaming what it was testing — one of them
  literally asked "did the cwd leak in?", which was the opposite of the truth.
  `AP` is absolute now, and setup commands go through `setup()`, which silences
  output but **checks the exit status**. Silencing both is what made a failure to
  run indistinguishable from a failure to pass.

A third failure mode, worse than a lying check: a check that is honest but not
deterministic. `clone` asserted that a cloned plugin declaration materialises at
session start; that is claude's asynchronous background work, and it took 3 starts
once and 5 the next before not happening at all. It is a `warn` now, not a `bad` —
see the comment there for the measurement. Before adding a check, ask what it
would take to make it go red when nothing is wrong; a smoke run that is red for
reasons nobody controls teaches people to ignore red.

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
whether the behaviour is driven by a key in that file. Real example:
`defaultToAgentsView` makes a profile open in the agents view, where a short
message answers "Too short — describe the task" — which reads like a broken
profile and is just an inherited UI preference.

It was shared, by symlink, until `57f545f`. It is not any more, because
user-scope MCP servers live in it and sharing it made a per-profile MCP server
impossible. Do not link it back.

Do not sync it per key either. Rewriting a file the agent owns would fight it on
every write. `Agent.FirstRun` is not that and must not grow into it: it copies
an allowlist of keys **once, at create, into a file the profile does not have
yet**, `O_EXCL` so it can never rewrite one, and never looks at it again. It
exists because sharing the credential makes a profile logged in but not
started — measured on claude v2.1.220, a credential-only profile opens on the
theme picker, and `hasCompletedOnboarding` alone is what gets past it.
`settings.json`, empty or carrying a theme, changes nothing.

Two things that measurement also settles, so do not re-derive them:

- `claude -p` never shows the wizard, which is why a credential-only profile
  looked complete when it was verified that way. Verify interactive behaviour
  interactively — `CLAUDE_CONFIG_DIR=<dir> timeout 25 script -qec claude /dev/null`
  under a pty, then strip the escape sequences before grepping, because they land
  mid-word and a naive `grep "text style"` finds nothing.
- Outside a profile claude reads `~/.claude.json`; inside one it reads
  `$CLAUDE_CONFIG_DIR/.claude.json`. Different directories, same base name.

`hasTrustDialogAccepted` is deliberately **not** seeded. It lives under
`projects.<path>` alongside that project's prompt history, so there is no way to
carry it without carrying history, and one trust prompt per profile per project
is the honest answer for a separate environment anyway.

Do not add profile-level overrides for these keys either. `defaultToAgentsView`
was measured: Claude Code reads it only from `.claude.json`, so the same key in a
profile's `settings.json` has no effect, and the only per-profile lever is
`disableAgentView`, which removes background agents entirely rather than just
choosing a startup view. Before believing a report that a profile behaves
differently from a bare agent, run the bare agent — that one turned out to behave
identically, and the profile was never involved.

Claude Code also gates behaviour on remote feature flags cached in that same file
under `cachedGrowthBookFeatures`, so which settings rows are even writable can
change without any local change. Read the flag rather than inferring it from a
symptom: reading one wrong produced two contradictory diagnoses in a row here.

## Anything that turns user input into a path must call `profile.ValidName`

`--from` once skipped it and became a path traversal that copied the user's real
`~/.claude` into a profile. `profile.Dir` joins and cleans, so `..` escapes.

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

Neither touches the real home any more. Both build their own, seeded, inside a
container, and throw it away.

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
