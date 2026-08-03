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

echo
if [ $fail -eq 0 ]; then
    echo "all checks passed — this is ap's own side only; make smoke is still the registry's"
else
    echo "FAILURES"
fi
exit $fail
