//go:build unix

package profile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ackstorm/agent-profile/internal/agent"
)

// sliceSettings builds a new settings file out of the named keys of an existing
// one. It is `ap create --only-settings`, and it is only ever used to BUILD a
// file that does not exist yet — never to edit one. That is what makes the TOML
// arm possible without a parser: see sliceTOML.
//
// found is the subset of keys that existed, in the order given; the caller warns
// about the rest. Finding nothing returns a nil slice rather than an empty file:
// an empty settings file reads as "deliberately blank", which is not what a
// mistyped key means.
func sliceSettings(f agent.Format, b []byte, keys []string) (out []byte, found []string, err error) {
	if f == agent.TOML {
		return sliceTOML(b, keys)
	}
	return sliceJSON(b, keys)
}

// sliceJSON copies the value at each dotted path into a fresh object with the
// same nesting. Naming a parent brings its children, because the value is taken
// whole — which is what someone means when they type `mcpServers`.
//
// Stated limit: a path splits on ".", so a key holding a literal dot cannot be
// named. It is reported as not found, like any other unresolvable path, which is
// the honest answer — the alternative is an escape syntax for a case nobody has
// hit.
func sliceJSON(b []byte, keys []string) ([]byte, []string, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(b, &root); err != nil {
		return nil, nil, err
	}
	out := map[string]any{}
	var found []string
	for _, k := range keys {
		path := strings.Split(k, ".")
		v, ok := pickJSON(root, path)
		if !ok {
			continue
		}
		insertJSON(out, path, v)
		found = append(found, k)
	}
	if len(found) == 0 {
		return nil, nil, nil
	}
	// MarshalIndent sorts map keys and re-indents the raw values it is handed,
	// so the result is stable and two-space indented whatever the source looked
	// like.
	enc, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return append(enc, '\n'), found, nil
}

// pickJSON walks one level per path element, decoding as it goes. The value is
// returned raw: re-encoding a subtree nobody asked to change would rewrite its
// numbers and its key order for no reason.
func pickJSON(level map[string]json.RawMessage, path []string) (json.RawMessage, bool) {
	v, ok := level[path[0]]
	if !ok {
		return nil, false
	}
	if len(path) == 1 {
		return v, true
	}
	var next map[string]json.RawMessage
	if err := json.Unmarshal(v, &next); err != nil {
		return nil, false // a scalar named as a parent: no such path
	}
	return pickJSON(next, path[1:])
}

// insertJSON writes v at path, creating the objects above it. Two paths sharing
// a prefix land in the same object instead of the second replacing the first.
//
// Order-independent on purpose: if a shorter path already placed a raw value at
// an ancestor of path, that value already contains everything path would add,
// so insertJSON does nothing rather than descending into a json.RawMessage —
// which is not a map[string]any and would otherwise look like "no child here
// yet" and get silently replaced by an empty one, discarding every sibling the
// parent held. sliceJSON still reports path as found: it existed in the
// source, which is the caller's question, independent of whether inserting it
// changed the output.
func insertJSON(out map[string]any, path []string, v json.RawMessage) {
	for len(path) > 1 {
		existing, present := out[path[0]]
		if present {
			child, ok := existing.(map[string]any)
			if !ok {
				return // an ancestor already claimed this subtree whole
			}
			out, path = child, path[1:]
			continue
		}
		child := map[string]any{}
		out[path[0]] = child
		out, path = child, path[1:]
	}
	out[path[0]] = v
}

// tomlBlock is a run of source lines under one name.
type tomlBlock struct {
	name  string
	lines []string
}

// sliceTOML copies whole blocks of the source verbatim. It does not parse TOML,
// and it never will: there is no decoder in the standard library, a TOML 1.0
// parser is ~1000 lines maintained forever, and a partial one that silently
// mis-reads someone's config is worse than not having this feature.
//
// It does not need one, because the file is being BUILT, not edited:
//
//	A line whose first non-space character is "[" opens a section. A block runs
//	from its header to the line before the next header. Blocks are copied
//	verbatim.
//
// Before the first header live the top-level scalars; each "key = value" line
// there opens its own block by the same rule, which is also what keeps a
// multi-line array with its key — its continuation lines are not valid key
// lines, so they never open a block of their own.
//
// The guarantee this gives is stronger than "we parse TOML correctly": every
// emitted byte is a byte of the source. Nothing can be re-encoded wrongly,
// comments and formatting survive, and the only possible defect is cutting in
// the wrong place. TestSliceTOMLEmitsOnlySourceLines asserts it as a property.
func sliceTOML(b []byte, keys []string) ([]byte, []string, error) {
	// Two ways to cut in the wrong place, both refused rather than guessed at.
	//
	// A "[" at the start of a line inside a multiline string is
	// indistinguishable from a header without a parser. Measured: zero
	// occurrences in the reference ~/.codex/config.toml.
	if bytes.Contains(b, []byte(`"""`)) || bytes.Contains(b, []byte("'''")) {
		return nil, nil, fmt.Errorf(`refusing to slice: this file contains a multiline string (""" or '''), ` +
			`and a "[" at the start of a line inside one cannot be told from a table header without a TOML parser`)
	}
	// The same ambiguity, caused by a multi-line array instead of a string:
	// once `matrix = [` leaves a bracket open, the next line's own "[" — say
	// `[1, 2],` — reads exactly like a header. hasUnclosedBracketAtLineEnd
	// finds that by tracking bracket depth, not by understanding the array.
	if hasUnclosedBracketAtLineEnd(b) {
		return nil, nil, fmt.Errorf(`refusing to slice: this file contains an array that spans multiple lines, ` +
			`and a "[" at the start of a line inside one cannot be told from a table header without a TOML parser`)
	}

	var blocks []tomlBlock
	headers := false
	for _, ln := range strings.Split(string(b), "\n") {
		if name, ok := tomlHeader(ln); ok {
			headers = true
			blocks = append(blocks, tomlBlock{name: name})
		} else if !headers {
			if name, ok := tomlKey(ln); ok {
				blocks = append(blocks, tomlBlock{name: name})
			}
		}
		if len(blocks) == 0 {
			continue // a leading comment, before anything is named
		}
		last := &blocks[len(blocks)-1]
		last.lines = append(last.lines, ln)
	}

	var out []string
	hit := make(map[string]bool, len(keys))
	for _, bl := range blocks {
		// A block can match more than one requested key at once — "tui" and
		// "tui.model_availability_nux" both match a block named
		// "tui.model_availability_nux". Every matching key is marked found,
		// whatever order the caller gave them in, but the block is still
		// emitted exactly once. Stopping at the first match (an earlier
		// version did, with `break`) made the result depend on argument
		// order: naming the parent before the child left the child reported
		// missing even though its content had already shipped under the
		// parent's match.
		matched := false
		for _, k := range keys {
			// Naming a parent takes its children: "tui" matches [tui] and
			// [tui.model_availability_nux], and never [tuition].
			if bl.name != k && !strings.HasPrefix(bl.name, k+".") {
				continue
			}
			hit[k] = true
			matched = true
		}
		if !matched {
			continue
		}
		if len(out) > 0 {
			out = append(out, "")
		}
		out = append(out, trimTrailingBlanks(bl.lines)...)
	}
	if len(out) == 0 {
		return nil, nil, nil
	}
	var found []string
	for _, k := range keys {
		if hit[k] {
			found = append(found, k) // in the order the caller gave them
		}
	}
	return []byte(strings.Join(out, "\n") + "\n"), found, nil
}

// trimTrailingBlanks drops the empty lines a block collected from the gap before
// the next header, so joining two blocks leaves exactly one blank line between
// them. Dropping only — nothing is added, so the byte-subset property holds.
func trimTrailingBlanks(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// bracketDelta returns how many unquoted "[" a line opens net of unquoted "]"
// it closes, stopping at an unquoted "#" (the rest of the line is a comment).
// Quote-skipping mirrors tomlHeader, for the same reason: codex writes real
// filesystem paths, which can hold either bracket character, into the strings
// this scans past.
func bracketDelta(line string) int {
	depth := 0
	var quote byte
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case quote != 0:
			if c == '\\' && quote == '"' {
				i++ // an escape inside a basic string
				continue
			}
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '#':
			return depth
		case c == '[':
			depth++
		case c == ']':
			depth--
		}
	}
	return depth
}

// hasUnclosedBracketAtLineEnd reports whether some line leaves an unquoted "["
// unmatched by the time it ends — an array continuing onto the next line. That
// next line's own "[" (an array element, not a table) would then be
// indistinguishable from a header. Detection only: this refuses the file
// rather than attempting to slice through the array.
func hasUnclosedBracketAtLineEnd(b []byte) bool {
	depth := 0
	for _, ln := range strings.Split(string(b), "\n") {
		if depth > 0 {
			return true // this line starts as a continuation of the last one
		}
		depth += bracketDelta(ln)
	}
	return false
}

// tomlHeader returns the table name of a header line — the text between the
// brackets, verbatim, quotes and all — and whether the line is one at all.
//
// The closing bracket is found with a scanner that skips quoted spans, because
// codex writes real paths into table names: [projects."/tmp/a]b"] closes at the
// last bracket, not at the one inside the string. Ten lines; a plain
// IndexByte(']') is wrong on the paths codex actually writes.
//
// [[x]] is a header of x. Every matching block is then emitted in source order,
// which is free because the blocks are verbatim.
func tomlHeader(line string) (string, bool) {
	s := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(s, "[") {
		return "", false
	}
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimPrefix(s, "[") // an array of tables
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == '\\' && quote == '"' {
				i++ // an escape inside a basic string
				continue
			}
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == ']':
			return strings.TrimSpace(s[:i]), true
		}
	}
	return "", false // unterminated: not a header, and not ours to fix
}

// tomlKey returns the key part of a top-level "key = value" line: the text
// before the first "=", trimmed, with surrounding quotes stripped.
//
// The key must be a bare key or a fully quoted one. That is what stops a line
// inside a multi-line array — `"a=b",` — from looking like a new key and
// splitting the array away from the key it belongs to.
func tomlKey(line string) (string, bool) {
	s := strings.TrimSpace(line)
	if s == "" || strings.HasPrefix(s, "#") {
		return "", false
	}
	i := strings.IndexByte(s, '=')
	if i < 0 {
		return "", false
	}
	k := strings.TrimSpace(s[:i])
	if len(k) >= 2 && (k[0] == '"' && k[len(k)-1] == '"' || k[0] == '\'' && k[len(k)-1] == '\'') {
		return k[1 : len(k)-1], true
	}
	for j := 0; j < len(k); j++ {
		c := k[j]
		bare := c == '-' || c == '_' || c == '.' ||
			c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
		if !bare {
			return "", false
		}
	}
	return k, k != ""
}
