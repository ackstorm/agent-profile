#!/usr/bin/env bash
#
# scripts/walkthrough.sh — the two sequences a real person actually types.
#
# Usage: ./scripts/walkthrough.sh            run both walkthroughs
#        ./scripts/walkthrough.sh newcomer   run one of them
#
# This is an EXECUTABLE README, and it is a different question from the other
# checks. sandbox.sh asserts properties one at a time ("delete does not follow
# the shim"); this runs a sequence end to end, in the order a person meets it,
# and prints what they would see.
#
# It exists because three real defects shipped past a green sandbox and were
# found by typing commands by hand:
#
#   - `ap list <ref> --raw` ignored --raw, and `ap list <agent> --root <dir>`
#     ignored --root entirely: both flags were read before the re-parse that
#     picks them up after the subject;
#   - `ap list` printed `default` as "read-only" long after the ledger made
#     that false;
#   - the wrong-argument-order error said "needs a root" while staring at
#     something plainly a manifest path.
#
# Every one of those is invisible to a per-property assertion and obvious the
# moment somebody reads a transcript. So the failure mode this guards is "the
# sequence stopped making sense", and the cheapest honest way to guard it is to
# run the sequence and check every step's exit status.
#
# Deterministic on purpose: stub agents, a home built and thrown away, and local
# sources rather than the network. `make smoke` is where real agents live, and
# `ap manifest render examples/agent-profiles/*.yaml` is what keeps the shipped
# examples parsing.

set -euo pipefail

if [[ "${AP_IN_DEVTOOLS:-0}" != "1" ]]; then
    cd "$(dirname "${BASH_SOURCE[0]}")/.."
    exec ./scripts/dev.sh ./scripts/walkthrough.sh "$@"
fi

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$ROOT/.gocache/walkthrough"
AP="$WORK/ap"
fail=0

dim=$'\033[2m'; bold=$'\033[1m'; green=$'\033[32m'; red=$'\033[31m'; off=$'\033[0m'

note() { printf '\n%s# %s%s\n' "$dim" "$1" "$off"; }
act()  { printf '\n%s══ %s %s\n' "$bold" "$1" "$off"; }

# step "<what it should do>" <command...> — echo it, run it, show it, check it.
# Output is indented so a transcript reads like a terminal session.
step() {
    local want="$1"; shift
    printf '\n%s$ ap %s%s\n' "$bold" "$*" "$off"
    local out rc
    out=$("$AP" "$@" 2>&1) && rc=0 || rc=$?
    [ -n "$out" ] && printf '%s\n' "$out" | sed 's/^/  /'
    if [ "$rc" != 0 ]; then
        printf '  %sFAIL%s exit %s — expected to succeed (%s)\n' "$red" "$off" "$rc" "$want"
        fail=1
    fi
    LAST_OUT="$out"
}

# refuse "<why it must refuse>" <command...> — the same, for a command that
# MUST fail. A walkthrough that only ever runs happy paths teaches nothing
# about the guards, and a guard nothing exercises is one nobody notices losing.
refuse() {
    local why="$1"; shift
    printf '\n%s$ ap %s%s\n' "$bold" "$*" "$off"
    local out rc
    out=$("$AP" "$@" 2>&1) && rc=0 || rc=$?
    [ -n "$out" ] && printf '%s\n' "$out" | sed 's/^/  /'
    if [ "$rc" = 0 ]; then
        printf '  %sFAIL%s succeeded, and it must refuse: %s\n' "$red" "$off" "$why"
        fail=1
    fi
    LAST_OUT="$out"
}

# says "<substring>" — assert the last step's output. Used sparingly: the point
# of this script is the sequence, not a second copy of the unit tests.
says() {
    if ! printf '%s' "$LAST_OUT" | grep -qF -- "$1"; then
        printf '  %sFAIL%s the output does not mention %s\n' "$red" "$off" "$1"
        fail=1
    fi
}

seed() {
    rm -rf "$WORK"
    mkdir -p "$WORK"/{bin,link,src/scripts}

    cat >"$WORK/bin/stub" <<'STUB'
#!/bin/sh
echo "argv:$*"
env | grep -E '^(CLAUDE_CONFIG_DIR|CODEX_HOME|PI_CODING_AGENT_DIR|XDG_CONFIG_HOME)=' || true
STUB
    chmod +x "$WORK/bin/stub"
    for a in claude codex pi opencode; do ln -sf stub "$WORK/bin/$a"; done

    printf '# brainstorming\n\nAsk before building.\n' >"$WORK/src/SKILL.md"
    printf 'print("ok")\n'                             >"$WORK/src/scripts/run.py"

    # The manifest sits BESIDE the source it names. §16 keeps a local path
    # inside the manifest's own directory, so a manifest in manifests/ cannot
    # reach ../src — which the first run of this script discovered.
    cat >"$WORK/team.yaml" <<YAML
version: "1"
name: team
targets:
  - claude
  - codex

skills:
  brainstorming:
    source:
      local:
        path: ./src

mcps:
  memory:
    transport:
      type: http
      url: https://memory.company.com/mcp
      headers:
        Authorization:
          prefix: "Bearer "
          value_from:
            secret: memory-token

inputs:
  secrets:
    memory-token:
      env: MEMORY_TOKEN
YAML

    go build -o "$AP" ./cmd/ap
}

export HOME="$WORK/home"
mkdir -p "$HOME/.claude" "$HOME/.codex"
seed
export PATH="$WORK/bin:$PATH"
export AP_LINK_DIR="$WORK/link"
export MEMORY_TOKEN=not-a-real-secret

# ---------------------------------------------------------------------------

newcomer() {
    act "NEWCOMER — I just installed this. What is it?"

    note "Nothing of mine is a profile yet: the four rows are the configs I already had."
    step "list the agents"                  list
    says "not a profile"

    note "Make one. It writes a wrapper too, so the profile is a command I can type."
    step "create a profile"                 create claude:plan

    note "Put something in it. No manifest, no config file — one command."
    step "install one skill"                install claude:plan skill brainstorming --local "$WORK/src"
    says "skills/brainstorming/SKILL.md"

    note "What does this profile actually hold? This reads the LEDGER, not a manifest."
    step "list what is installed"           list claude:plan
    says "skill"

    note "Use it. Everything after the reference goes to the agent untouched."
    step "run the agent under it"           run claude:plan --version
    says "CLAUDE_CONFIG_DIR=$HOME/.local/share/agent-profile/profiles/claude/plan"

    note "I edited a file by hand. Removal must not take that with it."
    printf 'mine\n' >"$HOME/.local/share/agent-profile/profiles/claude/plan/skills/brainstorming/scripts/run.py"
    step "preview the removal"              uninstall claude:plan skill brainstorming --dry-run
    says "modified since install"
    says "nothing was removed"

    note "Do it for real: my edit stays, ap's own files go."
    step "remove it"                        uninstall claude:plan skill brainstorming
    if [ ! -f "$HOME/.local/share/agent-profile/profiles/claude/plan/skills/brainstorming/scripts/run.py" ]; then
        printf '  %sFAIL%s the hand-edited file was removed\n' "$red" "$off"; fail=1
    fi

    note "And throw the whole profile away when I am done with it."
    step "delete the profile"               delete claude:plan --yes
}

experienced() {
    act "EXPERIENCED — I have a manifest and four machines to set up"

    note "First: does it even compose? No target named, so it checks EVERY one it declares."
    note "No network, nothing written — this is the CI check."
    step "validate the manifest"            manifest render "$WORK/team.yaml"

    note "Naming the runtimes is required. The refusal lists what the manifest declares."
    refuse "the runtimes were not named"    manifest apply "$WORK/team.yaml"
    says "--all-targets"

    note "Will it resolve HERE, with my credentials? This fetches and checks contracts."
    note "It does not write to the profile — but it is not a no-op either."
    step "dry run"                          manifest apply "$WORK/team.yaml" --target claude --dry-run
    says "nothing was written"

    note "Two runtimes, one command. The profile name comes from the manifest."
    step "apply for two runtimes"           manifest apply "$WORK/team.yaml" --target claude --target codex
    says "claude:team"
    says "codex:team"

    note "The same MCP server, in each runtime's own native shape and file."
    step "what claude got"                  list claude:team
    says ".claude.json"
    step "what codex got"                   list codex:team
    says "config.toml"

    note "The credential is a REFERENCE, never a value: MEMORY_TOKEN is set right now,"
    note "and what landed on disk is still the variable's name."
    if grep -rqF 'not-a-real-secret' "$HOME/.local/share/agent-profile/profiles/"; then
        printf '  %sFAIL%s a resolved secret VALUE reached the profile\n' "$red" "$off"; fail=1
    else
        printf '\n  %sOK%s   no secret value on disk\n' "$green" "$off"
    fi

    note "A launch mode over that profile. {} is where my argument lands, as ONE argv element."
    step "name a variant"                   variant claude:team:review -- --effort=xhigh "/code-review {}"
    step "run it"                           run claude:team:review src/auth.go
    says "argv:--effort=xhigh /code-review src/auth.go"

    note "I assembled this by hand. Hand it to someone else as a manifest."
    step "export the ledger"                manifest export claude:team
    says "brainstorming"
    "$AP" manifest export claude:team >"$WORK/exported.yaml" 2>/dev/null
    step "and it composes"                  manifest render "$WORK/exported.yaml"

    note "Surgical removal from a file the runtime and I both own."
    step "drop one MCP server"              uninstall codex:team mcp memory
    says "mcp_servers.memory"
    if ! grep -q 'model' "$HOME/.local/share/agent-profile/profiles/codex/team/config.toml" 2>/dev/null; then
        : # nothing else was in it; the surgical case is asserted in sandbox.sh
    fi

    note "What would a third-party installer inherit if I pointed it at this profile?"
    step "show the environment"             env claude:team
    says "CLAUDE_CONFIG_DIR"
}

case "${1:-all}" in
    newcomer)    newcomer ;;
    experienced) experienced ;;
    all)         newcomer; experienced ;;
    *) echo "usage: $0 [newcomer|experienced]" >&2; exit 2 ;;
esac

printf '\n'
if [ "$fail" = 0 ]; then
    printf '%sthe walkthrough runs end to end%s\n' "$green" "$off"
else
    printf '%sthe walkthrough broke — a step above failed%s\n' "$red" "$off"
    exit 1
fi
