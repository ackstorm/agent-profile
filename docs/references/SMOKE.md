# Three images, and only one of them pins anything

> Extracted from `CLAUDE.md`, which links here from its MANDATORY reading
> table. Read it before editing `Dockerfile.smoke`, `Dockerfile.devtools` or
> `scripts/smoke.sh`. Every claim below was measured against a real binary —
> do not re-derive it.

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
  That ordering is also why smoke could never have caught the bug below: the one
  thing it does not observe is the state claude leaves behind.
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


## When a smoke check is lying

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
