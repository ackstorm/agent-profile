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

# Read the same way opencode and profile.ConfigBase do.
real_config=${XDG_CONFIG_HOME:-$HOME/.config}

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
  # Capture to a file, never a pipe. `opencode debug config` emits ~730 KB
  # unisolated but exits without waiting for the pipe to drain, so piping it into
  # grep loses everything past 64 KiB (one pipe buffer) and silently truncates the
  # JSON. Do not "simplify" this back into a pipeline.
  cfg="$d/debug-config.json"
  timeout 180 "$AP" run opencode:apsmoke debug config > "$cfg" 2>/dev/null
  if grep -q apsmoke "$cfg"; then
    pass opencode "profile agent loaded"
  else
    bad opencode "profile agent NOT loaded - check the config shim"
  fi
  # The config path must be the shim, not the real ~/.config/opencode. This is the
  # single assertion that opencode is isolated at all.
  if timeout 180 "$AP" run opencode:apsmoke debug paths 2>/dev/null \
       | grep -E '^config' | grep -qF "$d/xdg/opencode"; then
    pass opencode "config root is the profile, via the shim"
  else
    bad opencode "config root is NOT the profile - is XDG_CONFIG_HOME reaching opencode?"
  fi
  # And the global config must be gone. It used to be asserted PRESENT, because
  # opencode was additive; the shim is what changed that.
  gsize=$(wc -c < "$cfg")
  if [ "$gsize" -lt 100000 ]; then
    pass opencode "global config does NOT load ($gsize bytes resolved)"
  else
    bad opencode "resolved config is $gsize bytes - the global config is still loading"
  fi
  # The other half of the shim: every OTHER program must still find its own real
  # config, or setting a shared variable would have broken the whole process tree.
  if [ -d "$real_config/git" ]; then
    a=$(git config --list --global 2>/dev/null | wc -l)
    b=$(XDG_CONFIG_HOME="$d/xdg" git config --list --global 2>/dev/null | wc -l)
    if [ "$a" = "$b" ] && [ "$a" != 0 ]; then
      pass opencode "git still reads its own config through the shim ($a settings)"
    else
      bad opencode "git sees $b settings through the shim, $a outside - passthrough is broken"
    fi
  else
    skip opencode
  fi
else
  skip opencode
fi

# --- every variable set must point inside the profile -----------------------
# This replaced a blanket "no XDG_* at all". opencode has no private config
# variable, so isolating it means setting XDG_CONFIG_HOME; what is guaranteed now
# is that it points INTO the profile, and that the data, state and cache
# directories are never redirected, which is what keeps sessions shared.
# internal/run.TestEnvOnlySetsPathsInsideTheProfile is the unit-level version.
leak=0
for ag in $("$AP" agents | awk '{print $1}'); do
  d=$("$AP" which "$ag:apsmoke" 2>/dev/null) || continue
  while IFS='=' read -r k v; do
    [ -n "$k" ] || continue
    case "$k" in
      HOME | XDG_DATA_HOME | XDG_STATE_HOME | XDG_CACHE_HOME)
        bad "$ag" "ap env redirects $k, which must stay shared"
        leak=1
        ;;
    esac
    case "$v" in
      "$d" | "$d"/*) ;;
      *)
        bad "$ag" "ap env sets $k=$v, which is outside the profile"
        leak=1
        ;;
    esac
  done < <("$AP" env "$ag:apsmoke" 2>/dev/null)
done
[ $leak -eq 0 ] && pass env "every override points inside the profile; data and state untouched"

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
