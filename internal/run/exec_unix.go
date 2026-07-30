//go:build unix

package run

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/ackstorm/agent-profile/internal/agent"
)

// Exec replaces the current process with the agent binary, so the agent owns
// the TTY and signals directly. It does not return on success.
func Exec(a agent.Agent, dir string, args []string) error {
	return ExecBin(a, dir, a.Bin, args)
}

// ExecBin is Exec for a binary that is not the agent itself — `ap env <ref>
// <command>`, for the installers and helpers that write into an agent's config
// directory and read the same variable to find it.
//
// The environment is built exactly as it is for the agent, deliberately: a tool
// that populates a profile has to see the same config root the agent will, shim
// and all, or it writes somewhere the agent never reads.
func ExecBin(a agent.Agent, dir, bin string, args []string) error {
	path, err := exec.LookPath(bin)
	if err != nil {
		return fmt.Errorf("cannot find %q on PATH: %w", bin, err)
	}
	argv := append([]string{bin}, args...)
	return syscall.Exec(path, argv, Env(a, dir, os.Environ()))
}
