//go:build windows

package hydrate

import (
	"os"
	"syscall"
	"unsafe"
)

// The windows half. golang.org/x/sys/windows exports these; reaching kernel32
// through syscall's lazy DLL loader is the same two calls without the
// dependency, which is what keeps pkg/ standard-library-only.
var (
	kernel32     = syscall.NewLazyDLL("kernel32.dll")
	procLockFile = kernel32.NewProc("LockFileEx")
	procUnlock   = kernel32.NewProc("UnlockFileEx")
)

const (
	exclusiveLock    = 0x0002
	failImmediately  = 0x0001
	errLockViolation = syscall.Errno(33)
)

// tryLock takes a non-blocking exclusive byte-range lock.
//
// LOCKFILE_FAIL_IMMEDIATELY is always set, and contention is retried by the
// caller's bounded loop, exactly as the unix half retries on EWOULDBLOCK.
// LockFileEx's own blocking wait is not cancellable, so a caller could not
// bound it — which CLAUDE.md forbids.
func tryLock(f *os.File) (bool, error) {
	var ov syscall.Overlapped
	r, _, err := procLockFile.Call(
		f.Fd(), uintptr(exclusiveLock|failImmediately), 0,
		1, 0, uintptr(unsafe.Pointer(&ov)),
	)
	if r != 0 {
		return true, nil
	}
	if errno, ok := err.(syscall.Errno); ok && errno == errLockViolation {
		return false, nil
	}
	return false, err
}

func unlock(f *os.File) error {
	var ov syscall.Overlapped
	r, _, err := procUnlock.Call(f.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(&ov)))
	if r != 0 {
		return nil
	}
	return err
}
