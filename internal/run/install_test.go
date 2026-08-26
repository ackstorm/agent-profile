package run

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ackstorm/agent-profile/pkg/agentreg"
)

func has(env []string, kv string) bool {
	for _, e := range env {
		if e == kv {
			return true
		}
	}
	return false
}

func hasKey(env []string, key string) bool {
	for _, e := range env {
		if k, _, ok := strings.Cut(e, "="); ok && k == key {
			return true
		}
	}
	return false
}

// The bug this stops coming back: stripping by NAME would delete
// XDG_CONFIG_HOME for a claude install, because that string happens to be
// opencode's config variable — breaking npm, cargo and every generic tool the
// command shells out to.
func TestStripProfilePathsKeepsAConfigVariablePointingOutsideTheRoot(t *testing.T) {
	root := "/data/agent-profile/profiles"
	base := []string{
		"XDG_CONFIG_HOME=/home/me/myconfig",
		"CODEX_HOME=" + filepath.Join(root, "codex", "plan"),
		"PATH=/usr/bin",
	}
	got := StripProfilePaths(root, base)
	if !has(got, "XDG_CONFIG_HOME=/home/me/myconfig") {
		t.Error("XDG_CONFIG_HOME was stripped by NAME; the rule is by value")
	}
	if !has(got, "PATH=/usr/bin") {
		t.Error("PATH went missing")
	}
	if hasKey(got, "CODEX_HOME") {
		t.Error("CODEX_HOME points inside the profile root and must be removed")
	}
}

func TestStripProfilePathsRemovesTheRootItselfAndAnythingUnderIt(t *testing.T) {
	root := "/data/profiles"
	got := StripProfilePaths(root, []string{
		"A=/data/profiles",
		"B=/data/profiles/claude/x",
		"C=/data/profiles-elsewhere", // a prefix of the string, NOT inside the root
		"D=/data",
	})
	if hasKey(got, "A") || hasKey(got, "B") {
		t.Error("the root itself and a path under it must both be removed")
	}
	if !hasKey(got, "C") {
		t.Error("/data/profiles-elsewhere is not inside /data/profiles; a string prefix is not a path prefix")
	}
	if !hasKey(got, "D") {
		t.Error("an ancestor of the root is not inside it")
	}
}

// A claude install must not be redirected by an inherited CODEX_HOME, and must
// still see its own variable pointed at the profile.
func TestInstallEnvSetsOnlyItsOwnAgentVariable(t *testing.T) {
	a, _ := agentreg.Lookup("claude")
	root := "/data/profiles"
	dir := filepath.Join(root, "claude", "execute")
	got := InstallEnv(a, dir, root, []string{
		"CODEX_HOME=" + filepath.Join(root, "codex", "execute"),
		"PATH=/usr/bin",
	})
	if !has(got, "CLAUDE_CONFIG_DIR="+dir) {
		t.Errorf("CLAUDE_CONFIG_DIR is not the profile: %q", got)
	}
	if hasKey(got, "CODEX_HOME") {
		t.Error("an inherited CODEX_HOME survived into a claude install")
	}
}

// name: default runs with no override at all, exactly as `ap run
// claude:default` does — the environment the agent sees when ap is not
// involved, which is the entire point of naming it.
func TestInstallEnvForDefaultSetsNoOverride(t *testing.T) {
	a, _ := agentreg.Lookup("claude")
	got := InstallEnv(a, "", "/data/profiles", []string{
		"CLAUDE_CONFIG_DIR=/data/profiles/claude/other",
		"PATH=/usr/bin",
	})
	if hasKey(got, "CLAUDE_CONFIG_DIR") {
		t.Errorf("a default install must set and inherit no CLAUDE_CONFIG_DIR: %q", got)
	}
}

// opencode's install has to see the shim, or a tool that populates the profile
// writes where the agent never reads.
func TestInstallEnvGivesOpencodeItsShims(t *testing.T) {
	a, _ := agentreg.Lookup("opencode")
	root := "/data/profiles"
	dir := filepath.Join(root, "opencode", "execute")
	got := InstallEnv(a, dir, root, []string{"PATH=/usr/bin"})
	if !has(got, "XDG_CONFIG_HOME="+filepath.Join(dir, "xdg")) {
		t.Errorf("opencode install does not see the config shim: %q", got)
	}
	if !has(got, "XDG_DATA_HOME="+filepath.Join(dir, "xdg-data")) {
		t.Errorf("opencode install does not see the data shim: %q", got)
	}
}
