package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ackstorm/agent-profile/internal/agent"
	"github.com/ackstorm/agent-profile/internal/profile"
	"github.com/ackstorm/agent-profile/internal/run"
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

// pi writes {"type":"session",...} first, with id, timestamp and cwd. Its
// directory names encode the cwd too, but with the same hyphen ambiguity claude
// has, so the file is the only trustworthy source.
func TestReadPiSession(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "2026-07-23T09-47-18-290Z_019f8e5f-4d92-7906-a769-bb2437e3dfe9.jsonl")
	body := `{"type":"session","version":3,"id":"019f8e5f-4d92-7906-a769-bb2437e3dfe9","timestamp":"2026-07-23T09:47:18.290Z","cwd":"/home/jcm/.pi/agent"}
`
	if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readPi(f)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "019f8e5f-4d92-7906-a769-bb2437e3dfe9" || got.Dir != "/home/jcm/.pi/agent" {
		t.Errorf("got %+v", got)
	}
}

// claude's first lines are bookkeeping; cwd appears a few lines in and the
// ai-title around line 20. The id is the filename.
func TestReadClaudeSession(t *testing.T) {
	dir := t.TempDir()
	id := "db5b0ec4-e90d-4f0e-8ebd-28bfe677f5a2"
	var b strings.Builder
	b.WriteString(`{"type":"last-prompt","sessionId":"` + id + `"}` + "\n")
	b.WriteString(`{"type":"mode","mode":"normal"}` + "\n")
	b.WriteString(`{"type":"user","cwd":"/home/jcm/Projects/agent-profile"}` + "\n")
	b.WriteString(`{"type":"ai-title","aiTitle":"Designing a command"}` + "\n")
	f := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(f, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readClaude(f)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != id {
		t.Errorf("ID = %q, want %q", got.ID, id)
	}
	if got.Dir != "/home/jcm/Projects/agent-profile" {
		t.Errorf("Dir = %q", got.Dir)
	}
	if got.Title != "Designing a command" {
		t.Errorf("Title = %q", got.Title)
	}
}

// A transcript with no ai-title is normal — one measured file was 24.9 MB and had
// none. The reader must stop early and still return the cwd, rather than reading
// megabytes to find something that is not there.
func TestReadClaudeStopsAtTheBound(t *testing.T) {
	dir := t.TempDir()
	id := "aaaaaaaa-0000-0000-0000-000000000000"
	var b strings.Builder
	b.WriteString(`{"type":"user","cwd":"/tmp/here"}` + "\n")
	for i := 0; i < maxMetaLines*4; i++ {
		b.WriteString(`{"type":"assistant","text":"` + strings.Repeat("x", 2000) + `"}` + "\n")
	}
	// Past the bound on purpose: if this is found, the reader did not stop.
	b.WriteString(`{"type":"ai-title","aiTitle":"TOO LATE"}` + "\n")
	f := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(f, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readClaude(f)
	if err != nil {
		t.Fatal(err)
	}
	if got.Dir != "/tmp/here" {
		t.Errorf("Dir = %q, want /tmp/here", got.Dir)
	}
	if got.Title == "TOO LATE" {
		t.Error("reader went past the bound: a 25 MB transcript would be read whole")
	}
}

// The directory name is NOT the cwd. `/` and `.` both encode to `-`, so
// -home-jcm--claude is /home/jcm/.claude and -home-jcm-Projects-agent-profile is
// indistinguishable from /home/jcm/Projects/agent/profile. A reader that decoded
// it would chdir somewhere that does not exist, or worse, somewhere that does.
func TestReadClaudeIgnoresTheDirectoryName(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "-home-jcm-Projects-agent-profile")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(dir, "bbbbbbbb-0000-0000-0000-000000000000.jsonl")
	if err := os.WriteFile(f, []byte(`{"type":"user","cwd":"/real/place"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readClaude(f)
	if err != nil {
		t.Fatal(err)
	}
	if got.Dir != "/real/place" {
		t.Errorf("Dir = %q, want the cwd from inside the file", got.Dir)
	}
}

// opencode session list --format json, as measured. The parser is tested
// separately from the subprocess: running opencode needs a login and a network,
// which belongs in smoke, not in a unit test.
func TestParseOpencodeSessions(t *testing.T) {
	body := `[
	  {"id":"ses_04c349ff8ffeUR9Xu92KuB3fkL","title":"MCP Authentication","updated":1785427932102,"created":1785427877895,"directory":"/tmp/kiko2"},
	  {"id":"ses_05cf77d8bffeE0v0ND4GOQ1Ya9","title":"Directory listing","updated":1785146677843,"created":1785146600000,"directory":"/home/jcm"}
	]`
	got, err := parseOpencode([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d sessions, want 2", len(got))
	}
	if got[0].ID != "ses_04c349ff8ffeUR9Xu92KuB3fkL" || got[0].Dir != "/tmp/kiko2" {
		t.Errorf("got %+v", got[0])
	}
	if got[0].Title != "MCP Authentication" {
		t.Errorf("Title = %q", got[0].Title)
	}
	// Milliseconds since the epoch, not seconds. Getting this wrong dates every
	// opencode session to 1970 and sorts them all to the bottom.
	if y := got[0].Updated.Year(); y < 2020 || y > 2100 {
		t.Errorf("Updated = %v, want a plausible year (ms, not s)", got[0].Updated)
	}
}

func mixedSessions() []Session {
	t1 := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 8, 5, 11, 0, 0, 0, time.UTC)
	return []Session{
		{ID: "s1", Updated: t1},
		{ID: "s2", Updated: t2},
		{ID: "s3", Updated: t3},
	}
}

// Sessions come back newest first, across agents and profiles, capped at max.
func TestScanOrdersNewestFirstAndCaps(t *testing.T) {
	got := sortAndCap(mixedSessions(), 2)
	if len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}
	if !got[0].Updated.After(got[1].Updated) {
		t.Errorf("not ordered newest first: %v then %v", got[0].Updated, got[1].Updated)
	}
	if got[0].ID != "s2" || got[1].ID != "s3" {
		t.Errorf("got ids %s, %s; want s2, s3", got[0].ID, got[1].ID)
	}
}

func manyClaudeTranscripts(t *testing.T, count int) (string, []string) {
	dir := t.TempDir()
	files := make([]string, 0, count)
	now := time.Now()
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("%08d-0000-0000-0000-000000000000", i)
		f := filepath.Join(dir, id+".jsonl")
		body := fmt.Sprintf(`{"type":"user","cwd":"/project/%d"}`+"\n", i)
		if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		// Set mod time so ordering is deterministic
		mt := now.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(f, mt, mt); err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	return dir, files
}

// Only the files that will be printed are opened. There are 351 transcripts and
// 427 MB under ~/.claude/projects on the reference machine; opening all of them to
// print ten is the difference between an instant command and a slow one.
func TestScanOpensOnlyWhatItPrints(t *testing.T) {
	dir, files := manyClaudeTranscripts(t, 50)
	opened := 0
	countingOpen := func(p string) (Session, error) { opened++; return readClaude(p) }
	if _, err := scanClaude(dir, "claude", "execute", 5, countingOpen); err != nil {
		t.Fatal(err)
	}
	if opened > 5 {
		t.Errorf("opened %d transcripts to print 5: sort by mtime before reading", opened)
	}
	_ = files
}

// profile.Default is not a profile: it names the agent's real configuration, and
// reaching it means inheriting the user's environment untouched. Scan used to
// hand run.Env the directory profile.Dir resolves Default to, which produced a
// real override pointing at <real config>/xdg-data — a directory that does not
// exist. Measured: opencode listed nothing for :default and would have created
// state inside the user's own config directory.
func TestEnvDirIsEmptyForDefault(t *testing.T) {
	a, ok := agent.Lookup("opencode")
	if !ok {
		t.Fatal("no opencode in the registry")
	}
	if got := envDir(a, profile.Default); got != "" {
		t.Errorf("envDir(default) = %q, want empty so run.Env sets no override", got)
	}
	if got := envDir(a, "plan"); got == "" {
		t.Error("envDir on a real profile is empty: it would inherit instead of isolating")
	}

	// The property that actually matters, stated against run.Env itself.
	base := []string{"XDG_DATA_HOME=/real/share", "XDG_CONFIG_HOME=/real/config"}
	for _, e := range run.Env(a, envDir(a, profile.Default), base) {
		if strings.HasPrefix(e, "XDG_DATA_HOME=") && e != "XDG_DATA_HOME=/real/share" {
			t.Errorf("scanning :default rewrote the environment: %s", e)
		}
	}
}

// The cap is the last thing applied, never the first. Filtering after a global
// cap makes `ap sessions claude:finops` answer "No sessions found" while finops
// has three, because fifty busier sessions filled the window first.
func TestDirectoryFilterIsAppliedBeforeTheCap(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	// Ten transcripts; only the oldest belongs to /wanted.
	for i := 0; i < 10; i++ {
		want := "/other"
		if i == 0 {
			want = "/wanted"
		}
		f := filepath.Join(dir, fmt.Sprintf("%08d-0000-0000-0000-000000000000.jsonl", i))
		body := fmt.Sprintf(`{"type":"user","cwd":%q}`+"\n", want)
		if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		mt := now.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(f, mt, mt); err != nil {
			t.Fatal(err)
		}
	}
	got, err := scanFileStore(dir, "claude", "x", 3, Filter{Dir: "/wanted"}, readClaude, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d sessions, want the one in /wanted: filtering happens after the cap", len(got))
	}
	if got[0].Dir != "/wanted" {
		t.Errorf("Dir = %q", got[0].Dir)
	}
}

// claude's ai-title sits past 64 KiB in most real transcripts: measured between
// 60,961 and 79,948 bytes across the eight most recent, five of them over. A
// 64 KiB budget therefore drops the title from the majority of rows while
// looking like it works.
func TestReadClaudeFindsATitlePastSixtyFourKilobytes(t *testing.T) {
	dir := t.TempDir()
	id := "cccccccc-0000-0000-0000-000000000000"
	var b strings.Builder
	b.WriteString(`{"type":"user","cwd":"/tmp/here"}` + "\n")
	// Eight lines of 10 KB each puts the title at ~80 KB, like the real ones.
	for i := 0; i < 8; i++ {
		b.WriteString(`{"type":"assistant","text":"` + strings.Repeat("x", 10000) + `"}` + "\n")
	}
	b.WriteString(`{"type":"ai-title","aiTitle":"Found past the old budget"}` + "\n")
	f := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(f, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readClaude(f)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Found past the old budget" {
		t.Errorf("Title = %q: the byte budget cuts before the title real transcripts carry", got.Title)
	}
}

// A single very long line must not lose the whole session. The longest measured
// first-50 line was 97,988 bytes, which overflows a 64 KiB scanner buffer and
// makes Scan return "no cwd in the first 50 lines" for a perfectly good session.
func TestReadClaudeSurvivesAVeryLongLine(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "dddddddd-0000-0000-0000-000000000000.jsonl")
	body := `{"type":"attachment","content":"` + strings.Repeat("y", 98000) + `"}` + "\n" +
		`{"type":"user","cwd":"/tmp/survived"}` + "\n"
	if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readClaude(f)
	if err != nil {
		t.Fatalf("a 98 KB line dropped the session: %v", err)
	}
	if got.Dir != "/tmp/survived" {
		t.Errorf("Dir = %q", got.Dir)
	}
}

// A stub transcript is not an error worth printing. Three files of ~146 bytes —
// sessions that died before writing anything — put three warning lines on stderr
// on every single `ap sessions`, which trains people to ignore warnings.
func TestAStubTranscriptIsSkippedQuietly(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "eeeeeeee-0000-0000-0000-000000000000.jsonl")
	body := `{"type":"last-prompt","leafUuid":"x","sessionId":"eeeeeeee-0000-0000-0000-000000000000"}` + "\n"
	if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	warned := 0
	got, err := scanFileStore(dir, "claude", "x", 10, Filter{}, readClaude, func(error) { warned++ })
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %d sessions from a stub transcript", len(got))
	}
	if warned != 0 {
		t.Errorf("warned %d times about a transcript that is simply not a session", warned)
	}
}

// Measured: opencode prints nothing at all when the current project has no
// sessions, not "[]". That is the common case, since its listing is project
// scoped, so treating it as a parse error means a warning on nearly every run.
func TestParseOpencodeAcceptsEmptyOutput(t *testing.T) {
	for _, in := range []string{"", "  \n"} {
		got, err := parseOpencode([]byte(in))
		if err != nil {
			t.Errorf("parseOpencode(%q) errored: %v", in, err)
		}
		if len(got) != 0 {
			t.Errorf("parseOpencode(%q) returned %d sessions", in, len(got))
		}
	}
}
