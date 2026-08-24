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

`make smoke` runs there, not on the host. Two things follow:

- **A missing agent fails the run.** Every `command -v <agent>` guard ends in
  `bad`, so an absent binary sets `fail=1` and smoke exits non-zero. The image
  installs all four, so absence means a broken image — most likely a package
  that renamed its binary, which is exactly what pinning nothing invites.
  Not `skip`: that keeps `fail` at 0, so the run prints "all checks passed"
  having tested nothing. `skip` is only for a check that cannot observe its
  property in this environment, and it must say why — see the
  `ANTHROPIC_API_KEY` and `--from default` cases.
- The seeded home is load-bearing. Every "shared state survived" assertion is
  vacuous against an empty one, and three checks were caught passing that way:
  a `[user]` git section with no keys made the shim passthrough compare 0 against
  0, and the credential and transcript assertions had nothing to lose. When you
  add a check, seed what it needs to be able to fail.

Every credential in the seed is synthesised, from the **field names** of a real
one and never a value. All four agents are covered: claude's
`.credentials.json`, codex's and pi's `auth.json`, and opencode's three
(`auth.json`, `account.json`, `mcp-auth.json`). None needs to be accepted,
because no check is about acceptance: what is asserted is that the profile
REACHED the file through ap's symlink. Each agent reports that without
validating anything and without a network —

| agent    | asked with                                        | reached | not reached                  |
|----------|---------------------------------------------------|---------|------------------------------|
| claude   | a `-p` run                                        | "Failed to authenticate" | "Not logged in · Please run /login" |
| codex    | `codex login status`                              | "Logged in" | "Not logged in"           |
| pi       | `pi auth check --provider … --no-refresh --json`  | `"status":"ready"` | `credentials_not_configured` |
| opencode | `opencode auth list`                              | provider line, "1 credentials" | "0 credentials" |

Only "not reached" is ap's business. With `ANTHROPIC_API_KEY` set claude does not
open the credential at all, so that check skips rather than passing with the link
severed.

pi's entry is what the rule above costs in practice. Its seed held `{}` until the
checks were added, and `{}` answers `credentials_not_configured` — the same
answer a severed link gives, so no check written against it could ever have
failed. Seeding a credential it can actually read is what made the assertion
possible.

Two of those four answers contain their own negative, so both are anchored:
`"status":"ready"` on the JSON field, never a bare `ready`, because the negative
is `not_ready`; and opencode on the **provider name**, never `1 credentials`,
which is a substring of `11 credentials`.

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


## An install must be asserted on the path, not on the agent's word

claude, codex and pi all install the same marketplace plugin, from the same
`owner/repo`, into their own profile. What is under test is never what the plugin
does — it is that the install landed in the directory ap redirected.

Two traps, both measured by mutating the redirect away (`Env` returning the base
environment for codex and pi) and re-running:

- **Asking the agent whether it installed something proves nothing.** With the
  redirect gone, `codex plugin list` still says "installed, enabled" and `pi list`
  still lists the package: they are describing the real `~/.codex` and
  `~/.pi/agent`. Both checks stayed green through the mutation until they were
  anchored on the profile path the listing prints. Assert the path.
- **`plugins/cache`, never the marketplace clone.** On claude and on codex alike,
  `SKILL.md` exists under the marketplace checkout as soon as the marketplace is
  added, whether or not anything was installed. Searching there reads as "the
  skill is available" while asserting "a git clone happened".

`codex plugin list` also needs anchoring on `"installed, enabled"`: the negative
answer is `not installed`, which contains `installed`.

pi's packages are not claude plugins — pi clones the repo and records it in its
own `settings.json`, and never reads the skill. It is checked anyway, because the
property under test is the same one.

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
