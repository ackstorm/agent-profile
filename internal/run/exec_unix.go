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
	path, err := exec.LookPath(a.Bin)
	if err != nil {
		return fmt.Errorf("cannot find %q on PATH: %w", a.Bin, err)
	}
	argv := append([]string{a.Bin}, args...)
	return syscall.Exec(path, argv, Env(a, dir, os.Environ()))
}
