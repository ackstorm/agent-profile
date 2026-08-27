package source

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// catalogueFile is the Claude plugin marketplace manifest, relative to the
// marketplace's fetched root (§21.1).
const catalogueFile = ".claude-plugin/marketplace.json"

// ParseRef splits §22's `<item>@<marketplace>` grammar.
func ParseRef(ref string) (item, marketplace string, err error) {
	i := strings.LastIndex(ref, "@")
	if i <= 0 || i == len(ref)-1 {
		return "", "", fmt.Errorf("bad reference %q: want <item>@<marketplace>", ref)
	}
	return ref[:i], ref[i+1:], nil
}

// ResolveItem turns one `<item>@<marketplace>` into a directory inside the
// marketplace's ALREADY-FETCHED tree.
//
// It never fetches. That is the whole of v0.6.3's same-repo rule: a catalogue's
// entries live in the catalogue's own repository, so resolving one is a path
// join, and one clone serves every entry. There is no second network hop, which
// is why §21.2 could be removed rather than merely satisfied.
func ResolveItem(marketplaceName, kind, marketplaceDir, item string) (string, error) {
	switch kind {
	case "skills":
		return resolveSkillItem(marketplaceName, marketplaceDir, item)
	case "plugins":
		return resolvePluginItem(marketplaceName, marketplaceDir, item)
	}
	return "", fmt.Errorf("marketplace %q has unknown type %q", marketplaceName, kind)
}

// resolveSkillItem is §21.1's skills contract: item <name> is directory <name>
// under the marketplace root. There is no catalogue file — the directory
// listing IS the catalogue.
func resolveSkillItem(name, dir, item string) (string, error) {
	got, err := subtree(dir, item)
	if err != nil {
		return "", fmt.Errorf("marketplace %q: item %q: %w", name, item, err)
	}
	fi, err := os.Stat(got)
	if err != nil || !fi.IsDir() {
		return "", fmt.Errorf("marketplace %q has no item %q", name, item)
	}
	return got, nil
}

// catalogue is the subset of marketplace.json this specification uses. Every
// other upstream field — description, version, author, category, homepage,
// license — is accepted and ignored, so a catalogue that grows one keeps
// working.
type catalogue struct {
	Plugins []catalogueEntry `json:"plugins"`
}

// catalogueEntry's Source is heterogeneous upstream: a bare string is a path
// relative to the catalogue's own repository, and an object names another
// repository through one of several shapes. Decoding it as json.RawMessage is
// what lets the same-repo rule be enforced on the DISTINCTION rather than on
// one particular external shape.
type catalogueEntry struct {
	Name   string          `json:"name"`
	Source json.RawMessage `json:"source"`
}

func resolvePluginItem(name, dir, item string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(catalogueFile)))
	if err != nil {
		return "", fmt.Errorf("marketplace %q: no %s in the resolved source root %s", name, catalogueFile, dir)
	}
	var c catalogue
	if err := json.Unmarshal(raw, &c); err != nil {
		return "", fmt.Errorf("marketplace %q: %s does not parse: %w", name, catalogueFile, err)
	}

	for _, e := range c.Plugins {
		if e.Name != item {
			continue
		}
		rel, err := sameRepoPath(name, item, e.Source)
		if err != nil {
			return "", err
		}
		got, err := subtree(dir, rel)
		if err != nil {
			return "", fmt.Errorf("marketplace %q: item %q: %w", name, item, err)
		}
		return got, nil
	}
	return "", fmt.Errorf("marketplace %q has no item %q (it lists %s)", name, item, listNames(c))
}

// sameRepoPath accepts only a relative path inside the catalogue's own
// repository, which upstream writes as a bare JSON string.
//
// Anything else names another repository, and v1 refuses it (§21.1). The refusal
// is not caution about an unimplemented feature: it is what removes the
// cross-host second hop, and with it §21.2's entire guard. An implementation
// that "helpfully" fetched an external entry would be handing a marketplace
// owner every credential its users hold — and the manifest's author does not
// write the catalogue, so reviewing your own manifest could not protect you.
func sameRepoPath(marketplace, item string, src json.RawMessage) (string, error) {
	var rel string
	if err := json.Unmarshal(src, &rel); err != nil {
		return "", ErrForeignEntry{
			Marketplace: marketplace,
			Entry:       item,
			EntryURL:    describeForeign(src),
		}
	}
	if rel == "" {
		return "", fmt.Errorf("marketplace %q: item %q declares an empty source", marketplace, item)
	}
	if strings.Contains(rel, "://") {
		return "", ErrForeignEntry{Marketplace: marketplace, Entry: item, EntryURL: rel}
	}
	clean := path.Clean(rel)
	if path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("marketplace %q: item %q names %q, which escapes the catalogue's own repository",
			marketplace, item, rel)
	}
	return clean, nil
}

// describeForeign names what an external entry pointed at, for the error. It
// reads only the fields upstream uses to name a repository, so a catalogue
// growing a new one degrades to the raw JSON rather than to silence.
func describeForeign(src json.RawMessage) string {
	var obj struct {
		Source string `json:"source"`
		URL    string `json:"url"`
		Repo   string `json:"repo"`
	}
	if err := json.Unmarshal(src, &obj); err == nil {
		switch {
		case obj.URL != "":
			return obj.URL
		case obj.Repo != "":
			return obj.Repo
		case obj.Source != "":
			return "source kind " + obj.Source
		}
	}
	return strings.TrimSpace(string(src))
}

// listNames is the "did you mean" half of a missing-item error. A catalogue is
// someone else's file, and "no item x" without the list is a guessing game.
func listNames(c catalogue) string {
	names := make([]string, 0, len(c.Plugins))
	for _, e := range c.Plugins {
		names = append(names, e.Name)
	}
	if len(names) == 0 {
		return "nothing"
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
