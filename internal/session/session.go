// Package session reads what each agent records about its past conversations.
//
// Four agents, four storage layouts, and none of them is a documented interface —
// every one here was read off the real files. When a reader stops finding
// sessions, assume the layout moved and re-measure; do not widen a parser until
// it matches something.
package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Session is one resumable conversation, in the only terms every agent shares.
type Session struct {
	// Agent and Profile say where it was found: "claude" and "execute", or
	// profile.Default for the bare agent outside any profile.
	Agent   string
	Profile string
	// ID is the agent's own identifier, passed back to it verbatim to resume.
	ID string
	// Dir is the absolute working directory the session belongs to. Resuming
	// anywhere else is wrong in all four agents; see the plan for the measurements.
	Dir string
	// Title is the agent's own summary, empty when it never wrote one.
	Title string
	// Updated is when the session was last written, used for ordering.
	Updated time.Time
	// Path is the file or record it came from, for error messages.
	Path string
}

// maxMetaLines and maxMetaBytes bound how far into a transcript a reader will
// look for metadata. claude puts cwd around line 3 and its title around line 20,
// but one measured file was 24.9 MB with no title at all — without a bound that
// file is read whole to learn nothing.
const (
	maxMetaLines = 50
	maxMetaBytes = 64 << 10
)

// readCodex reads the session_meta line codex writes first.
func readCodex(path string) (Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return Session{}, err
	}
	defer func() { _ = f.Close() }()

	var meta struct {
		Timestamp string `json:"timestamp"`
		Type      string `json:"type"`
		Payload   struct {
			SessionID string `json:"session_id"`
			Cwd       string `json:"cwd"`
		} `json:"payload"`
	}
	line, err := bufio.NewReaderSize(f, maxMetaBytes).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return Session{}, fmt.Errorf("%s: empty", path)
	}
	if err := json.Unmarshal(line, &meta); err != nil {
		return Session{}, fmt.Errorf("%s: %w", path, err)
	}
	if meta.Type != "session_meta" || meta.Payload.SessionID == "" {
		return Session{}, fmt.Errorf("%s: no session_meta on the first line", path)
	}
	s := Session{ID: meta.Payload.SessionID, Dir: meta.Payload.Cwd, Path: path}
	s.Updated, _ = time.Parse(time.RFC3339, meta.Timestamp)
	return s, nil
}

// readPi reads the session header pi writes as its first line.
func readPi(path string) (Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return Session{}, err
	}
	defer func() { _ = f.Close() }()

	var meta struct {
		Type      string `json:"type"`
		ID        string `json:"id"`
		Timestamp string `json:"timestamp"`
		Cwd       string `json:"cwd"`
	}
	line, err := bufio.NewReaderSize(f, maxMetaBytes).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return Session{}, fmt.Errorf("%s: empty", path)
	}
	if err := json.Unmarshal(line, &meta); err != nil {
		return Session{}, fmt.Errorf("%s: %w", path, err)
	}
	if meta.Type != "session" || meta.ID == "" {
		return Session{}, fmt.Errorf("%s: no session header on the first line", path)
	}
	s := Session{ID: meta.ID, Dir: meta.Cwd, Path: path}
	s.Updated, _ = time.Parse(time.RFC3339, meta.Timestamp)
	return s, nil
}

// readClaude scans the head of a transcript for the cwd and the title.
//
// The id is the filename: claude names each transcript <uuid>.jsonl. The cwd is
// NOT the directory name — see TestReadClaudeIgnoresTheDirectoryName.
//
// Bounded on purpose. Measured, the cwd lands around line 3 and the ai-title
// around line 20, but a transcript need not have a title at all and the largest
// on the reference machine is 24.9 MB.
func readClaude(path string) (Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return Session{}, err
	}
	defer func() { _ = f.Close() }()

	s := Session{
		ID:   strings.TrimSuffix(filepath.Base(path), ".jsonl"),
		Path: path,
	}
	if fi, err := f.Stat(); err == nil {
		s.Updated = fi.ModTime()
	}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), maxMetaBytes)
	read := 0
	for i := 0; i < maxMetaLines && sc.Scan(); i++ {
		read += len(sc.Bytes())
		if read > maxMetaBytes {
			break
		}
		var e struct {
			Type    string `json:"type"`
			Cwd     string `json:"cwd"`
			AiTitle string `json:"aiTitle"`
		}
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			continue // a long or malformed line is not a reason to drop the session
		}
		if s.Dir == "" && e.Cwd != "" {
			s.Dir = e.Cwd
		}
		if s.Title == "" && e.AiTitle != "" {
			s.Title = e.AiTitle
		}
		if s.Dir != "" && s.Title != "" {
			break
		}
	}
	if s.Dir == "" {
		return Session{}, fmt.Errorf("%s: no cwd in the first %d lines", path, maxMetaLines)
	}
	return s, nil
}
