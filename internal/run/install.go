//go:build unix

package run

import (
	"path/filepath"
	"strings"

	"github.com/ackstorm/agent-profile/internal/agent"
)

// StripProfilePaths removes every variable in base whose value resolves inside
// root. It is the environment a manifest's `bootstrap` commands run under: the
// one ap inherited, with exactly this one subtraction.
//
// The subtraction exists because `ap sync` may be run from a shell that is
// already inside a profile — a CLAUDE_CONFIG_DIR, or a shimmed XDG_CONFIG_HOME
// — and a bootstrap command is supposed to write to the MACHINE. Without this,
// `npm install -g` under such a shell lands in whichever profile that shell
// happened to be in.
//
// The rule is "points inside the profile root", never "is one of the four
// config variables", and the difference is load-bearing. opencode's variable is
// XDG_CONFIG_HOME, which every freedesktop-following program reads: stripping it
// by name would break a perfectly legitimate XDG_CONFIG_HOME=~/myconfig for npm,
// cargo and everything else a bootstrap command might invoke. Stripping it by
// value removes exactly what ap itself could have set, and nothing else.
//
// root is a parameter rather than a call to profile.Root() so this package keeps
// importing only internal/agent. The caller already knows the answer.
func StripProfilePaths(root string, base []string) []string {
	out := make([]string, 0, len(base))
	for _, e := range base {
		if _, v, ok := strings.Cut(e, "="); ok && inside(root, v) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// InstallEnv is the environment one platform's `install` commands run under:
// the same one Env builds for the agent, shim and all, over a base that has
// already had every other profile's variables removed.
//
// Same one as the agent's, deliberately: a tool that populates a profile has to
// resolve the same config root the agent will resolve, or it writes somewhere
// the agent never reads.
//
// StripProfilePaths runs FIRST and Env second, so this agent's own overrides —
// which do point inside root, and must — are added after the removal rather than
// caught by it.
//
// dir == "" is `name: default`: no override at all, exactly as `ap run
// claude:default` behaves.
func InstallEnv(a agent.Agent, dir, root string, base []string) []string {
	return Env(a, dir, StripProfilePaths(root, base))
}

// inside reports whether p is root or lies under it.
//
// Compared twice, and each comparison is between two paths resolved the SAME
// way. A macOS-only defect has already shipped in this repository from
// comparing a resolved path against an unresolved one, which only diverges
// where /var is a symlink to /private/var. The cleaned comparison answers for
// paths that do not exist yet; the resolved one answers for a symlinked
// ancestor; mixing the two answers wrongly.
func inside(root, p string) bool {
	if root == "" || p == "" {
		return false
	}
	if under(filepath.Clean(root), filepath.Clean(p)) {
		return true
	}
	r, err1 := filepath.EvalSymlinks(root)
	q, err2 := filepath.EvalSymlinks(p)
	return err1 == nil && err2 == nil && under(r, q)
}

// under is a PATH prefix test, not a string one: /data/profiles-elsewhere is
// not inside /data/profiles.
func under(root, p string) bool {
	return p == root || strings.HasPrefix(p, root+string(filepath.Separator))
}
