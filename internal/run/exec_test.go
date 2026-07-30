//go:build unix

package run

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ackstorm/agent-profile/internal/agent"
)

func TestExecMissingBinaryGivesUsefulError(t *testing.T) {
	a := agent.Agent{Name: "nope", Bin: "definitely-not-installed-xyz", ConfigEnv: "X_DIR"}
	err := Exec(a, t.TempDir(), nil)
	if err == nil {
		t.Fatal("Exec = nil error, want not-found")
	}
	if !strings.Contains(err.Error(), "definitely-not-installed-xyz") {
		t.Errorf("error %q does not name the missing binary", err)
	}
}

// ExecBin runs something that is not the agent - an installer that writes into
// the agent's config directory - and it has to arrive with the agent's variable
// set anyway, or it writes where the agent will never look. Same child-process
// technique as the test below.
func TestExecBinRunsAnotherBinaryWithTheAgentsVariable(t *testing.T) {
	if os.Getenv("AP_EXEC_CHILD") == "1" {
		a := agent.Agent{Name: "fake", Bin: "fake-agent", ConfigEnv: "FAKE_CONFIG_DIR"}
		if err := ExecBin(a, "/p/plan", "fake-installer", []string{"add", "skill"}); err != nil {
			os.Stderr.WriteString("exec failed: " + err.Error())
			os.Exit(3)
		}
		return
	}

	bin := t.TempDir()
	// Only the installer exists on PATH. If ExecBin looked up a.Bin instead of
	// the binary it was given, the child would fail to find fake-agent.
	script := filepath.Join(bin, "fake-installer")
	body := "#!/bin/sh\necho \"cfg=$FAKE_CONFIG_DIR argv=$*\"\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestExecBinRunsAnotherBinaryWithTheAgentsVariable")
	cmd.Env = append(os.Environ(), "AP_EXEC_CHILD=1", "PATH="+bin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "cfg=/p/plan argv=add skill") {
		t.Errorf("child output = %q, want cfg=/p/plan argv=add skill", out)
	}
}

// Exec replaces the process, so it is verified from a child: a fake agent on
// PATH prints its config variable and argv, and we assert both arrived.
func TestExecPassesConfigVarAndArgs(t *testing.T) {
	if os.Getenv("AP_EXEC_CHILD") == "1" {
		a := agent.Agent{Name: "fake", Bin: "fake-agent", ConfigEnv: "FAKE_CONFIG_DIR"}
		if err := Exec(a, "/p/plan", []string{"plugin", "install", "x"}); err != nil {
			os.Stderr.WriteString("exec failed: " + err.Error())
			os.Exit(3)
		}
		return
	}

	bin := t.TempDir()
	script := filepath.Join(bin, "fake-agent")
	body := "#!/bin/sh\necho \"cfg=$FAKE_CONFIG_DIR argv=$*\"\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestExecPassesConfigVarAndArgs")
	cmd.Env = append(os.Environ(), "AP_EXEC_CHILD=1", "PATH="+bin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "cfg=/p/plan argv=plugin install x") {
		t.Errorf("child output = %q, want cfg=/p/plan argv=plugin install x", out)
	}
}
