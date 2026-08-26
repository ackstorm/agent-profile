package agentreg

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Default names the agent's real, machine-wide configuration — the one it uses
// when ap is not involved. It is a sentinel, not a directory: nothing is ever
// created for it, Link never runs against it, no shim is built.
//
// It exists so `ap run codex:default` is a way to reach your normal setup
// through the same command as everything else, and so
// `ap create codex:plan --from default` can start a profile from the
// configuration you already have.
//
// Read-only, always. Dir resolves it to the real config directory, which is
// exactly why nothing that writes may accept it: `ap delete codex:default`
// would otherwise remove the configuration of the agent itself.
const Default = "default"

// ValidName rejects anything that could escape Root or collide with dotfiles.
//
// Exported because every caller that turns user input into a path under Root
// must run it — not just ParseRef. `ap create --from <name>` skipped it once,
// and `--from ../../../.claude` then copied files out of the real home.
func ValidName(s string) error {
	if s == "" {
		return fmt.Errorf("profile name must not be empty")
	}
	if s == Default {
		return fmt.Errorf("profile name %q is reserved for the agent's real config; "+
			"see ap run/which/env/--from, which accept it read-only", s)
	}
	if strings.HasPrefix(s, ".") {
		return fmt.Errorf("invalid profile name %q: must not start with '.'", s)
	}
	if s != filepath.Base(s) || strings.ContainsAny(s, `/\`) {
		return fmt.Errorf("invalid profile name %q: must not contain a path separator", s)
	}
	for _, r := range s {
		ok := r == '-' || r == '_' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !ok {
			return fmt.Errorf("invalid profile name %q: use letters, digits, '-' and '_'", s)
		}
	}
	return nil
}
