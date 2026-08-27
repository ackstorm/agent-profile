//go:build windows

package run

import (
	"errors"
	"os"
	"os/exec"
)

// handOff starts the agent as a child and proxies its exit code.
//
// Windows has no exec replacement. syscall.Exec exists there only as a stub
// that always returns EWINDOWS, which is why the unix path compiles for windows
// and then fails at the one moment that matters — so the split is real, not
// cosmetic.
//
// Spawn semantics is what npm and every task runner do on Windows, and the
// difference is worth stating rather than discovering:
//
//   - ap stays alive as the parent for the agent's whole run, so a process
//     tree has one extra level and a tool that inspects it sees ap, not just
//     the agent;
//   - there is no SIGTERM forwarding parity. Windows has no signals in the
//     POSIX sense; console applications get CTRL_C_EVENT and CTRL_BREAK_EVENT,
//     delivered to the whole console process group, so Ctrl-C reaches the agent
//     because it shares ap's console — not because ap forwarded anything. A
//     programmatic kill of ap does NOT reach the agent.
//
// Standard input, output and error are the parent's own handles, so the agent
// has a real console and its own raw-mode terminal handling works.
func handOff(path string, argv, env []string) error {
	cmd := exec.Command(path, argv[1:]...) //nolint:gosec // path is exec.LookPath's answer for a registry binary
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	err := cmd.Run()
	if err == nil {
		os.Exit(0)
	}
	// The agent's exit code is the answer, not ap's. A non-zero exit is the
	// agent speaking, and turning it into ap's own generic failure would make
	// every script wrapping ap unable to tell the two apart.
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		os.Exit(exitErr.ExitCode())
	}
	return err
}
