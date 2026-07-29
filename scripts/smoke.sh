#!/usr/bin/env bash
# Manual end-to-end check: do the real binaries honour what the registry claims?
#
# Not part of `go test`: needs the agents installed and logged in. Every wait is
# bounded by `timeout` with an explicit failure path.
#
# Usage: ./scripts/smoke.sh        (expects ./ap built, or set AP=/path/to/ap)
set -u

AP=${AP:-./ap}
[ -x "$AP" ] || { echo "no ap binary at $AP - run: make build" >&2; exit 1; }

fail=0
pass() { printf '  \033[32mOK\033[0m   %-9s %s\n' "$1" "$2"; }
bad()  { printf '  \033[31mFAIL\033[0m %-9s %s\n' "$1" "$2"; fail=1; }
skip() { printf '  --   %-9s %s\n' "$1" "not installed"; }

echo "agent-profile smoke check"

# --- claude: a profile-only settings.json must change the resolved model ------
if command -v claude >/dev/null 2>&1; then
  "$AP" delete claude:apsmoke >/dev/null 2>&1
  "$AP" create claude:apsmoke >/dev/null 2>&1
  d=$("$AP" which claude:apsmoke)
  printf '{"model":"haiku"}' > "$d/settings.json"
  if timeout 180 "$AP" run claude:apsmoke -p --debug-file "$d/dbg.log" "reply with ok" \
       >/dev/null 2>&1 && grep -q "claude-haiku" "$d/dbg.log"; then
    pass claude "profile settings.json applied"
  else
    bad claude "profile settings.json NOT applied - check CLAUDE_CONFIG_DIR"
  fi
  # Credentials are shared, so the profile must not be asking for a login.
  if grep -qi "not logged in\|please run /login" "$d/dbg.log" 2>/dev/null; then
    bad claude "profile is not authenticated - .credentials.json link broken"
  else
    pass claude "authenticated via the shared credentials link"
  fi
else
  skip claude
fi

# --- codex: the profile dir must be reported as codex_home -------------------
if command -v codex >/dev/null 2>&1; then
  "$AP" delete codex:apsmoke >/dev/null 2>&1
  "$AP" create codex:apsmoke >/dev/null 2>&1
  d=$("$AP" which codex:apsmoke)
  # `codex doctor` pretty-prints paths: it collapses $HOME to ~ and elides the
  # middle with an ellipsis, so grepping the full path is a guaranteed false
  # negative. The trailing "<agent>/<profile>" survives the truncation.
  tail2="$(basename "$(dirname "$d")")/$(basename "$d")"
  if timeout 120 "$AP" run codex:apsmoke doctor 2>&1 | grep -qF "$tail2"; then
    pass codex "codex_home is the profile dir"
  else
    bad codex "codex_home NOT redirected - check CODEX_HOME"
  fi
  if timeout 120 "$AP" run codex:apsmoke login status 2>&1 | grep -qi "logged in"; then
    pass codex "authenticated via the shared auth.json link"
  else
    bad codex "not logged in - auth.json link broken"
  fi
else
  skip codex
fi

# --- pi: an empty profile must report no packages, unlike the real home ------
if command -v pi >/dev/null 2>&1; then
  "$AP" delete pi:apsmoke >/dev/null 2>&1
  "$AP" create pi:apsmoke >/dev/null 2>&1
  if timeout 120 "$AP" run pi:apsmoke list 2>&1 | grep -qi "no packages installed"; then
    pass pi "profile package set is isolated"
  else
    bad pi "still reading the real package list - check PI_CODING_AGENT_DIR"
  fi
else
  skip pi
fi

# --- opencode: a profile-only agent must appear in the resolved config -------
if command -v opencode >/dev/null 2>&1; then
  "$AP" delete opencode:apsmoke >/dev/null 2>&1
  "$AP" create opencode:apsmoke >/dev/null 2>&1
  d=$("$AP" which opencode:apsmoke)
  mkdir -p "$d/agent"
  # shellcheck disable=SC2016  # $schema is a literal JSON key, not a shell variable
  printf '{"$schema":"https://opencode.ai/config.json"}' > "$d/opencode.json"
  printf -- '---\ndescription: ap smoke\nmode: primary\n---\nsmoke\n' > "$d/agent/apsmoke.md"
  # Capture to a file, never a pipe. `opencode debug config` emits ~730 KB but
  # exits without waiting for the pipe to drain, so piping it into grep loses
  # everything past 64 KiB (one pipe buffer) and silently truncates the JSON.
  # Do not "simplify" this back into a pipeline.
  cfg="$d/debug-config.json"
  timeout 180 "$AP" run opencode:apsmoke debug config > "$cfg" 2>/dev/null
  if grep -q apsmoke "$cfg"; then
    pass opencode "profile agent loaded"
  else
    bad opencode "profile agent NOT loaded - check OPENCODE_CONFIG_DIR"
  fi
  # Documents the additive behaviour rather than asserting isolation: opencode
  # cannot be isolated without XDG_CONFIG_HOME, which we refuse to set.
  if grep -q '"provider"' "$cfg"; then
    pass opencode "global config still loads (additive, as designed)"
  else
    bad opencode "global config vanished - is XDG_CONFIG_HOME being set?"
  fi
else
  skip opencode
fi

# --- no generic variable is ever set ----------------------------------------
leak=0
for ag in $("$AP" agents | awk '{print $1}'); do
  if "$AP" env "$ag:apsmoke" 2>/dev/null | grep -qE '^(XDG_|HOME=)'; then
    bad "$ag" "ap env sets a generic variable"
    leak=1
  fi
done
[ $leak -eq 0 ] && pass env "no XDG_* or HOME override in any agent"

# --- delete must not touch shared data --------------------------------------
before=$(find "$HOME/.claude/projects" -mindepth 1 -maxdepth 1 2>/dev/null | wc -l)
for p in claude codex pi opencode; do "$AP" delete "$p:apsmoke" >/dev/null 2>&1; done
after=$(find "$HOME/.claude/projects" -mindepth 1 -maxdepth 1 2>/dev/null | wc -l)
if [ "$before" -eq "$after" ]; then
  pass delete "shared sessions intact ($after entries)"
else
  bad delete "session count changed $before -> $after"
fi

echo
[ $fail -eq 0 ] && echo "all checks passed" || echo "FAILURES - fix the registry row, not this script"
exit $fail
