//go:build unix

package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ackstorm/agent-profile/internal/agent"
	"github.com/ackstorm/agent-profile/internal/profile"
)

// dispatch is where user input becomes a filesystem path, and it had no tests at
// all — which is how --from shipped without validation. These exercise the
// argument handling only; anything that would exec a real agent is out of scope.

// TestMain keeps the suite out of the real ~/.local/bin. `ap create` now writes
// a wrapper, so every test that creates a profile would otherwise litter the
// developer's own PATH directory with commands named after test fixtures. Tests
// that care about the wrapper still set AP_LINK_DIR themselves; this is only the
// floor for the ones that do not.
func TestMain(m *testing.M) {
	d, err := os.MkdirTemp("", "ap-link-dir")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("AP_LINK_DIR", d); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = os.RemoveAll(d)
	os.Exit(code)
}

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

// `create` has nothing to pass through, so --from is accepted on either side of
// the reference. Asserting only "no error" would pass vacuously if the flag were
// silently dropped, so check the clone actually happened both ways.
func TestDispatchCreateTakesFromOnEitherSideOfTheRef(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_DATA_HOME", root)
	if err := dispatch([]string{"create", "claude:plan"}); err != nil {
		t.Fatalf("create source = %v", err)
	}
	profiles := filepath.Join(root, "agent-profile", "profiles", "claude")
	if err := os.WriteFile(filepath.Join(profiles, "plan", "settings.json"), []byte(`{"x":1}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := dispatch([]string{"create", "--from", "plan", "claude:before"}); err != nil {
		t.Errorf("--from before the reference = %v", err)
	}
	if err := dispatch([]string{"create", "claude:after", "--from", "plan"}); err != nil {
		t.Errorf("--from after the reference = %v", err)
	}
	for _, name := range []string{"before", "after"} {
		if _, err := os.Stat(filepath.Join(profiles, name, "settings.json")); err != nil {
			t.Errorf("claude:%s was created but not cloned: %v", name, err)
		}
	}
}

// Relaxing the flag order must not also start swallowing junk.
func TestDispatchCreateRejectsExtraArgumentsAroundTheRef(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	for _, args := range [][]string{
		{"create", "claude:x", "bogus"},
		{"create", "claude:x", "--from"},
		{"create", "claude:x", "--nosuchflag"},
		{"create", "bogus", "claude:x"},
	} {
		if err := dispatch(args); err == nil {
			t.Errorf("dispatch(%q) = nil error, want error", args)
		}
	}
}

// run must NOT get the treatment create just got. Everything after the reference
// belongs to the agent, so an unknown flag there is the agent's business, not a
// parse error from ap. If someone "makes run consistent with create", this fails.
// `ap env <ref> <command>` is env(1)'s second form, so it inherits run's rule:
// everything after the reference belongs to the command. Without this, the
// installers this form exists for - `ap env claude:plan npx skills add ... -g`
// - would have their own flags eaten.
func TestDispatchEnvDoesNotParseFlagsAfterTheRef(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	err := dispatch([]string{"env", "claude:ghost", "npx", "--notapflag"})
	if err == nil {
		t.Fatal("env with a command on a missing profile = nil error, want error")
	}
	if strings.Contains(err.Error(), "notapflag") {
		t.Errorf("ap parsed a flag that belongs to the command: %v", err)
	}
	if !strings.Contains(err.Error(), "claude:ghost") {
		t.Errorf("error %q is not the missing-profile error", err)
	}
}

// The first form must keep working, and must stay read-only: printing the
// variable is what `ap env <ref>` has always meant, and it answers for a
// profile without touching it.
func TestEnvWithNoCommandStillPrints(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if err := dispatch([]string{"env", "claude:neverbuilt"}); err != nil {
		t.Errorf("ap env on a profile that does not exist = %v, want nil", err)
	}
}

func TestDispatchRunDoesNotParseFlagsAfterTheRef(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	err := dispatch([]string{"run", "claude:ghost", "--notapflag"})
	if err == nil {
		t.Fatal("run on a missing profile = nil error, want error")
	}
	if strings.Contains(err.Error(), "notapflag") {
		t.Errorf("ap parsed a flag that belongs to the agent: %v", err)
	}
	if !strings.Contains(err.Error(), "claude:ghost") {
		t.Errorf("error %q is not the missing-profile error", err)
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

// The generic missing-profile message tells the user to run `ap create
// <agent>:default`, which is unconditionally refused - a dead end. On a
// machine where the agent's real config does not exist yet, the message must
// say that instead, naming the path, rather than pointing at a command that
// can never succeed.
func TestDispatchRunOnMissingDefaultNamesTheRealPathNotACreateCommand(t *testing.T) {
	home := t.TempDir() // no .claude under here
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	err := dispatch([]string{"run", "claude:default"})
	if err == nil {
		t.Fatal("run on a nonexistent real config = nil error, want error")
	}
	if strings.Contains(err.Error(), "ap create claude:default") {
		t.Errorf("error %q points at a command that is always refused", err)
	}
	want := filepath.Join(home, ".claude")
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not name the real config path %q", err, want)
	}
}

// The bug being fixed is one line printed for every agent, so that is what the test
// pins. No stdout capture: the hint is a pure function, and testing the function
// catches the regression that testing the pipe would.
func TestSetupHintComesFromTheAgent(t *testing.T) {
	for _, name := range []string{"claude", "codex", "opencode", "pi"} {
		a, _ := agent.Lookup(name)
		got := setupHint(a, "x")
		if !strings.Contains(got, name+":x") {
			t.Errorf("%s: hint %q does not name the profile", name, got)
		}
	}
	claude, _ := agent.Lookup("claude")
	opencode, _ := agent.Lookup("opencode")
	if setupHint(claude, "x") == setupHint(opencode, "x") {
		t.Error("claude and opencode print the same hint: it is hardcoded again")
	}
}

func TestCopyInstructionsFailsBeforeCreatingAnythingWhenUnknown(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	// codex has no verified instructions file, so the flag must refuse rather than
	// create a profile and quietly copy nothing.
	err := dispatch([]string{"create", "codex:nomd", "--copy-instructions"})
	if err == nil || !strings.Contains(err.Error(), "codex") {
		t.Fatalf("want an error naming the agent, got %v", err)
	}
	a, _ := agent.Lookup("codex")
	if profile.Exists(a, "nomd") {
		t.Error("a profile was created despite the flag being unusable")
	}
}

// The whole feature in one test. Dir(a, "default") is the user's real config, so a
// delete that resolved it would remove the actual configuration of the agent. Nothing
// that writes may accept the name.
func TestDefaultIsRejectedByEverythingThatWrites(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	for _, args := range [][]string{
		{"create", "claude:default"},
		{"delete", "claude:default"},
	} {
		if err := dispatch(args); err == nil {
			t.Errorf("ap %v was accepted and must not be", args)
		}
	}
}

func TestDeleteDefaultLeavesTheRealConfigAlone(t *testing.T) {
	a, _ := agent.Lookup("claude")
	before, err := os.Stat(a.Config)
	if err != nil {
		t.Skip("no real claude config on this machine")
	}
	if err := dispatch([]string{"delete", "claude:default"}); err == nil {
		t.Fatal("ap delete claude:default must fail")
	}
	after, err := os.Stat(a.Config)
	if err != nil {
		t.Fatalf("the real config directory is gone: %v", err)
	}
	if before.ModTime() != after.ModTime() {
		t.Error("the real config directory was modified")
	}
}

// --from default must be accepted as a source, never rejected the same way the
// create/delete destination case is. A rejection here would mean the --from
// validation started reusing the plain ValidName check again instead of the
// Default-aware one.
// Points HOME at a fixture, not the developer's live ~/.claude: without this,
// go test cloned whatever the person running it happened to have in their
// real config. Asserts real content was cloned, not just "no error naming
// the word reserved" - which would also have passed if --from default had
// silently cloned nothing at all.
func TestDispatchCreateFromDefaultIsNeverRejectedAsInvalid(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{"model":"opus"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	if err := dispatch([]string{"create", "claude:fromdefault", "--from", "default"}); err != nil {
		t.Fatalf("--from default = %v, want nil", err)
	}
	a, _ := agent.Lookup("claude")
	got, err := os.ReadFile(filepath.Join(profile.Dir(a, "fromdefault"), "settings.json"))
	if err != nil {
		t.Fatalf("settings.json was not cloned: %v", err)
	}
	if string(got) != `{"model":"opus"}` {
		t.Errorf("settings.json = %q, want the fixture content", got)
	}
}

// Sharing the credential makes a profile logged in, not started: measured on
// claude v2.1.220, a profile holding nothing but the credential link opens on
// the theme picker. The flag that gates it lives in ~/.claude.json, which a
// profile does not have, so create seeds it.
func TestCreateSeedsTheFirstRunFlags(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	real := `{"hasCompletedOnboarding":true,"userID":"secret","projects":{"/tmp":{"history":["a prompt"]}}}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(real), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := dispatch([]string{"create", "claude:seeded"}); err != nil {
		t.Fatal(err)
	}
	a, _ := agent.Lookup("claude")
	b, err := os.ReadFile(filepath.Join(profile.Dir(a, "seeded"), ".claude.json"))
	if err != nil {
		t.Fatalf("create seeded nothing: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("the seed is not valid JSON: %v", err)
	}
	if got["hasCompletedOnboarding"] != true {
		t.Errorf("hasCompletedOnboarding = %v, want true", got["hasCompletedOnboarding"])
	}
	// The rest of that file is session state and machine identity. Copying it
	// wholesale would put the user's prompt history into every profile.
	if len(got) != 1 {
		t.Errorf("seeded more than the first-run keys: %v", got)
	}
}

// The seed is a floor for a profile that has no such file, never a rewrite of
// one that does. Nothing reaches this through dispatch today - .claude.json is
// not in CloneAllow - so it is tested where it lives, rather than through a
// clone that would pass whether the guard existed or not.
func TestSeedFirstRunNeverRewritesAnExistingFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"hasCompletedOnboarding":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	mine := []byte(`{"hasCompletedOnboarding":false,"mine":1}`)
	if err := os.WriteFile(filepath.Join(dir, ".claude.json"), mine, 0o600); err != nil {
		t.Fatal(err)
	}
	a, _ := agent.Lookup("claude")
	keys, err := seedFirstRun(a, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 0 {
		t.Errorf("reported writing %v over a file that already existed", keys)
	}
	got, _ := os.ReadFile(filepath.Join(dir, ".claude.json"))
	if string(got) != string(mine) {
		t.Errorf(".claude.json = %s, want it untouched", got)
	}
}

// A machine where the agent has never run outside a profile has no flags to
// copy. The wizard is then the correct behaviour, and create must not fail.
func TestCreateWithoutARealFirstRunFileStillWorks(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if err := dispatch([]string{"create", "claude:noflags"}); err != nil {
		t.Errorf("create = %v, want nil", err)
	}
}

// The wrapper is not a second step. `ap create claude:plan` has to be enough
// for `claude:plan` to be a command, because a profile nobody can type is a
// profile nobody uses - which is exactly what the separate `ap link` produced.
func TestCreateWritesTheWrapper(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("AP_LINK_DIR", bin)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if err := dispatch([]string{"create", "claude:autolinked"}); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(bin, "claude:autolinked")
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatalf("create left no wrapper: %v", err)
	}
	if fi.Mode().Perm()&0o111 == 0 {
		t.Error("wrapper is not executable")
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "exec ap run claude:autolinked") {
		t.Errorf("wrapper does not exec the profile: %q", b)
	}
}

// Create links, but not at the price of a real binary that happens to share the
// name: the foreign file survives and create still succeeds, because the
// profile itself is fine and reachable through `ap run`.
func TestCreateDoesNotClobberAForeignFile(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("AP_LINK_DIR", bin)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	p := filepath.Join(bin, "claude:foreign")
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho mine\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := dispatch([]string{"create", "claude:foreign"}); err != nil {
		t.Fatalf("create = %v, want nil: an unlinkable name must not fail the create", err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "#!/bin/sh\necho mine\n" {
		t.Errorf("create overwrote a file ap did not write: %q", b)
	}
}

func TestLinkWritesAnExecutableWrapper(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("AP_LINK_DIR", bin)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if err := dispatch([]string{"create", "claude:linked"}); err != nil {
		t.Fatal(err)
	}
	if err := dispatch([]string{"link", "claude:linked"}); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(bin, "claude:linked")
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o111 == 0 {
		t.Error("wrapper is not executable")
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "exec ap run claude:linked") {
		t.Errorf("wrapper does not exec the profile: %q", b)
	}
}

func TestLinkRefusesAProfileThatDoesNotExist(t *testing.T) {
	t.Setenv("AP_LINK_DIR", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if err := dispatch([]string{"link", "claude:ghost"}); err == nil {
		t.Error("want an error for a missing profile")
	}
}

// There is nothing to link: `ap run codex:default` is already the real config.
// Named for what actually fires: ref(args, "link") routes through ParseRef,
// which refuses Default before cmdLink's own "nothing to link" check is ever
// reached. That check is kept anyway as a belt-and-braces backstop - see its
// comment in cmdLink - but there is no way to exercise it through dispatch,
// so this test cannot and does not claim to.
func TestLinkRefusesDefaultViaParseRef(t *testing.T) {
	t.Setenv("AP_LINK_DIR", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if err := dispatch([]string{"link", "claude:default"}); err == nil {
		t.Error("want an error for claude:default")
	}
}

// Overwriting something ap did not write would be a good way to lose a real binary.
func TestLinkRefusesToOverwriteAForeignFile(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("AP_LINK_DIR", bin)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if err := os.WriteFile(filepath.Join(bin, "claude:linked"), []byte("#!/bin/sh\necho mine\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := dispatch([]string{"create", "claude:linked"}); err != nil {
		t.Fatal(err)
	}
	if err := dispatch([]string{"link", "claude:linked"}); err == nil {
		t.Error("want a refusal rather than clobbering a file ap did not write")
	}
}

func TestUnlinkRemovesOnlyOurWrapper(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("AP_LINK_DIR", bin)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	foreign := filepath.Join(bin, "claude:foreign")
	if err := os.WriteFile(foreign, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := dispatch([]string{"unlink", "claude:foreign"}); err == nil {
		t.Error("unlink must refuse a file ap did not write")
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Error("unlink removed a foreign file")
	}
}

// Unlinking a profile with no wrapper is not an error: most profiles are never
// linked at all.
func TestUnlinkOfANeverLinkedProfileIsNotAnError(t *testing.T) {
	t.Setenv("AP_LINK_DIR", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if err := dispatch([]string{"unlink", "claude:neverlinked"}); err != nil {
		t.Errorf("unlink of a never-linked profile = %v, want nil", err)
	}
}

// A missing link dir (nothing under it, unlike the case above where the dir
// exists but is empty) means nothing was ever linked, on any machine that has
// never run `ap link` at all - most of them. Regression: os.OpenRoot on a
// nonexistent dir returned an error that neither unlink nor delete's automatic
// wrapper cleanup treated as "nothing to remove".
func TestUnlinkToleratesAMissingLinkDir(t *testing.T) {
	t.Setenv("AP_LINK_DIR", filepath.Join(t.TempDir(), "does-not-exist"))
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if err := dispatch([]string{"unlink", "claude:neverlinked"}); err != nil {
		t.Errorf("unlink with a missing link dir = %v, want nil", err)
	}
}

// The sharper case: delete must not report failure - or leave the profile
// deleted while claiming otherwise - just because the link dir was never
// created.
func TestDeleteToleratesAMissingLinkDir(t *testing.T) {
	t.Setenv("AP_LINK_DIR", filepath.Join(t.TempDir(), "does-not-exist"))
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	a, _ := agent.Lookup("claude")
	if err := dispatch([]string{"create", "claude:nolinkdir"}); err != nil {
		t.Fatal(err)
	}
	if err := dispatch([]string{"delete", "--yes", "claude:nolinkdir"}); err != nil {
		t.Errorf("delete with a missing link dir = %v, want nil", err)
	}
	if profile.Exists(a, "nolinkdir") {
		t.Error("delete reported success but the profile still exists")
	}
}

// A deleted profile must not leave a wrapper that fails confusingly.
func TestDeleteRemovesTheWrapper(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("AP_LINK_DIR", bin)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	for _, args := range [][]string{{"create", "claude:temp"}, {"link", "claude:temp"}, {"delete", "--yes", "claude:temp"}} {
		if err := dispatch(args); err != nil {
			t.Fatalf("ap %v: %v", args, err)
		}
	}
	if _, err := os.Stat(filepath.Join(bin, "claude:temp")); !os.IsNotExist(err) {
		t.Error("the wrapper outlived its profile")
	}
}

func TestCopyInstructionsWritesARealFileNotALink(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	a, _ := agent.Lookup("claude")
	if _, err := os.Stat(a.Instructions.Source); err != nil {
		// Not a pass: this asserts nothing about --copy-instructions when it skips.
		// It only runs on a machine (or container) that has ~/.claude/CLAUDE.md;
		// the devtools image does not, so it skips there on every CI run too.
		t.Skipf("skipping: %s does not exist on this machine, so --copy-instructions has nothing to copy", a.Instructions.Source)
	}
	if err := dispatch([]string{"create", "claude:withmd", "--copy-instructions"}); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(profile.Dir(a, "withmd"), a.Instructions.Name)
	fi, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Error("instructions were linked, not copied: editing them would hit the real file")
	}
	want, _ := os.ReadFile(a.Instructions.Source)
	got, _ := os.ReadFile(p)
	if string(got) != string(want) {
		t.Error("the copy does not match the source")
	}
}

// --- the confirmation guard --------------------------------------------------

// noStdin points os.Stdin at /dev/null for the duration of a test, which is the
// "nobody is there to answer" case: a script, a CI runner, a hook.
func noStdin(t *testing.T) {
	t.Helper()
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdin
	os.Stdin = f
	t.Cleanup(func() { os.Stdin = orig; _ = f.Close() })
}

// Delete is the only irreversible thing this program does, and a profile holds
// its own session transcripts. With no answer available it must refuse, and the
// profile must still be there afterwards - the second half is the point. Revert
// the confirm call in cmdDelete and this fails on Exists, not on the error.
func TestDeleteWithoutYesAndWithNoAnswerKeepsTheProfile(t *testing.T) {
	t.Setenv("AP_LINK_DIR", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	noStdin(t)
	a, _ := agent.Lookup("claude")
	if err := dispatch([]string{"create", "claude:keepme"}); err != nil {
		t.Fatal(err)
	}
	if err := dispatch([]string{"delete", "claude:keepme"}); err == nil {
		t.Error("delete with no way to confirm = nil error, want a refusal")
	}
	if !profile.Exists(a, "keepme") {
		t.Fatal("delete removed the profile without an answer")
	}
	// And --yes is the stated way through, on either side of the reference.
	if err := dispatch([]string{"delete", "claude:keepme", "--yes"}); err != nil {
		t.Fatalf("delete --yes = %v, want nil", err)
	}
	if profile.Exists(a, "keepme") {
		t.Error("delete --yes left the profile behind")
	}
}

// A typo must be answered before the prompt, not by it: asking "remove ...?"
// about a directory that does not exist teaches the user to type y at anything.
func TestDeleteOfAMissingProfileNamesTheOnesThatExist(t *testing.T) {
	t.Setenv("AP_LINK_DIR", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	noStdin(t)
	if err := dispatch([]string{"create", "claude:real"}); err != nil {
		t.Fatal(err)
	}
	err := dispatch([]string{"delete", "claude:nope"})
	if err == nil {
		t.Fatal("delete of a missing profile = nil error")
	}
	if !strings.Contains(err.Error(), "real") {
		t.Errorf("error does not name the profiles that do exist: %v", err)
	}
}

// --- stdout stays consumable -------------------------------------------------

func stdoutOf(t *testing.T, f func() error) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	ferr := f()
	os.Stdout = orig
	_ = w.Close()
	b, _ := io.ReadAll(r)
	_ = r.Close()
	if ferr != nil {
		t.Fatal(ferr)
	}
	return string(b)
}

// stderrOf captures the prompt, which goes to stderr so it is never mistaken
// for output. Unlike stdoutOf it returns the error rather than failing on it:
// the interesting case here is the command that correctly refuses.
func stderrOf(t *testing.T, f func() error) (string, error) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = w
	ferr := f()
	os.Stderr = orig
	_ = w.Close()
	b, _ := io.ReadAll(r)
	_ = r.Close()
	return string(b), ferr
}

// `which` and `env` are the two commands whose output is consumed rather than
// read - $(ap which ...) and env(1) - so they print one bare line and nothing
// else. No tick, no "~", no aligned block: a tilde in a shell substitution is a
// literal directory name that resolves nowhere, and any decoration at all breaks
// the substitution outright. This is the test that stops the receipt formatting
// from spreading to them.
func TestWhichAndEnvPrintOneBareConsumableLine(t *testing.T) {
	t.Setenv("AP_LINK_DIR", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	a, _ := agent.Lookup("claude")
	if err := dispatch([]string{"create", "claude:bare"}); err != nil {
		t.Fatal(err)
	}
	dir := profile.Dir(a, "bare")

	if got := stdoutOf(t, func() error { return dispatch([]string{"which", "claude:bare"}) }); got != dir+"\n" {
		t.Errorf("ap which = %q, want exactly %q", got, dir+"\n")
	}
	want := a.ConfigEnv + "=" + dir + "\n"
	if got := stdoutOf(t, func() error { return dispatch([]string{"env", "claude:bare"}) }); got != want {
		t.Errorf("ap env = %q, want exactly %q", got, want)
	}
}

func TestOnPathMatchesEntriesExactly(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/home/x/.local/bin/:/opt/bin")
	for _, tc := range []struct {
		dir  string
		want bool
	}{
		{"/home/x/.local/bin", true}, // the trailing slash in PATH must not matter
		{"/usr/bin", true},
		{"/home/x/.local", false}, // a prefix is not a match
		{"/home/x/.local/bin/extra", false},
	} {
		if got := onPath(tc.dir); got != tc.want {
			t.Errorf("onPath(%q) = %v, want %v", tc.dir, got, tc.want)
		}
	}
}

// --- ap variant --------------------------------------------------------------

// The store, and a wrapper that execs the three-segment reference. Both, from
// one command: a name you cannot type is a name you do not use, and a wrapper
// whose arguments ap run cannot resolve is the hidden state this design exists
// to avoid.
func TestVariantWritesTheStoreAndTheWrapper(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("AP_LINK_DIR", bin)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if err := dispatch([]string{"create", "claude:review"}); err != nil {
		t.Fatal(err)
	}
	args := []string{"--dangerously-skip-permissions", "--model=claude-opus-5[1m]", "--effort=xhigh"}
	if err := dispatch(append([]string{"variant", "claude:review:opus", "--"}, args...)); err != nil {
		t.Fatal(err)
	}
	a, _ := agent.Lookup("claude")
	got, err := profile.VariantArgs(a, "review", "opus")
	if err != nil {
		t.Fatalf("the store has no entry: %v", err)
	}
	if strings.Join(got, "\x00") != strings.Join(args, "\x00") {
		t.Errorf("stored %q, want %q", got, args)
	}
	b, err := os.ReadFile(filepath.Join(bin, "claude:review:opus"))
	if err != nil {
		t.Fatalf("variant left no wrapper: %v", err)
	}
	if !strings.Contains(string(b), "exec ap run claude:review:opus") {
		t.Errorf("wrapper does not exec the variant: %q", b)
	}
	// The arguments live in the store, never in the wrapper. If they leak in
	// here, `ap run claude:review:opus` and typing the name diverge — and the
	// source of truth starts depending on the artefact derived from it.
	if strings.Contains(string(b), "--effort=xhigh") {
		t.Errorf("the wrapper carries the payload; the store is the only source of truth: %q", b)
	}
}

// Never creates the parent implicitly: forty megabytes and a background
// reconciliation is not something a two-line command should trigger by typo.
func TestVariantRefusesAMissingParent(t *testing.T) {
	t.Setenv("AP_LINK_DIR", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	err := dispatch([]string{"variant", "claude:nope:v", "--", "-p"})
	if err == nil {
		t.Fatal("a variant over a missing profile = nil error, want an error")
	}
	if !strings.Contains(err.Error(), "claude:nope") {
		t.Errorf("error %q does not name the missing parent", err)
	}
	a, _ := agent.Lookup("claude")
	if profile.Exists(a, "nope") {
		t.Error("the parent profile was created implicitly")
	}
}

func TestVariantRejectsBadInvocations(t *testing.T) {
	t.Setenv("AP_LINK_DIR", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if err := dispatch([]string{"create", "claude:review"}); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"variant"},
		{"variant", "claude:review:opus"},               // no separator, no payload
		{"variant", "claude:review:opus", "--"},         // empty payload: same as the parent
		{"variant", "claude:review:opus", "-p"},         // missing the -- separator
		{"variant", "claude:review", "--", "-p"},        // two segments: no variant named
		{"variant", "claude:review:opus:x", "--", "-p"}, // four segments
		{"variant", "claude:default:opus", "--", "-p"},
		{"variant", "claude:review:default", "--", "-p"},
	} {
		if err := dispatch(args); err == nil {
			t.Errorf("dispatch(%q) = nil error, want error", args)
		}
	}
}

// Everything after the first -- is payload, literally — including flags ap
// itself owns. `--from` belongs to `ap create`, which parses it on either side
// of its reference with parseAroundRef; if `ap variant` ever reuses that helper
// this stores nothing, or stores the wrong thing, and the failure is silent.
func TestVariantStoresFlagsApItselfOwns(t *testing.T) {
	t.Setenv("AP_LINK_DIR", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if err := dispatch([]string{"create", "claude:review"}); err != nil {
		t.Fatal(err)
	}
	payload := []string{"--from", "default", "--yes", "-p"}
	if err := dispatch(append([]string{"variant", "claude:review:literal", "--"}, payload...)); err != nil {
		t.Fatal(err)
	}
	a, _ := agent.Lookup("claude")
	got, err := profile.VariantArgs(a, "review", "literal")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "\x00") != strings.Join(payload, "\x00") {
		t.Errorf("stored %q, want %q — ap parsed a payload that belongs to the agent", got, payload)
	}
	// And nothing was cloned: --from was payload, not an instruction.
	if profile.Exists(a, "literal") {
		t.Error("ap acted on --from inside the payload")
	}
}

// The separator is required, and the error has to name it: without it the
// command looks like it takes a bare list, and the first thing a user tries is
// `ap variant <ref> -p`.
func TestVariantWithoutTheSeparatorNamesIt(t *testing.T) {
	t.Setenv("AP_LINK_DIR", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if err := dispatch([]string{"create", "claude:review"}); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"variant", "claude:review:opus"},
		{"variant", "claude:review:opus", "-p"},
	} {
		err := dispatch(args)
		if err == nil {
			t.Fatalf("dispatch(%q) = nil error, want error", args)
		}
		if !strings.Contains(err.Error(), "--") {
			t.Errorf("error %q does not name the -- separator", err)
		}
	}
}

// Editing is delete-then-write, at the dispatch level too.
func TestVariantRefusesAnExistingVariant(t *testing.T) {
	t.Setenv("AP_LINK_DIR", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if err := dispatch([]string{"create", "claude:review"}); err != nil {
		t.Fatal(err)
	}
	if err := dispatch([]string{"variant", "claude:review:opus", "--", "-p"}); err != nil {
		t.Fatal(err)
	}
	if err := dispatch([]string{"variant", "claude:review:opus", "--", "--effort=xhigh"}); err == nil {
		t.Error("a second ap variant silently replaced the first")
	}
}

// `ap create` makes profiles and only profiles. Overloading it so the same word
// sometimes builds forty megabytes and sometimes writes two lines is worse than
// one noun.
func TestCreateStillRefusesAThreeSegmentReference(t *testing.T) {
	t.Setenv("AP_LINK_DIR", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if err := dispatch([]string{"create", "claude:review:opus"}); err == nil {
		t.Error("ap create accepted a three-segment reference")
	}
}

// link and unlink handle a variant's wrapper with the same rules and the same
// marker as any other, and link refuses a name ap run could not resolve.
func TestLinkAndUnlinkAVariant(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("AP_LINK_DIR", bin)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	for _, args := range [][]string{
		{"create", "claude:review"},
		{"variant", "claude:review:opus", "--", "-p"},
		{"unlink", "claude:review:opus"},
	} {
		if err := dispatch(args); err != nil {
			t.Fatalf("ap %v: %v", args, err)
		}
	}
	if _, err := os.Stat(filepath.Join(bin, "claude:review:opus")); !os.IsNotExist(err) {
		t.Fatal("unlink left the variant wrapper behind")
	}
	if err := dispatch([]string{"link", "claude:review:opus"}); err != nil {
		t.Fatalf("link of a variant = %v", err)
	}
	if _, err := os.Stat(filepath.Join(bin, "claude:review:opus")); err != nil {
		t.Fatalf("link wrote no wrapper: %v", err)
	}
	// A name is invocable only if `ap run` can resolve it.
	if err := dispatch([]string{"link", "claude:review:ghost"}); err == nil {
		t.Error("link invented a name for a variant that does not exist")
	}
}

// The wrapper's exact bytes are load-bearing across versions: every wrapper
// already on disk is recognised as ours by that header. A refactor of
// writeWrapper must not move a single byte of the two-segment case.
func TestLinkRendersTheSameWrapperBytesAsBefore(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("AP_LINK_DIR", bin)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if err := dispatch([]string{"create", "claude:plan"}); err != nil {
		t.Fatal(err)
	}
	if err := dispatch([]string{"link", "claude:plan"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(bin, "claude:plan"))
	if err != nil {
		t.Fatal(err)
	}
	want := "#!/bin/sh\n# written by ap link. Safe to delete.\nexec ap run claude:plan \"$@\"\n"
	if string(b) != want {
		t.Errorf("wrapper = %q, want %q", b, want)
	}
}

// --- run composition ---------------------------------------------------------

// One rule, no special cases: the variant's arguments, then the caller's. Later
// wins in every CLI here, so a caller can override a baked default for one
// invocation without editing anything.
//
// Tested as a function, not through a pipe: run.Exec replaces the process, and
// testing the function catches the regression that testing the exec would.
func TestRunArgsPutsTheVariantFirstAndTheCallerSecond(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	a, _ := agent.Lookup("claude")
	if _, err := profile.Create(a, "review"); err != nil {
		t.Fatal(err)
	}
	baked := []string{"--dangerously-skip-permissions", "--model=claude-opus-5[1m]"}
	if err := profile.WriteVariant(a, "review", "opus", baked); err != nil {
		t.Fatal(err)
	}

	got, err := runArgs(a, "review", "opus", []string{"--model=haiku", "hello"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--dangerously-skip-permissions", "--model=claude-opus-5[1m]", "--model=haiku", "hello"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("runArgs = %q, want %q", got, want)
	}

	// No variant: the caller's arguments, untouched. This is every run that
	// existed before this feature.
	plain, err := runArgs(a, "review", "", []string{"--effort", "xhigh"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(plain, "\x00") != strings.Join([]string{"--effort", "xhigh"}, "\x00") {
		t.Errorf("runArgs with no variant = %q, want the caller's arguments unchanged", plain)
	}
}

// A typo in the third segment must name the variants that do exist.
func TestRunNamesTheVariantsThatExist(t *testing.T) {
	t.Setenv("AP_LINK_DIR", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if err := dispatch([]string{"create", "claude:review"}); err != nil {
		t.Fatal(err)
	}
	if err := dispatch([]string{"variant", "claude:review:ci", "--", "-p"}); err != nil {
		t.Fatal(err)
	}
	err := dispatch([]string{"run", "claude:review:opus"})
	if err == nil {
		t.Fatal("run of a missing variant = nil error, want an error")
	}
	if !strings.Contains(err.Error(), "ci") {
		t.Errorf("error %q does not name the variants that exist", err)
	}
}

// A dangling variant needs no new guard: the existing profile lookup reports
// the missing profile and how to create it, which is the more useful answer.
func TestRunOnAVariantOfAMissingProfileReportsTheProfile(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	err := dispatch([]string{"run", "claude:ghost:opus"})
	if err == nil {
		t.Fatal("run on a missing profile = nil error, want an error")
	}
	if !strings.Contains(err.Error(), "ap create claude:ghost") {
		t.Errorf("error %q does not tell the user how to fix it", err)
	}
}

// Only `ap run` composes. `ap env <ref> <cmd...>` execs something that is not
// the agent — an installer, npx skills add — and a variant's arguments are the
// AGENT's flags. Handing `--dangerously-skip-permissions` to npx would at best
// fail and at worst mean something else entirely.
//
// Asserted through the argument builder rather than the exec, which replaces
// the process: cmdEnv must never call runArgs, and this is the shape of that.
func TestEnvDoesNotComposeAVariantsArgumentsIntoTheCommand(t *testing.T) {
	t.Setenv("AP_LINK_DIR", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if err := dispatch([]string{"create", "claude:review"}); err != nil {
		t.Fatal(err)
	}
	if err := dispatch([]string{"variant", "claude:review:opus", "--", "--dangerously-skip-permissions"}); err != nil {
		t.Fatal(err)
	}
	// A command that does not exist, so the error names what would have been
	// exec'd and nothing is actually run.
	err := dispatch([]string{"env", "claude:review:opus", "ap-no-such-binary"})
	if err == nil {
		t.Fatal("env with a missing command = nil error, want an error")
	}
	if strings.Contains(err.Error(), "dangerously") {
		t.Errorf("env composed the variant's arguments into the command: %v", err)
	}
	if !strings.Contains(err.Error(), "ap-no-such-binary") {
		t.Errorf("error %q does not name the command that was to be run", err)
	}
}

// A variant has no configuration of its own, so both of these answer for the
// parent. Inventing a second directory that nothing writes to would be the
// alternative, and it would be a lie.
func TestWhichAndEnvOnAVariantAnswerForTheParent(t *testing.T) {
	t.Setenv("AP_LINK_DIR", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if err := dispatch([]string{"create", "claude:review"}); err != nil {
		t.Fatal(err)
	}
	if err := dispatch([]string{"variant", "claude:review:opus", "--", "-p"}); err != nil {
		t.Fatal(err)
	}
	a, _ := agent.Lookup("claude")
	dir := profile.Dir(a, "review")

	if got := stdoutOf(t, func() error { return dispatch([]string{"which", "claude:review:opus"}) }); got != dir+"\n" {
		t.Errorf("ap which on a variant = %q, want the parent's directory %q", got, dir+"\n")
	}
	want := a.ConfigEnv + "=" + dir + "\n"
	if got := stdoutOf(t, func() error { return dispatch([]string{"env", "claude:review:opus"}) }); got != want {
		t.Errorf("ap env on a variant = %q, want the parent's variable %q", got, want)
	}
}

// --- deleting variants -------------------------------------------------------

// Confirmation is proportional to what is lost. A profile holds session
// transcripts; a variant holds two lines of text, which is what makes it cheap
// to try five of them. Revert the "no confirmation" branch and this hangs or
// fails on the missing answer.
func TestDeleteAVariantAsksNothingAndLeavesTheProfile(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("AP_LINK_DIR", bin)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	noStdin(t)
	for _, args := range [][]string{
		{"create", "claude:review"},
		{"variant", "claude:review:opus", "--", "-p"},
		{"delete", "claude:review:opus"},
	} {
		if err := dispatch(args); err != nil {
			t.Fatalf("ap %v: %v", args, err)
		}
	}
	a, _ := agent.Lookup("claude")
	if !profile.Exists(a, "review") {
		t.Fatal("deleting a variant removed the profile")
	}
	if _, err := profile.VariantArgs(a, "review", "opus"); err == nil {
		t.Error("the store entry outlived the delete")
	}
	if _, err := os.Stat(filepath.Join(bin, "claude:review:opus")); !os.IsNotExist(err) {
		t.Error("the variant's wrapper outlived the delete")
	}
	if err := dispatch([]string{"delete", "claude:review:ghost"}); err == nil {
		t.Error("deleting a variant that does not exist = nil error, want an error")
	}
}

// A variant without its parent is a command that fails confusingly — the same
// reason delete already removes wrappers. Revert either half of the cascade
// (DeleteVariants, or the wrapper loop) and this fails.
func TestDeleteAProfileRemovesItsVariantsAndTheirWrappers(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("AP_LINK_DIR", bin)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	for _, args := range [][]string{
		{"create", "claude:review"},
		{"variant", "claude:review:opus", "--", "-p"},
		{"variant", "claude:review:ci", "--", "-p"},
		{"delete", "--yes", "claude:review"},
	} {
		if err := dispatch(args); err != nil {
			t.Fatalf("ap %v: %v", args, err)
		}
	}
	a, _ := agent.Lookup("claude")
	if got, _ := profile.Variants(a, "review"); len(got) != 0 {
		t.Errorf("the variants outlived their profile: %v", got)
	}
	for _, v := range []string{"opus", "ci"} {
		if _, err := os.Stat(filepath.Join(bin, "claude:review:"+v)); !os.IsNotExist(err) {
			t.Errorf("the wrapper for claude:review:%s outlived its profile", v)
		}
	}
}

// Deleting a profile names the variants that go with it, so the answer is given
// with the whole cost in view.
func TestDeleteAProfileNamesItsVariantsInTheConfirmation(t *testing.T) {
	t.Setenv("AP_LINK_DIR", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	noStdin(t)
	for _, args := range [][]string{
		{"create", "claude:review"},
		{"variant", "claude:review:opus", "--", "-p"},
		{"variant", "claude:review:ci", "--", "-p"},
	} {
		if err := dispatch(args); err != nil {
			t.Fatalf("ap %v: %v", args, err)
		}
	}
	out, err := stderrOf(t, func() error { return dispatch([]string{"delete", "claude:review"}) })
	if err == nil {
		t.Fatal("delete with no way to confirm = nil error, want a refusal")
	}
	for _, want := range []string{"2 variants", "opus", "ci"} {
		if !strings.Contains(out, want) {
			t.Errorf("the prompt %q does not mention %q", out, want)
		}
	}
	a, _ := agent.Lookup("claude")
	if !profile.Exists(a, "review") {
		t.Error("delete removed the profile without an answer")
	}
}

// --- ap list -----------------------------------------------------------------

// A command whose name silently disables every permission prompt is a real
// hazard, and ap exists precisely so that you have enough profiles not to
// remember what each one does. Printing the arguments is what stops that being
// invisible, without inventing any special handling for that flag in
// particular — and it is free, because the store is already a list of strings.
func TestListShowsVariantsNestedUnderTheirProfileWithTheirArguments(t *testing.T) {
	t.Setenv("AP_LINK_DIR", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	for _, args := range [][]string{
		{"create", "claude:review"},
		{"variant", "claude:review:opus", "--", "--dangerously-skip-permissions", "--effort=xhigh"},
		{"variant", "claude:review:ci", "--", "-p"},
	} {
		if err := dispatch(args); err != nil {
			t.Fatalf("ap %v: %v", args, err)
		}
	}
	out := stdoutOf(t, func() error { return dispatch([]string{"list", "claude"}) })
	for _, want := range []string{
		"review",
		"review:opus",
		"--dangerously-skip-permissions --effort=xhigh",
		"review:ci",
		"-p",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("ap list output does not contain %q:\n%s", want, out)
		}
	}
	// The variant lines are nested under the agent's line, not mixed into the
	// profile column, or the listing stops being readable down the page.
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("want one agent line and two variant lines, got %d:\n%s", len(lines), out)
	}
	if strings.Contains(lines[0], "review:opus") {
		t.Errorf("the variant is on the profile line: %q", lines[0])
	}
}

// scripts/smoke.sh parses this output — `for ag in $("$AP" list | ...)` — and an
// indented variant line reaching that loop becomes a bogus agent name, red-ing
// two blocks that have nothing to do with variants. The contract is that an
// agent line starts in column 0 and a variant line is indented; this applies
// smoke's own filter and asserts what it yields, so the coupling fails here
// rather than on somebody's machine at release time.
func TestListTopLevelStaysParseableByScriptsSmoke(t *testing.T) {
	t.Setenv("AP_LINK_DIR", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	for _, args := range [][]string{
		{"create", "claude:review"},
		{"variant", "claude:review:opus", "--", "--dangerously-skip-permissions"},
	} {
		if err := dispatch(args); err != nil {
			t.Fatalf("ap %v: %v", args, err)
		}
	}
	out := stdoutOf(t, func() error { return dispatch([]string{"list"}) })

	// The same selection scripts/smoke.sh's agents() makes: lines that begin in
	// column 0, up to the first colon.
	var got []string
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if line == "" || line[0] == ' ' || line[0] == '\t' {
			continue
		}
		name, _, ok := strings.Cut(line, ":")
		if !ok {
			t.Fatalf("agent line %q has no colon; smoke.sh's filter yields nothing for it", line)
		}
		got = append(got, name)
	}
	if strings.Join(got, " ") != strings.Join(agent.Names(), " ") {
		t.Errorf("smoke.sh's filter over `ap list` yields %v, want exactly the agents %v", got, agent.Names())
	}
}

// --- what survives a failure partway through --------------------------------

// The irreversible thing has already happened by the time the wrappers are
// removed, so a wrapper ap refuses to touch must not abandon the rest of the
// cascade or swallow the receipt. It used to do both: `ap delete` printed only
// "refusing to remove ...", never mentioned the profile it had just erased, and
// left the later variants' wrappers pointing at a store it had already emptied.
func TestDeleteReportsTheProfileEvenWhenAWrapperIsRefused(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("AP_LINK_DIR", bin)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	for _, args := range [][]string{
		{"create", "claude:review"},
		{"variant", "claude:review:aaa", "--", "-p"},
		{"variant", "claude:review:zzz", "--", "-p"},
	} {
		if err := dispatch(args); err != nil {
			t.Fatalf("ap %v: %v", args, err)
		}
	}
	// Something ap did not write, sitting where the first variant's wrapper was.
	foreign := filepath.Join(bin, "claude:review:aaa")
	if err := os.WriteFile(foreign, []byte("#!/bin/sh\necho not ours\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	out := stdoutOf(t, func() error { return dispatch([]string{"delete", "--yes", "claude:review"}) })
	if !strings.Contains(out, `deleted "claude:review"`) {
		t.Errorf("the receipt never said what was deleted:\n%s", out)
	}
	// The refusal must not have stopped the variant after it.
	if _, err := os.Stat(filepath.Join(bin, "claude:review:zzz")); !os.IsNotExist(err) {
		t.Error("the cascade stopped at the refused wrapper; a later variant kept its wrapper")
	}
	// And the foreign file is still not ours to remove.
	if _, err := os.Stat(foreign); err != nil {
		t.Errorf("ap removed a file it did not write: %v", err)
	}
	a, _ := agent.Lookup("claude")
	if got, _ := profile.Variants(a, "review"); len(got) != 0 {
		t.Errorf("the store outlived the profile: %v", got)
	}
}

// One unreadable entry must not take the listing down with it. It used to
// return the error, so `ap list` printed the claude line and then aborted with
// exit 1 — codex, pi and opencode never appeared. That also silently defeated
// scripts/smoke.sh's agents(), which parses this output: it yielded one agent
// instead of four, and the two blocks driven by it tested nothing while
// reporting nothing wrong.
func TestListReportsAnUnreadableVariantWithoutAbandoningTheRest(t *testing.T) {
	t.Setenv("AP_LINK_DIR", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	for _, args := range [][]string{
		{"create", "claude:review"},
		{"variant", "claude:review:good", "--", "-p"},
	} {
		if err := dispatch(args); err != nil {
			t.Fatalf("ap %v: %v", args, err)
		}
	}
	a, _ := agent.Lookup("claude")
	// A zero-byte entry: what an interrupted write used to be able to leave.
	empty := filepath.Join(profile.VariantsRoot(), a.Name, "review", "broken")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	out := stdoutOf(t, func() error { return dispatch([]string{"list"}) })
	if !strings.Contains(out, "review:broken") {
		t.Errorf("the unreadable entry is not reported at all:\n%s", out)
	}
	if !strings.Contains(out, "review:good") {
		t.Errorf("a readable variant was lost:\n%s", out)
	}
	// Every agent still gets its line, which is what smoke.sh's agents() reads.
	for _, name := range agent.Names() {
		if !strings.Contains(out, name+":") {
			t.Errorf("agent %q vanished from the listing:\n%s", name, out)
		}
	}
}

// --only-settings needs a source. Inventing an implicit `default` would make a
// bare `ap create` start copying the real config.
func TestCreateOnlySettingsRequiresFrom(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	err := dispatch([]string{"create", "claude:nofrom", "--only-settings", "theme"})
	if err == nil {
		t.Fatal("--only-settings without --from was accepted")
	}
	if !strings.Contains(err.Error(), "--from") {
		t.Errorf("error = %v, want it to name --from", err)
	}
	a, _ := agent.Lookup("claude")
	if profile.Exists(a, "nofrom") {
		t.Error("the profile was created before the flags were validated")
	}
}

// The whole feature, end to end: the named keys arrive and the other five
// CloneAllow entries do not. Asserted by their absence on disk.
func TestCreateOnlySettingsSkipsEveryOtherCloneAllowEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	real := filepath.Join(home, ".claude")
	if err := os.MkdirAll(filepath.Join(real, "skills", "s"), 0o700); err != nil {
		t.Fatal(err)
	}
	settings := `{"theme":"dark-ansi","statusLine":{"type":"command","command":"bash /x.sh"},"permissions":{"allow":["Bash"]}}`
	if err := os.WriteFile(filepath.Join(real, "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "CLAUDE.md"), []byte("global"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "skills", "s", "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := dispatch([]string{"create", "claude:slim", "--from", "default",
		"--only-settings", "statusLine", "--only-settings", "theme"}); err != nil {
		t.Fatal(err)
	}
	a, _ := agent.Lookup("claude")
	dir := profile.Dir(a, "slim")
	b, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatalf("settings.json was not written: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["theme"] != "dark-ansi" || got["statusLine"] == nil {
		t.Errorf("settings.json = %s, want exactly statusLine and theme", b)
	}
	for _, gone := range []string{"CLAUDE.md", "skills"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); !os.IsNotExist(err) {
			t.Errorf("--only-settings carried %s, which is the opposite of a fresh profile", gone)
		}
	}
}

// A typo must not seed silence, and must not fail a create that otherwise
// worked: the profile exists, the key that did resolve is there, and the one
// that did not is a warning on stderr. Captured with stderrOf (already used
// elsewhere in this file) rather than only checking the file — a test that
// never looks at stderr would stay green even if `rc.warn` were deleted.
func TestCreateOnlySettingsWarnsAboutAMissingKeyAndStillCreates(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{"theme":"dark"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stderr, err := stderrOf(t, func() error {
		return dispatch([]string{"create", "claude:typo", "--from", "default",
			"--only-settings", "theme", "--only-settings", "statuLine"})
	})
	if err != nil {
		t.Fatalf("a missing key failed the create: %v", err)
	}
	if !strings.Contains(stderr, "statuLine") || !strings.Contains(stderr, "settings.json") {
		t.Errorf("stderr = %q, want it to name the missing key and the file it was looked for in", stderr)
	}
	a, _ := agent.Lookup("claude")
	if !profile.Exists(a, "typo") {
		t.Error("the profile was not created")
	}
	b, err := os.ReadFile(filepath.Join(profile.Dir(a, "typo"), "settings.json"))
	if err != nil || !strings.Contains(string(b), "dark") {
		t.Errorf("settings.json = %s (%v), want the key that did resolve", b, err)
	}
}

// The flag repeats; it never splits on commas. One value, one key, the way the
// rest of this CLI works.
func TestOnlySettingsIsRepeatableNotCommaSeparated(t *testing.T) {
	var got repeatedFlag
	for _, v := range []string{"a", "b,c"} {
		if err := got.Set(v); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Join(got, "|") != "a|b,c" {
		t.Errorf("repeatedFlag = %q, want the values verbatim", got)
	}
}
