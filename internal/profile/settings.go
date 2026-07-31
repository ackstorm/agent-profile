//go:build unix

package profile

import (
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

// sliceTOML is written in the next task.
func sliceTOML(b []byte, keys []string) ([]byte, []string, error) {
	return nil, nil, fmt.Errorf("not implemented")
}
