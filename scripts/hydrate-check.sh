#!/usr/bin/env bash
# The hydrator image, exercised as ach-runtime will run it.
#
# The whole point of this check is the CONDITIONS, not the manifest: no terminal,
# no $HOME, a non-root user, and a root that exists only as a command-line
# argument. Every one of those has been an assumption in this program at some
# point, and a unit test cannot fail on any of them.
#
# The source is a git repository created here and mounted in, so the check needs
# no network and is deterministic — while still exercising git inside the image,
# which is the one dependency the image could ship broken.
set -euo pipefail

IMAGE="${AP_HYDRATE_IMAGE:-agent-profile-hydrate:latest}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WORK="$ROOT/.gocache/hydrate"
fail=0

pass() { printf '  \033[32mOK\033[0m   %-9s %s\n' "$1" "$2"; }
bad()  { printf '  \033[31mFAIL\033[0m %-9s %s\n' "$1" "$2"; fail=1; }

rm -rf "$WORK"
mkdir -p "$WORK/src/skills/pdf/scripts" "$WORK/config" "$WORK/manifest"
printf '# pdf\n'   > "$WORK/src/skills/pdf/SKILL.md"
printf 'print()\n' > "$WORK/src/skills/pdf/scripts/convert.py"

git -C "$WORK/src" init --quiet --initial-branch=main
git -C "$WORK/src" add -A
git -C "$WORK/src" \
    -c user.name=t -c user.email=t@e -c commit.gpgsign=false \
    commit --quiet -m seed

sed 's/^name: hydrated$/name: default/' > "$WORK/manifest/default.yaml" <<'YAML'
version: "1"
name: hydrated
targets:
  - claude
YAML

cat > "$WORK/manifest/agent-profile.yaml" <<'YAML'
version: "1"
name: hydrated
targets:
  - claude
inputs:
  secrets:
    memory-token:
      env: MEMORY_TOKEN
skills:
  pdf:
    source:
      git:
        url: /src
        ref: main
        subpath: skills/pdf
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
YAML

# claude's own expansion syntax, built rather than written, so no shell quoting
# rule has to be argued with.
ref="\${MEMORY_TOKEN}"

echo
echo "agent-profile hydrate check (headless, no HOME, root as a parameter)"

# --rm and no -t: no terminal, deliberately. --user is the PodSpec's, and the
# cache has to be somewhere writable that is not a home directory.
#
# The uid is the HOST's rather than the image's 65532, and that is this
# harness's problem, not ap's: the seeded repository is bind-mounted and owned
# by the host user, and git refuses a repository someone else owns. It refuses
# it from system and global config only, on purpose, so no environment variable
# can wave it through — and ap must not paper over a user's own git safety
# setting. A real source is a URL and never meets this. What the image itself
# runs as is asserted separately, below.
run() {
    docker run --rm \
        --user "$(id -u):$(id -g)" \
        -e HOME= \
        -e XDG_CACHE_HOME=/tmp/cache \
        -e MEMORY_TOKEN=unused-by-materialization \
        -v "$WORK/src:/src:ro" \
        -v "$WORK/manifest:/manifest:ro" \
        -v "$WORK/config:/config" \
        "$IMAGE" "$@"
}

# The image's own identity: an init container that defaults to root is one
# misconfigured PodSpec away from writing a volume as root.
uid=$(docker run --rm --entrypoint id "$IMAGE" -u 2>&1 || true)
if [ "$uid" = "0" ]; then
    bad image "the image runs as root by default"
elif ! printf '%s' "$uid" | grep -Eq '^[0-9]+$'; then
    bad image "could not read the image's default uid: $uid"
else
    pass image "runs as a non-root uid ($uid) by default"
fi

if out=$(run manifest apply /manifest/agent-profile.yaml --target claude --root /config 2>&1); then
    if [ ! -f "$WORK/config/skills/pdf/SKILL.md" ] ||
       [ ! -f "$WORK/config/skills/pdf/scripts/convert.py" ]; then
        bad hydrate "the skill was not materialized: $out"
    elif [ ! -f "$WORK/config/.claude.json" ]; then
        bad hydrate "the MCP server was not merged into the runtime's own file"
    elif ! grep -qF "$ref" "$WORK/config/.claude.json"; then
        # §34: a reference, never a value. The variable was in the container's
        # environment precisely so that writing its value would be possible.
        bad hydrate "the credential is not a reference: $(cat "$WORK/config/.claude.json")"
    elif [ ! -f "$WORK/config/.ap-ledger.json" ]; then
        bad hydrate "nothing was recorded in the ledger"
    else
        pass hydrate "materialized into an empty directory with no HOME"
    fi
else
    bad hydrate "ap manifest apply failed in the image: $out"
fi

# One directory holds ONE runtime's configuration. The manifest declares one
# target here, but --root with several must refuse rather than have them
# overwrite each other.
if out=$(run manifest apply /manifest/agent-profile.yaml --root /config 2>&1); then
    bad root "apply ran without naming a runtime"
elif printf '%s' "$out" | grep -q -- '--all-targets'; then
    pass root "the runtimes must be named, even for a one-target manifest"
else
    bad root "refused for some other reason: $out"
fi

# Off a terminal a pipe is not consent, and that must still hold where there is
# no terminal at all.
if out=$(echo y | run manifest apply /manifest/default.yaml --target claude 2>&1); then
    bad gate "the real-config gate passed with an answer read off a pipe"
elif printf '%s' "$out" | grep -q 'no terminal to confirm on'; then
    pass gate "no terminal, no consent"
else
    bad gate "failed for some other reason than the gate: $out"
fi

echo
if [ "$fail" = 0 ]; then
    echo "all checks passed — the image hydrates headlessly"
else
    echo "FAILURES"
    exit 1
fi
