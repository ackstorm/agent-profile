package profile

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestVariantsRootIsASiblingOfProfiles(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/tmp/xdg-data")
	want := filepath.Join("/tmp/xdg-data", "agent-profile", "variants")
	if got := VariantsRoot(); got != want {
		t.Errorf("VariantsRoot() = %q, want %q", got, want)
	}
}

// The whole point of the format: arguments go file -> []string -> syscall.Exec,
// and no shell is ever involved. So the hostile ones are not special cases —
// they are the ordinary case, and they must come back byte for byte.
func TestVariantArgsRoundTripWithoutAShell(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	a := agentOrFail(t, "claude")
	want := []string{
		"--dangerously-skip-permissions",
		"--model=claude-opus-5[1m]", // a glob if a shell ever saw it
		"$(rm -rf /)",               // a command substitution if a shell ever saw it
		"--append-system-prompt", "be terse; use 'quotes' and \"quotes\"",
		"--model=opus ", // a trailing space: nothing is trimmed
		"  --leading",   // and nothing is trimmed at the front either
		"",              // an empty line is an empty argument, and "" is legal argv
		"/code-review",  // the final newline terminates, it does not separate
	}
	if err := WriteVariant(a, "review", "opus", want); err != nil {
		t.Fatal(err)
	}
	got, err := VariantArgs(a, "review", "opus")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Errorf("VariantArgs = %q, want %q", got, want)
	}
}

// The case the format has to decide rather than stumble into: a trailing empty
// argument and a file that merely ends in a newline look alike unless the final
// newline is a terminator. It is. `ap run x:y:z ""` is a legal, if odd, thing to
// bake, and the slice goes straight to syscall.Exec.
func TestVariantArgsKeepsATrailingEmptyArgument(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	a := agentOrFail(t, "claude")
	if err := WriteVariant(a, "review", "trailing", []string{"-p", ""}); err != nil {
		t.Fatal(err)
	}
	got, err := VariantArgs(a, "review", "trailing")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"-p", ""}) {
		t.Errorf("VariantArgs = %q, want [-p \"\"] — the final newline terminates, it does not separate", got)
	}
}

// The stated cost of one argument per line. An escape syntax for a case nobody
// has is the speculative configurability this repository removes elsewhere, so
// the format says no out loud instead.
func TestWriteVariantRefusesANewlineInAnArgument(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	a := agentOrFail(t, "claude")
	err := WriteVariant(a, "review", "nl", []string{"-p", "line one\nline two"})
	if err == nil {
		t.Fatal("a newline in an argument was accepted; the store would read back two arguments")
	}
	if _, err := VariantArgs(a, "review", "nl"); err == nil {
		t.Error("the refused variant was written anyway")
	}
}

func TestWriteVariantRefusesAnEmptyPayload(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	a := agentOrFail(t, "claude")
	if err := WriteVariant(a, "review", "nothing", nil); err == nil {
		t.Error("a variant with no arguments was accepted; it would behave identically to its parent")
	}
}

// Editing is delete-then-write, exactly as a profile is. No --force: the file is
// two lines and the pair of commands is one line of shell.
func TestWriteVariantRefusesAnExistingVariant(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	a := agentOrFail(t, "claude")
	if err := WriteVariant(a, "review", "opus", []string{"-p"}); err != nil {
		t.Fatal(err)
	}
	if err := WriteVariant(a, "review", "opus", []string{"--effort=xhigh"}); err == nil {
		t.Fatal("an existing variant was silently overwritten")
	}
	got, err := VariantArgs(a, "review", "opus")
	if err != nil || !slices.Equal(got, []string{"-p"}) {
		t.Errorf("VariantArgs = %q (%v), want the original arguments", got, err)
	}
}

// Outside the profile directory, deliberately: the profile directory IS the
// agent's configuration directory. The agent lists it, validates it and
// rewrites parts of it, so ap's own metadata has no business being there.
// Revert VariantsRoot to point inside the profile and this fails.
func TestVariantsAreNotStoredInTheProfile(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	a := agentOrFail(t, "claude")
	dir, err := Create(a, "review")
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteVariant(a, "review", "opus", []string{"-p"}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("the store put %d entries inside the agent's config directory: %v", len(entries), entries)
	}
	if strings.HasPrefix(VariantsRoot(), dir) {
		t.Errorf("VariantsRoot %q is inside the profile %q", VariantsRoot(), dir)
	}
}

func TestVariantsListsSortedAndToleratesNone(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	a := agentOrFail(t, "claude")
	got, err := Variants(a, "never-had-any")
	if err != nil || len(got) != 0 {
		t.Errorf("Variants of a profile with none = (%v,%v), want (empty,nil)", got, err)
	}
	for _, v := range []string{"opus", "ci"} {
		if err := WriteVariant(a, "review", v, []string{"-p"}); err != nil {
			t.Fatal(err)
		}
	}
	got, err = Variants(a, "review")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"ci", "opus"}) {
		t.Errorf("Variants = %v, want [ci opus]", got)
	}
}

func TestDeleteVariantRemovesOneAndDeleteVariantsRemovesAll(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	a := agentOrFail(t, "claude")
	for _, v := range []string{"opus", "ci"} {
		if err := WriteVariant(a, "review", v, []string{"-p"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := DeleteVariant(a, "review", "opus"); err != nil {
		t.Fatal(err)
	}
	got, _ := Variants(a, "review")
	if !slices.Equal(got, []string{"ci"}) {
		t.Errorf("after DeleteVariant, Variants = %v, want [ci]", got)
	}
	if err := DeleteVariant(a, "review", "opus"); err == nil {
		t.Error("deleting a variant twice = nil error, want a refusal")
	}
	if err := DeleteVariants(a, "review"); err != nil {
		t.Fatal(err)
	}
	if got, _ := Variants(a, "review"); len(got) != 0 {
		t.Errorf("after DeleteVariants, Variants = %v, want none", got)
	}
	// A profile that never had any must not turn the cascade into an error.
	if err := DeleteVariants(a, "never-had-any"); err != nil {
		t.Errorf("DeleteVariants on a profile with none = %v, want nil", err)
	}
}

// A missing variant names the ones that do exist, the same as notThere does for
// a profile: a typo becomes a one-line fix instead of a second command.
func TestVariantArgsNamesTheVariantsThatExist(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	a := agentOrFail(t, "claude")
	if err := WriteVariant(a, "review", "ci", []string{"-p"}); err != nil {
		t.Fatal(err)
	}
	_, err := VariantArgs(a, "review", "opus")
	if err == nil {
		t.Fatal("a missing variant = nil error, want an error")
	}
	if !strings.Contains(err.Error(), "ci") {
		t.Errorf("error %q does not name the variants that exist", err)
	}
}

// A variant becomes visible complete or not at all. WriteVariant used to create
// the entry and then write it, so an interrupted run left a truncated argument
// list that reads back WITHOUT error — a variant whose first line is
// --dangerously-skip-permissions and whose rest was lost still execs, and
// WriteVariant then refuses to replace it because it "already exists".
//
// The temporary file is the mechanism, so this asserts what it must never do:
// appear in the listing, survive a successful write, or block the next one.
func TestWriteVariantLeavesNoVisibleTemporary(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	a := agentOrFail(t, "claude")

	// A leftover from a crash between write and link must not block a retry, and
	// must never be listed as a variant of its own.
	dir := filepath.Join(VariantsRoot(), a.Name, "review")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".opus.tmp"), []byte("stale\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := Variants(a, "review"); err != nil || len(got) != 0 {
		t.Errorf("a leftover temporary is listed as a variant: %v (%v)", got, err)
	}
	if err := WriteVariant(a, "review", "opus", []string{"-p"}); err != nil {
		t.Fatalf("a leftover temporary blocked a write: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "opus" {
			t.Errorf("write left %q behind; only the variant itself may remain", e.Name())
		}
	}
	got, err := VariantArgs(a, "review", "opus")
	if err != nil || !slices.Equal(got, []string{"-p"}) {
		t.Errorf("VariantArgs = %q (%v), want [-p]", got, err)
	}
}

// The only recursive removal this feature adds, and the one case that
// distinguishes its guard from doing nothing.
//
// A symlink at the LEAF is not that case: os.RemoveAll already Lstats, so it
// unlinks the link rather than recursing, with or without os.Root. The first
// version of this test asserted exactly that and passed with the guard removed
// — which is the vacuous test CLAUDE.md warns is worse than none.
//
// What os.Root actually buys is a symlinked ANCESTOR, the same reason Link uses
// it: with `variants/<agent>` pointing somewhere else, a plain
// os.RemoveAll(VariantsRoot()/<agent>/<profile>) traverses the link and deletes
// out there. Replace root.RemoveAll with os.RemoveAll and this fails.
func TestDeleteVariantsDoesNotEscapeThroughASymlinkedAncestor(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	a := agentOrFail(t, "claude")

	// Somewhere outside the store, holding what a traversal would reach.
	outside := t.TempDir()
	victim := filepath.Join(outside, "review")
	if err := os.Mkdir(victim, 0o700); err != nil {
		t.Fatal(err)
	}
	canary := filepath.Join(victim, "important.jsonl")
	if err := os.WriteFile(canary, []byte("do not lose me"), 0o600); err != nil {
		t.Fatal(err)
	}

	// The agent level — an ancestor of the path DeleteVariants builds — is a
	// symlink pointing at it.
	if err := os.MkdirAll(VariantsRoot(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(VariantsRoot(), a.Name)); err != nil {
		t.Fatal(err)
	}

	// The error is not the point and is not asserted: refusing is correct, and so
	// is removing nothing. Reaching through the link is not.
	_ = DeleteVariants(a, "review")

	if _, err := os.Stat(canary); err != nil {
		t.Fatalf("DELETEVARIANTS ESCAPED THROUGH A SYMLINKED ANCESTOR AND ATE REAL DATA: %v", err)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("the directory outside the store was removed: %v", err)
	}
}
