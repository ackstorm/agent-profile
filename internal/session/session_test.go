package session

import (
	"os"
	"path/filepath"
	"testing"
)

// codex writes one session_meta line first, carrying the id, the cwd and the
// timestamp. Nothing else in the file needs reading.
func TestReadCodexSession(t *testing.T) {
	dir := t.TempDir()
	day := filepath.Join(dir, "sessions", "2026", "06", "24")
	if err := os.MkdirAll(day, 0o700); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(day, "rollout-2026-06-24T11-04-51-019ef8e0-060c-7ef0-b878-63c558abbb23.jsonl")
	body := `{"timestamp":"2026-06-24T09:06:39.649Z","type":"session_meta","payload":{"session_id":"019ef8e0-060c-7ef0-b878-63c558abbb23","cwd":"/tmp/project1","cli_version":"0.142.0"}}
{"type":"message","payload":{"role":"user"}}
`
	if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := readCodex(f)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "019ef8e0-060c-7ef0-b878-63c558abbb23" {
		t.Errorf("ID = %q", got.ID)
	}
	if got.Dir != "/tmp/project1" {
		t.Errorf("Dir = %q, want /tmp/project1", got.Dir)
	}
}

// A file whose first line is not session_meta is not a session. Returning a
// zero-valued record would put a row with no id in the listing, and `ap resume`
// on it would exec the agent with an empty argument.
func TestReadCodexRejectsAFileWithNoMeta(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "rollout-x.jsonl")
	if err := os.WriteFile(f, []byte("{\"type\":\"message\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readCodex(f); err == nil {
		t.Error("readCodex accepted a file with no session_meta")
	}
}
