# Which of the two credentials survives is the user's call, not `Link`'s

> Extracted from `CLAUDE.md`, which links here from its MANDATORY reading
> table. Read it before editing `internal/profile/share.go` or anything that
> resolves a share conflict. Every claim below was measured against a real
> binary — do not re-derive it.
>
> `CLAUDE.md` keeps only the standing rule these two sections produced. The
> measurements that justify it are here.

## A share the agent overwrote is healed, not refused

`Link` used to abort on finding a real file where a share's symlink belongs.
That was wrong, and it was measured wrong on this machine: two of three claude
profiles held a 74 KB regular file at `.credentials.json`, differing from the
shared one in exactly the four `claudeAiOauth` leaves out of 796 — claude's own
temp-file-plus-rename, during ordinary use. `ap run` on those profiles was dead
until someone moved the file by hand, which is what the error text asked for.

It now does that itself: rename to `<rel>.ap-orphan` through the same `os.Root`,
relink, and say so — `rc.warn` from `create`, stderr from `run`, one wording in
`orphanWarning`. Renamed and not removed because it is a credential and may hold
the newer of the two tokens; both halves are mutation-tested by
`TestLinkMovesRealDataAsideAndRelinks` (restore the refusal and it fails; heal by
`RemoveAll` and it fails). A second overwrite overwrites the first orphan, which
is the point — of two stale credentials the older one is the one worth losing.

## …but which of the two survives is the user's call, not `Link`'s

Healing alone was still wrong, because "the older one is the one worth losing"
was an assumption `Link` never checked. The shared credential is the one it
always kept, and **nothing is able to update it from inside a profile**: claude's
rename replaces the symlink, so a refresh or a `/login` performed in a profile
lands in the profile and the shared file keeps the old token. Measured here,
`claudeAiOauth` carries a `refreshTokenExpiresAt` about 29 days out that only a
refresh moves forward. So a shared credential nothing may write eventually
expires outright, and from then on every profile asks for a login it has nowhere
to put — the loop the healing was supposed to end.

So `Link` now takes a `resolve func(Conflict) Resolution` and asks. `Orphan` is
what it always did; `Promote` copies the profile's file over the shared one,
keeping what it replaced at `<shared>.ap-previous`, and then orphans and relinks
exactly as before. Four rules hold it together, each mutation-tested:

- **A nil resolver means `Orphan`.** `ap create` passes nil. Promotion is a
  decision and silence is not one (`TestLinkWithNoResolverNeverTouchesTheSharedPath`).
- **Identical files are not a conflict** and `resolve` never hears about them.
  claude rewrites the credential whether or not the tokens changed, and a prompt
  whose two answers produce the same bytes teaches people to dismiss the prompt
  that matters (`TestLinkDoesNotAskAboutAnIdenticalFile`).
- **Off a terminal, ap never asks and never promotes.** `askToPromote` checks
  `os.Stdin.Stat` for `ModeCharDevice` — not `x/term`, this is stdlib only. The
  sandbox check feeds a literal `1` down a pipe and asserts it is ignored;
  written with `</dev/null` instead it was vacuous, because an empty answer also
  means keep. `dev.sh` passes `-it`, so an unguarded prompt would also hang
  `make sandbox` itself, and `echo … | ap run claude:x -p` puts the agent's own
  prompt on stdin, which ap must not eat.
- **A symlink at the shared path is refused, not replaced.** That is somebody's
  dotfile manager: following it writes a credential somewhere ap was never
  pointed at, replacing it strands the file they version. The refusal happens
  before anything is moved, so the run fails with the profile untouched and the
  same choice comes back next time (`TestPromoteRefusesASymlinkedSharedPath`).

The backup is not optional. Promotion is the only thing in this program that
writes outside a profile, and ap cannot tell whose account either credential
belongs to — the identity lives in `.claude.json`, which is deliberately not
shared. A `/login` with a different account inside a profile therefore *can*
become the machine-wide login; `.ap-previous` is the only way back. Both writes
go through an `os.Root` on the shared file's own directory, which is defence in
depth rather than the tested guard: with the refusal in place nothing reaches it
holding a symlink.

Do not widen this into ap syncing credentials on its own. It moves one file, once,
because a human at a terminal said so.

