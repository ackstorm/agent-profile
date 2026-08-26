//go:build !windows

package hydrate

import (
	"errors"
	"os"
	"syscall"
)

// tryLock takes a non-blocking exclusive flock. Standard library only: ach uses
// golang.org/x/sys for this, and syscall.Flock is the same call without the
// dependency.
func tryLock(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, syscall.EWOULDBLOCK):
		return false, nil
	default:
		return false, err
	}
}

func unlock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
