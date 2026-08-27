# Phase 8 — Distribution: one implementation, three ways in

**Spec of record:** `agent-profile-declarative-spec-v0.6.3.md` (§26.1, §33.2, §35.1).
**Roadmap:** `2026-08-26-declarative-v1-roadmap.md` § "Phase 8".

D6 (binary vs SDK) is deferred by choice and does not block: the zero-logic-CLI
rule is adopted regardless, because it is free from the start and expensive to
retrofit.

---

## 0. What Phase 8 is deciding

### 0.1 A root must be nameable as a literal directory

The exit criterion is "the image materializes SPEC §35's example into an empty
directory with **no `$HOME` set**". Today every root-taking command names its
root with a reference, and `profile.Dir` derives a named profile's directory
from `XDG_DATA_HOME` or `$HOME`.

That derivation is fine for a laptop and is exactly what §33.2 forbids for a
container: "A root is a **parameter**. An implementation MUST NOT infer one from
the environment." An init container that has to arrange `XDG_DATA_HOME` so that
`<it>/agent-profile/profiles/claude/hydrated` lands on the mount the main
container reads is inferring a root from the environment through two layers of
indirection.

**Settled: `--root <dir>` names the directory literally, and then the positional
is a bare AGENT name.** It is not a third root in §33.2's sense — it is the same
one mechanism (point the agent's configuration-directory variable at a
directory) with the directory stated instead of derived. The two spellings are
mutually exclusive and saying both is an error, because they answer the same
question and a silent precedence rule is how the wrong directory gets written.

The interactive gate does not apply: a literal directory is neither the user's
real configuration nor a profile ap manages, so there is nothing for ap to
claim it can or cannot undo. `--root ~/.claude` is the user pointing at their
own configuration deliberately, with the path in their own hand — the display
the gate exists to provide is the command line they just typed.

Spec amendment for v0.6.4, recorded in `2026-08-26-open-decisions.md`.

### 0.2 What "zero-logic CLI" is actually claiming

`cmd/ap` is argument parsing, gates and printing. The claim worth testing is
narrow and falsifiable: **for one manifest, the CLI's result and the library's
result are the same bytes.** A golden text comparison would test the printer;
comparing the two ROOTS tests the thing that matters, because the root is the
product.

`oneResourceProfile`, `deriveSecretName` and `locator` stay in `cmd/ap`: they
turn *flags* into a `schema.Profile`, which is argument parsing. Nothing outside
a CLI has flags. If `ach` ever needs to install one resource without a manifest,
it builds the same one-resource Profile itself in three lines and calls
`hydrate.Apply` — which is the parity this phase asserts.

### 0.3 The image pins the binary and nothing else

`Dockerfile.devtools` pins every tool because `make verify` must mean one thing
everywhere. `Dockerfile.smoke` pins nothing because a pinned agent freezes the
thing under observation. The hydrator image is a third case: it ships **one**
artifact, `ap`, built from this tree, and needs git for git sources and CA
certificates for HTTPS. Nothing else belongs in it.

It runs as a **non-root user with no `$HOME`**, because that is the condition the
exit criterion names and the condition an init container actually runs under.

---

## 1. Tasks

### T1 — `--root <dir>`  `cmd/ap/root.go`, `apply.go`, `install.go`, `uninstall.go`, `listref.go`, `manifest.go`
One helper resolves (subject, `--root`) to an `agentreg.Agent` and a directory,
refuses both spellings at once, and refuses neither.

### T2 — parity test  `cmd/ap/parity_test.go`
One manifest, two runs: `dispatch` into root A, `hydrate.Apply` into root B,
trees compared byte for byte including the ledger's file records.

### T3 — the image  `Dockerfile.hydrate`, `Makefile`, `scripts/hydrate-check.sh`
Build it, then run SPEC §35's shape through it into an empty directory with
`HOME` unset and a non-root user, and assert what landed.

### T4 — the published surface  `pkg/doc.go`, `docs/references/DECLARATIVE.md`
What is exported, what "version 1" promises, and what breaking it costs.

**Gates:** `verify`, then `sandbox`, `smoke`, `secrets`, and the new image check.

## 2. Exit criteria (roadmap's)

- the parity test passes
- the image materializes SPEC §35's example into an empty directory with no
  `$HOME` set

## 3. Mutation tests this phase owes

1. Let `--root` and a `<agent>:<profile>` reference coexist with a silent
   precedence → the ambiguity test goes red.
2. Break one field the CLI passes to `hydrate.Plan` → the parity test goes red.

---

# Phase 8 — COMPLETE (2026-08-26)

Gates: `verify`, `crossbuild` (now including `GOOS=darwin go vet ./...`),
`secrets`, `sandbox`, `smoke`, and the new `make hydrate`. Two mutation tests
recorded.

## What the plan did not predict

### The container found a wrong terminal check that had shipped for months

`stdinIsTerminal` tested `os.ModeCharDevice`. `/dev/null` is a character device.
So is `/dev/zero`, and so is `/dev/urandom` — and systemd, cron and every
container runtime hand a process `/dev/null` on stdin by default.

`docker run` with no `-t` did not refuse. It **printed the gate's question and
read the answer off a pipe**. The outcome that time was safe by luck (EOF is not
`y`), but the rule this program states is "a pipe is not consent", and a check
that accepts any character device accepts one that can deliver a `y`.

It is a `TCGETS` ioctl now (`TIOCGETA` on darwin/BSD), standard library only.
No unit test could have found this: a Go test's stdin is whatever `go test` was
given, and the wrong answer and the right answer agree on a pipe. It took a
runtime that hands out `/dev/null`.

`GOOS=darwin go vet ./...` joined `crossbuild` in the same change, because the
fix is the first thing in this tree whose constant is spelled differently on the
two supported platforms and `verify` runs in a linux container.

### Preflight made the whole topology impossible

§28 checks that the runtime CLI is executable. An init container does not have
the agent binary — that is the entire point of the split — so the first run of
the image failed at preflight before touching anything.

The same applies to every active stdio MCP command: `npx` belongs to the
runtime's process, which is in the other container. `Preflight` now takes
`runtimeIsLocal`, true whenever the root came from a reference, because a
profile exists to be launched. One parameter, threaded through `Resolve` and
`ResolveProfile`; an earlier attempt at a second `ResolveNoCLI` entry point and
a sentinel error was worse and was thrown away.

### `safe.directory` cannot be waived from the environment

The check mounts a seeded git repository so it needs no network. Under the
image's `65532`, git refused a repository owned by the host user — and refuses
it from system and global config only, on purpose, so no `GIT_CONFIG_*`
variable can wave it through.

The harness runs as the host uid instead, and the image's own non-root default
is asserted separately with `docker run --entrypoint id`. ap does not paper over
a user's git safety setting.

## Scope stated, not silently dropped

- **`--root` for `ap run`, `env`, `create`, `delete`, `variant`.** Not added.
  Those are launcher verbs and a literal directory is not a profile — there is
  no wrapper to write and no namespace to delete. The root-taking verbs are the
  five that touch a ledger.
- **D6 (binary vs SDK)** stays deferred, and this phase did not need it: the
  parity test asserts both ways in produce the same bytes, which is what makes
  the question cheap to answer later.
- **The image is not published.** It builds and is exercised locally; wiring it
  into a registry is `ach-runtime`'s adoption, which is Phase 9 and in another
  repository.
