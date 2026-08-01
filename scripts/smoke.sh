#!/usr/bin/env bash
# End-to-end check: do the real binaries honour what the registry claims?
#
# Runs inside Dockerfile.smoke, which has claude, codex, opencode and pi
# installed from npm, unpinned on purpose. It used to run on the host and needed
# all four installed and logged in there, which made the release gate something
# only one machine could run, and made it write to the real home to do it.
#
# Nothing of yours is mounted but the repo. HOME is a directory inside the image
# that this script fills itself, so the "shared state survives a delete"
# assertions have something real to lose — an empty home makes them vacuous, and
# vacuous is what this whole file exists to avoid.
#
# Credentials: none, measured rather than assumed.
#
#   * Nothing here needs a model call except the clone block, which was already a
#     warn because claude materialises plugins asynchronously. Set
#     ANTHROPIC_API_KEY to include it; without one it says so and moves on. It is
#     the only thing any key would buy, and it buys a warn.
#   * codex's "authenticated through the shared auth.json" looked like it needed
#     a login, and briefly did. It does not: `codex login status` reads that file
#     and masks what it finds, without calling anything to validate it, so a
#     synthesised auth.json answers the question — which is whether the profile
#     reaches the file through ap's symlink, not whether the token would work.
#     (`printenv OPENAI_API_KEY | codex login --with-api-key` also works, and is
#     what this used to do. `--api-key` was removed upstream, which is the kind
#     of drift this script exists to catch.)
#   * claude's equivalent check has no such answer and is off: a seeded fake
#     cannot tell "the link is broken" from "the token is not valid", and with
#     ANTHROPIC_API_KEY set claude never reads the credential at all, so the
#     check would pass with the link severed. It prints a skip saying so.
#
# Every wait is bounded by `timeout` with an explicit failure path.
#
# Usage: ./scripts/smoke.sh      (or `make smoke`, which builds what it needs)
set -u

# Re-enter the smoke image. One variable is forwarded and it is optional: bare
# `-e VAR` passes it only when it is actually set, so an absent key stays absent
# instead of arriving as an empty string that claude would read as a
# present-but-invalid one. The repo is the only mount.
if [ "${AP_IN_SMOKE:-0}" != "1" ]; then
    root=$(cd "$(dirname "$0")/.." && pwd)
    image=${AP_SMOKE_IMAGE:-agent-profile-smoke:latest}
    docker image inspect "$image" >/dev/null 2>&1 || {
        echo "smoke: building $image (first run)" >&2
        docker build -q -t "$image" -f "$root/Dockerfile.smoke" "$root" >/dev/null || exit 1
    }
    [ -x "$root/.gocache/smoke/ap" ] || {
        echo "no linux ap binary at .gocache/smoke/ap - run: make smoke" >&2
        exit 1
    }
    exec docker run --rm \
        --user "$(id -u):$(id -g)" \
        -v "$root:/workspace" \
        -e AP_IN_SMOKE=1 \
        -e HOME=/home/smoke/run \
        -e ANTHROPIC_API_KEY \
        -w /workspace \
        "$image" ./scripts/smoke.sh "$@"
fi

# Built for linux by `make smoke`, because the host binary may not be one.
AP=${AP:-/workspace/.gocache/smoke/ap}
[ -x "$AP" ] || { echo "no ap binary at $AP - run: make smoke" >&2; exit 1; }
# Absolute, once, here. The plugin block runs its agent calls from a neutral
# empty directory (see the comment above it), and a relative ./ap resolves
# against *that* directory, where there is no binary: the command never runs, the
# assertion that follows fails, and its message blames the thing it was testing.
# Six checks failed that way, pointing at a cwd leak that was not happening.
AP=$(cd "$(dirname "$AP")" && pwd)/$(basename "$AP")

# setup runs a command whose success is a precondition, not an assertion. Output
# is noise, but the exit status is not: silencing both is what let the failure
# above masquerade as six unrelated ones. Callers check the return value.
setup() { "$@" >/dev/null 2>&1; }

# The agent names, from the machine format rather than the human one.
#
# This used to read the human listing - `sed -n 's/^\([^ :][^:]*\):.*/\1/p'`
# over the lines that begin in column 0 - and that coupling broke twice. The
# plain `cut -d: -f1` before it turned "  review:opus  --dangerously..." into an
# agent named "review", so `command -v review` skipped and `ap which
# review:apsmoke` failed, in two blocks that test neither. `--raw` exists so
# nothing has to parse the format meant for reading: field 1 is the reference,
# and every line has one. TestListRawIsWhatScriptsSmokeParses pins it.
agents() { "$AP" list --raw | cut -f1 | cut -d: -f1 | sort -u; }

# Read the same way opencode and profile.ConfigBase do.
real_config=${XDG_CONFIG_HOME:-$HOME/.config}

fail=0
pass() { printf '  \033[32mOK\033[0m   %-9s %s\n' "$1" "$2"; }
bad()  { printf '  \033[31mFAIL\033[0m %-9s %s\n' "$1" "$2"; fail=1; }
skip() { printf '  --   %-9s %s\n' "$1" "${2:-not installed}"; }
# warn is for an observation about someone else's asynchronous behaviour: worth
# printing, never worth failing a release on. Only one check uses it, and its
# comment says why. Reach for bad() unless you can show the thing being observed
# is not deterministic.
warn() { printf '  \033[33mWARN\033[0m %-9s %s\n' "$1" "$2"; }

# --- a home that has been used ----------------------------------------------
# Fresh every run, inside the image. Two things depend on it holding real
# content: the sharing assertions need something to share, and the final
# "delete touched nothing" assertion needs something that could be lost. Against
# an empty home both are green no matter what the code does.
mkdir -p "$HOME"/.claude/projects "$HOME"/.codex/sessions "$HOME"/.pi/agent/sessions \
    "$HOME"/.config/opencode "$HOME"/.config/git "$HOME"/.config/nvim
printf '{"hasCompletedOnboarding":true}' >"$HOME/.claude.json"
printf '{"theme":"dark"}' >"$HOME/.claude/settings.json"
printf '{}' >"$HOME/.pi/agent/auth.json"
# Real keys, not just the section header: the shim's passthrough check compares
# how many settings git sees through the shim against how many it sees outside,
# and refuses to pass when both are zero. A bare "[user]" makes both zero, which
# is a check that cannot fail dressed up as one that passed.
printf '[user]\n\tname = smoke\n\temail = smoke@example.invalid\n[init]\n\tdefaultBranch = main\n' \
    >"$HOME/.config/git/config"
printf 'vim.opt.number = true\n' >"$HOME/.config/nvim/init.lua"
for p in alpha beta gamma; do
    mkdir -p "$HOME/.claude/projects/-smoke-$p"
    printf '{"session":"%s"}' "$p" >"$HOME/.claude/projects/-smoke-$p/transcript.jsonl"
done
printf '{"session":"one"}' >"$HOME/.codex/sessions/one.jsonl"

# claude's credential, synthesised. The shape came from reading back the FIELD
# NAMES of a real one - never a value - the same way codex's did.
#
# This looked unusable at first, on the reasoning that "not logged in" cannot
# tell a broken link from a bad token. Measured, claude says two different
# things, and the difference is exactly the one the check needs:
#
#   no credential reachable   -> "Not logged in · Please run /login"
#   credential read, rejected -> "Failed to authenticate: OAuth session expired
#                                 and could not be refreshed"
#
# The second is proof the file was opened, which is the whole assertion: that the
# profile reaches the credential through the link ap made. Whether the token is
# valid is Anthropic's business, not ap's - the same line drawn for codex.
#
# expiresAt is in the future so the message is about the token being rejected
# rather than about it being stale before it was ever sent.
printf '{"claudeAiOauth":{"accessToken":"sk-ant-oat01-notarealtoken","refreshToken":"sk-ant-ort01-notarealtoken","expiresAt":%s,"refreshTokenExpiresAt":%s,"scopes":["user:inference","user:profile"],"subscriptionType":"max","rateLimitTier":"default"}}' \
    "$(((  $(date +%s) + 86400 ) * 1000))" "$(((  $(date +%s) + 8640000 ) * 1000))" \
    >"$HOME/.claude/.credentials.json"

# codex's auth.json, synthesised rather than obtained. Measured: `codex login
# status` reads this file and masks what it finds - it does not call anything to
# validate it, so a key-shaped string that is not a key reports
# "Logged in using an API key" exactly as a real one does.
#
# That is enough, because of what the check downstream actually asserts: that a
# profile reports logged in ONLY by reaching this file through the symlink ap
# made. Whether the token would work is OpenAI's business, not ap's.
#
# So this whole script needs no credential of any kind. It briefly required
# OPENAI_API_KEY - `printenv OPENAI_API_KEY | codex login --with-api-key`, which
# also works - and that was a secret demanded for something a literal could do.
# The shape came from running the real login once and reading back the field
# names; if codex ever starts validating, this check goes red, which is the
# correct outcome and the reason smoke exists.
printf '{\n  "auth_mode": "apikey",\n  "OPENAI_API_KEY": "sk-notarealkey%s"\n}\n' \
    "$(printf '0%.0s' $(seq 1 40))" >"$HOME/.codex/auth.json"

echo "agent-profile smoke check"
echo "  agents: claude $(claude --version 2>/dev/null | awk '{print $1}') | codex $(codex --version 2>/dev/null | awk '{print $NF}') | pi $(pi --version 2>/dev/null | head -1) | opencode $(opencode --version 2>/dev/null | head -1)"

# --- claude: a profile-only settings.json must change the resolved model ------
if command -v claude >/dev/null 2>&1; then
  "$AP" delete --yes claude:apsmoke >/dev/null 2>&1
  "$AP" create claude:apsmoke >/dev/null 2>&1
  d=$("$AP" which claude:apsmoke)
  printf '{"model":"haiku"}' > "$d/settings.json"
  # BEFORE the agent runs, and that ordering is load-bearing. Measured: given a
  # credential it cannot refresh, claude REPLACES the file with a real one of its
  # own, which is precisely why Link re-asserts the symlink on every `ap run` (see
  # its doc comment). Asserted after the run, as it was, this goes red because
  # claude did its job, not because ap failed to do its.
  if [ ! -L "$d/.credentials.json" ]; then
    bad claude ".credentials.json is not a symlink - the profile is not sharing auth"
  else
    pass claude "the credential is shared"
  fi
  # The exit status is deliberately NOT part of the next assertion. Measured: with
  # no credential claude exits 1 with "Not logged in", and the debug log has
  # already recorded the model it resolved from the profile's settings.json -
  # resolution happens before authentication. Requiring exit 0, as this used to,
  # made a block about CLAUDE_CONFIG_DIR depend on a working login.
  #
  # stdout and stderr are captured, not discarded: the authentication message
  # goes THERE and never reaches --debug-file. Grepping the debug log for it, as
  # this used to, is a check that cannot fail - measured with the credential link
  # severed, and it stayed green.
  timeout 180 "$AP" run claude:apsmoke -p --debug-file "$d/dbg.log" "reply with ok" \
    >"$d/out.log" 2>&1
  if grep -q "claude-haiku" "$d/dbg.log" 2>/dev/null; then
    pass claude "profile settings.json applied"
  else
    bad claude "profile settings.json NOT applied - check CLAUDE_CONFIG_DIR"
  fi
  # REACHED, not accepted. Measured, the two answers differ exactly where it
  # matters: a profile that cannot get to the credential says "Not logged in ·
  # Please run /login", and one that read it and had it rejected says "Failed to
  # authenticate: OAuth session expired". Only the first is a broken link, and
  # only the first is ap's business - the same line drawn for codex.
  #
  # ANTHROPIC_API_KEY makes claude authenticate without opening the credential at
  # all, so with one set this would pass with the link severed. Skipped there.
  if [ -n "${ANTHROPIC_API_KEY:-}" ]; then
    skip claude "ANTHROPIC_API_KEY bypasses the credential, so this cannot observe it"
  elif grep -qi "not logged in\|please run /login" "$d/out.log" 2>/dev/null; then
    bad claude "profile never reached the credential - .credentials.json link broken"
  else
    pass claude "the credential was reached through the shared link"
  fi
  stray=""
  for p in .claude.json CLAUDE.md projects plugins/cache; do
    [ -L "$d/$p" ] && stray="$stray $p"
  done
  if [ -n "$stray" ]; then
    bad claude "still shared:$stray"
  else
    pass claude "config and history are the profile's own"
  fi
else
  skip claude
fi

# --- codex: the profile dir must be reported as codex_home -------------------
if command -v codex >/dev/null 2>&1; then
  "$AP" delete --yes codex:apsmoke >/dev/null 2>&1
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
  # The one authentication check the image can answer. The seed wrote a real
  # auth.json into the home (synthetic, see there), and the profile can only
  # report "logged in" by reaching it through the link ap made.
  #
  # ANCHORED. This was `grep -qi "logged in"`, and "Not logged in" contains
  # "logged in", so it matched both answers: the check passed whether or not the
  # link existed, on the host too, for as long as it has been here. Found by
  # reverting the guard - Link mutated to skip codex's Shared entry, profile
  # therefore empty, and the check stayed green.
  #
  # Measured, both branches: "Logged in using an API key - sk-xxx***yyy" and
  # "Not logged in".
  if timeout 120 "$AP" run codex:apsmoke login status 2>&1 | grep -qiE '^ *logged in'; then
    pass codex "authenticated via the shared auth.json link"
  else
    bad codex "not logged in - auth.json link broken"
  fi
else
  skip codex
fi

# --- pi: an empty profile must report no packages, unlike the real home ------
if command -v pi >/dev/null 2>&1; then
  "$AP" delete --yes pi:apsmoke >/dev/null 2>&1
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
  "$AP" delete --yes opencode:apsmoke >/dev/null 2>&1
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

# --- default: <agent>:default is the real config dir, undeletable, and
#     --from default clones configuration only, none of the runtime ----------
for ag in $(agents); do
  command -v "$ag" >/dev/null 2>&1 || { skip "$ag"; continue; }
  # marker is one file CloneAllow actually names for this agent, checked only
  # when present in the real config - proof that --from default clones
  # something, not just that it leaves runtime behind. runtime is real state
  # CloneAllow never names, including opencode's node_modules (62 MB on the
  # reference machine): an empty list here would make the leak check below
  # vacuous, always passing whether or not the exclusion actually works.
  case "$ag" in
    claude) real="$HOME/.claude"; runtime="projects tmp telemetry plugins/cache"; marker="settings.json" ;;
    codex) real="$HOME/.codex"; runtime="sessions history.jsonl plugins/cache"; marker="config.toml" ;;
    pi) real="$HOME/.pi/agent"; runtime="sessions"; marker="settings.json" ;;
    opencode) real="$real_config/opencode"; runtime="node_modules"; marker="opencode.json" ;;
    *)
      bad "$ag" "smoke.sh does not know this agent's real config dir - add a case above"
      continue
      ;;
  esac

  got=$("$AP" which "$ag:default" 2>/dev/null)
  if [ "$got" = "$real" ]; then
    pass "$ag" "default resolves to the real config dir"
  else
    bad "$ag" "default resolves to $got, want $real"
  fi

  if "$AP" delete --yes "$ag:default" >/dev/null 2>&1; then
    bad "$ag" "ap delete $ag:default SUCCEEDED - it must always refuse"
  else
    pass "$ag" "ap delete $ag:default refused, as it must"
  fi

  if [ ! -d "$real" ]; then
    skip "$ag"
    continue
  fi
  "$AP" delete --yes "$ag:apsmokedefault" >/dev/null 2>&1
  if "$AP" create "$ag:apsmokedefault" --from default >/dev/null 2>&1; then
    clonedir=$("$AP" which "$ag:apsmokedefault")

    if [ -e "$real/$marker" ]; then
      if [ -e "$clonedir/$marker" ]; then
        pass "$ag" "--from default cloned $marker"
      else
        bad "$ag" "--from default did NOT clone $marker, though it exists in the real config"
      fi
    fi

    leaked=""
    for r in $runtime; do
      [ -e "$clonedir/$r" ] && leaked="$leaked $r"
    done
    if [ -n "$leaked" ]; then
      bad "$ag" "--from default cloned runtime state:$leaked"
    else
      pass "$ag" "--from default cloned configuration only"
    fi
  else
    bad "$ag" "ap create --from default failed"
  fi
  "$AP" delete --yes "$ag:apsmokedefault" >/dev/null 2>&1
done

# --- what a clone carries: a plugin and its skill yes, an MCP server no ------
# Every agent call below runs from an EMPTY directory, never $HOME and never the
# repo. Claude reads <cwd>/.claude/settings.json as PROJECT settings, so running
# from $HOME makes the real ~/.claude/settings.json load as project config: the
# profile then appears to inherit plugins it never declared, and marketplaces
# clone into it out of nowhere. That false positive is very convincing. Keep the
# neutral cwd.
if command -v claude >/dev/null 2>&1; then
  neutral=$(mktemp -d)
  mkrepo=forrestchang/andrej-karpathy-skills # a plugin whose payload IS a skill,
  mk=karpathy-skills                         # so one install proves both
  plug=andrej-karpathy-skills
  skill=karpathy-guidelines

  for p in claude:apsmokeplug claude:apsmokeclone; do setup "$AP" delete --yes "$p"; done
  setup "$AP" create claude:apsmokeplug || bad plugin "ap create claude:apsmokeplug failed"
  od=$("$AP" which claude:apsmokeplug)

  # Preconditions, so a command that could not run says so instead of leaving the
  # assertions below to report an empty profile as a leak or a broken registry row.
  if ! (cd "$neutral" && setup timeout 180 "$AP" run claude:apsmokeplug plugin marketplace add "$mkrepo"); then
    bad plugin "plugin marketplace add $mkrepo failed - re-run it by hand to see why"
  fi
  if ! (cd "$neutral" && setup timeout 180 "$AP" run claude:apsmokeplug plugin install "$plug@$mk"); then
    bad plugin "plugin install $plug@$mk failed - re-run it by hand to see why"
  fi

  # Scope must be "user", i.e. the profile's own settings.json. A "project"
  # scope here means the cwd leaked in and every assertion below is worthless.
  if (cd "$neutral" && timeout 120 "$AP" run claude:apsmokeplug plugin list 2>&1) |
    grep -A2 -F "$plug@$mk" | grep -q "Scope: user"; then
    pass plugin "installed into the profile at user scope"
  else
    bad plugin "$plug@$mk not installed at user scope - did the cwd leak in?"
  fi

  # plugins/cache, not plugins. The skill file exists in TWO places: inside the
  # cloned marketplace repo at plugins/marketplaces/<mk>/skills/, which is there
  # as soon as the marketplace is cloned whether or not anything is installed,
  # and at plugins/cache/<mk>/<plug>/<version>/skills/, which only exists once
  # the plugin is actually installed. Searching plugins/ matched the first and
  # so passed without proving the install - it read as "the skill is available"
  # while asserting "a git clone happened".
  if [ -n "$(find "$od/plugins/cache" -name SKILL.md -path "*$skill*" -print -quit 2>/dev/null)" ]; then
    pass plugin "the plugin's skill is on disk in the profile"
  else
    bad plugin "no $skill SKILL.md under $od/plugins"
  fi

  # An MCP server at user scope lands in the profile's .claude.json.
  mcp_ok=0
  if command -v npx >/dev/null 2>&1; then
    (cd "$neutral" && timeout 60 "$AP" run claude:apsmokeplug mcp add apsmokemcp --scope user \
      -- npx -y @modelcontextprotocol/server-filesystem /tmp) >/dev/null 2>&1
    if (cd "$neutral" && timeout 180 "$AP" run claude:apsmokeplug mcp list 2>&1) | grep -q apsmokemcp; then
      pass mcp "the profile's own MCP server is configured and reachable"
      mcp_ok=1
    else
      bad mcp "apsmokemcp not listed - MCP at user scope did not reach the profile"
    fi
  else
    skip mcp
  fi

  # --- and now the clone ---
  if "$AP" create claude:apsmokeclone --from apsmokeplug >/dev/null 2>&1; then
    cd2=$("$AP" which claude:apsmokeclone)

    if grep -q "$plug@$mk" "$cd2/settings.json" 2>/dev/null; then
      pass clone "the plugin declaration was cloned"
    else
      bad clone "settings.json in the clone does not declare $plug@$mk"
    fi

    # Everything past this point observes CLAUDE, not ap, and it is deliberately
    # non-fatal. What ap owes the clone is the declaration above, and that is
    # deterministic. Turning it into a working install is claude's asynchronous
    # business, and it is not reliable enough to gate a release on.
    #
    # Measured on claude v2.1.220, from a cold clone whose settings.json declares
    # two marketplaces:
    #
    #   extraKnownMarketplaces: claude-plugins-official, karpathy-skills
    #   known_marketplaces.json after reconciliation: claude-plugins-official only
    #
    # The official one materialises immediately - the debug log shows a
    # pre-resolved git SHA for it. A third-party marketplace declared in exactly
    # the same shape sometimes registers and sometimes does not: it took 3 session
    # starts once, 5 the next, and did not happen at all within 4 starts plus 80s
    # of wall clock on two consecutive runs. Until then the session log says
    #
    #   Skipping orphaned enabledPlugins entry <plug>@<mk>: marketplace not registered
    #
    # so `enabledPlugins` in a cloned settings.json is a declaration claude may or
    # may not act on. It cannot be asserted, and a red smoke run caused by someone
    # else's background timing teaches people to ignore red.
    #
    # Do not "fix" this by cloning plugins/known_marketplaces.json: it records an
    # absolute installLocation inside the SOURCE profile, so a clone carrying it
    # would point at another profile's directory. It is state, and state is not
    # cloned. Bounded retry, explicit outcome either way, never an open poll.
    #
    # The one block in this file that needs a real model call: what is being
    # observed is work claude does during a session, so a session has to happen.
    # Without a key it is announced and skipped rather than reported either way.
    reconciled=0
    attempt=0
    if [ -z "${ANTHROPIC_API_KEY:-}" ]; then
      reconciled=skip
    fi
    if [ "$reconciled" = skip ]; then
      skip clone "materialising needs a session, so it needs ANTHROPIC_API_KEY"
    else
      for attempt in 1 2 3 4; do
        (cd "$neutral" && timeout 300 "$AP" run claude:apsmokeclone -p "say ok") >/dev/null 2>&1
        sleep 20
        if (cd "$neutral" && timeout 120 "$AP" run claude:apsmokeclone plugin list 2>&1) | grep -q -F "$plug@$mk"; then
          reconciled=1
          break
        fi
      done
      if [ "$reconciled" = 1 ]; then
        pass clone "session start(s) materialised the declared plugin ($attempt attempt(s))"
      else
        warn clone "claude had not registered $mk after 4 starts and 80s - declaration is cloned, materialising it is claude's own background work"
      fi
    fi
    # plugins/cache for the same reason as the origin's check above: under
    # plugins/ this passed on the marketplace clone alone, reporting a skill the
    # clone could not actually load. It went green in the same run whose WARN
    # said nothing had reconciled.
    if [ "$reconciled" = skip ]; then
      skip clone "nothing could reconcile, so the skill cannot be looked for"
    elif [ -n "$(find "$cd2/plugins/cache" -name SKILL.md -path "*$skill*" -print -quit 2>/dev/null)" ]; then
      pass clone "the plugin's skill came with it"
    elif [ "$reconciled" = 1 ]; then
      # Reconciliation DID happen, so the skill missing afterwards is a real
      # defect and not the timing caveat above.
      bad clone "the plugin reconciled but no $skill SKILL.md landed in the clone"
    else
      warn clone "no $skill SKILL.md yet - nothing reconciled, see above"
    fi

    # Documented limitation, asserted so it cannot change silently: MCP servers
    # live in .claude.json, which is history, trust state and counters as much as
    # config, so the clone allowlist does not name it. If this ever starts
    # passing, the allowlist changed and this expectation is the thing to update.
    if [ "$mcp_ok" = 1 ]; then
      if (cd "$neutral" && timeout 180 "$AP" run claude:apsmokeclone mcp list 2>&1) | grep -q apsmokemcp; then
        bad clone "the MCP server WAS cloned - .claude.json is in the allowlist now, update this check"
      else
        pass clone "MCP servers are not cloned, as documented (they live in .claude.json)"
      fi
    fi
  else
    bad clone "ap create --from apsmokeplug failed"
  fi

  for p in claude:apsmokeplug claude:apsmokeclone; do "$AP" delete --yes "$p" >/dev/null 2>&1; done
  rm -rf "$neutral"
else
  skip plugin
fi

# --- first run: create seeds the onboarding flag, and only that key ----------
# Without it a new profile opens on claude's theme picker even though the shared
# credential has it logged in. Checked on the file rather than by driving the
# wizard: the wizard needs a pty and 25s, this needs neither.
if command -v claude >/dev/null 2>&1 && [ -f "$HOME/.claude.json" ]; then
  "$AP" delete --yes claude:apsmokeseed >/dev/null 2>&1
  "$AP" create claude:apsmokeseed >/dev/null 2>&1
  seeded="$("$AP" which claude:apsmokeseed)/.claude.json"
  if grep -q '"hasCompletedOnboarding"' "$seeded" 2>/dev/null; then
    pass firstrun "the onboarding flag was seeded"
  else
    bad firstrun "a new profile would open on the theme picker"
  fi
  # The rest of that file is prompt history, per-project trust and machine
  # identity. Seeding it wholesale would put all of it in every profile.
  if grep -q '"projects"\|"userID"\|"oauthAccount"' "$seeded" 2>/dev/null; then
    bad firstrun "the seed carried more than the first-run flags"
  else
    pass firstrun "and nothing else from ~/.claude.json"
  fi
  "$AP" delete --yes claude:apsmokeseed >/dev/null 2>&1
else
  skip firstrun
fi

# --- link: create writes a wrapper that is executable and reaches the profile
# `ap create` links; no `ap link` step here on purpose, because that is the
# behaviour under test.
if command -v claude >/dev/null 2>&1; then
  linkdir=$(mktemp -d)
  AP_LINK_DIR="$linkdir" "$AP" delete --yes claude:apsmokelink >/dev/null 2>&1
  if AP_LINK_DIR="$linkdir" "$AP" create claude:apsmokelink >/dev/null 2>&1; then
    w="$linkdir/claude:apsmokelink"
    apdir=$(dirname "$AP") # AP is absolute; see the top of this file
    if [ -x "$w" ] && grep -q 'exec ap run claude:apsmokelink' "$w" &&
      timeout 60 env PATH="$apdir:$PATH" "$w" --version >/dev/null 2>&1; then
      pass link "ap create wrote a wrapper that reaches the profile"
    else
      bad link "wrapper did not run claude through the profile"
    fi
  else
    bad link "ap create failed"
  fi
  AP_LINK_DIR="$linkdir" "$AP" delete --yes claude:apsmokelink >/dev/null 2>&1
  if [ -e "$linkdir/claude:apsmokelink" ]; then
    bad link "the wrapper outlived ap delete"
  else
    pass link "ap delete removed the wrapper"
  fi
  rm -rf "$linkdir"
else
  skip claude
fi

# --- variant: the stored arguments actually reach the binary ----------------
# -p is the observable one: with it claude prints an answer and exits, without
# it it opens an interactive session and the timeout kills it. Nothing else in
# the suite proves the store is read at all - every Go test stops at the
# []string.
#
# Its own profile, like every other block that needs one (apsmokeplug,
# apsmokeclone, apsmokeseed, apsmokelink). claude:apsmoke is created once at the
# top and read by the env loop and the delete block at the bottom, so a block
# that ends by deleting it would take the fixture out from under both - and
# `ap create` on it would fail first anyway, because it already exists.
if command -v claude >/dev/null 2>&1; then
  vlink=$(mktemp -d)
  vstore="${XDG_DATA_HOME:-$HOME/.local/share}/agent-profile/variants/claude/apsmokevar"
  setup env AP_LINK_DIR="$vlink" "$AP" delete --yes claude:apsmokevar
  if setup env AP_LINK_DIR="$vlink" "$AP" create claude:apsmokevar &&
    setup env AP_LINK_DIR="$vlink" "$AP" variant claude:apsmokevar:apv -- -p --model haiku; then
    # Exit 124 is `timeout` killing it, and that is the whole observable: with the
    # stored -p reaching claude it answers or refuses and exits; without it, it
    # opens an interactive session and sits there until killed. The exit CODE is
    # not usable - with no credential claude exits 1, which says nothing about
    # whether the stored arguments arrived. Requiring exit 0, as this used to,
    # made the check need a working login.
    #
    # scripts/sandbox.sh asserts the same thing far more precisely, against a stub
    # that reports its own argv. This stays because it is the only place the REAL
    # binary is observed changing behaviour because of what the store held.
    rc=0
    timeout 60 "$AP" run claude:apsmokevar:apv "reply with ok" >/dev/null 2>&1 || rc=$?
    if [ "$rc" -ne 124 ]; then
      pass variant "stored arguments reached the agent"
    else
      bad variant "ap run <a>:<p>:<v> never returned - are the stored args reaching claude?"
    fi
    # The cascade, end to end: deleting the profile takes the variant with it.
    #
    # Stat the store and the wrapper, never `ap list | grep`. `ap list` shows
    # variants of profiles that EXIST, so once the profile is gone it prints
    # nothing about the variant whether or not the cascade ran - the grep this
    # replaced went green with the store entry and the wrapper both still on
    # disk, which is the vacuous check CLAUDE.md says is worse than none.
    setup env AP_LINK_DIR="$vlink" "$AP" delete --yes claude:apsmokevar
    if [ -e "$vstore" ]; then
      bad variant "the variant args outlived their profile at $vstore"
    elif [ -e "$vlink/claude:apsmokevar:apv" ]; then
      bad variant "the variant's wrapper outlived its profile"
    else
      pass variant "deleting the profile removed the variant and its wrapper"
    fi
  else
    bad variant "could not set up the variant fixture"
    setup env AP_LINK_DIR="$vlink" "$AP" delete --yes claude:apsmokevar
  fi
  rm -rf "$vlink"
else
  skip variant
fi

# --- every variable set must point inside the profile -----------------------
# This replaced a blanket "no XDG_* at all". opencode has no private config
# variable, so isolating it means setting XDG_CONFIG_HOME; what is guaranteed now
# is that it points INTO the profile, and that the data, state and cache
# directories are never redirected, which is what keeps sessions shared.
# internal/run.TestEnvOnlySetsPathsInsideTheProfile is the unit-level version.
leak=0
for ag in $(agents); do
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
# The transcript count alone is no longer enough, and had stopped being enough
# some time ago: projects/ and sessions/ moved to Unshared, so nothing in a
# profile points at them any more and counting them cannot go red. What a
# profile DOES link back to is the credential - for claude it is the only
# symlink a fresh profile has at all. Measured with Delete mutated to resolve
# links before removing: every credential below is destroyed and the transcript
# count does not move.
before=$(find "$HOME/.claude/projects" -mindepth 1 -maxdepth 1 2>/dev/null | wc -l)
for p in claude codex pi opencode; do "$AP" delete --yes "$p:apsmoke" >/dev/null 2>&1; done
after=$(find "$HOME/.claude/projects" -mindepth 1 -maxdepth 1 2>/dev/null | wc -l)
lost=""
for c in "$HOME/.claude/.credentials.json" "$HOME/.codex/auth.json" "$HOME/.pi/agent/auth.json"; do
  [ -e "$c" ] || lost="$lost ${c#"$HOME"/}"
done
if [ -n "$lost" ]; then
  bad delete "delete followed a link out of the profile and destroyed:$lost"
elif [ "$before" -eq "$after" ]; then
  pass delete "credentials and shared sessions intact ($after entries)"
else
  bad delete "session count changed $before -> $after"
fi

echo
[ $fail -eq 0 ] && echo "all checks passed" || echo "FAILURES - fix the registry row, not this script"
exit $fail
