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

## Two tests that must never be deleted or weakened

**`TestDeleteDoesNotFollowSymlinks`** (`internal/profile/share_test.go`) and
`TestDeleteDoesNotFollowNestedSymlinks` (`security_test.go`). A `Delete` that
descended into the `projects` symlink would erase every Claude Code transcript
the user has. That is the only irreversible bug this program can have.

**`TestEnvNeverTouchesGenericVars`** (`internal/run/run_test.go`). It locks in
that no shared environment variable is ever set. Without it, someone "improves"
opencode isolation and breaks `git` for every subprocess the agent spawns.

If a change makes either fail, the change is wrong. Do not adjust the test.

## Never set a shared environment variable

`ap` sets only each agent's own config-directory variable. Not `HOME`, not
`XDG_CONFIG_HOME`, not anything another program also reads.

The agent runs as its own process and everything it spawns inherits the
environment, so redirecting a shared variable sends `git`, `gh`, `npm` and every
language server looking for *their* config inside the profile directory.

This is why opencode is **additive** rather than isolated: `OPENCODE_CONFIG_DIR`
only appends to its search path, and the variable that would replace the root is
`XDG_CONFIG_HOME`. Additive is the accepted trade. Do not "fix" it.

## The registry describes other people's software

`internal/agent/agent.go` asserts how four external CLIs behave. Every field was
verified by running the real binary — not read from documentation, and not
recalled from training data.

If you change a row, prove it first:

```bash
CODEX_HOME=/tmp/x codex doctor
PI_CODING_AGENT_DIR=/tmp/x pi list
OPENCODE_CONFIG_DIR=/tmp/x opencode debug config > /tmp/cfg.json
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

## Flag order is load-bearing

`ap`'s own flags come **before** the `<agent>:<profile>` reference; everything
after it goes to the agent verbatim. Do not add GNU-style intermixed parsing:
passthrough is the feature, and `ap run claude:plan --effort xhigh` has to reach
claude untouched.

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

## Anything that turns user input into a path must call `profile.ValidName`

`--from` once skipped it and became a path traversal that copied the user's real
`~/.claude` into a profile. `profile.Dir` joins and cleans, so `..` escapes.

## Deliberately absent — do not add

- **`ap use` / `ap shell` / an active profile.** A "current profile" that a bare
  `claude` would ignore is hidden state that lies to the user.
- **`--from-base`.** Copying out of `~/.claude` needs a per-agent allowlist of a
  directory that grows every release; a stale list copies caches silently.
- **Windows.** It would need a second execution model and a second sharing
  mechanism. The build tags say so.
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
does not already exist — then it runs `verify`, `secrets` and `snapshot`, and only
after all three pass does it tag and push. **Every gate runs before the tag exists**, so a failure
leaves origin with no orphan tag and the fix is simply another `make release`.

`release.yml` fires on the pushed tag, re-runs `verify`, then `make
release-publish` (goreleaser). `release-publish` is CI-facing; it needs
`GITHUB_TOKEN` and is the only target that publishes anything.

`make snapshot` alone is the dry run: the same four archives and `checksums.txt`,
nothing published.
