# Three stages and the real config, as `ap sync` manifests

```
plan → execute → review          (+ default: the agent you already had)
```

```bash
ap sync ./examples/agent-profiles --dry-run   # read the plan first
ap sync ./examples/agent-profiles             # default first, then the three profiles
```

16 targets (3 stages × claude, codex, pi, opencode — plus the four `default`
ones), 16 variants, 69 install commands.

## The same tools everywhere

| | claude | codex | pi | opencode |
|---|---|---|---|---|
| rtk (binary) | `bootstrap`, machine-level, guarded | same | same | same |
| pyright (binary) | `bootstrap`, machine-level, guarded | same | same | same |
| ponytail | `plugin marketplace add DietrichGebert/ponytail` + `plugin install ponytail@ponytail` | same, but `plugin add` | `pi install git:github.com/DietrichGebert/ponytail` | `opencode plugin -g "@dietrichgebert/ponytail"` |
| superpowers | `obra/superpowers-marketplace` + `plugin install superpowers@superpowers-marketplace` | same, `plugin add` | `pi install git:github.com/obra/superpowers` | `opencode plugin -g "superpowers@git+…"` |
| pyright-lsp | `plugin install pyright-lsp@claude-plugins-official` | — | — | — |
| rtk hooks | `rtk init -g --auto-patch` | `rtk init -g --codex` | `rtk init -g --agent pi --auto-patch` | only in `00-default.yaml` — see below |

Nothing per-stage. Every profile gets the same set, so what makes the stages
different is the session history and the variants, not the tooling.

`bootstrap` runs **once per manifest, before any profile exists and with no
agent variable set** — the right place for rtk, the wrong place for a plugin.
It is guarded with `command -v rtk >/dev/null ||` because a directory sync runs
four manifests and neither rtk nor pyright needs installing twice.

`install` runs **inside one profile**, with that platform's config variable
pointed at it, so `claude plugin install …` in `claude:review` writes to
`~/.local/share/agent-profile/profiles/claude/review`, never to `~/.claude`.

### Which of these actually honour the variable, measured

ap sets one variable and cannot make a third-party tool read it (§8.5 of the
spec). So each was run against a throwaway home and the files were looked for:

| | result |
|---|---|
| `rtk init -g --auto-patch` under `CLAUDE_CONFIG_DIR` | ✔ `CLAUDE.md`, `RTK.md`, `settings.json` **inside the profile**; the real `~/.claude` stayed empty |
| `rtk init -g --codex` under `CODEX_HOME` | ✔ `AGENTS.md`, `RTK.md` inside the profile |
| `rtk init -g --opencode` under a shimmed `XDG_CONFIG_HOME` | ✘ writes `~/.config/opencode/plugins/rtk.ts` — resolves `$HOME/.config` directly |

That last row is why no opencode profile runs `rtk init`: inside a profile it
would leak into the real config. It stays in `00-default.yaml`, where writing
globally is exactly the intent. `rtk`'s own state (`~/.config/rtk`) is global in
every case, which is fine — it is rtk's, not the agent's.

Check any tool you add the same way:

```bash
ap create claude:probe
ap env claude:probe <the install command>
ls ~/.local/share/agent-profile/profiles/claude/probe   # did it land here…
ls ~/.claude                                            # …or here?
ap delete claude:probe --yes
```

## The variants

| Stage | claude | codex |
|---|---|---|
| `plan` | `brainstorm` · `write` · `opus` | `brainstorm` · `write` |
| `execute` | `run` · `subagents` · `worktree` | `run` · `yolo` |
| `review` | `review` · `simplify` · `security` · `finish` | `review` (codex's own) · `simplify` |

`pi` and `opencode` get the tools and no variants: pi was not installed on the
machine these were written on, and opencode's argv was not measured here, so
nothing is claimed about either one's flags.

A worked pass:

```bash
ap run claude:plan:write      "add a --json flag to ap list"
ap run claude:execute:run     docs/superpowers/plans/2026-08-24-json-flag.md
ap run claude:review:review
ap run codex:review:review               # a second opinion, another model
ap run claude:review:finish              # tests, then merge / PR / keep
```

## `00-default.yaml` is not a profile

`name: default` targets `~/.claude`, `~/.codex`, `~/.config/opencode` and pi's
real config directory,
so a bare `claude` typed anywhere gets the same set the profiles get. **Nothing is
created for it** — no directory, no shared links, no shim, no wrapper — and the
install commands run with no override set at all.

There is one gate, not two: `--yes` covers it like everything else, and
`--dry-run` prints the resolved directory so you see what is at stake first.

```bash
ap sync ./examples/agent-profiles --dry-run
ap sync ./examples/agent-profiles --yes
```

It is named `00-` so it runs first — manifests load in filename order, and its
`rtk init -g` writes machine-level configuration before any profile installs on
top of it. Installing into it does **not** reach the profiles: a profile is a
separate config root, and only `.credentials.json` and `projects` are shared
back. Measured: a fresh profile lists no plugins while the real config lists
nine, and `--from default` does not carry them either.

Variants are not accepted under it, and the schema refuses them: a variant is a
file ap writes for a profile, and nothing is ever written for the real config.

## The `{}` in every prompt

`args: --effort=xhigh "/superpowers:writing-plans {}"` is a prompt **prefix**.
What you type after the reference is substituted where `{}` is, as one argument:

```bash
ap run claude:plan:write "add a --json flag to ap list"
#  → claude --effort=xhigh "/superpowers:writing-plans add a --json flag to ap list"
```

Without `{}` the arguments would be appended instead, and claude takes one
trailing positional and drops a second in silence — the bug the placeholder
exists to make inexpressible. codex takes a positional `PROMPT` the same way,
so its variants use `{}` too.

## Why a profile per stage rather than one profile with 16 variants

A variant is only a set of launch arguments; the profile underneath is the
configuration, the plugins, the credentials and the **session history**.
Splitting by stage means `ap sessions claude:review` lists reviews and nothing
else, and the reviewer never resumes into the session that wrote the code.

If you want the opposite — one shared history, several ways in — put every
variant under one `name:` and delete the other two files.

## Things worth knowing before you run this

- **The install lines were checked against the real binaries** for claude
  (`claude plugin marketplace add <source>`, `claude plugin install
  <plugin>@<marketplace>`) and codex (`codex plugin marketplace add <SOURCE>`,
  `codex plugin add <PLUGIN@MARKETPLACE>`). The `pi` lines are as upstream
  documents them and were **not** run here.
- **`00-` runs first** — filename order — so its machine-level bootstrap and
  its global `rtk init` are done before any profile installs on top.
- **The bootstrap block repeats in all four files.** v1 has no includes and no
  merge semantics, deliberately — two files claiming one `<platform>:<name>` is
  an error, not an override. Four copies of one guarded line is the cost.
- **Nothing is pinned.** Two people syncing a week apart can get different
  plugin versions.
- **ap cannot tell whether an install honoured the variable.** A tool that
  resolves `~/.claude` directly writes to your real config and reports success.
  Check a new one once with `ap env claude:probe <command>` and see where the
  files land.

Syncing a directory runs its commands as you. `ap sync` shows them and asks
first, and off a terminal it refuses unless you pass `--yes`.
