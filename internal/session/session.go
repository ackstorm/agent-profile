// Package session reads what each agent records about its past conversations.
//
// Four agents, four storage layouts, and none of them is a documented interface —
// every one here was read off the real files. When a reader stops finding
// sessions, assume the layout moved and re-measure; do not widen a parser until
// it matches something.
package session

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ackstorm/agent-profile/internal/agent"
	"github.com/ackstorm/agent-profile/internal/profile"
	"github.com/ackstorm/agent-profile/internal/run"
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

// parseOpencode decodes `opencode session list --format json`.
//
// Timestamps are milliseconds since the epoch.
func parseOpencode(b []byte) ([]Session, error) {
	var rows []struct {
		ID        string `json:"id"`
		Title     string `json:"title"`
		Updated   int64  `json:"updated"`
		Directory string `json:"directory"`
	}
	if err := json.Unmarshal(b, &rows); err != nil {
		return nil, fmt.Errorf("opencode session list: %w", err)
	}
	out := make([]Session, 0, len(rows))
	for _, r := range rows {
		if r.ID == "" {
			continue
		}
		out = append(out, Session{
			ID:      r.ID,
			Title:   r.Title,
			Dir:     r.Directory,
			Updated: time.UnixMilli(r.Updated),
			Path:    "opencode.db",
		})
	}
	return out, nil
}

// opencodeSessions runs opencode's own listing under a profile's environment.
//
// Two limits the caller must surface rather than hide:
//   - opencode groups sessions by git project, so this returns only what belongs
//     to the current working directory's project. Measured: 93 sessions in the db,
//     56 listed from /tmp, 0 from a git repo with no opencode history.
//   - it costs about a second per profile.
func opencodeSessions(bin string, env []string, max int) ([]Session, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "session", "list", "--format", "json", "-n", strconv.Itoa(max))
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("opencode session list: %w", err)
	}
	return parseOpencode(out)
}

type readerFunc func(path string) (Session, error)

type candidate struct {
	path  string
	mtime time.Time
}

func scanFileStore(storeDir, agentName, profileName string, max int, read readerFunc, warn func(error)) ([]Session, error) {
	var candidates []candidate
	err := filepath.WalkDir(storeDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		candidates = append(candidates, candidate{path: path, mtime: info.ModTime()})
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	slices.SortFunc(candidates, func(a, b candidate) int {
		if a.mtime.After(b.mtime) {
			return -1
		}
		if a.mtime.Before(b.mtime) {
			return 1
		}
		return 0
	})

	limit := len(candidates)
	if max > 0 && limit > max {
		limit = max
	}

	var out []Session
	for i := 0; i < limit; i++ {
		s, err := read(candidates[i].path)
		if err != nil {
			if warn != nil {
				warn(err)
			}
			continue
		}
		s.Agent = agentName
		s.Profile = profileName
		if s.Updated.IsZero() {
			s.Updated = candidates[i].mtime
		}
		out = append(out, s)
	}
	return out, nil
}

func scanClaude(storeDir, agentName, profileName string, max int, read readerFunc) ([]Session, error) {
	return scanFileStore(storeDir, agentName, profileName, max, read, nil)
}

func sortAndCap(sessions []Session, max int) []Session {
	slices.SortFunc(sessions, func(a, b Session) int {
		if a.Updated.After(b.Updated) {
			return -1
		}
		if a.Updated.Before(b.Updated) {
			return 1
		}
		return 0
	})
	if max > 0 && len(sessions) > max {
		return sessions[:max]
	}
	return sessions
}

// Scan returns the most recent sessions across every agent and profile,
// newest first.
//
// Per-store it stats first and reads only the newest max files: the transcripts
// are large and there are hundreds of them.
//
// Errors from one profile never fail the whole scan — a profile with an
// unreadable store is reported through warn and the rest still list. A listing
// that aborts because one directory is odd is a listing nobody trusts.
func Scan(max int, warn func(error)) []Session {
	var all []Session
	for _, name := range agent.Names() {
		a, ok := agent.Lookup(name)
		if !ok || a.Sessions == nil {
			continue
		}
		profs, err := profile.List(a)
		if err != nil {
			if warn != nil {
				warn(err)
			}
			continue
		}
		for _, p := range profs {
			pDir := profile.Dir(a, p)
			if a.Sessions.Layout == agent.LayoutExec {
				bin, err := exec.LookPath(a.Bin)
				if err != nil {
					continue
				}
				env := run.Env(a, pDir, os.Environ())
				sessions, err := opencodeSessions(bin, env, max)
				if err != nil {
					if warn != nil {
						warn(err)
					}
					continue
				}
				for i := range sessions {
					sessions[i].Agent = a.Name
					sessions[i].Profile = p
				}
				all = append(all, sessions...)
			} else {
				storeDir := filepath.Join(pDir, a.Sessions.Rel)
				var reader readerFunc
				switch a.Sessions.Layout {
				case agent.LayoutClaudeProjects:
					reader = readClaude
				case agent.LayoutCodexRollouts:
					reader = readCodex
				case agent.LayoutPiSessions:
					reader = readPi
				default:
					continue
				}
				sessions, err := scanFileStore(storeDir, a.Name, p, max, reader, warn)
				if err != nil {
					if warn != nil {
						warn(err)
					}
					continue
				}
				all = append(all, sessions...)
			}
		}
	}
	return sortAndCap(all, max)
}
