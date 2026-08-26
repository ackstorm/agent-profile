package hydrate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ackstorm/agent-profile/pkg/schema"
)

// ledgerName is the ledger inside the root. A dotfile because a root can be the
// user's real configuration directory.
const ledgerName = ".ap-ledger.json"

// ledgerVersion is bumped only for a change no older reader could survive.
const ledgerVersion = 1

// FileRec records one file that was written.
//
// The shape is ach's, and the three fields are what make the ledger honest
// rather than authoritative (§33.1):
//
//   - Hash: on removal, a file whose hash no longer matches was edited by the
//     user, so the verdict is "modified" and it is left alone. The ledger never
//     claims a file it no longer recognises.
//   - Merge and Keys: a deep-merged settings.json or config.toml loses only the
//     dotted keys that were contributed. The file survives, and every key the
//     user added by hand survives with it.
//
// Merge is empty for a whole-file write. Phase 5 writes "deep" for structured
// configuration; Phase 6 writes "composite" for a marker-bounded region.
type FileRec struct {
	RelPath string   `json:"relPath"`
	Hash    string   `json:"hash"`
	Merge   string   `json:"merge,omitempty"`
	Keys    []string `json:"keys,omitempty"`
}

// ResourceRec is one materialized resource: the first arm of the ledger.
//
// ResolvedRef is a RECEIPT, not a pin (§32.2). Nothing re-uses it as a
// resolution input — a ref re-resolves on every run — and it exists so drift is
// reportable without a lockfile.
type ResourceRec struct {
	Name        string         `json:"name"`
	Kind        string         `json:"kind"`
	Ref         string         `json:"ref,omitempty"`
	Source      *schema.Source `json:"source,omitempty"`
	ResolvedRef string         `json:"resolvedRef,omitempty"`
	InstalledAt string         `json:"installedAt"`
	Files       []FileRec      `json:"files"`
}

// DefinitionRec is a resolved definition that materializes NO file: the second
// arm.
//
// It is not optional, and the reason is the interaction between two other rules
// (F3). A manifest is an INPUT and is not kept (§1.1). A marketplace declared in
// one writes no file into the root. So a file-only ledger loses the marketplace
// the moment the manifest is gone, and a later single-capability install of
// <item>@<that-marketplace> would have nothing to resolve the name against.
//
// AuthBinding is the binding's NAME. There is deliberately no field for a
// value: ach-cli stores tokens in a credentials.json at 0600, and §34 forbids
// that — a resolution-time consumer "MUST NOT persist it". The cost is stated
// rather than discovered: the variable must be present in the environment on
// every run, where a tool that stores the token asks once.
type DefinitionRec struct {
	Name        string         `json:"name"`
	Kind        string         `json:"kind"`
	Source      *schema.Source `json:"source,omitempty"`
	ResolvedRef string         `json:"resolvedRef,omitempty"`
	AuthScheme  string         `json:"authScheme,omitempty"`
	AuthBinding string         `json:"authBinding,omitempty"`
}

// Ledger is per root, and it is the ONLY state (§33.1).
type Ledger struct {
	Version     int             `json:"version"`
	Resources   []ResourceRec   `json:"resources"`
	Definitions []DefinitionRec `json:"definitions"`
}

// LoadLedger reads a root's ledger. An absent one is an EMPTY ledger, not an
// error: a root nothing has been applied to is the normal first case, and
// making it an error would put a "does this exist yet" branch in every caller.
func LoadLedger(root string) (*Ledger, error) {
	raw, err := os.ReadFile(filepath.Join(root, ledgerName))
	if err != nil {
		if os.IsNotExist(err) {
			return &Ledger{Version: ledgerVersion}, nil
		}
		return nil, fmt.Errorf("reading the ledger in %s: %w", root, err)
	}
	var l Ledger
	if err := json.Unmarshal(raw, &l); err != nil {
		return nil, fmt.Errorf("the ledger in %s is corrupt: %w", root, err)
	}
	if l.Version > ledgerVersion {
		return nil, fmt.Errorf("the ledger in %s was written by a newer ap (version %d)", root, l.Version)
	}
	return &l, nil
}

// Save writes the ledger atomically: a temporary file in the same directory,
// then a rename.
//
// The same shape source.Cache.Publish uses, for the same reason. A half-written
// ledger claims files that may not exist, and every later verdict — remove,
// skip, report as modified — would rest on a record that was never true.
func (l *Ledger) Save(root string) error {
	l.Version = ledgerVersion
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	// The temp file is a sibling, so the rename stays within one filesystem.
	// os.Rename across devices fails, and a copy fallback is exactly the
	// non-atomic write this avoids.
	f, err := os.CreateTemp(root, ".ap-ledger-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()

	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	// Flush to the device before the rename. A rename is atomic with respect
	// to other readers, not with respect to a power loss that leaves the
	// renamed inode empty.
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(root, ledgerName))
}

// Resource returns the record for one resource, if the ledger holds it.
func (l *Ledger) Resource(kind, name string) (ResourceRec, bool) {
	for _, r := range l.Resources {
		if r.Kind == kind && r.Name == name {
			return r, true
		}
	}
	return ResourceRec{}, false
}

// Put replaces a resource's record, or appends it. Apply is additive over the
// ROOT, not over the ledger: re-applying a resource replaces what is recorded
// for it, or the record would grow a duplicate on every run.
func (l *Ledger) Put(r ResourceRec) {
	for i, existing := range l.Resources {
		if existing.Kind == r.Kind && existing.Name == r.Name {
			l.Resources[i] = r
			return
		}
	}
	l.Resources = append(l.Resources, r)
}

// PutDefinition is Put for the second arm.
func (l *Ledger) PutDefinition(d DefinitionRec) {
	for i, existing := range l.Definitions {
		if existing.Kind == d.Kind && existing.Name == d.Name {
			l.Definitions[i] = d
			return
		}
	}
	l.Definitions = append(l.Definitions, d)
}
