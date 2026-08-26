package source

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Cache is a content-addressed store of fetched source trees. Its Root is
// always a parameter: nothing in pkg/ reads $HOME.
//
// The shape is ach/internal/cachefs's (bootstrap.go, stage.go, sweep.go),
// which is dependency-free, reduced to the one operation this package needs.
type Cache struct{ Root string }

// objects holds published entries; tmp holds fills in progress. They are
// siblings under one root so a publish is a rename within a filesystem —
// os.Rename across devices fails, and a cross-device fallback would have to
// copy, which is exactly the non-atomic publish this design exists to avoid.
const (
	objectsDir = "objects"
	tmpDir     = "tmp"
)

// NewCache prepares root and sweeps any stage-* directory a killed process
// left behind. The sweep is not tidiness: tmp is unbounded otherwise, and a
// process that dies mid-fill leaves bytes that passed no check.
func NewCache(root string) (*Cache, error) {
	if root == "" {
		return nil, fmt.Errorf("cache root must not be empty")
	}
	c := &Cache{Root: root}
	for _, d := range []string{objectsDir, tmpDir} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o700); err != nil {
			return nil, err
		}
	}
	return c, c.sweep()
}

// Path is where key's entry lives, published or not. Callers use it for
// reporting; Has is what says whether anything is there.
func (c *Cache) Path(key string) string {
	return filepath.Join(c.Root, objectsDir, key)
}

// Has reports whether key is published. It is the ONLY thing distinguishing
// verified content from bytes that merely arrived, which is why Publish
// removes a failed fill rather than leaving it for a later run to find.
func (c *Cache) Has(key string) bool {
	fi, err := os.Stat(c.Path(key))
	return err == nil && fi.IsDir()
}

// Publish fills a fresh temporary directory and moves it into place under key.
// The rename is what makes an entry atomic: a reader either sees no entry or a
// complete one, never a partial fill.
//
// A failed fill removes the temporary directory and publishes nothing. See
// Has: a half-published entry would be served as if it had passed its digest
// check.
func (c *Cache) Publish(key string, fill func(dir string) error) (string, error) {
	if err := validKey(key); err != nil {
		return "", err
	}
	final := c.Path(key)
	if c.Has(key) {
		return final, nil
	}
	tmp, err := os.MkdirTemp(filepath.Join(c.Root, tmpDir), "stage-")
	if err != nil {
		return "", err
	}
	if err := fill(tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return "", err
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.RemoveAll(tmp)
		// A concurrent publisher winning the race is success, not failure:
		// the entry it wrote is the same content under the same key.
		if c.Has(key) {
			return final, nil
		}
		return "", err
	}
	return final, nil
}

// validKey refuses anything that is not a single path element. A cache key is
// derived from a resolved SHA or a digest, so it is already constrained — but
// it reaches Path through filepath.Join, and this package is the last place
// that would notice a key becoming attacker-influenced later.
func validKey(key string) error {
	if key == "" {
		return fmt.Errorf("cache key must not be empty")
	}
	if key != filepath.Base(key) || key == "." || key == ".." ||
		strings.ContainsAny(key, `/\`) || strings.HasPrefix(key, ".") {
		return fmt.Errorf("invalid cache key %q: want a single path element", key)
	}
	return nil
}

// sweep removes every stage-* directory under tmp. Anything there is by
// definition an interrupted fill: Publish renames on success and removes on
// failure, so a survivor means the process died in between.
func (c *Cache) sweep() error {
	dir := filepath.Join(c.Root, tmpDir)
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range ents {
		if !strings.HasPrefix(e.Name(), "stage-") {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
