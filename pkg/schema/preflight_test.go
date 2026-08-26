package schema

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubBin drops an executable file named name into dir, so exec.LookPath
// against a PATH pointed at dir finds it. Hermetic: nothing installed on the
// host machine is consulted.
func stubBin(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestPreflightFailsNamingAMissingRuntimeBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // empty: nothing resolves
	err := Preflight(Profile{}, "claude")
	if err == nil || !strings.Contains(err.Error(), "claude") {
		t.Errorf("err = %v, want it to name the missing runtime CLI", err)
	}
}

func TestPreflightFailsNamingAnActiveStdioMCPWithAMissingCommand(t *testing.T) {
	dir := t.TempDir()
	stubBin(t, dir, "claude")
	t.Setenv("PATH", dir)

	p := Profile{MCPs: map[string]MCP{
		"memory": {Enabled: true, Transport: Transport{Type: "stdio", Command: "no-such-mcp-command"}},
	}}
	err := Preflight(p, "claude")
	if err == nil || !strings.Contains(err.Error(), "memory") || !strings.Contains(err.Error(), "no-such-mcp-command") {
		t.Errorf("err = %v, want it to name the mcp and its command", err)
	}
}

func TestPreflightIgnoresADisabledMCPWithAMissingCommand(t *testing.T) {
	dir := t.TempDir()
	stubBin(t, dir, "claude")
	t.Setenv("PATH", dir)

	p := Profile{MCPs: map[string]MCP{
		"memory": {Enabled: false, Transport: Transport{Type: "stdio", Command: "no-such-mcp-command"}},
	}}
	if err := Preflight(p, "claude"); err != nil {
		t.Errorf("err = %v, want nil: a disabled MCP's command must not be checked", err)
	}
}

func TestPreflightPassesWithAnActiveStdioMCPWhoseCommandResolves(t *testing.T) {
	dir := t.TempDir()
	stubBin(t, dir, "claude")
	stubBin(t, dir, "mcp-server")
	t.Setenv("PATH", dir)

	p := Profile{MCPs: map[string]MCP{
		"memory": {Enabled: true, Transport: Transport{Type: "stdio", Command: "mcp-server"}},
	}}
	if err := Preflight(p, "claude"); err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}

func TestPreflightSkipsAnHTTPMCPsCommandCheck(t *testing.T) {
	dir := t.TempDir()
	stubBin(t, dir, "claude")
	t.Setenv("PATH", dir)

	p := Profile{MCPs: map[string]MCP{
		"memory": {Enabled: true, Transport: Transport{Type: "http", URL: "https://example.com"}},
	}}
	if err := Preflight(p, "claude"); err != nil {
		t.Errorf("err = %v, want nil: an http transport has no command to resolve", err)
	}
}
