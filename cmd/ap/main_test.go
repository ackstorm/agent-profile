//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// dispatch is where user input becomes a filesystem path, and it had no tests at
// all — which is how --from shipped without validation. These exercise the
// argument handling only; anything that would exec a real agent is out of scope.

func TestDispatchUnknownCommand(t *testing.T) {
	if err := dispatch([]string{"frobnicate"}); err == nil {
		t.Error("unknown command = nil error, want error")
	}
}

func TestDispatchRejectsBadReferences(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	for _, args := range [][]string{
		{"create", "claude"},
		{"create", "claude:"},
		{"create", ":plan"},
		{"create", "nope:plan"},
		{"create", "claude:../escape"},
		{"which", "claude:a/b"},
		{"env", "claude:.hidden"},
		{"delete", "claude:.."},
		{"create"},
		{"which", "claude:a", "claude:b"},
		{"agents", "extra"},
		{"list", "claude", "codex"},
		{"list", "notanagent"},
	} {
		if err := dispatch(args); err == nil {
			t.Errorf("dispatch(%q) = nil error, want error", args)
		}
	}
}

// The critical one. --from built a path without validation, so
// "--from ../../../.claude" escaped the profile root and Clone copied the real
// home into the new profile.
func TestDispatchCreateFromRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_DATA_HOME", root)

	// A directory outside the profile root holding something private.
	outside := t.TempDir()
	secret := filepath.Join(outside, "history.jsonl")
	if err := os.WriteFile(secret, []byte("SECRET-HISTORY"), 0o600); err != nil {
		t.Fatal(err)
	}

	profiles := filepath.Join(root, "agent-profile", "profiles", "claude")
	rel, err := filepath.Rel(profiles, outside)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(rel, "..") {
		t.Fatalf("test setup: %q is not outside the profile root", rel)
	}

	if err := dispatch([]string{"create", "--from", rel, "claude:evil"}); err == nil {
		t.Error("--from with a traversal = nil error, want refusal")
	}
	if _, err := os.Stat(filepath.Join(profiles, "evil", "history.jsonl")); err == nil {
		t.Fatal("EXFILTRATION: --from copied a file from outside the profile root")
	}
}

// "--from ." resolved to the parent of the destination, so the copy recursed into
// itself until the path was too long for the filesystem.
func TestDispatchCreateFromRejectsDotAndDotDot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_DATA_HOME", root)
	profiles := filepath.Join(root, "agent-profile", "profiles", "claude")

	for _, from := range []string{".", ".."} {
		name := "overlap" + strings.Repeat("x", len(from))
		if err := dispatch([]string{"create", "--from", from, "claude:" + name}); err == nil {
			t.Errorf("--from %q = nil error, want refusal", from)
		}
		// No runaway directory tree, and no half-created profile left behind.
		var dirs int
		filepath.WalkDir(filepath.Join(profiles, name), func(_ string, _ os.DirEntry, _ error) error {
			dirs++
			return nil
		})
		if dirs > 1 {
			t.Errorf("--from %q left %d entries behind", from, dirs)
		}
	}
}

func TestDispatchCreateFromRejectsMissingAndSelf(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if err := dispatch([]string{"create", "--from", "nope", "claude:x"}); err == nil {
		t.Error("--from a missing profile = nil error, want error")
	}
	if err := dispatch([]string{"create", "--from", "self", "claude:self"}); err == nil {
		t.Error("--from equal to the destination = nil error, want error")
	}
}

// A failed clone must not leave a profile that `ap create` then refuses to retry.
func TestDispatchCreateIsRetryableAfterAFailedClone(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if err := dispatch([]string{"create", "--from", ".", "claude:retry"}); err == nil {
		t.Fatal("expected the overlapping clone to fail")
	}
	if err := dispatch([]string{"create", "claude:retry"}); err != nil {
		t.Errorf("create after a failed clone = %v, want it to be retryable", err)
	}
}

func TestDispatchRunRequiresAnExistingProfile(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	err := dispatch([]string{"run", "claude:ghost"})
	if err == nil {
		t.Fatal("run on a missing profile = nil error, want error")
	}
	if !strings.Contains(err.Error(), "ap create claude:ghost") {
		t.Errorf("error %q does not tell the user how to fix it", err)
	}
}
