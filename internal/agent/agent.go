// Package agent holds the table of supported coding agents and how each one
// can be pointed at an alternate config directory.
//
// Every field here was verified by running the real binary, not read from
// documentation. scripts/smoke.sh re-checks each claim; when it fails, fix the
// row here rather than the check.
package agent

import (
	"os"
	"path/filepath"
	"sort"
)

// Mode says what the agent's config-dir variable does to its normal config.
type Mode int

const (
	// Replace: the variable becomes the agent's only config root. Sessions and
	// credentials that live under it must be linked back, or every profile
	// starts logged out with no history.
	Replace Mode = iota
	// Additive: the variable is an extra config directory searched alongside the
	// normal one. Nothing forks, but the global config still applies. No agent
	// uses this today; it stays because the next one added might.
	Additive
)

func (m Mode) String() string {
	if m == Additive {
		return "additive"
	}
	return "replace"
}

// Format is how an agent's settings file is written, which is all
// --only-settings needs to know to slice it.
type Format int

const (
	// JSON is decoded and re-encoded: the selected subtrees are copied whole,
	// with stable key order and two-space indent.
	JSON Format = iota
	// TOML is never parsed — there is no TOML decoder in the standard library
	// and this repository takes no dependencies. Whole blocks are copied
	// verbatim instead, so every emitted byte is a byte of the source.
	TOML
)

// Share is the one piece of state that stays common to every profile: a link at
// Rel (relative to the profile dir) pointing at From (absolute, in the real home).
//
// In practice that is the credential and nothing else. Everything a session depends
// on — plugins, skills, MCP servers, instructions — lives in the profile, so
// anything that records a session belongs there too. A `plan` transcript resumed
// inside an `exec` profile replays tool calls to servers that are not installed.
//
// There used to be a Kind field distinguishing a directory share from a file one.
// Every share is a file now. Add it back if a directory share ever returns; it was
// five lines.
type Share struct {
	Rel  string
	From string
}

// Instructions is an agent's machine-wide instructions file — CLAUDE.md for claude,
// AGENTS.md elsewhere — which `ap create --copy-instructions` copies into a new
// profile.
//
// A copy, once, at create. Not a Share: nothing re-asserts it, nothing keeps the two
// files in step afterwards, and editing the profile's copy is the normal thing to do
// with it. The field names differ from Share's on purpose, so a misplaced value does
// not compile.
type Instructions struct {
	// Name is the file inside the profile.
	Name string
	// Source is the machine-wide file it is copied from.
	Source string
}

// FirstRun is the handful of keys a fresh profile must start with so the agent
// does not put its first-run wizard in front of a profile that is already
// logged in.
//
// Measured, claude v2.1.220: a config directory holding nothing but the shared
// credential runs the theme picker on every new profile. `claude -p` does not —
// which is why the credential-only design looked complete when it was verified
// that way. A `.claude.json` carrying `hasCompletedOnboarding` alone is enough;
// `settings.json`, empty or carrying a theme, changes nothing.
//
// Like Instructions and unlike Share, this is copied once, at create, into a
// file the profile does not have yet — the agent owns it from then on, and
// nothing re-asserts it. That is what separates it from the per-key filtering of
// a live shared file, which would fight the agent on every write.
//
// nil means "not verified for this agent", the same bar as Instructions.
type FirstRun struct {
	// Name is the file inside the profile.
	Name string
	// Source is the machine-wide file the keys are read from.
	Source string
	// Keys are the only keys copied. Keep this to first-run flags: everything
	// else in these files is either session state or belongs to the profile.
	Keys []string
}

// Shim describes an agent that has no config-directory variable of its own, so
// isolating it means setting a variable other programs also read.
//
// The profile then carries a directory, Rel, that is safe to point that variable
// at: it holds Entry (a link to the profile itself, which is what the agent
// looks for) plus one passthrough link per entry of the real config base, so
// every other program the agent spawns still resolves to its own real config.
// profile.Shim builds it and re-asserts it on every run.
//
// Only reach for this when the agent genuinely has no private variable. Verify
// by reading the code that computes its config path, not the documentation.
type Shim struct {
	// Rel is the subdirectory of the profile that ConfigEnv points at.
	Rel string
	// Entry is the name the agent looks for inside it, linked to the profile.
	Entry string
}

// Agent is one supported CLI.
type Agent struct {
	Name string
	// Bin is the executable name looked up on PATH.
	Bin string
	// Config is the agent's real, machine-wide config directory — the one it uses
	// when ap is not involved. It is the source for `--from default` and the target
	// `<agent>:default` resolves to.
	//
	// Not derived from ConfigEnv: that variable says where to point an agent, not
	// where it already looks. pi's is ~/.pi/agent and opencode's is under ~/.config,
	// neither of which follows from the name.
	Config string
	// ConfigEnv is the variable set to the profile directory, or to the shim
	// directory inside it when Shim is set. Agent specific wherever the agent
	// offers one, because every child process inherits what we set.
	ConfigEnv string
	Mode      Mode
	Shared    []Share
	// Unshared lists relative paths that USED to be in Shared. Link removes a
	// symlink found at any of them, so dropping an entry from Shared actually
	// un-shares it in profiles created before the change rather than leaving the
	// old link in place forever. Removing a symlink never touches its target.
	//
	// Only ever a symlink is removed: a real file at one of these paths belongs to
	// the profile now and is left alone.
	//
	// Entries can be deleted once no profile from the release that shared them is
	// plausibly still on disk.
	Unshared []string
	// Instructions names the agent's global instructions file, for
	// `ap create --copy-instructions`.
	//
	// Deliberately NOT a Share. A Share is linked, and re-asserted on every run; this
	// is copied once, at create, and never looked at again. Same two strings, opposite
	// lifecycle — sharing one struct between them would tell the next reader that Link
	// handles this, and Link never touches it.
	//
	// nil means "not verified for this agent". Only fill it in once you have run the
	// binary and watched it read the file, the same bar as every other row here. A
	// guessed path is worse than none: the flag would silently copy nothing, or copy
	// to a name the agent never opens.
	Instructions *Instructions
	// FirstRun names the keys `ap create` seeds a new profile with so the agent
	// does not re-run its first-run wizard. See FirstRun's doc comment.
	FirstRun *FirstRun
	// Setup is printed after `ap create`, because a profile now starts genuinely
	// empty and the next step is different for every agent. One %s verb, given the
	// <agent>:<profile> reference. Each command was taken from that binary's own
	// --help output, not guessed; `scripts/smoke.sh` does not check these, so keep
	// them short and keep them true.
	Setup string
	// State lists paths holding what a profile accumulates by being used — session
	// transcripts, command history. Clone skips them, so `ap create --from` inherits
	// the configuration and never the other profile's history.
	//
	// Distinct from Unshared, which is a migration list and will eventually empty.
	// This one is permanent: it says what belongs to a profile rather than to the
	// machine. A path may appear in both, and for the entries that stopped being
	// shared in v0.2.0 it does.
	//
	// Not merely a size concern, though it is that too — 304 MB of transcripts on
	// the reference machine. A clone that carried another profile's sessions would
	// let you resume, inside the clone, a conversation that used tools the clone
	// does not have.
	State []string
	// CloneAllow is what "configuration" means for this agent: the paths, relative to
	// the config directory, that `ap create --from` copies. Everything else is left
	// behind.
	//
	// An allowlist rather than a list of exclusions, and the difference matters. A
	// real config directory is mostly accumulated runtime — on the reference machine
	// claude's held 1.4 GB of tmp, 309 MB of transcripts and 108 MB of plugin content
	// — and that set grows with every upstream release. An exclusion list would start
	// copying each new cache directory silently. This way a new config file is simply
	// not cloned, which someone notices and fixes.
	//
	// Directories are copied whole. A missing entry is not an error: profiles and real
	// config directories differ in what they happen to contain.
	//
	// Hardcoded here for now. These belong in a user config file, so cases like a hook
	// script living somewhere unusual can be declared per machine.
	CloneAllow []string
	// Shim is non-nil only for an agent whose isolation needs a shared variable.
	Shim *Shim
	// Settings is the agent's settings file, relative to its config directory:
	// the one file `ap create --only-settings` slices. Always also a CloneAllow
	// entry, so a narrowed clone can never reach a path the unfiltered clone
	// cannot — TestEveryAgentDeclaresItsSettingsFile is what keeps that true.
	//
	// A separate field rather than CloneAllow[0]: deriving it from the slice
	// would make reordering that list change behaviour, which nobody would see
	// in review.
	Settings string
	// SettingsFormat says how Settings is sliced.
	SettingsFormat Format
}

func home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}

// ConfigBase is the directory freedesktop-config-following programs resolve
// their config root against, read exactly the way they read it: XDG_CONFIG_HOME
// when set, otherwise ~/.config.
//
// The one definition, used by opencode's Config below and by
// profile.ConfigBase — which delegates here rather than keeping its own copy,
// since agent has no dependency on profile and can be the single source
// without an import cycle. Before this was one function, opencode's Config
// hardcoded ~/.config/opencode while profile.ConfigBase() (and
// scripts/smoke.sh, independently, in shell) already honoured
// XDG_CONFIG_HOME — a three-way disagreement that made `ap create --from
// default` and `ap run opencode:default` silently target the wrong directory
// on any machine that sets XDG_CONFIG_HOME.
func ConfigBase() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return d
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(h, ".config")
}

func registry() map[string]Agent {
	h := home()
	// Every agent shares exactly one thing: its credential. A profile is a separate
	// environment, and everything else — config, plugins, skills, session history —
	// belongs to the environment that produced it.
	//
	// A profile needs only the credential to be logged in: verified on a fresh
	// config dir holding nothing but that link, where `claude -p` answered with no
	// login and no onboarding and claude wrote its own .claude.json with
	// oauthAccount filled in from the credential.
	return map[string]Agent{
		"claude": {
			Name:      "claude",
			Bin:       "claude",
			Config:    filepath.Join(h, ".claude"),
			ConfigEnv: "CLAUDE_CONFIG_DIR",
			Mode:      Replace,
			Shared: []Share{
				{Rel: ".credentials.json", From: filepath.Join(h, ".claude", ".credentials.json")},
			},
			// .claude.json was shared for hasTrustDialogAccepted. It is also where
			// user-scope MCP servers live, so sharing it made a per-profile MCP server
			// impossible; every workaround involved injecting flags into `ap run`.
			// projects/ is session transcripts. See Share's doc comment for why they
			// are not shared.
			Unshared: []string{".claude.json", "CLAUDE.md", "plugins/cache", "projects"},
			State:    []string{"projects"},
			// Plugin intent rides in settings.json (extraKnownMarketplaces /
			// enabledPlugins), so cloning it carries the same plugins without copying
			// the 108 MB of plugin content — claude re-materialises them itself on the
			// next couple of starts.
			CloneAllow: []string{"settings.json", "CLAUDE.md", "skills", "commands", "hooks", "agents"},
			// Measured: a settings.json holding only statusLine and theme starts
			// cleanly under a pty (claude v2.1.220) — no complaint about the file,
			// and the status line's own command renders at the bottom of the
			// screen. `claude -p` was not used for this: it never shows a wizard
			// or a status line either way, so it would have proven nothing.
			Settings:       "settings.json",
			SettingsFormat: JSON,
			// Verified. It was in Shared until this release, so the path is known good.
			Instructions: &Instructions{Name: "CLAUDE.md", Source: filepath.Join(h, ".claude", "CLAUDE.md")},
			// Note the paths: claude reads ~/.claude.json outside a profile, but
			// writes <CLAUDE_CONFIG_DIR>/.claude.json inside one. Measured: without
			// hasCompletedOnboarding every new profile opens on the theme picker.
			FirstRun: &FirstRun{
				Name:   ".claude.json",
				Source: filepath.Join(h, ".claude.json"),
				Keys:   []string{"hasCompletedOnboarding"},
			},
			Setup: "ap run %s plugin install <plugin>",
		},
		"codex": {
			Name:      "codex",
			Bin:       "codex",
			Config:    filepath.Join(h, ".codex"),
			ConfigEnv: "CODEX_HOME",
			Mode:      Replace,
			Shared: []Share{
				{Rel: "auth.json", From: filepath.Join(h, ".codex", "auth.json")},
			},
			Unshared: []string{"sessions", "history.jsonl"},
			State:    []string{"sessions", "history.jsonl"},
			// Every plugin declaration lives in config.toml and nowhere else:
			// [marketplaces.<name>] (source_type/source) and [plugins."<p>@<m>"]
			// (enabled = true). plugins/ itself (28 MB on the reference machine,
			// plugins/cache/<marketplace>/<plugin>/<version>/) is a regenerable
			// cache — even the built-in openai-curated marketplace resyncs itself
			// into .tmp/plugins — so no plugins path belongs here.
			//
			// Verified by running codex against a profile cloned with only
			// config.toml: `codex plugin list` reported the declared plugin as not
			// installed, and stayed that way through a full session, during which
			// codex happily installed its own curated default instead. Unlike
			// claude, codex never reconciles a declaration against its cache on its
			// own — one `codex plugin add <p>@<m>` fixes it, idempotently. See the
			// clone warning in clone.go that surfaces exactly that command.
			CloneAllow: []string{"config.toml", "hooks.json", "skills"},
			// Measured: `codex doctor` (v0.146.0) against a config.toml holding only
			// [tui] and [tui.model_availability_nux], with no top-level model, still
			// reports "config loaded" / "config.toml parse ok" and no missing-model
			// error — codex falls back to its own default model provider.
			Settings:       "config.toml",
			SettingsFormat: TOML,
			// Instructions is nil on purpose. The AGENTS.md convention is documented
			// upstream for codex, opencode and pi, but no global file exists on the
			// reference machine, so none of them has been watched reading one. Verify
			// the way everything else here was verified — write a marker instruction
			// into the candidate path, run the agent with a print-mode prompt, see
			// whether the marker comes back — then add the row. Until then
			// --copy-instructions fails loudly for these agents, which is the honest
			// answer.
			Setup: "ap run %s mcp   (settings live in config.toml inside the profile)",
		},
		"pi": {
			Name:      "pi",
			Bin:       "pi",
			Config:    filepath.Join(h, ".pi", "agent"),
			ConfigEnv: "PI_CODING_AGENT_DIR",
			Mode:      Replace,
			Shared: []Share{
				// Empty ({}) on the reference machine: pi reads provider keys from env
				// vars, which the child inherits anyway. Linked because it costs nothing
				// and covers the case where keys are stored here.
				{Rel: "auth.json", From: filepath.Join(h, ".pi", "agent", "auth.json")},
			},
			Unshared:       []string{"sessions"},
			State:          []string{"sessions"},
			CloneAllow:     []string{"settings.json", "models.json"},
			Setup:          "ap run %s config",
			Settings:       "settings.json",
			SettingsFormat: JSON,
		},
		"opencode": {
			Name:   "opencode",
			Bin:    "opencode",
			Config: filepath.Join(ConfigBase(), "opencode"),
			// opencode has no private config-dir variable. OPENCODE_CONFIG_DIR only
			// APPENDS to its search path, and OPENCODE_CONFIG / OPENCODE_CONFIG_CONTENT
			// append too. Its config root is computed as
			//     (XDG_CONFIG_HOME || homedir()/.config) / "opencode"
			// and nothing else feeds into it, so XDG_CONFIG_HOME is the only lever.
			// Pointing it at the shim rather than the raw profile is what keeps that
			// safe for every other program in the process tree.
			ConfigEnv: "XDG_CONFIG_HOME",
			Mode:      Replace,
			Shim:      &Shim{Rel: "xdg", Entry: "opencode"},
			// Never node_modules — 62 MB on the reference machine, and reinstalled
			// by opencode itself.
			CloneAllow:     []string{"opencode.json", "agents", "command", "skills"},
			Settings:       "opencode.json",
			SettingsFormat: JSON,
			// Sessions AND auth live under XDG_DATA_HOME and XDG_STATE_HOME, which ap
			// never redirects — redirecting them is exactly what the shim exists to
			// avoid doing to every other program in the tree. So opencode cannot honour
			// the one-credential rule: its sessions stay global across profiles. Known
			// asymmetry, documented in the README, not worth a second shim.
			Shared: nil,
			// State is nil for the same reason: its sessions are outside the profile
			// entirely, so a clone cannot carry them.
			Setup: "ap run %s providers   (a custom provider means editing opencode.json inside the profile)",
		},
	}
}

// Lookup returns the agent by name.
func Lookup(name string) (Agent, bool) {
	a, ok := registry()[name]
	return a, ok
}

// Names returns supported agent names, sorted.
func Names() []string {
	r := registry()
	out := make([]string, 0, len(r))
	for k := range r {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
