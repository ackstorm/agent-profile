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

	if err := writeDocTo(path, doc, isTOML); err != nil {
		return nil, err
	}
	return keys, nil
}

// writeDocTo encodes doc and writes it to path, creating parent directories
// as needed. It is MergeInto's write tail, factored out so AppendInto and
// RemoveFrom share it instead of duplicating the encode-and-write sequence.
func writeDocTo(path string, doc map[string]any, isTOML bool) error {
	out, err := encodeDoc(doc, isTOML)
	if err != nil {
		return fmt.Errorf("encoding %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o600)
}

// AppendInto adds element to the string list at key in the structured document
// at path, creating the file and the list if absent, and reports whether it
// added anything.
//
// MergeInto is deliberately not used here. It descends into MAPS and records
// dotted keys; a list is neither, so mergeMap would replace the whole array and
// record the container key — and uninstall, bounded by recorded keys, would
// then delete every package in the file including the user's. That is the exact
// failure mergeMap's own comment describes for mcpServers.
//
// Idempotent, because apply must converge: an element already present is left
// where it is rather than appended again.
func AppendInto(path, key, element string) (bool, error) {
	isTOML := strings.EqualFold(filepath.Ext(path), ".toml")
	doc, err := readDoc(path, isTOML)
	if err != nil {
		return false, err
	}
	var list []any
	switch v := doc[key].(type) {
	case nil:
		if _, present := doc[key]; present {
			return false, fmt.Errorf("%s: %q is null, not a list", path, key)
		}
	case []any:
		list = v
	default:
		// The user's data, in the place ours goes. Replacing it would destroy
		// work this program did not create.
		return false, fmt.Errorf("%s: %q is not a list", path, key)
	}
	for _, e := range list {
		if s, ok := e.(string); ok && s == element {
			return false, nil
		}
	}
	doc[key] = append(list, element)
	return true, writeDocTo(path, doc, isTOML)
}

// RemoveFrom deletes exactly one element from the string list at key, and
// nothing else. It is AppendInto's inverse, and the bound is the same one
// MergeOut applies to keys: a list holding four packages, three of them the
// user's, loses one.
//
// An emptied list is LEFT in place, like an emptied container in MergeOut: the
// user may have created it, and an empty list costs them nothing.
func RemoveFrom(path, key, element string) error {
	isTOML := strings.EqualFold(filepath.Ext(path), ".toml")
	doc, err := readDoc(path, isTOML)
	if err != nil {
		return err
	}
	list, ok := doc[key].([]any)
	if !ok {
		return nil
	}
	out := make([]any, 0, len(list))
	for _, e := range list {
		if s, ok := e.(string); ok && s == element {
			continue
		}
		out = append(out, e)
	}
	doc[key] = out
	return writeDocTo(path, doc, isTOML)
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
		// Descend exactly one level: a container the user shares with us
		// (mcpServers) is descended into; the entry itself is replaced whole
		// and recorded whole.
		//
		// The container is CREATED when absent rather than written as a unit.
		// Writing it as a unit records the key "mcpServers", and Phase 7 would
		// then remove every server in the file — including the user's — when
		// uninstalling one of ours. That is the whole reason this function
		// reports keys at all.
		if isMap && prefix == "" {
			existing, ok := doc[k].(map[string]any)
			if !ok {
				if _, present := doc[k]; present {
					// The user has a NON-mapping value where we expect a
					// container. There is nothing to merge into, so it is
					// replaced and recorded whole — honestly, rather than
					// pretending we own only part of it.
					doc[k] = v
					*keys = append(*keys, path)
					continue
				}
				existing = map[string]any{}
				doc[k] = existing
			}
			mergeMap(existing, sub, path, keys)
			continue
		}
		doc[k] = v
		*keys = append(*keys, path)
	}
}

// MergeOut deletes exactly these dotted keys from the structured document at
// path, and nothing else. It is MergeInto's inverse and the second half of what
// makes a shared configuration file safe to install into.
//
// Precisely these keys: a document holding four MCP servers, three of them the
// user's, loses one. That bound is §33.3's, and it is the reason MergeInto
// reports keys rather than leaving uninstall to recompute them from a document
// that has since moved on.
//
// An emptied container is LEFT in place. Removing "mcpServers" once its last
// entry goes would be a claim the ledger cannot support — the user may have
// created that container, and an empty mapping costs them nothing.
func MergeOut(path string, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	isTOML := strings.EqualFold(filepath.Ext(path), ".toml")

	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	// readDoc's refusal, for the same reason: a file that does not parse was
	// broken by someone else, and rewriting it would destroy work this program
	// did not create.
	doc := map[string]any{}
	if len(bytes.TrimSpace(raw)) > 0 {
		if isTOML {
			err = toml.Unmarshal(raw, &doc)
		} else {
			err = json.Unmarshal(raw, &doc)
		}
		if err != nil {
			return fmt.Errorf("%s does not parse, so it will not be touched: %w", path, err)
		}
	}

	for _, k := range keys {
		deleteDotted(doc, k)
	}

	out, err := encodeDoc(doc, isTOML)
	if err != nil {
		return fmt.Errorf("encoding %s: %w", path, err)
	}
	return os.WriteFile(path, out, 0o600)
}

// deleteDotted removes one recorded key. MergeInto descends exactly one level,
// so a recorded key is one or two segments and this walks the same shape back.
func deleteDotted(doc map[string]any, key string) {
	head, rest, nested := strings.Cut(key, ".")
	if !nested {
		delete(doc, head)
		return
	}
	sub, ok := doc[head].(map[string]any)
	if !ok {
		return
	}
	delete(sub, rest)
}
