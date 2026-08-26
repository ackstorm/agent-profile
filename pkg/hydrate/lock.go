package hydrate

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// lockName is the lock file inside the root. It is a dotfile because a root can
// be the user's real configuration directory, where anything visible is
// clutter the agent did not put there.
const lockName = ".ap-lock"

// lockWait bounds the wait for a contended root. Two ap processes writing one
// root is rare and short; ten seconds is long enough for a normal apply to
// finish and short enough that a stuck one is reported rather than waited on
// forever. CLAUDE.md bans an unbounded wait for exactly this reason.
const lockWait = 10 * time.Second

// LockRoot takes an exclusive advisory lock on root, held until the returned
// function is called.
//
// The lock is an OS advisory lock, not a lock FILE whose existence means
// "locked": a process killed mid-apply releases an advisory lock when its
// handles close, where a sentinel file would strand every later run behind a
// lock nobody holds.
//
// One lock, and only this one. §37.3: the source cache is unlocked because its
// publication is atomic.
func LockRoot(root string) (release func() error, err error) {
	if root == "" {
		return nil, fmt.Errorf("lock: root must not be empty")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(root, lockName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}

	// Bounded retry with an explicit failure path, never a naked poll: the
	// holder may exit, and it may not.
	deadline := time.Now().Add(lockWait)
	for attempt := 0; ; attempt++ {
		ok, lockErr := tryLock(f)
		if lockErr != nil {
			_ = f.Close()
			return nil, fmt.Errorf("lock %s: %w", root, lockErr)
		}
		if ok {
			break
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf("%s is locked by another ap process; it has held the lock for over %s", root, lockWait)
		}
		time.Sleep(backoff(attempt))
	}

	return func() error {
		// Unlock before closing, so a failure to unlock is reported rather
		// than hidden by the close that would have released it anyway.
		unlockErr := unlock(f)
		closeErr := f.Close()
		if unlockErr != nil {
			return unlockErr
		}
		return closeErr
	}, nil
}

// backoff grows to a 200ms ceiling. The ceiling matters more than the curve:
// an apply that takes seconds should not be polled hundreds of times.
func backoff(attempt int) time.Duration {
	d := time.Duration(1<<min(attempt, 7)) * time.Millisecond
	return min(d, 200*time.Millisecond)
}
