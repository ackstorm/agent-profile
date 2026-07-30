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

Host-only targets, deliberately: `smoke` (drives the real agent binaries and
asserts the user's session count survives), `install`, and the housekeeping ones.

`AP_IN_DEVTOOLS=1` skips the container. It exists for CI's macOS runner, which
has no docker. Do not reach for it to avoid a slow first image build.

Do not add tool bootstrapping back into the Makefile — no `go install` into
`./bin`. Tool versions belong in `Dockerfile.devtools`, pinned, so `make verify`
means one thing everywhere.

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

## `.claude.json` is shared state, so expect surprises from it

It is linked for `hasTrustDialogAccepted`, but Claude Code keeps far more in it,
and all of it becomes common to every profile. Before treating "the profile
behaves oddly" as an ap bug, check whether the behaviour is driven by a key in
that file. Real example: `defaultToAgentsView` makes a profile open in the agents
view, where a short message answers "Too short — describe the task" — which reads
like a broken profile and is just an inherited UI preference.

Do not add per-key filtering. Rewriting a file the agent owns would fight it on
every write, and the symlink is what keeps trust and login working.

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
make smoke         # host-only; needs the four agents installed and logged in
```

`make doctor` is the fast preflight when something looks wrong with the
container itself rather than the code.

`make fuzz` exercises the path validation, which is where the traversal bug was.

`scripts/smoke.sh` touches the real home. It creates and deletes `apsmoke`
profiles and asserts the user's session count is unchanged afterwards.

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
