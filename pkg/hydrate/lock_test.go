package hydrate

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Two processes must not write one root at once. The wait is BOUNDED and its
// failure names the root: CLAUDE.md bans a naked poll precisely because the
// holder may never let go.
func TestARootIsLockedAgainstASecondHolder(t *testing.T) {
	root := t.TempDir()

	release, err := LockRoot(root)
	if err != nil {
		t.Fatal(err)
	}

	// A second acquisition must not succeed while the first is held. It is
	// allowed to wait, so this asserts through a bounded window rather than
	// expecting an instant answer.
	done := make(chan error, 1)
	go func() {
		r2, err := LockRoot(root)
		if err == nil {
			_ = r2()
		}
		done <- err
	}()

	select {
	case err := <-done:
		t.Fatalf("a second lock resolved while the first was held: %v", err)
	case <-time.After(300 * time.Millisecond):
	}

	if err := release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("the second lock failed after release: %v", err)
		}
	case <-time.After(lockWait):
		t.Fatal("the second lock never resolved after release")
	}
}

// A lock is per root. Locking one profile must not stop another being written.
func TestTwoRootsLockIndependently(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	releaseA, err := LockRoot(a)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = releaseA() }()

	releaseB, err := LockRoot(b)
	if err != nil {
		t.Fatalf("a second root was blocked by the first: %v", err)
	}
	if err := releaseB(); err != nil {
		t.Error(err)
	}
}

// The lock is an OS advisory lock, not a sentinel file whose existence means
// "locked". The file survives release on purpose — a process killed mid-apply
// releases an advisory lock when its handles close, where a sentinel would
// strand every later run behind a lock nobody holds.
func TestALeftoverLockFileDoesNotBlockALaterRun(t *testing.T) {
	root := t.TempDir()
	release, err := LockRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, lockName)); err != nil {
		t.Fatalf("the lock file was removed; that is not how advisory locking works: %v", err)
	}
	release2, err := LockRoot(root)
	if err != nil {
		t.Fatalf("a leftover lock file blocked a later run: %v", err)
	}
	if err := release2(); err != nil {
		t.Error(err)
	}
}
