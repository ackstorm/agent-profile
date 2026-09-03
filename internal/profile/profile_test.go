package profile

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ackstorm/agent-profile/pkg/agentreg"
)

func agentOrFail(t *testing.T, name string) agentreg.Agent {
	t.Helper()
	a, ok := agentreg.Lookup(name)
	if !ok {
		t.Fatalf("agent %q not in registry", name)
	}
	return a
}

func TestRootHonoursXDGDataHome(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/tmp/xdg-data")
	want := filepath.Join("/tmp/xdg-data", "agent-profile", "profiles")
	if got := Root(); got != want {
		t.Errorf("Root() = %q, want %q", got, want)
	}
}

func TestParseRef(t *testing.T) {
	tests := []struct {
		in          string
		agent, prof string
		wantErr     bool
	}{
		{in: "claude:plan", agent: "claude", prof: "plan"},
		{in: "codex:review", agent: "codex", prof: "review"},
		{in: "opencode:my_profile-2", agent: "opencode", prof: "my_profile-2"},
		{in: "claude", wantErr: true},
		{in: ":plan", wantErr: true},
		{in: "claude:", wantErr: true},
		{in: "claude:pl:an", wantErr: true},
		{in: "nope:plan", wantErr: true},        // unknown agent
		{in: "claude:../escape", wantErr: true}, // path traversal
		{in: "claude:a/b", wantErr: true},
		{in: "claude:.hidden", wantErr: true},
		{in: "claude:has space", wantErr: true},
	}
	for _, tc := range tests {
		a, p, err := ParseRef(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseRef(%q) = (%q,%q,nil), want error", tc.in, a.Name, p)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseRef(%q) unexpected error: %v", tc.in, err)
			continue
		}
		if a.Name != tc.agent || p != tc.prof {
			t.Errorf("ParseRef(%q) = (%q,%q), want (%q,%q)", tc.in, a.Name, p, tc.agent, tc.prof)
		}
	}
}

func TestCreateAndList(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	a := agentOrFail(t, "claude")
	dir, err := Create(a, "plan")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Fatalf("Create did not make a directory at %s: %v", dir, err)
	}
	got, err := List(a)
	if err != nil {
		t.Fatal(err)
	}
	// agentreg.Default is always prepended, ahead of created profiles.
	if len(got) != 2 || got[0] != agentreg.Default || got[1] != "plan" {
		t.Errorf("List = %v, want [%s plan]", got, agentreg.Default)
	}
}

// Profiles of one agent must not show up under another. codex has no created
// profiles at all, so its List holds nothing but the always-present agentreg.Default.
func TestListIsPerAgent(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if _, err := Create(agentOrFail(t, "claude"), "plan"); err != nil {
		t.Fatal(err)
	}
	got, err := List(agentOrFail(t, "codex"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != agentreg.Default {
		t.Errorf("codex List = %v, want [%s]", got, agentreg.Default)
	}
}

func TestCreateRejectsExisting(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	a := agentOrFail(t, "claude")
	if _, err := Create(a, "plan"); err != nil {
		t.Fatal(err)
	}
	_, err := Create(a, "plan")
	if err == nil {
		t.Fatal("second Create = nil error, want already-exists error")
	}
	// And it names the way forward. `ap create` on an existing profile is
	// usually someone after its wrapper — a profile `ap manifest apply` built
	// before apply provisioned one — and a bare refusal stranded them.
	if !strings.Contains(err.Error(), "ap link claude:plan") {
		t.Errorf("the refusal does not name `ap link`: %v", err)
	}
}

// A missing profile directory is not an error; List reports only agentreg.Default.
func TestListEmptyIsNotAnError(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	got, err := List(agentOrFail(t, "codex"))
	if err != nil {
		t.Fatalf("List on missing dir: %v", err)
	}
	if len(got) != 1 || got[0] != agentreg.Default {
		t.Errorf("List = %v, want [%s]", got, agentreg.Default)
	}
}

func TestExists(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	a := agentOrFail(t, "claude")
	if Exists(a, "plan") {
		t.Error("Exists = true before Create")
	}
	if _, err := Create(a, "plan"); err != nil {
		t.Fatal(err)
	}
	if !Exists(a, "plan") {
		t.Error("Exists = false after Create")
	}
}

// agentreg.Default names the agent's real config directory, never a directory ap
// creates. Dir must resolve it that way for `ap which <agent>:default` and for
// --from default to find something to clone.
func TestWhichDefaultPrintsTheRealConfigDir(t *testing.T) {
	a := agentOrFail(t, "claude")
	if got := Dir(a, agentreg.Default); got != a.Config {
		t.Errorf("Dir(default) = %q, want %q", got, a.Config)
	}
}

// Exists must track whether the real config directory happens to exist on this
// machine, the same as Dir(a, agentreg.Default) resolving to a.Config would imply — not
// hardcode true, which would make `ap which claude:default` look usable on a
// machine where claude was never even installed.
func TestExistsForDefaultTracksTheRealConfigDir(t *testing.T) {
	a := agentOrFail(t, "claude")
	_, statErr := os.Stat(a.Config)
	want := statErr == nil
	if got := Exists(a, agentreg.Default); got != want {
		t.Errorf("Exists(default) = %v, want %v (whether %s exists)", got, want, a.Config)
	}
}

// With no profiles created at all, agentreg.Default is still there: it names the real
// config, not something `ap create` produced.
func TestListAlwaysIncludesDefault(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	out, err := List(agentOrFail(t, "claude"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(out, agentreg.Default) {
		t.Errorf("List = %v, want it to include %q", out, agentreg.Default)
	}
}

// agentreg.Default is reserved: Dir(a, "default") is the user's real config directory,
// and every writing path routes through agentreg.ValidName, so this is what makes
// `ap create claude:default` and `ap delete claude:default` refuse.
func TestValidNameRejectsDefault(t *testing.T) {
	if err := agentreg.ValidName(agentreg.Default); err == nil {
		t.Error("agentreg.ValidName(agentreg.Default) = nil error, want a reservation error")
	}
}

// ParseRef is the writing path and must keep rejecting agentreg.Default. ParseRefAllowDefault
// is the escape hatch for the four read-only paths (run, which, env, --from) that may
// resolve to the real config directory.
func TestParseRefRejectsDefaultButParseRefAllowDefaultAccepts(t *testing.T) {
	if _, _, err := ParseRef("claude:default"); err == nil {
		t.Error("ParseRef(claude:default) = nil error, want rejection")
	}
	a, name, err := ParseRefAllowDefault("claude:default")
	if err != nil {
		t.Fatalf("ParseRefAllowDefault(claude:default): %v", err)
	}
	if name != agentreg.Default || a.Name != "claude" {
		t.Errorf("ParseRefAllowDefault(claude:default) = (%q,%q), want (claude,%q)", a.Name, name, agentreg.Default)
	}
}

// ParseRefAllowDefault must still apply every other agentreg.ValidName rule; the
// allowance is for the literal sentinel, not a bypass of validation.
func TestParseRefAllowDefaultStillRejectsTraversal(t *testing.T) {
	if _, _, err := ParseRefAllowDefault("claude:../escape"); err == nil {
		t.Error("ParseRefAllowDefault(claude:../escape) = nil error, want rejection")
	}
}

// Depth is exactly three. The third segment is validated by agentreg.ValidName
// — so `default` is refused as a VARIANT name, because that name would be a file
// ap creates — while the profile segment is validated with the sentinel allowed,
// so `claude:default:opus` parses. What a bare `claude:default` means is each
// command's own decision, not the parser's.
func TestParseVariantRef(t *testing.T) {
	tests := []struct {
		in                   string
		agent, prof, variant string
		wantErr              bool
	}{
		{in: "claude:review", agent: "claude", prof: "review"},
		{in: "claude:review:opus", agent: "claude", prof: "review", variant: "opus"},
		{in: "codex:plan:ci", agent: "codex", prof: "plan", variant: "ci"},
		{in: "claude:review:opus:extra", wantErr: true}, // four segments, permanently
		{in: "claude:review:default", wantErr: true},    // reserved as a variant name too
		{in: "claude:default:opus", agent: "claude", prof: "default", variant: "opus"},
		{in: "claude:review:", wantErr: true},
		{in: "claude:review:a/b", wantErr: true},
		{in: "claude:review:.hidden", wantErr: true},
		{in: "claude:review:..", wantErr: true},
		{in: "nope:review:opus", wantErr: true},
		{in: "claude", wantErr: true},
		{in: "claude:default", agent: "claude", prof: "default"}, // each command decides
	}
	for _, tc := range tests {
		a, p, v, err := ParseVariantRef(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseVariantRef(%q) = (%q,%q,%q,nil), want error", tc.in, a.Name, p, v)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseVariantRef(%q) unexpected error: %v", tc.in, err)
			continue
		}
		if a.Name != tc.agent || p != tc.prof || v != tc.variant {
			t.Errorf("ParseVariantRef(%q) = (%q,%q,%q), want (%q,%q,%q)",
				tc.in, a.Name, p, v, tc.agent, tc.prof, tc.variant)
		}
	}
}

// A variant over the sentinel parses, and the sentinel is still refused as the
// variant NAME. The pair is the whole rule: a variant is one file under
// VariantsRoot, so hanging one off `default` writes nothing inside the agent's
// real config directory — but naming one `default` would create exactly the
// file the sentinel promises never exists.
func TestAVariantMayHangOffTheRealConfigButMayNotBeNamedForIt(t *testing.T) {
	if _, p, v, err := ParseVariantRef("claude:default"); err != nil || p != agentreg.Default || v != "" {
		t.Errorf(`ParseVariantRef("claude:default") = (%q,%q,%v), want ("default","",nil)`, p, v, err)
	}
	if _, p, v, err := ParseVariantRef("claude:default:opus"); err != nil || p != agentreg.Default || v != "opus" {
		t.Errorf(`ParseVariantRef("claude:default:opus") = (%q,%q,%v), want ("default","opus",nil)`, p, v, err)
	}
	if _, _, _, err := ParseVariantRef("claude:review:default"); err == nil {
		t.Error("a variant NAMED default was accepted; that name is a file ap would create")
	}
	if _, p, v, err := ParseVariantRef("claude:review:opus"); err != nil || p != "review" || v != "opus" {
		t.Errorf("ParseVariantRef on a normal variant = (%q,%q,%v)", p, v, err)
	}
}

// `ap create` keeps the strict two-segment parser: a profile is the only thing
// it makes, and `ap variant` is the verb for the other one.
func TestParseRefStillRefusesThreeSegments(t *testing.T) {
	if _, _, err := ParseRef("claude:review:opus"); err == nil {
		t.Error("ParseRef accepted a three-segment reference; create would then make a profile named for a variant")
	}
}
