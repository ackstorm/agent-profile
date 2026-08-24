package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ackstorm/agent-profile/internal/agent"
	"github.com/ackstorm/agent-profile/internal/manifest"
)

// The command runs somewhere disposable, not where ap was invoked and not where
// the manifest lives. smoke.sh learned this one the hard way: a command that
// resolved against the wrong cwd looked exactly like one that worked.
func TestRunCommandRunsInAFreshEmptyDirectoryThatIsRemoved(t *testing.T) {
	out := filepath.Join(t.TempDir(), "where")
	if err := runCommand("pwd > "+out+"; ls -A | wc -l >> "+out, os.Environ()); err != nil {
		t.Fatalf("runCommand: %v", err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(string(b))
	if len(lines) != 2 {
		t.Fatalf("captured %q, want a directory and a count", string(b))
	}
	cwd, _ := os.Getwd()
	if lines[0] == cwd {
		t.Error("the command ran in ap's own working directory")
	}
	if lines[1] != "0" {
		t.Errorf("the working directory held %s entries, want an empty one", lines[1])
	}
	if _, err := os.Stat(lines[0]); err == nil {
		t.Error("the temporary directory survived the command")
	}
}

func TestRunCommandSeesExactlyTheEnvironmentItIsGiven(t *testing.T) {
	out := filepath.Join(t.TempDir(), "env")
	err := runCommand("printf '%s\\n' \"${AP_TEST_MARKER:-unset}\" \"${AP_TEST_ABSENT:-unset}\" > "+out,
		[]string{"PATH=" + os.Getenv("PATH"), "AP_TEST_MARKER=here"})
	if err != nil {
		t.Fatalf("runCommand: %v", err)
	}
	b, _ := os.ReadFile(out)
	if got := strings.Fields(string(b)); len(got) != 2 || got[0] != "here" || got[1] != "unset" {
		t.Fatalf("environment = %q, want [here unset]", got)
	}
}

func TestRunCommandReportsAFailingCommand(t *testing.T) {
	if err := runCommand("exit 3", os.Environ()); err == nil {
		t.Fatal("runCommand(exit 3) = nil error, want a non-zero exit")
	}
}

// A command must not be able to eat the terminal ap has already finished asking
// its questions on, and in a script a blocked read would sit there until the
// timeout ten minutes later.
func TestRunCommandGivesTheCommandNoStdin(t *testing.T) {
	// Real bytes are put on ap's OWN stdin first. Without them the check is
	// vacuous — under `go test` stdin is normally already at EOF, so a command
	// that did inherit it would read nothing and look exactly like one that was
	// given none. Found by mutation: c.Stdin = os.Stdin left this test green.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_, _ = w.WriteString("leaked\n")
		_ = w.Close()
	}()
	old := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = old; _ = r.Close() }()

	out := filepath.Join(t.TempDir(), "in")
	if err = runCommand("cat > "+out+" 2>/dev/null; echo done >> "+out, os.Environ()); err != nil {
		t.Fatalf("runCommand: %v", err)
	}
	b, _ := os.ReadFile(out)
	if strings.TrimSpace(string(b)) != "done" {
		t.Fatalf("the command read %q from stdin, want nothing", string(b))
	}
}
func TestBuildPlanResolvesWhatTheReportWillClaim(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("AP_LINK_DIR", t.TempDir())

	// One existing profile with one existing variant, so both verbs appear.
	if err := dispatch([]string{"create", "claude:execute"}); err != nil {
		t.Fatal(err)
	}
	if err := dispatch([]string{"variant", "claude:execute:opus", "--", "--old"}); err != nil {
		t.Fatal(err)
	}

	src := `version: 1
name: execute
bootstrap:
  - true
platforms:
  claude:
    install:
      - true
    variants:
      opus:
        args: --model=claude-opus-5
      fresh:
        args: -p
  codex:
`
	m, err := manifest.Parse("execute.yaml", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	p := buildPlan([]manifest.Manifest{m})

	if len(p.files) != 1 || len(p.files[0].targets) != 2 {
		t.Fatalf("plan = %+v, want one file with two targets", p)
	}
	c := p.files[0].targets[0]
	if c.ref != "claude:execute" || !c.exists {
		t.Fatalf("claude target = %+v, want an existing claude:execute", c)
	}
	if k := p.files[0].targets[1]; k.ref != "codex:execute" || k.exists {
		t.Fatalf("codex target = %+v, want a non-existent codex:execute", k)
	}
	// Sorted by variant name, and each carrying the verb the report will use.
	if len(c.variants) != 2 || c.variants[0].name != "fresh" || c.variants[0].verb != "created" {
		t.Fatalf("variants[0] = %+v, want fresh (created)", c.variants)
	}
	if c.variants[1].name != "opus" || c.variants[1].verb != "updated" {
		t.Fatalf("variants[1] = %+v, want opus (updated)", c.variants[1])
	}
	if !p.hasCommands() {
		t.Error("hasCommands = false with a bootstrap and an install declared")
	}
	if len(p.defaultTargets()) != 0 {
		t.Error("defaultTargets is not empty for a manifest that names no default")
	}
}

func TestBuildPlanPointsADefaultTargetAtTheRealConfigDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	m, err := manifest.Parse("d.yaml", []byte("version: 1\nname: default\nplatforms:\n  claude:\n    install:\n      - true\n"))
	if err != nil {
		t.Fatal(err)
	}
	p := buildPlan([]manifest.Manifest{m})
	d := p.defaultTargets()
	if len(d) != 1 {
		t.Fatalf("defaultTargets = %+v, want one", d)
	}
	if !d[0].isDefault || d[0].path != filepath.Join(home, ".claude") {
		t.Fatalf("default target = %+v, want isDefault and %s", d[0], filepath.Join(home, ".claude"))
	}
}

// --dry-run changes nothing: no profile root, no variants, and nothing ran.
func TestSyncDryRunChangesNothing(t *testing.T) {
	home := t.TempDir()
	data := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("AP_LINK_DIR", t.TempDir())

	marker := filepath.Join(t.TempDir(), "ran")
	dir := t.TempDir()
	src := "version: 1\nname: execute\nbootstrap:\n  - touch " + marker +
		"\nplatforms:\n  claude:\n    install:\n      - touch " + marker +
		"\n    variants:\n      opus:\n        args: --model=claude-opus-5\n"
	if err := os.WriteFile(filepath.Join(dir, "execute.yaml"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := dispatch([]string{"sync", dir, "--dry-run"}); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("--dry-run ran a command")
	}
	if _, err := os.Stat(filepath.Join(data, "agent-profile", "profiles", "claude", "execute")); err == nil {
		t.Error("--dry-run created a profile")
	}
	if _, err := os.Stat(filepath.Join(data, "agent-profile", "variants", "claude", "execute", "opus")); err == nil {
		t.Error("--dry-run wrote a variant")
	}
}

// notATerminal points os.Stdin at a pipe for the test's duration.
//
// Without it these tests would depend on whether the shell that started `go
// test` had a terminal, which is not a property of the code under test:
// scripts/dev.sh passes -it, so inside the container it does, and gate would
// take the interactive path and read an answer nobody typed. The gates below
// are all statements about the OFF-a-terminal path, so it is pinned here.
func notATerminal(t *testing.T) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = old
		_ = r.Close()
		_ = w.Close()
	})
}

// stdin in `go test` is not a character device, so every call below is on the
// off-a-terminal path — which is the path that must never run anything.

func TestGateRefusesCommandsOffATerminalWithoutYes(t *testing.T) {
	notATerminal(t)
	p := syncPlan{files: []filePlan{{path: "x.yaml", bootstrap: []string{"true"}}}}
	err := gate(p, false)
	if err == nil {
		t.Fatal("gate = nil error off a terminal, want a refusal")
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Errorf("error = %q, want it to name --yes", err)
	}
}

func TestGateAllowsCommandsOffATerminalWithYes(t *testing.T) {
	notATerminal(t)
	p := syncPlan{files: []filePlan{{path: "x.yaml", bootstrap: []string{"true"}}}}
	if err := gate(p, true); err != nil {
		t.Fatalf("gate(--yes) = %v, want nil", err)
	}
}

// A manifest that runs nothing never prompts and never refuses: creating
// profiles and writing variants runs no third-party code.
func TestGateNeverAsksAboutAManifestThatRunsNothing(t *testing.T) {
	notATerminal(t)
	p := syncPlan{files: []filePlan{{
		path:    "x.yaml",
		targets: []targetPlan{{ref: "claude:x", variants: []variantPlan{{name: "v", args: []string{"-p"}, verb: "created"}}}},
	}}}
	if err := gate(p, false); err != nil {
		t.Fatalf("gate = %v for a plan with no commands, want nil", err)
	}
}

// The second gate is gone by request: --yes covers a default target too. What
// must NOT come back is silence — the plan is still shown, and off a terminal
// the single gate still refuses. See gate's comment for what was traded away.
func TestGateYesCoversADefaultTarget(t *testing.T) {
	notATerminal(t)
	p := syncPlan{files: []filePlan{{
		path: "d.yaml",
		targets: []targetPlan{{
			ref: "claude:default", name: "default", isDefault: true,
			path: "/home/me/.claude", install: []string{"true"},
		}},
	}}}
	if err := gate(p, true); err != nil {
		t.Fatalf("gate(--yes) = %v for a default target, want nil", err)
	}
	// Without --yes and without a terminal it still refuses, naming the one
	// flag there is.
	err := gate(p, false)
	if err == nil {
		t.Fatal("gate off a terminal = nil error, want a refusal")
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Errorf("error = %q, want it to name --yes", err)
	}
}

// syncFixture writes one manifest into a fresh directory and points HOME, the
// profile root and the wrapper directory at throwaway ones.
func syncFixture(t *testing.T, body string) (dir string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("AP_LINK_DIR", t.TempDir())
	dir = t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "m.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// §8.2: bootstrap is step 8a and runs before step 8b, so there is no profile in
// existence for it to write into. Asserted by having the command look.
func TestSyncBootstrapRunsBeforeAnyProfileExists(t *testing.T) {
	out := filepath.Join(t.TempDir(), "seen")
	dir := syncFixture(t, "version: 1\nname: execute\nbootstrap:\n  - ls -A \"$XDG_DATA_HOME/agent-profile/profiles\" > "+out+" 2>&1 || true\nplatforms:\n  claude:\n")
	if err := dispatch([]string{"sync", dir, "--yes"}); err != nil {
		t.Fatalf("sync: %v", err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("the bootstrap command did not run: %v", err)
	}
	if strings.Contains(string(b), "claude") {
		t.Errorf("bootstrap saw a profile root holding %q; it must run before 8b", string(b))
	}
}

// §8.2 again: no agent variable is set at all, even though the manifest
// declares three platforms. There is no platform whose environment a bootstrap
// command could be mistaken for.
func TestSyncBootstrapSetsNoAgentVariable(t *testing.T) {
	out := filepath.Join(t.TempDir(), "env")
	dir := syncFixture(t, "version: 1\nname: execute\nbootstrap:\n"+
		"  - printf '%s|%s|%s' \"$CLAUDE_CONFIG_DIR\" \"$CODEX_HOME\" \"$PI_CODING_AGENT_DIR\" > "+out+
		"\nplatforms:\n  claude:\n  codex:\n  pi:\n")
	if err := dispatch([]string{"sync", dir, "--yes"}); err != nil {
		t.Fatalf("sync: %v", err)
	}
	b, _ := os.ReadFile(out)
	if string(b) != "||" {
		t.Errorf("bootstrap saw agent variables %q, want none", string(b))
	}
}

func TestSyncBootstrapFailureSkipsEveryPlatform(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	dir := syncFixture(t, "version: 1\nname: execute\nbootstrap:\n  - exit 1\nplatforms:\n"+
		"  claude:\n    install:\n      - touch "+marker+"\n  codex:\n    install:\n      - touch "+marker+"\n")
	if err := dispatch([]string{"sync", dir, "--yes"}); err == nil {
		t.Fatal("sync = nil error after a failed bootstrap, want non-zero")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("an install ran after its manifest's bootstrap failed")
	}
	root := filepath.Join(os.Getenv("XDG_DATA_HOME"), "agent-profile", "profiles")
	if entries, err := os.ReadDir(root); err == nil && len(entries) > 0 {
		t.Errorf("profiles were created after a failed bootstrap: %v", entries)
	}
}

// The install sees the profile it is meant to populate, through the same
// variable the agent will read.
func TestSyncInstallRunsInItsPlatformsEnvironment(t *testing.T) {
	out := filepath.Join(t.TempDir(), "env")
	dir := syncFixture(t, "version: 1\nname: execute\nplatforms:\n  claude:\n    install:\n"+
		"      - printf '%s' \"$CLAUDE_CONFIG_DIR\" > "+out+"\n")
	if err := dispatch([]string{"sync", dir, "--yes"}); err != nil {
		t.Fatalf("sync: %v", err)
	}
	b, _ := os.ReadFile(out)
	want := filepath.Join(os.Getenv("XDG_DATA_HOME"), "agent-profile", "profiles", "claude", "execute")
	if string(b) != want {
		t.Errorf("install saw CLAUDE_CONFIG_DIR=%q, want %q", string(b), want)
	}
}

// §8.3: an inherited CODEX_HOME pointing into another profile must not redirect
// a claude install.
func TestSyncInstallSeesOnlyItsOwnAgentVariable(t *testing.T) {
	out := filepath.Join(t.TempDir(), "env")
	dir := syncFixture(t, "version: 1\nname: execute\nplatforms:\n  claude:\n    install:\n"+
		"      - printf '%s' \"${CODEX_HOME:-none}\" > "+out+"\n")
	t.Setenv("CODEX_HOME", filepath.Join(os.Getenv("XDG_DATA_HOME"), "agent-profile", "profiles", "codex", "other"))
	if err := dispatch([]string{"sync", dir, "--yes"}); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if b, _ := os.ReadFile(out); string(b) != "none" {
		t.Errorf("a claude install saw CODEX_HOME=%q", string(b))
	}
}

// The mutation this stops: stripping by NAME instead of by value.
func TestSyncKeepsAConfigVariableThatPointsOutsideTheProfileRoot(t *testing.T) {
	out := filepath.Join(t.TempDir(), "env")
	dir := syncFixture(t, "version: 1\nname: execute\nplatforms:\n  claude:\n    install:\n"+
		"      - printf '%s' \"${XDG_CONFIG_HOME:-none}\" > "+out+"\n")
	mine := filepath.Join(t.TempDir(), "myconfig")
	t.Setenv("XDG_CONFIG_HOME", mine)
	if err := dispatch([]string{"sync", dir, "--yes"}); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if b, _ := os.ReadFile(out); string(b) != mine {
		t.Errorf("XDG_CONFIG_HOME reached the install as %q, want %q — stripping by name breaks npm for everyone", string(b), mine)
	}
}

// §10: an existing profile is reused untouched. Its sessions, credentials,
// settings and hand-made variants all survive.
func TestSyncReusesAnExistingProfileWithoutResetting(t *testing.T) {
	dir := syncFixture(t, "version: 1\nname: execute\nplatforms:\n  claude:\n    variants:\n      opus:\n        args: --model=claude-opus-5\n")
	if err := dispatch([]string{"create", "claude:execute"}); err != nil {
		t.Fatal(err)
	}
	pd := filepath.Join(os.Getenv("XDG_DATA_HOME"), "agent-profile", "profiles", "claude", "execute")
	keep := filepath.Join(pd, "settings.json")
	if err := os.WriteFile(keep, []byte(`{"theme":"dark"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := dispatch([]string{"sync", dir, "--yes"}); err != nil {
		t.Fatalf("sync: %v", err)
	}
	b, err := os.ReadFile(keep)
	if err != nil || string(b) != `{"theme":"dark"}` {
		t.Fatalf("the profile's own settings.json = %q, %v; sync must never reset an existing profile", string(b), err)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("XDG_DATA_HOME"), "agent-profile", "variants", "claude", "execute", "opus")); err != nil {
		t.Errorf("the variant was not written: %v", err)
	}
}

// §16, the most important test of §7: tokenizing BEFORE substituting is what
// keeps a caller's argument from changing how many tokens a variant has.
func TestSyncVariantArgsTokenizeBeforeSubstituting(t *testing.T) {
	dir := syncFixture(t, "version: 1\nname: execute\nplatforms:\n  claude:\n    variants:\n"+
		"      execute-plan:\n        args: --effort=xhigh \"/plan:run {}\"\n")
	if err := dispatch([]string{"sync", dir, "--yes"}); err != nil {
		t.Fatalf("sync: %v", err)
	}
	got, err := runArgs(mustAgent(t, "claude"), "execute", "execute-plan", []string{"fix the parser"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--effort=xhigh", "/plan:run fix the parser"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv = %q, want %q — substituting before tokenizing yields four", got, want)
	}
}

func mustAgent(t *testing.T, name string) agent.Agent {
	t.Helper()
	a, ok := agent.Lookup(name)
	if !ok {
		t.Fatalf("no agent %q", name)
	}
	return a
}

// §16: fed a literal "1" down a PIPE, not </dev/null — an empty answer also
// means no, and a check written that way would be vacuous.
func TestSyncDoesNotRunInstallOffATerminal(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	dir := syncFixture(t, "version: 1\nname: execute\nplatforms:\n  claude:\n    install:\n      - touch "+marker+"\n")

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	// "y", not "1": an answer that means NO makes this vacuous, because the
	// run then stops for the wrong reason and passes with the terminal check
	// removed. Found by mutation — the guard was replaced with `if false` and
	// this test stayed green.
	go func() {
		_, _ = w.WriteString("y\ny\n")
		_ = w.Close()
	}()
	old := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = old; _ = r.Close() }()

	err = dispatch([]string{"sync", dir})
	if err == nil {
		t.Fatal("sync off a pipe = nil error, want a refusal naming --yes")
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Errorf("error = %q, want the refusal to name --yes", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("an install ran with an answer read off a pipe; a pipe is not consent")
	}
}

// --yes now runs a default manifest's commands, and the report says which
// directory they ran against.
func TestSyncDefaultRunsUnderYes(t *testing.T) {
	notATerminal(t)
	marker := filepath.Join(t.TempDir(), "ran")
	dir := syncFixture(t, "version: 1\nname: default\nplatforms:\n  claude:\n    install:\n      - touch "+marker+"\n")
	if err := dispatch([]string{"sync", dir, "--yes"}); err != nil {
		t.Fatalf("sync --yes on a default manifest = %v, want it to run", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("the default install did not run: %v", err)
	}
}

// §6.1: nothing is created for a default target. No Create, no Link, no Shim,
// no first-run seeding, no wrapper.
func TestSyncDefaultNeverCreatesLinksOrShims(t *testing.T) {
	out := filepath.Join(t.TempDir(), "env")
	dir := syncFixture(t, "version: 1\nname: default\nplatforms:\n  claude:\n    install:\n"+
		"      - printf '%s' \"${CLAUDE_CONFIG_DIR:-none}\" > "+out+"\n")
	home := os.Getenv("HOME")
	real := filepath.Join(home, ".claude")
	if err := os.MkdirAll(real, 0o700); err != nil {
		t.Fatal(err)
	}
	link := os.Getenv("AP_LINK_DIR")

	if err := dispatch([]string{"sync", dir, "--yes"}); err != nil {
		t.Fatalf("sync: %v", err)
	}
	// The install ran with NO override at all, exactly as `ap run
	// claude:default` does.
	if b, _ := os.ReadFile(out); string(b) != "none" {
		t.Errorf("a default install saw CLAUDE_CONFIG_DIR=%q, want none", string(b))
	}
	entries, err := os.ReadDir(real)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("the real config directory gained %v; nothing is created for the sentinel", entries)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude.json")); err == nil {
		t.Error("first-run flags were seeded over the real config")
	}
	if e, _ := os.ReadDir(link); len(e) != 0 {
		t.Errorf("a wrapper was written for a default target: %v", e)
	}
}
