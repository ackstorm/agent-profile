#!/usr/bin/env bash
#
# scripts/dev.sh — run a command inside the devtools container.
#
# The host needs docker and nothing else. Every Go/lint/release invocation goes
# through here; the Makefile wraps its own targets automatically, so you rarely
# call this by hand.
#
#   ./scripts/dev.sh go test ./...
#   ./scripts/dev.sh make test
#   ./scripts/dev.sh bash          # interactive shell in the container
#
# What it does:
#   * mounts the repo at /workspace, running as the host UID:GID so files it
#     writes (./ap, coverage.out, dist/) belong to you, not root
#   * persists the Go module and build caches under ./.gocache so a second
#     `make test` is fast
#   * refuses to nest: a target that re-invokes make inside the container runs
#     the command directly

set -euo pipefail

IMAGE="${AP_DEVTOOLS_IMAGE:-agent-profile-devtools:latest}"
WORKSPACE="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Already inside: run directly. This is also the escape hatch for CI, which has
# a real Go toolchain and sets AP_IN_DEVTOOLS=1 to skip the container entirely.
if [[ "${AP_IN_DEVTOOLS:-0}" == "1" ]]; then
    exec "${@:-bash}"
fi

if ! docker info >/dev/null 2>&1; then
    echo "scripts/dev.sh: docker daemon not reachable — is Docker running, and is your user in the docker group?" >&2
    exit 1
fi

# Pre-create the caches so docker does not mkdir them as root.
mkdir -p "${WORKSPACE}/.gocache/gopath" "${WORKSPACE}/.gocache/build"

# TTY only when there is one, so CI and pipes keep working.
TTY_ARGS=()
if [[ -t 0 && -t 1 ]]; then
    TTY_ARGS=(-it)
fi

if [[ "${AP_DEVTOOLS_REBUILD:-0}" == "1" ]] || ! docker image inspect "${IMAGE}" >/dev/null 2>&1; then
    echo "scripts/dev.sh: building ${IMAGE} (first run or rebuild requested)" >&2
    docker build -t "${IMAGE}" -f "${WORKSPACE}/Dockerfile.devtools" "${WORKSPACE}"
fi

if [[ $# -eq 0 ]]; then
    set -- bash
fi

exec docker run --rm "${TTY_ARGS[@]}" \
    --user "$(id -u):$(id -g)" \
    -v "${WORKSPACE}:/workspace" \
    -e AP_IN_DEVTOOLS=1 \
    -e HOME=/workspace/.gocache \
    -e GOPATH=/workspace/.gocache/gopath \
    -e GOCACHE=/workspace/.gocache/build \
    -e GOMODCACHE=/workspace/.gocache/gopath/pkg/mod \
    `# bare -e: forwarded only when actually set in the environment, so an` \
    `# unset token stays unset rather than becoming an empty string that` \
    `# goreleaser would misread as present.` \
    -e GITHUB_TOKEN \
    -w /workspace \
    "${IMAGE}" \
    "$@"
