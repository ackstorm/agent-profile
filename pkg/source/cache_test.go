package source

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// The rename is what makes an entry atomic, and the removal of a failed fill
// is what keeps Has honest. Both are asserted here because together they are
// the reason the cache needs no lock (SPEC §37.3): a reader sees a complete
// entry or none, so concurrent resolvers waste a fetch and cannot corrupt.
func TestPublishIsAtomicAndReusesAnExistingEntry(t *testing.T) {
	c, err := NewCache(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	fill := func(dir string) error {
		calls++
		return os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o600)
	}
	p1, err := c.Publish("k", fill)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := c.Publish("k", fill)
	if err != nil {
		t.Fatal(err)
	}
	if p1 != p2 || calls != 1 {
		t.Errorf("second Publish refilled: calls = %d, paths %q/%q", calls, p1, p2)
	}
	if b, err := os.ReadFile(filepath.Join(p1, "f")); err != nil || string(b) != "x" {
		t.Errorf("published entry = %q, %v", b, err)
	}

	// A fill that fails must leave NOTHING behind. A half-written entry is
	// worse than none: the next run would find Has(key) true and serve a
	// truncated tree as if it had passed its digest check.
	sentinel := errors.New("fill failed")
	if _, err := c.Publish("bad", func(dir string) error {
		_ = os.WriteFile(filepath.Join(dir, "half"), []byte("y"), 0o600)
		return sentinel
	}); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want the fill's own error", err)
	}
	if c.Has("bad") {
		t.Error("a failed fill left a published entry")
	}
	if ents, _ := os.ReadDir(filepath.Join(c.Root, tmpDir)); len(ents) != 0 {
		t.Errorf("tmp not swept after a failed fill: %d entries", len(ents))
	}
}

// A process killed mid-fill leaves a stage-* directory holding bytes that
// passed no check. NewCache sweeps them, so tmp is bounded and nothing
// half-fetched survives into a later run.
func TestNewCacheSweepsAnInterruptedFill(t *testing.T) {
	root := t.TempDir()
	if _, err := NewCache(root); err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(root, tmpDir, "stage-killed")
	if err := os.MkdirAll(filepath.Join(stage, "partial"), 0o700); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(root, tmpDir, "not-a-stage")
	if err := os.MkdirAll(keep, 0o700); err != nil {
		t.Fatal(err)
	}

	if _, err := NewCache(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Errorf("stage-killed survived the sweep: %v", err)
	}
	// Only stage-* is swept. Anything else under tmp was not put there by
	// Publish, and removing it would be this package deleting a stranger's
	// files inside a directory it was merely given.
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("the sweep removed something it did not create: %v", err)
	}
}

// A key reaches Path through filepath.Join. It is derived from a resolved SHA
// or a digest today, so this cannot currently be reached with a hostile value
// — which is exactly why it is asserted now, while the constraint is cheap to
// state and before a later caller derives a key from something an author
// writes.
func TestPublishRefusesAKeyThatIsNotOnePathElement(t *testing.T) {
	c, err := NewCache(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"", ".", "..", "../escape", "a/b", `a\b`, ".hidden"} {
		called := false
		if _, err := c.Publish(key, func(string) error { called = true; return nil }); err == nil {
			t.Errorf("key %q was accepted", key)
		}
		if called {
			t.Errorf("key %q ran its fill before being rejected", key)
		}
	}
}
