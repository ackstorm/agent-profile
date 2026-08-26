package hydrate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// MergeInto deep-merges contribution into the structured document at path,
// creating the file if it is absent, and returns the dotted keys it wrote.
//
// This is Phase 4's additive rule one level down. A whole-file write leaves a
// hand-added FILE alone; a deep merge leaves a hand-added KEY alone. Both are
// §33: the profile is the source of truth for what it declares and for nothing
// else.
//
// The returned keys are what Phase 7 removes on uninstall — precisely these and
// nothing else — which is why they are reported rather than recomputed later
// from a document that may have moved on.
func MergeInto(path string, contribution map[string]any) ([]string, error) {
	if len(contribution) == 0 {
		return nil, nil
	}
	isTOML := strings.EqualFold(filepath.Ext(path), ".toml")

	doc, err := readDoc(path, isTOML)
	if err != nil {
		return nil, err
	}
	var keys []string
	mergeMap(doc, contribution, "", &keys)
	sort.Strings(keys)

	out, err := encodeDoc(doc, isTOML)
	if err != nil {
		return nil, fmt.Errorf("encoding %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return nil, err
	}
	return keys, nil
}

// readDoc parses the document, or returns an empty one when the file is absent.
//
// A file that does not PARSE is an error, never a file to overwrite. The user
// broke it, or it was never ours; replacing it would destroy work this program
// did not create and cannot restore. This is the same refusal Phase 4 makes for
// a file whose hash drifted.
func readDoc(path string, isTOML bool) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]any{}, nil
	}
	doc := map[string]any{}
	if isTOML {
		if err := toml.Unmarshal(raw, &doc); err != nil {
			return nil, fmt.Errorf("%s does not parse as TOML, so it will not be touched: %w", path, err)
		}
		return doc, nil
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%s does not parse as JSON, so it will not be touched: %w", path, err)
	}
	return doc, nil
}

func encodeDoc(doc map[string]any, isTOML bool) ([]byte, error) {
	if isTOML {
		var buf bytes.Buffer
		if err := toml.NewEncoder(&buf).Encode(doc); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}
	// Indented, with a trailing newline: the file lands in someone's editor and
	// often in their version control, and a one-line JSON blob makes every
	// future diff useless.
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// mergeMap writes contribution into doc, recording a dotted key per LEAF it
// owns.
//
// A leaf here is one resource's entry — "mcpServers.memory" — not every scalar
// inside it. Recording the scalars would make uninstall remove a server's url
// and leave its headers behind; recording the entry removes the server.
func mergeMap(doc, contribution map[string]any, prefix string, keys *[]string) {
	for _, k := range sortedKeys(contribution) {
		v := contribution[k]
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		sub, isMap := v.(map[string]any)
		existing, hasExisting := doc[k].(map[string]any)
		// Descend only while BOTH sides are mappings and we are still above the
		// entry level: a container the user shares with us (mcpServers) is
		// descended into; the entry itself is replaced whole.
		if isMap && hasExisting && prefix == "" {
			mergeMap(existing, sub, path, keys)
			continue
		}
		doc[k] = v
		*keys = append(*keys, path)
	}
}
