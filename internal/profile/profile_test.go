package profile

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ackstorm/agent-profile/internal/agent"
)

func agentOrFail(t *testing.T, name string) agent.Agent {
	t.Helper()
	a, ok := agent.Lookup(name)
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
	if len(got) != 1 || got[0] != "plan" {
		t.Errorf("List = %v, want [plan]", got)
	}
}

// Profiles of one agent must not show up under another.
func TestListIsPerAgent(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if _, err := Create(agentOrFail(t, "claude"), "plan"); err != nil {
		t.Fatal(err)
	}
	got, err := List(agentOrFail(t, "codex"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("codex List = %v, want empty", got)
	}
}

func TestCreateRejectsExisting(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	a := agentOrFail(t, "claude")
	if _, err := Create(a, "plan"); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(a, "plan"); err == nil {
		t.Error("second Create = nil error, want already-exists error")
	}
}

func TestListEmptyIsNotAnError(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	got, err := List(agentOrFail(t, "codex"))
	if err != nil {
		t.Fatalf("List on missing dir: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("List = %v, want empty", got)
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
