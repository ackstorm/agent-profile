# Three stages and a shared base, as declarative manifests

```
plan → execute → review        each its own profile, on four runtimes
```

```bash
ap manifest render ./examples/agent-profiles/plan.yaml     # what does it MEAN?
ap manifest apply claude:plan ./examples/agent-profiles/plan.yaml --dry-run
ap manifest apply claude:plan ./examples/agent-profiles/plan.yaml
```

`render` with no `--target` composes **every** target the manifest declares and
prints nothing; its exit status is the answer. That is the contract check to run
in CI, and it is why there is no separate `validate`.

## A manifest is an input

Applying one does not make it the profile's source of truth. Nothing writes back
to it, and you may throw it away:

```bash
ap list claude:plan                  # what the profile actually holds
ap uninstall claude:plan skill pdf   # bounded by that, not by any manifest
ap manifest export claude:plan       # the ledger, back as a manifest
```

Apply is **additive**. A manifest that stops mentioning a skill does not remove
it; removal is `ap uninstall`, and it is explicit on purpose.

## What these replaced, and what does not survive the change

These four files replace manifests written for `ap sync`, which is gone. That
command's centrepiece was an `install:` list of shell one-liners — `claude
plugin install ponytail@ponytail`, `rtk init -g --auto-patch` — run as you, with
the profile's configuration variable set.

Nothing here runs a shell. A capability is **declared** and ap materializes it,
which is what lets one declaration work on four runtimes and what lets
`ap uninstall` know precisely what to take back. The trade is real and worth
stating plainly:

| `ap sync` did | this does |
|---|---|
| ran a tool's own installer | fetches a source and writes the files itself |
| whatever the installer left behind | a ledger recording every file and merged key |
| no removal | `ap uninstall`, bounded by that ledger |
| arbitrary code execution, gated by a prompt | no shell, so no gate to get wrong |

**Running an installer is not expressible any more.** If a tool must run its own
bootstrap, run it yourself under the profile's environment:

```bash
ap env claude:plan -- rtk init -g --auto-patch
```

## Not every runtime takes everything

Four runtimes do not agree, and ap says so rather than dropping the difference
in silence (§8). Applying `base.yaml` to codex warns that skills have no
destination inside `CODEX_HOME` — codex reads `~/.agents/skills`, which is
outside the directory ap isolates. That warning is the feature: "why is my skill
missing" has exactly one useful answer.
