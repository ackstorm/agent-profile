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

// ValidNameAllowDefault is ValidName with the sentinel permitted.
//
// It exists for one caller: a manifest's own `name`, which addresses the
// profile it defines and may legitimately be "default" — that is how a manifest
// provisions the configuration the agent already uses. Everything a write needs
// to refuse about the sentinel is enforced where it matters instead, by the
// gate that displays the resolved absolute path and asks.
//
// Every OTHER rule still runs, and one of them is why this function is not
// simply skipped: a manifest may come from a repository somebody else wrote,
// so `name: ../../../.ssh` is a path traversal with an author behind it.
func ValidNameAllowDefault(s string) error {
	if s == Default {
		return nil
	}
	return ValidName(s)
}
