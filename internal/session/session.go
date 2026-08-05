// Package session reads what each agent records about its past conversations.
//
// Four agents, four storage layouts, and none of them is a documented interface —
// every one here was read off the real files. When a reader stops finding
// sessions, assume the layout moved and re-measure; do not widen a parser until
// it matches something.
package session

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
// look for metadata. Without a bound, one measured file was 24.9 MB with no
// title at all and would be read whole to learn nothing.
//
// The byte budget is 256 KiB because 64 KiB was measured to be just under the
// data it was meant to reach. Across the eight most recent transcripts on the
// reference machine the ai-title landed between 60,961 and 79,948 bytes in, five
// of them past 64 KiB, so that budget silently dropped the title from most rows.
// The longest single line in the first fifty was 97,988 bytes, which also
// overflowed a 64 KiB scanner buffer and lost those sessions entirely.
//
// 50 lines is the real bound; the byte budget only stops a pathological file.
const (
	maxMetaLines = 50
	maxMetaBytes = 256 << 10
)

// errNotASession marks a file that is readable but is not a session: a transcript
// that died before recording anything. Three such files (~146 bytes each) put
// three warning lines on stderr on every `ap sessions`, which is how people learn
// to ignore warnings. Skipped quietly instead.
var errNotASession = errors.New("not a session")

// Filter narrows a scan. Every field is optional; an empty one matches anything.
//
// Applied BEFORE the cap, always. Filtering a capped list makes
// `ap sessions claude:finops` answer "No sessions found" while finops has three,
// because busier profiles filled the window first.
type Filter struct {
	Agent   string
	Profile string
	// Dir keeps only sessions belonging to this working directory.
	Dir string
}

func (f Filter) matchesStore(agentName, profileName string) bool {
	if f.Agent != "" && f.Agent != agentName {
		return false
	}
	if f.Profile != "" && f.Profile != profileName {
		return false
	}
	return true
}

func (f Filter) matches(s Session) bool {
	if !f.matchesStore(s.Agent, s.Profile) {
		return false
	}
	if f.Dir != "" && filepath.Clean(s.Dir) != filepath.Clean(f.Dir) {
		return false
	}
	return true
}

// envDir is the directory run.Env should be given for a profile.
//
// Empty for profile.Default, which is not a profile at all but the agent's own
// machine-wide configuration: reaching it means inheriting the environment
// untouched. Passing profile.Dir's answer instead builds a real override at
// <real config>/xdg-data, a directory that does not exist — measured, opencode
// then listed nothing for :default and would have written state into the user's
// own config directory.
func envDir(a agent.Agent, profileName string) string {
	if profileName == profile.Default {
		return ""
	}
	return profile.Dir(a, profileName)
}

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
		return Session{}, fmt.Errorf("%s: no session_meta on the first line: %w", path, errNotASession)
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
		return Session{}, fmt.Errorf("%s: no session header on the first line: %w", path, errNotASession)
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
		return Session{}, fmt.Errorf("%s: no cwd in the first %d lines: %w", path, maxMetaLines, errNotASession)
	}
	return s, nil
}

// parseOpencode decodes `opencode session list --format json`.
//
// Timestamps are milliseconds since the epoch.
func parseOpencode(b []byte) ([]Session, error) {
	// Measured: with no sessions in the current project opencode prints NOTHING,
	// not "[]". Treating that as a parse error puts "unexpected end of JSON input"
	// on stderr for every profile that simply has no history here, which is the
	// common case given the listing is project-scoped.
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, nil
	}
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

func scanFileStore(storeDir, agentName, profileName string, max int, f Filter, read readerFunc, warn func(error)) ([]Session, error) {
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

	// Newest first, reading only until max have PASSED the filter. Capping the
	// candidate list first and filtering afterwards is what made a rarely used
	// profile invisible behind fifty busier sessions.
	var out []Session
	for _, c := range candidates {
		if max > 0 && len(out) >= max {
			break
		}
		s, err := read(c.path)
		if err != nil {
			// A file that is simply not a session is not worth a warning.
			if warn != nil && !errors.Is(err, errNotASession) {
				warn(err)
			}
			continue
		}
		s.Agent = agentName
		s.Profile = profileName
		if s.Updated.IsZero() {
			s.Updated = c.mtime
		}
		if !f.matches(s) {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

func scanClaude(storeDir, agentName, profileName string, max int, read readerFunc) ([]Session, error) {
	return scanFileStore(storeDir, agentName, profileName, max, Filter{}, read, nil)
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

// Result is what a scan found, plus what it had to do to find it.
type Result struct {
	Sessions []Session
	// ConsultedOpencode reports whether any opencode store was asked, whether or
	// not it answered with rows. The caller must say so: opencode's listing is
	// scoped to the current git project, so an empty answer means "none in this
	// project", never "none at all". Deciding this from the returned rows instead
	// hides the caveat in exactly the case where it matters most.
	ConsultedOpencode bool
}

// Scan returns the most recent sessions matching f across every agent and
// profile, newest first.
//
// Per-store it stats first and reads only as far as it must: the transcripts are
// large and there are hundreds of them. The filter is applied before the cap, so
// a rarely used profile cannot be hidden by busier ones.
//
// Errors from one profile never fail the whole scan — a profile with an
// unreadable store is reported through warn and the rest still list. A listing
// that aborts because one directory is odd is a listing nobody trusts.
func Scan(max int, f Filter, warn func(error)) Result {
	var res Result
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
			if !f.matchesStore(a.Name, p) {
				continue
			}
			pDir := profile.Dir(a, p)
			if a.Sessions.Layout == agent.LayoutExec {
				bin, err := exec.LookPath(a.Bin)
				if err != nil {
					continue
				}
				res.ConsultedOpencode = true
				// envDir, not pDir: :default must inherit the environment.
				env := run.Env(a, envDir(a, p), os.Environ())
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
				for _, s := range sessions {
					if f.matches(s) {
						res.Sessions = append(res.Sessions, s)
					}
				}
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
				sessions, err := scanFileStore(storeDir, a.Name, p, max, f, reader, warn)
				if err != nil {
					if warn != nil {
						warn(err)
					}
					continue
				}
				res.Sessions = append(res.Sessions, sessions...)
			}
		}
	}
	res.Sessions = sortAndCap(res.Sessions, max)
	return res
}
