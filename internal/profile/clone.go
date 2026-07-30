//go:build unix

package profile

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ackstorm/agent-profile/internal/agent"
)

// Clone copies an agent's declared configuration from one config directory into
// another. src may be another profile or, via `--from default`, the agent's real
// config directory — Clone does not distinguish the two.
//
// One selection rule: only the paths named in a.CloneAllow are copied, each
// walked whole (files and, for a directory entry, everything under it). A
// symlink is never copied, whether it is the allowlisted entry itself or found
// while walking under one — those are the Shared entries, and copying their
// contents would duplicate the user's session history, while copying the link
// itself is pointless since Link recreates it straight after. A missing
// allowlist entry is not an error: profiles and real config directories differ
// in what they happen to contain.
//
// Everything else in src — accumulated runtime state, caches, the config shim —
// is left behind simply by never being named. See Agent.CloneAllow for why that
// is an allowlist and not a list of exclusions.
//
// dst must be an existing empty directory that is not inside src. Clone does not
// create it; the caller does.
func Clone(a agent.Agent, src, dst string) error {
	_, err := CloneWithWarnings(a, src, dst)
	return err
}

// CloneWithWarnings does what Clone does, and additionally reports cloned files
// that need a human's attention afterward: one that still names the source
// config directory by absolute path (warnAbsolute), and, for codex, an enabled
// plugin declaration whose cache this profile does not have
// (warnUninstalledCodexPlugins). Clone is the plain entry point most callers
// want; this one exists so cmd/ap can surface the warnings after `create --from`.
func CloneWithWarnings(a agent.Agent, src, dst string) ([]string, error) {
	// Resolve src before anything else. Lstat below would otherwise inspect the
	// symlink rather than the directory it points to.
	src, err := filepath.EvalSymlinks(src)
	if err != nil {
		return nil, fmt.Errorf("cannot read source profile: %w", err)
	}
	fi, err := os.Lstat(src)
	if err != nil {
		return nil, fmt.Errorf("cannot read source profile %s: %w", src, err)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("source profile %s is not a directory", src)
	}
	if err := containment(src, dst); err != nil {
		return nil, err
	}

	root, err := os.OpenRoot(dst)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()

	for _, rel := range a.CloneAllow {
		if err := cloneEntry(root, src, rel); err != nil {
			return nil, fmt.Errorf("cloning %s: %w", rel, err)
		}
	}

	warnings, err := warnAbsolute(a, src, dst)
	if err != nil {
		return nil, err
	}
	if a.Name == "codex" {
		pluginWarnings, err := warnUninstalledCodexPlugins(dst, a.Name+":"+filepath.Base(dst))
		if err != nil {
			return nil, err
		}
		warnings = append(warnings, pluginWarnings...)
	}
	return warnings, nil
}

// cloneEntry copies one CloneAllow entry — a file or a whole directory — from
// src into dst through root, which is confined to dst.
//
// escapesRoot is checked before anything else, rather than left to os.Root to
// catch: filepath.Join(src, rel) Cleans the two together, so a "rel" such as
// "../../etc/passwd" can collapse into a path that simply does not exist under
// src at typical temp-dir depths — which "tolerate missing" would then wave
// through instead of rejecting.
func cloneEntry(root *os.Root, src, rel string) error {
	if escapesRoot(rel) {
		return fmt.Errorf("allowlist entry %q escapes the profile directory", rel)
	}
	srcPath := filepath.Join(src, rel)
	fi, err := os.Lstat(srcPath)
	if os.IsNotExist(err) {
		return nil // tolerated: profiles and real config dirs differ in contents
	}
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return nil // never copy a symlink; Link recreates the shared ones
	}
	if !fi.IsDir() {
		return copyFileToRoot(root, srcPath, rel, fi)
	}
	return filepath.WalkDir(srcPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		entryRel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			// Also creates the entry's own root directory, on the first call
			// (entryRel == rel), which is what makes an allowlisted directory that
			// happens to be empty in the source still appear in the clone.
			return root.MkdirAll(entryRel, info.Mode().Perm())
		}
		if !d.Type().IsRegular() {
			return nil // sockets, fifos, devices: nothing a profile needs
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return copyFileToRoot(root, path, entryRel, info)
	})
}

// escapesRoot reports whether rel, once cleaned, references something above
// the directory it will be joined onto — checked precisely rather than with
// strings.Contains(rel, ".."), which would also reject a name that merely
// contains the two characters, like "settings..bak".
func escapesRoot(rel string) bool {
	cleaned := filepath.Clean(rel)
	return cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(os.PathSeparator))
}

// containment rejects a destination that is src itself or lives inside src.
//
// Without this, `ap create --from . <name>` resolves the source to the parent of
// the destination, so WalkDir descends into the directory Clone is writing to and
// recurses until the path is too long for the filesystem — 2000+ directories, and
// with a populated source it re-copies every real file at each level.
// src arrives already resolved, so dst must be resolved too or the comparison is
// between a real path and a symlinked one. On macOS that is the normal case:
// /var is a symlink to /private/var, so an unresolved dst under /var/folders
// looks like it escapes a src under /private/var/folders and the guard passes on
// exactly the overlap it exists to catch.
func containment(src, dst string) error {
	dst, err := filepath.EvalSymlinks(dst)
	if err != nil {
		return fmt.Errorf("cannot read destination profile: %w", err)
	}
	rel, err := filepath.Rel(src, dst)
	if err != nil {
		return err // different volumes: cannot overlap
	}
	if rel == "." {
		return fmt.Errorf("source and destination are the same directory: %s", src)
	}
	// Escaping src means rel is ".." or starts with "../" — checked precisely
	// rather than with HasPrefix(rel, ".."), which would also accept a sibling
	// directory literally named "..foo".
	if rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("destination %s is inside the source %s", dst, src)
	}
	return nil
}

// warnAbsolute reports cloned files that contain, as a literal path, either the
// agent's real config directory (a.Config) or the profile Clone actually copied
// from (src) — two distinct traps, both worth a warning:
//
//   - a.Config: a hook command of "/home/user/.claude/hooks/x.js" runs the real
//     script forever, from every profile that was ever cloned from anything,
//     because it never named a particular clone in the first place. This is the
//     common case — it is what any settings.json carries once it has been
//     copied out of the real config even once — and it is checked regardless of
//     whether src happens to be that same real config directory (`--from
//     default`) or some other profile.
//   - src, when it differs from a.Config: the clone runs the SOURCE PROFILE's
//     script, not its own copy. Reported with its own wording — calling a
//     profile "the real config directory" would be simply wrong.
//
// "$CLAUDE_CONFIG_DIR/hooks/x.js" follows the profile in both cases - verified,
// hooks inherit that variable and it resolves to whichever profile is running.
//
// Reported, never rewritten. Editing someone's hook command on their behalf is
// how you break it in a way nobody can find.
func warnAbsolute(a agent.Agent, src, dst string) ([]string, error) {
	var warnings []string
	err := filepath.WalkDir(dst, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() > 1<<20 {
			return nil // large files are never the small settings/config kind this looks for
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		content := string(b)
		rel, err := filepath.Rel(dst, path)
		if err != nil {
			return err
		}
		if a.Config != "" && strings.Contains(content, a.Config) {
			warnings = append(warnings, fmt.Sprintf(
				"%s names your real config directory (%s), so this profile will run that copy, not its own - point it at $%s instead",
				rel, a.Config, a.ConfigEnv))
		}
		// Skipped when src is the real config directory (`--from default`): that
		// is the case above, and reporting it twice under two different names
		// would only confuse.
		if src != a.Config && strings.Contains(content, src) {
			warnings = append(warnings, fmt.Sprintf(
				"%s names the source profile (%s), so this clone will run the source profile's copy, not its own - point it at $%s instead",
				rel, src, a.ConfigEnv))
		}
		return nil
	})
	return warnings, err
}

// codexPluginHeader matches a config.toml section header for a plugin
// declaration, e.g. [plugins."probe@aptest"].
var codexPluginHeader = regexp.MustCompile(`^\[plugins\."([^"]+)"\]$`)

// warnUninstalledCodexPlugins reports a codex plugin declared enabled in the
// cloned config.toml whose cache this profile does not have.
//
// codex keeps every plugin declaration in config.toml and nowhere else
// ([marketplaces.<name>], [plugins."<p>@<m>"]), but unlike claude it never
// reconciles that declaration against its cache on its own: a profile cloned
// with only config.toml reports the plugin as not installed from
// `codex plugin list`, and stays that way through a full session — during
// which codex happily installs its own curated default instead. One
// `codex plugin add <p>@<m>` fixes it, idempotently, so this is reported
// rather than run on the user's behalf at create time.
func warnUninstalledCodexPlugins(dst, ref string) ([]string, error) {
	b, err := os.ReadFile(filepath.Join(dst, "config.toml"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var warnings []string
	var current string
	enabled := false
	flush := func() {
		if current == "" || !enabled {
			return
		}
		plugin, marketplace, ok := strings.Cut(current, "@")
		if !ok {
			return
		}
		cacheDir := filepath.Join(dst, "plugins", "cache", marketplace, plugin)
		if _, err := os.Stat(cacheDir); os.IsNotExist(err) {
			warnings = append(warnings, fmt.Sprintf(
				"config.toml declares plugin %q as enabled, but it is not installed in this profile - fix with: ap run %s plugin add %s",
				current, ref, current))
		}
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if m := codexPluginHeader.FindStringSubmatch(line); m != nil {
			flush()
			current, enabled = m[1], false
			continue
		}
		if strings.HasPrefix(line, "[") {
			flush()
			current, enabled = "", false
			continue
		}
		if current != "" && line == "enabled = true" {
			enabled = true
		}
	}
	flush()
	return warnings, nil
}

// copyFileToRoot copies the regular file at src to rel inside root. Every write
// — the parent directory and the file itself — goes through root, confined to
// dst, so a rel containing ".." fails instead of writing outside the clone.
func copyFileToRoot(root *os.Root, src, rel string, fi os.FileInfo) error {
	if parent := filepath.Dir(rel); parent != "." {
		if err := root.MkdirAll(parent, 0o700); err != nil {
			return err
		}
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }() // read-only: a close error says nothing
	// Same permissions as the source: profiles hold credentials and settings.
	out, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, fi.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close() // returning the copy error, which is the useful one
		return err
	}
	return out.Close()
}
