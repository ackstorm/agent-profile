//go:build !windows

package run

import "syscall"

// handOff replaces this process with the agent.
//
// It does not return on success, which is the whole point: the agent inherits
// the TTY, the process group and the signal handling with nothing of ap's left
// in between. Every signal reaches the agent as if it had been launched
// directly, because it was — there is no parent to forward anything.
func handOff(path string, argv, env []string) error {
	return syscall.Exec(path, argv, env)
}
