# Windows: what is done, and the four things blocking a published binary

Decision D1 made Windows a supported target with the **full** command surface,
explicitly not a hydration-only binary, and pre-authorized dropping the binaries
if it turned into a swamp.

It is not one swamp. The part that was expected to be hard is done; four
specific things block shipping, and one of them is silent.

## Done

- **`run` uses spawn semantics.** `internal/run/handoff_windows.go`: start the
  child with `os/exec`, inherit the parent's standard handles so the agent has a
  real console and its own raw-mode terminal handling works, and exit with the
  child's own code.

  The exit code matters more than it looks. A non-zero exit is the AGENT
  speaking; turning it into ap's own generic failure would leave every script
  wrapping ap unable to tell "the agent said no" from "ap broke".

- **`syscall.Exec` is not a compile error on Windows.** It exists there as a
  stub that always returns `EWINDOWS`. That is why the unix code cross-compiles
  cleanly and would fail at the single moment that matters, and it is why the
  split is a real platform split rather than a cosmetic one.

- **`internal/run` and all of `pkg/` build AND vet for `windows/amd64`**, gated
  by `make crossbuild`. The vet is the point: an unshipped path rots silently,
  and a build alone would not catch a `%s` given an `error`.

## Blocking, in order of how badly they fail

**1. The wrapper filename is a silent NTFS alternate data stream.** `ap create
claude:plan` writes `~/.local/bin/claude:plan`. On NTFS, `name:stream` is the
alternate-data-stream syntax, so this creates a stream called `plan` on a file
called `claude` — and **reports success**. Nothing errors, nothing warns, and
the wrapper is unreachable. This is the "worse than a warning" class: a guessed
path that writes to a name nothing opens.

Needs a real design, not a port: a sanitized filename plus a `.cmd`, and a
decision about what `ap link`/`unlink`/`delete` recognise as theirs. The
recognition bytes are load-bearing across versions — see `wrapperHeader`.

**2. The wrapper body is `#!/bin/sh`.** Inert on Windows. It has to become a
`.cmd` that forwards `%*`, which changes `wrapperScript`, its header constant,
and everything that identifies an ap-written wrapper.

**3. Shares are symlinks.** `os.Symlink` on Windows needs
`SeCreateSymbolicLinkPrivilege` or Developer Mode. `Link` heals a real file
found at a shared path but has no notion of "this filesystem will not give me a
symlink at all". Options are junctions for directories (no privilege needed),
copy-with-writeback, or refusing sharing on Windows and saying so — each is a
product decision.

**4. None of it is testable here.** `make sandbox` and `make smoke` are Linux
containers, and there is no Windows runner. Shipping a binary whose sharing and
wrapper paths have never been *executed* would be exactly the guessed-registry-
row mistake in another dress — the one `CLAUDE.md` exists to prevent.

## To close it

A Windows CI job running `go test ./...`, then blockers 1–3 as designs rather
than ports, then the sandbox equivalent. Until then the spawn path stays vetted
and unshipped, which costs nothing and keeps the decision reversible.
