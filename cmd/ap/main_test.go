//go:build unix

package main

import (
	"encoding/json"
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
	if err := dispatch([]string{"delete", "claude:nolinkdir"}); err != nil {
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
	for _, args := range [][]string{{"create", "claude:temp"}, {"link", "claude:temp"}, {"delete", "claude:temp"}} {
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
