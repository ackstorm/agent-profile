# `.claude.json` is where the surprises live

> Extracted from `CLAUDE.md`, which links here from its MANDATORY reading
> table. Read it before diagnosing "this profile behaves oddly" or touching
> `Agent.FirstRun`. Every claim below was measured against a real binary —
> do not re-derive it.

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

