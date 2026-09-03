#!/usr/bin/env bash
#
# scripts/sandbox.sh — the home-safety checks, against a fake home, in a container.
#
# Usage: ./scripts/sandbox.sh            run the checks
#        ./scripts/sandbox.sh <cmd...>   run anything else in that environment
#
# This is NOT a replacement for scripts/smoke.sh, and running it does not mean
# smoke has been run. The two answer different questions:
#
#   smoke.sh  does the REAL binary honour what internal/agent claims? Needs
#             claude, codex, opencode and pi installed and logged in, and the
#             final assertion counts the transcripts in YOUR ~/.claude/projects,
#             which is a question a fake home cannot be asked. Host-only, and
#             deliberately so.
#
#   this      does `ap` keep its hands off a home that already has things in it?
#             Needs no agent and no login, because every check here observes ap's
#             own side of the contract: what it copies, what it deletes, what it
#             puts in the environment, what argv it execs with. That side is
#             fully observable with a stub on PATH.
#
# It exists because smoke.sh in a container is a lie: every one of its blocks is
# gated on `command -v <agent>`, so an empty image skips all twelve and exits 0
# announcing "all checks passed", with the transcript assertion answering 0 -> 0.
# A check that cannot go red is worse than no check — CLAUDE.md says so about the
# ones already found here, and this script is the answer for the half of smoke
# that never needed a real agent in the first place.
#
# The home is thrown away and rebuilt on every run, under .gocache (gitignored,
# already mounted). Nothing outside the repo is touched: HOME is reassigned
# before any check runs, and the container mounts nothing else.

set -euo pipefail

# Re-enter the devtools container, then reassign HOME once inside. Not via
# `dev.sh env HOME=...`: dev.sh passes -e HOME itself, and the two would be
# arguing about the same variable across the docker boundary.
if [[ "${AP_IN_DEVTOOLS:-0}" != "1" ]]; then
    cd "$(dirname "${BASH_SOURCE[0]}")/.."
    exec ./scripts/dev.sh ./scripts/sandbox.sh "$@"
fi

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SANDBOX="$ROOT/.gocache/sandbox"
AP="$SANDBOX/ap"

fail=0
pass() { printf '  \033[32mOK\033[0m   %-9s %s\n' "$1" "$2"; }
bad() { printf '  \033[31mFAIL\033[0m %-9s %s\n' "$1" "$2"; fail=1; }

# A home that has been used. An empty one makes every check below vacuous in the
# same way the container makes smoke.sh vacuous: nothing to lose means nothing
# can be observed being lost.
seed() {
    rm -rf "$SANDBOX"
    mkdir -p "$SANDBOX"/{bin,link}

    # claude: configuration, the credential ap symlinks, and three transcripts
    # standing in for the thing a bad Delete would erase.
    mkdir -p "$HOME/.claude"/{skills/demo,commands,projects}
    echo '{"theme":"dark","statusLine":{"type":"command","command":"echo hi"}}' >"$HOME/.claude/settings.json"
    echo '# instructions' >"$HOME/.claude/CLAUDE.md"
    echo '{"token":"not-a-real-secret"}' >"$HOME/.claude/.credentials.json"
    echo '{"hasCompletedOnboarding":true,"defaultToAgentsView":false}' >"$HOME/.claude.json"
    for p in alpha beta gamma; do
        mkdir -p "$HOME/.claude/projects/-home-me-$p"
        echo "{\"session\":\"$p\"}" >"$HOME/.claude/projects/-home-me-$p/transcript.jsonl"
    done

    mkdir -p "$HOME/.codex/sessions" "$HOME/.pi/agent/sessions"
    echo 'model = "gpt-5"' >"$HOME/.codex/config.toml"
    echo '{"token":"not-a-real-secret"}' >"$HOME/.codex/auth.json"
    echo '{"provider":"anthropic"}' >"$HOME/.pi/agent/settings.json"
    echo '{}' >"$HOME/.pi/agent/auth.json"

    # ~/.config as a machine actually has it: opencode plus four other programs
    # that have nothing to do with ap. The shim links all of them, so they are
    # what a Delete that followed those links would take with it.
    mkdir -p "$HOME/.config"/{opencode/agents,git,nvim,htop,systemd/user}
    echo '{"model":"anthropic/claude-sonnet-5"}' >"$HOME/.config/opencode/opencode.json"
    echo '[user]' >"$HOME/.config/git/config"
    echo 'vim.opt.number = true' >"$HOME/.config/nvim/init.lua"
    echo 'fields=0' >"$HOME/.config/htop/htoprc"
    echo '[Unit]' >"$HOME/.config/systemd/user/thing.service"

    # Four names, one stub: it reports the argv it was execed with and the
    # config variable it was given, which is exactly ap's half of the contract.
    # What the real agent then DOES with that variable is the registry's claim,
    # and only smoke.sh on a host can check it.
    cat >"$SANDBOX/bin/stub" <<'STUB'
#!/bin/sh
echo "argv:$*"
echo "cwd:$(pwd)"
# And again with the element boundaries visible. "$*" joins with a space, so a
# check written against it alone cannot tell one argument from two - which is
# precisely the property `ap run`'s {} placeholder exists to produce, since a
# prompt has to reach the agent as ONE element of argv.
for x in "$@"; do printf 'arg:[%s]\n' "$x"; done
env | grep -E '^(CLAUDE_CONFIG_DIR|CODEX_HOME|PI_CODING_AGENT_DIR|XDG_CONFIG_HOME)=' || true
STUB
    chmod +x "$SANDBOX/bin/stub"
    for a in claude codex pi opencode; do ln -sf stub "$SANDBOX/bin/$a"; done

    go build -o "$AP" ./cmd/ap
}

export HOME="$SANDBOX/home"
mkdir -p "$HOME"
seed
export PATH="$SANDBOX/bin:$PATH"
export AP_LINK_DIR="$SANDBOX/link"
# XDG_DATA_HOME is deliberately NOT set: the profiles root has to sit where it
# really sits, $HOME/.local/share, or `--from ../../../.claude` climbs out of a
# root that is somewhere else and lands on nothing. The traversal check then
# passes because create failed with "does not exist" — the right colour for the
# wrong reason, which is the failure mode this whole script is about.
# create prints a receipt and a PATH warning. Neither is what any check reads.
quiet() { "$@" >/dev/null 2>&1; }

# With arguments, this is just the environment: `./scripts/sandbox.sh bash` drops
# you into the fake home with ap built, the stubs on PATH and nothing of yours
# reachable. Chasing something by hand is what it is for.
if [ $# -gt 0 ]; then
    exec "$@"
fi

echo
echo "agent-profile sandbox check (fake home, no real agents)"

# --- delete must not reach what the profile links back to -------------------
# The crown jewel: TestDeleteDoesNotFollowSymlinks is the unit-level version.
# This is the same assertion end to end through the built binary, somewhere it
# can be allowed to go wrong.
#
# It asserts on the CREDENTIALS, not on the transcripts, because the credential
# is what a profile actually links into the real home today — one Share per
# agent, and for claude it is the only symlink a fresh profile has at all.
# projects/ and sessions/ moved to Unshared, so nothing points at them any more
# and counting them observes nothing. Measured, not assumed: with Delete mutated
# to resolve links before removing, ~/.claude/.credentials.json is destroyed and
# the transcript count does not move. scripts/smoke.sh:519 still counts only the
# transcripts, so the release gate is watching the half that can no longer be
# lost — see the note in the README.
sessions=$(find "$HOME/.claude/projects" "$HOME/.codex/sessions" -type f 2>/dev/null | wc -l)
creds="$HOME/.claude/.credentials.json $HOME/.codex/auth.json $HOME/.pi/agent/auth.json"
lost=""
for ag in claude codex pi; do
    quiet "$AP" create "$ag:sbx" && quiet "$AP" delete --yes "$ag:sbx" || lost="$lost $ag(setup)"
done
for c in $creds; do
    [ -f "$c" ] || lost="$lost ${c#"$HOME"/}"
done
if [ -n "$lost" ]; then
    bad delete "delete followed a link out of the profile and destroyed:$lost"
elif [ "$(find "$HOME/.claude/projects" "$HOME/.codex/sessions" -type f 2>/dev/null | wc -l)" -ne "$sessions" ]; then
    bad delete "shared session count changed"
else
    pass delete "credentials and sessions intact after three create+delete cycles"
fi

# --- delete must not follow the config shim ---------------------------------
# opencode is the shimmed agent, so its profile holds a link to every entry of
# ~/.config. A Delete that followed them would erase the configuration of every
# program on the machine — the worst thing this tool could do, and the reason
# TestDeleteDoesNotFollowTheConfigShim may never be weakened.
entries=$(find "$HOME/.config" -mindepth 1 -maxdepth 1 | wc -l)
files=$(find "$HOME/.config" -type f | wc -l)
if quiet "$AP" create opencode:sbx && quiet "$AP" delete --yes opencode:sbx; then
    if [ "$(find "$HOME/.config" -mindepth 1 -maxdepth 1 | wc -l)" -eq "$entries" ] &&
        [ "$(find "$HOME/.config" -type f | wc -l)" -eq "$files" ]; then
        pass shim "$HOME/.config survived intact ($entries entries, $files files)"
    else
        bad shim "the shim's passthrough links were followed into ~/.config"
    fi
else
    bad shim "could not create and delete opencode:sbx"
fi

# --- --from default copies configuration, never the runtime -----------------
# The traversal bug lived on this path: --from once skipped profile.ValidName
# and `--from ../../../.claude` copied the real home into a profile.
if quiet "$AP" create claude:sbxclone --from default; then
    d=$("$AP" which claude:sbxclone)
    if [ ! -f "$d/settings.json" ]; then
        bad from "settings.json was not cloned - the profile starts unconfigured"
    elif [ -e "$d/projects" ]; then
        bad from "the clone carried projects/ - session transcripts are not configuration"
    elif [ -e "$d/.credentials.json" ] && [ ! -L "$d/.credentials.json" ]; then
        bad from "the clone COPIED the credential instead of sharing it by link"
    else
        pass from "configuration cloned, transcripts and credential not"
    fi
    quiet "$AP" delete --yes claude:sbxclone
else
    bad from "ap create --from default failed"
fi
# The guard itself, on the same path. The number of ".." is computed, never
# guessed: profile.Dir joins under <data>/agent-profile/profiles/<agent>, so a
# hardcoded "../../../.claude" lands on ~/.local/share/.claude, which does not
# exist — create then fails with "source profile does not exist" and the check
# goes green with the guard removed. Measured: it did. Escaping all the way to
# $HOME/.claude, which the seed filled, is what makes the refusal mean the guard
# refused rather than the target being absent.
rel=".local/share/agent-profile/profiles/claude"
esc=$(printf '../%.0s' $(seq 1 "$(printf '%s' "$rel" | awk -F/ '{print NF}')"))
if [ ! -f "$HOME/.claude/settings.json" ]; then
    bad from "the traversal target is empty - this check could not observe a copy"
elif quiet "$AP" create claude:sbxbad --from "$esc.claude"; then
    bad from "--from $esc.claude was ACCEPTED - profile.ValidName is not on this path"
    quiet "$AP" delete --yes claude:sbxbad
else
    pass from "--from rejects a traversal that would have reached ~/.claude"
fi

# --- run: argv reaches the binary, the variable points inside the profile ---
# Nothing in `go test` execs anything: run_test.go stops at the argv it would
# have used. This is the only place the exec itself is observed.
if quiet "$AP" create claude:sbxrun; then
    d=$("$AP" which claude:sbxrun)
    out=$("$AP" run claude:sbxrun --effort xhigh -p 2>&1 || true)
    if [ "$(printf '%s' "$out" | sed -n 's/^argv://p')" != "--effort xhigh -p" ]; then
        bad run "argv did not reach the binary verbatim: $out"
    elif [ "$(printf '%s' "$out" | sed -n 's/^CLAUDE_CONFIG_DIR=//p')" != "$d" ]; then
        bad run "CLAUDE_CONFIG_DIR is not the profile: $out"
    else
        pass run "argv verbatim, CLAUDE_CONFIG_DIR inside the profile"
    fi

    # --help AFTER the reference belongs to the agent. ap grew per-command help,
    # and the obvious way to wire it — intercept -h anywhere — would swallow this
    # and break the one contract run exists for. Asserted on arg:, not argv:,
    # because "$*" cannot tell one argument from two.
    out=$("$AP" run claude:sbxrun --help 2>&1 || true)
    if ! printf '%s' "$out" | grep -qx 'arg:\[--help\]'; then
        bad help "ap answered --help itself instead of passing it to the agent: $out"
    else
        pass help "--help after a reference still reaches the agent"
    fi

    # A variant's stored arguments run first and the user's after, so the later
    # one wins wherever the agent takes the last flag.
    if quiet "$AP" variant claude:sbxrun:sbxv -- --model haiku -p; then
        out=$("$AP" run claude:sbxrun:sbxv --model opus 2>&1 || true)
        if [ "$(printf '%s' "$out" | sed -n 's/^argv://p')" = "--model haiku -p --model opus" ]; then
            pass variant "stored arguments first, the user's after"
        else
            bad variant "wrong order or missing arguments: $out"
        fi
    else
        bad variant "could not store the variant"
    fi

    # A variant may hang off the agent's REAL config. The store is a sibling of
    # the profiles root, so nothing is written inside ~/.claude, and `run` still
    # sets no config variable at all — which is the property that makes this
    # read-only in the sense that matters. Both halves are asserted: a composed
    # argv proves the variant was found, and an absent CLAUDE_CONFIG_DIR proves
    # the launch was still the plain one.
    if quiet "$AP" variant claude:default:sbxdef -- --model haiku; then
        before=$(find "$HOME/.claude" -mindepth 1 -maxdepth 1 | sed 's|.*/||' | sort | tr '\n' ' ')
        out=$("$AP" run claude:default:sbxdef -p hi 2>&1 || true)
        after=$(find "$HOME/.claude" -mindepth 1 -maxdepth 1 | sed 's|.*/||' | sort | tr '\n' ' ')
        if [ "$(printf '%s' "$out" | sed -n 's/^argv://p')" != "--model haiku -p hi" ]; then
            bad variant "a variant over default did not compose: $out"
        elif printf '%s' "$out" | grep -q '^CLAUDE_CONFIG_DIR='; then
            bad variant "running a variant over default set a config variable: $out"
        elif [ "$before" != "$after" ]; then
            bad variant "the real config directory gained entries: $after (was $before)"
        else
            pass variant "over default: composed, no config variable, nothing written"
        fi
    else
        bad variant "could not store a variant over default"
    fi

    # A BARE default is still refused by the commands that would write for it,
    # and `ap delete` must take the variant back without touching the directory.
    if quiet "$AP" delete claude:default --yes; then
        bad variant "ap delete claude:default was accepted"
    elif ! [ -d "$HOME/.claude" ]; then
        bad variant "the real config directory is gone"
    elif ! quiet "$AP" delete claude:default:sbxdef; then
        bad variant "a variant over default could not be deleted"
    elif quiet "$AP" run claude:default:sbxdef; then
        bad variant "the deleted variant still runs"
    else
        pass variant "over default: the variant is removable, the config is not"
    fi

    # A variant that leaves {} is a prompt PREFIX: the caller's arguments are
    # substituted there, joined, and NOT also appended. Asserted on the bracketed
    # form, never on argv:, because "$*" joins with a space and would read the
    # same whether the prompt arrived as one element or as two - and one element
    # is the entire point. claude's grammar takes one trailing positional and
    # drops a second in silence, which is why appending cannot express this.
    if quiet "$AP" variant claude:sbxrun:sbxfill -- --effort=xhigh "/plan {}"; then
        out=$("$AP" run claude:sbxrun:sbxfill docs/a.md docs/b.md 2>&1 || true)
        if printf '%s' "$out" | grep -qxF 'arg:[/plan docs/a.md docs/b.md]'; then
            pass variant "{} substituted, and the prompt is one argument"
        else
            bad variant "the placeholder did not fill into a single argument: $out"
        fi
        # And nothing was appended as well, which a substitute-then-append
        # implementation would leave behind.
        if printf '%s' "$out" | grep -qxF 'arg:[docs/a.md]'; then
            bad variant "the caller's arguments were substituted AND appended: $out"
        fi
    else
        bad variant "could not store the placeholder variant"
    fi
# --- the state surface: install, list, uninstall, export ---------------------
#
# Every assertion here is ap's own side of the ledger: what it writes, what it
# refuses to take back, and what a preview does. None of it needs a real agent
# or a network — the source is a directory on disk — which is why it lives here
# and not in smoke.sh, and why it can assert exact file contents.
SRC="$SANDBOX/src-skill"
rm -rf "$SRC" && mkdir -p "$SRC/scripts"
printf '# pdf\n' > "$SRC/SKILL.md"
printf 'print()\n' > "$SRC/scripts/convert.py"
PROF="$HOME/.local/share/agent-profile/profiles/claude/plan"

# An install needs no manifest anywhere, and everything it writes lands inside
# the profile.
rm -rf "$PROF"
if quiet "$AP" install claude:plan skill pdf --local "$SRC"; then
    if [ ! -f "$PROF/skills/pdf/SKILL.md" ] || [ ! -f "$PROF/skills/pdf/scripts/convert.py" ]; then
        bad install "the skill's files are not in the profile"
    elif [ ! -f "$PROF/.ap-ledger.json" ]; then
        bad install "nothing was recorded in the ledger"
    elif find "$HOME/.local/share/agent-profile" -name '*.yaml' | grep -q .; then
        bad install "a manifest was written; a manifest is an input, not state"
    else
        pass install "one capability installed with no manifest on disk"
    fi
else
    bad install "ap install failed"
fi

# list reads the LEDGER. A manifest says nothing about what is installed.
out=$("$AP" list claude:plan 2>&1 || true)
case "$out" in
    *skill*pdf*) pass list "ap list <ref> reports what the ledger holds" ;;
    *)           bad list "the listing does not name the installed skill: $out" ;;
esac

# --dry-run previews and touches nothing.
out=$("$AP" uninstall claude:plan skill pdf --dry-run 2>&1 || true)
if [ ! -f "$PROF/skills/pdf/SKILL.md" ]; then
    bad uninstall "--dry-run removed a file"
else
    case "$out" in
        *"would remove"*) pass uninstall "--dry-run previews and removes nothing" ;;
        *)                bad uninstall "--dry-run said nothing useful: $out" ;;
    esac
fi

# The exit criterion: a file the user edited is never removed. The ledger's
# recorded hash is the only thing that can tell ap's write from the user's.
printf "print('mine')\n" > "$PROF/skills/pdf/scripts/convert.py"
if quiet "$AP" uninstall claude:plan skill pdf; then
    if [ ! -f "$PROF/skills/pdf/scripts/convert.py" ]; then
        bad uninstall "the edited file was removed"
    elif [ -f "$PROF/skills/pdf/SKILL.md" ]; then
        bad uninstall "the unchanged file survived"
    elif "$AP" list claude:plan 2>&1 | grep -q 'pdf'; then
        bad uninstall "the ledger still claims the removed skill"
    else
        pass uninstall "the edited file kept, the unchanged one removed"
    fi
else
    bad uninstall "ap uninstall failed"
fi

# Export closes the loop: the ledger, back as a manifest that composes.
rm -rf "$PROF"
if quiet "$AP" install claude:plan skill pdf --local "$SRC"; then
    "$AP" manifest export claude:plan > "$SANDBOX/exported.yaml" 2>/dev/null
    if ! grep -q 'pdf' "$SANDBOX/exported.yaml"; then
        bad export "the exported manifest does not mention the installed skill"
    elif ! quiet "$AP" manifest render "$SANDBOX/exported.yaml"; then
        bad export "the exported manifest does not compose for its own targets"
    elif quiet "$AP" manifest apply "$SANDBOX/exported.yaml" --target claude --profile copy &&
         cmp -s "$PROF/skills/pdf/SKILL.md" \
                "$HOME/.local/share/agent-profile/profiles/claude/copy/skills/pdf/SKILL.md"; then
        pass export "a hand-built root exports to a manifest that re-applies"
    else
        bad export "the exported manifest did not re-apply to the same bytes"
    fi
else
    bad export "ap install failed before export"
fi

# --- a runtime-native plugin overrides the common one ------------------------
#
# SPEC §24.3's own case: pi packages plugins itself, so it takes ponytail its own
# way and the common plugin must NOT be materialized for it. ap writes the
# declaration into pi's settings.json and names the reconcile command; it never
# runs pi's installer, which is the line `ap sync` was removed to draw.
PSRC="$SANDBOX/src-plugin"
rm -rf "$PSRC" && mkdir -p "$PSRC/skills/ponytail"
printf '# ponytail\n' > "$PSRC/skills/ponytail/SKILL.md"
PPROF="$HOME/.local/share/agent-profile/profiles/pi/native"
rm -rf "$PPROF"
# A package the user put there by hand, seeded BEFORE apply. It is what makes
# the removal bound below mean anything: ours is appended beside it, and
# uninstall must take exactly ours back.
mkdir -p "$PPROF"
printf '{"packages":["git:example.com/theirs"]}\n' > "$PPROF/settings.json"
cat >"$SANDBOX/native.yaml" <<YAML
version: "1"
name: native
targets:
  - pi
plugins:
  ponytail:
    source:
      local:
        path: src-plugin
runtimes:
  pi:
    plugins:
      ponytail:
        package: "git:example.com/ours"
YAML
out=$("$AP" manifest apply "$SANDBOX/native.yaml" --target pi 2>&1) && rc=0 || rc=1
if [ "$rc" != 0 ]; then
    bad native "apply failed: $out"
elif ! grep -q 'git:example.com/ours' "$PPROF/settings.json" 2>/dev/null; then
    bad native "the package was not declared in pi's settings.json"
elif find "$PPROF" -name 'SKILL.md' -path '*ponytail*' | grep -q .; then
    bad native "the common plugin materialized despite the runtime-native override"
elif ! printf '%s' "$out" | grep -q 'pi update'; then
    bad native "the reconcile command was not reported: $out"
# ap RUNS the reconcile now, and the stub reports the argv it was execed with.
# Asserted on 'arg:[...]', never on 'argv:': the stub's "$*" joins with a space,
# so a check written against that line cannot tell one argument from two - and a
# locator arriving as ONE element is the whole reason ReconcileArgv returns argv
# instead of a command line for someone downstream to split.
elif ! printf '%s' "$out" | grep -qF 'arg:[git:example.com/ours]'; then
    bad native "the reconcile did not run, or split the locator: $out"
# And it ran under the PROFILE's environment, so pi writes inside the profile
# rather than into the real home.
elif ! printf '%s' "$out" | grep -qF "PI_CODING_AGENT_DIR=$PPROF"; then
    bad native "the reconcile ran outside the profile environment: $out"
else
    pass native "the native package is declared, the common plugin suppressed, the reconcile run"
fi

# --no-reconcile declares and stops. The declaration still lands: what the flag
# turns off is running someone else's command, never recording what was asked
# for.
rm -rf "$PPROF"
out=$("$AP" manifest apply "$SANDBOX/native.yaml" --target pi --no-reconcile 2>&1) && rc=0 || rc=1
if [ "$rc" != 0 ]; then
    bad native "--no-reconcile failed: $out"
elif ! grep -q 'git:example.com/ours' "$PPROF/settings.json" 2>/dev/null; then
    bad native "--no-reconcile skipped the declaration, not just the command"
elif printf '%s' "$out" | grep -qF 'arg:[update]'; then
    bad native "--no-reconcile ran the command anyway: $out"
else
    pass native "--no-reconcile declares without running the command"
fi

# Restore the state the removal check below expects: the seeded package plus
# ours, applied once.
rm -rf "$PPROF"
mkdir -p "$PPROF"
printf '{"packages":["git:example.com/theirs"]}\n' > "$PPROF/settings.json"
quiet "$AP" manifest apply "$SANDBOX/native.yaml" --target pi

# Removal is bounded by the ELEMENT. A recorded container key would take every
# package in the list, and the user writes to this list by hand.
if ! quiet "$AP" uninstall pi:native native-plugin ponytail; then
    bad native "ap uninstall native-plugin failed"
elif grep -q 'git:example.com/ours' "$PPROF/settings.json"; then
    bad native "ours survived the removal"
elif ! grep -q 'git:example.com/theirs' "$PPROF/settings.json"; then
    bad native "the user's package was removed with ours"
else
    pass native "uninstall takes our element and leaves the user's"
fi

# A pipe is not consent. A manifest NAMED "default" reaches the real
# configuration and ap cannot undo a write there — and that field is decided by
# whoever wrote the manifest, which may not be you. Off a terminal it refuses,
# checked with stdinIsTerminal, never with the answer to a question nobody was
# asked. And NOTHING is created for the sentinel: no profile directory, no shim,
# no wrapper. Link especially must never run there, since the shared credential
# IS the file in that directory.
cat >"$SANDBOX/default.yaml" <<YAML
version: "1"
name: default
targets:
  - claude
skills:
  pdf:
    source:
      local:
        path: $SRC
YAML
rm -rf "$HOME/.claude/skills/pdf"
out=$(echo y | "$AP" manifest apply "$SANDBOX/default.yaml" --target claude 2>&1) && rc=0 || rc=1
# The refusal must be THE GATE's, not any other failure. A check that accepts
# a non-zero exit would stay green if the manifest simply stopped parsing.
if [ "$rc" = 0 ]; then
    bad gate "apply ran against the real config with an answer read off a pipe"
elif ! printf '%s' "$out" | grep -q 'no terminal to confirm on'; then
    bad gate "apply failed for some other reason than the gate: $out"
elif ! printf '%s' "$out" | grep -q "$HOME/.claude"; then
    bad gate "the refusal does not name the resolved absolute path: $out"
elif [ -e "$HOME/.claude/skills/pdf" ]; then
    bad gate "the skill was written into the real config despite the refusal"
elif [ -d "$HOME/.local/share/agent-profile/profiles/claude/default" ]; then
    bad gate "a profile directory was created for the sentinel"
else
    pass gate "a pipe is not consent, and nothing was created for default"
fi

    # An agent that rewrites its credential with temp-file-plus-rename leaves a
    # real file where ap's symlink was. Measured on two real claude profiles, so
    # this is reproduction, not hypothesis. `ap run` must heal it and keep going:
    # it used to abort, and the profile stayed unusable until someone moved the
    # file by hand. Asserted on all three of link, orphan and warning — healing
    # by deleting would satisfy the first two, and silence would satisfy all but
    # the third while a token the profile wrote vanished without a word.
    #
    # Feed the run a "1" on a pipe, which is the answer that would promote this
    # profile's credential over the shared one. It must be ignored, and the pipe is
    # what makes the check mean something: off a terminal ap must not prompt, so
    # nothing reads that byte. Closing stdin instead would prove nothing — an empty
    # answer means "keep" too, so the check would stay green with the terminal
    # guard deleted, which is the one thing it exists to catch.
    #
    # The property is not academic. dev.sh passes -it whenever there is a terminal,
    # so an unguarded prompt would hang this very script; and a user running
    # `echo … | ap run claude:x -p` has the agent's own prompt on stdin, which ap
    # must never eat.
    rm -f "$d/.credentials.json"
    echo '{"token":"overwritten-by-the-agent"}' >"$d/.credentials.json"
    shared_before=$(cat "$HOME/.claude/.credentials.json")
    out=$(printf '1\n' | "$AP" run claude:sbxrun -p 2>&1 || true)
    if [ ! -L "$d/.credentials.json" ]; then
        bad heal "ap run left the credential unshared: $out"
    elif [ ! -f "$d/.credentials.json.ap-orphan" ]; then
        bad heal "the overwritten credential was destroyed, not moved aside"
    elif ! printf '%s' "$out" | grep -q 'ap-orphan'; then
        bad heal "healing was silent - a token the profile wrote went missing unannounced"
    else
        pass heal "overwritten share relinked, the old file kept, and said so"
    fi
    # Nothing off a terminal may reach the real config directory. Compared by
    # content, not mtime: promoting writes the profile's bytes over these.
    if [ "$(cat "$HOME/.claude/.credentials.json")" != "$shared_before" ]; then
        bad heal "the shared credential was promoted over without anyone being asked"
    elif [ -e "$HOME/.claude/.credentials.json.ap-previous" ]; then
        bad heal "a promotion backup exists after a run that had nobody to ask"
    else
        pass heal "no terminal, no prompt, no write into the real config directory"
    fi
    rm -f "$d/.credentials.json.ap-orphan"

    quiet "$AP" delete --yes claude:sbxrun
else
    bad run "could not create claude:sbxrun"
fi

# --- the shim points into the profile and still passes everything else through
# Without the passthrough, XDG_CONFIG_HOME at the profile sends git, gh, npm and
# every language server looking for their config inside it. That is the bug the
# old blanket ban on XDG_CONFIG_HOME existed to prevent.
if quiet "$AP" create opencode:sbxshim; then
    d=$("$AP" which opencode:sbxshim)
    xdg=$("$AP" env opencode:sbxshim | sed -n 's/^XDG_CONFIG_HOME=//p')
    case "$xdg" in
    "$d"/*) ok=1 ;;
    *) ok=0 ;;
    esac
    if [ "$ok" -eq 0 ]; then
        bad shim "XDG_CONFIG_HOME=$xdg is outside the profile"
    elif [ "$(readlink -f "$xdg/opencode")" != "$(readlink -f "$d")" ]; then
        bad shim "<xdg>/opencode is not the profile - opencode would not be isolated"
    elif [ ! -f "$xdg/git/config" ] || [ ! -f "$xdg/nvim/init.lua" ]; then
        bad shim "the passthrough is missing - git and nvim would lose their config"
    else
        pass shim "XDG_CONFIG_HOME inside the profile, other programs still reach theirs"
    fi
    quiet "$AP" delete --yes opencode:sbxshim
else
    bad shim "could not create opencode:sbxshim"
fi

# The data shim, checked the same way as the config one. Sessions are the reason
# it exists: without it opencode writes opencode.db into the shared data dir and
# every profile sees every other profile's history.
mkdir -p "$HOME/.local/share"/{opencode,fonts,applications}
if quiet "$AP" create opencode:sbxdata; then
    d=$("$AP" which opencode:sbxdata)
    xdgdata=$("$AP" env opencode:sbxdata | sed -n 's/^XDG_DATA_HOME=//p')
    if [ -z "$xdgdata" ]; then
        bad datashim "opencode sets no XDG_DATA_HOME"
    elif [ "$xdgdata" != "$d/xdg-data" ]; then
        bad datashim "XDG_DATA_HOME=$xdgdata, want $d/xdg-data"
    elif [ "$(readlink -f "$xdgdata/opencode")" != "$(readlink -f "$d")" ]; then
        bad datashim "xdg-data/opencode does not resolve to the profile"
    elif [ ! -L "$xdgdata/fonts" ]; then
        bad datashim "no passthrough for fonts: every program reading XDG_DATA_HOME would be redirected into the profile"
    elif [ -d "$xdgdata/opencode" ] && [ -L "$xdgdata/applications" ]; then
        pass datashim "XDG_DATA_HOME shimmed with passthrough"
    else
        bad datashim "unexpected shim shape"
    fi
    quiet "$AP" delete --yes opencode:sbxdata
else
    bad datashim "could not create opencode:sbxdata"
fi

# --- sessions and resume: listing, resume argv and the chdir --------------
if quiet "$AP" create codex:sbxsess; then
    d=$("$AP" which codex:sbxsess)
    SID="019ef8e0-060c-7ef0-b878-63c558abbb23"
    SID2="019ef8e0-060c-7ef0-b878-63c558abbb24"
    mkdir -p "$d/sessions/2026/08/05" "$SANDBOX/work"
    printf '{"timestamp":"2026-08-05T10:00:00.000Z","type":"session_meta","payload":{"session_id":"%s","cwd":"%s"}}\n' \
        "$SID" "$SANDBOX/work" > "$d/sessions/2026/08/05/rollout-2026-08-05T10-00-00-$SID.jsonl"
    printf '{"timestamp":"2026-08-05T11:00:00.000Z","type":"session_meta","payload":{"session_id":"%s","cwd":"%s"}}\n' \
        "$SID2" "$SANDBOX/work" > "$d/sessions/2026/08/05/rollout-2026-08-05T11-00-00-$SID2.jsonl"

    out=$("$AP" sessions 2>&1 || true)
    if printf '%s' "$out" | grep -q "${SID:0:8}" && printf '%s' "$out" | grep -q "codex:sbxsess"; then
        pass sessions "ap sessions lists seeded session with profile"
    else
        bad sessions "ap sessions output missing seeded session: $out"
    fi

    out=$("$AP" sessions --max 1 2>&1 || true)
    rows=$(printf '%s' "$out" | grep -c "codex:sbxsess" || true)
    if [ "$rows" -eq 1 ]; then
        pass sessions "ap sessions --max 1 returned 1 row"
    else
        bad sessions "ap sessions --max 1 returned $rows rows"
    fi

    out=$("$AP" resume "$SID" 2>&1 || true)
    if printf '%s' "$out" | grep -qxF "arg:[resume]" && printf '%s' "$out" | grep -qxF "arg:[$SID]" && ! printf '%s' "$out" | grep -qxF 'arg:[{}]'; then
        pass resume "ap resume argv puts id at placeholder"
    else
        bad resume "ap resume argv missing resume or id: $out"
    fi

    out=$("$AP" resume "$SID" --model x 2>&1 || true)
    if printf '%s' "$out" | grep -qxF "arg:[--model]" && printf '%s' "$out" | grep -qxF "arg:[x]"; then
        pass resume "ap resume passes extra args through"
    else
        bad resume "ap resume extra args missing: $out"
    fi

    out=$( (cd /tmp && "$AP" resume "$SID") 2>&1 || true)
    if printf '%s' "$out" | grep -qxF "cwd:$SANDBOX/work"; then
        pass resume "ap resume chdirs to session directory before exec"
    else
        bad resume "ap resume did not chdir: $out"
    fi

    out=$(printf '1\n' | "$AP" resume 2>&1 || true)
    if printf '%s' "$out" | grep -q "argv:"; then
        bad resume "echo 1 | ap resume execed the agent off a pipe"
    else
        pass resume "echo 1 | ap resume did not exec off a pipe"
    fi

    SID_GONE="019ef8e0-060c-7ef0-b878-missingdir00"
    printf '{"timestamp":"2026-08-05T12:00:00.000Z","type":"session_meta","payload":{"session_id":"%s","cwd":"%s"}}\n' \
        "$SID_GONE" "$SANDBOX/missing_dir_for_test" > "$d/sessions/2026/08/05/rollout-2026-08-05T12-00-00-$SID_GONE.jsonl"
    out=$("$AP" resume "$SID_GONE" 2>&1 || true)
    if printf '%s' "$out" | grep -q "missing_dir_for_test" && ! printf '%s' "$out" | grep -q "argv:"; then
        pass resume "ap resume refuses missing directory and execs nothing"
    else
        bad resume "ap resume on missing directory failed check: $out"
    fi

    quiet "$AP" delete --yes codex:sbxsess
else
    bad sessions "could not create codex:sbxsess"
fi

echo
if [ $fail -eq 0 ]; then
    echo "all checks passed — this is ap's own side only; make smoke is still the registry's"
else
    echo "FAILURES"
fi
exit $fail
