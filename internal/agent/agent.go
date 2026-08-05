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

// Shim describes an agent that has no private variable for some part of its
// state, so isolating it means setting a variable other programs also read.
//
// The profile then carries a directory, Rel, that is safe to point Env at: it
// holds Entry (a link to the profile itself, which is what the agent looks for)
// plus one passthrough link per entry of the real base, so every other program
// the agent spawns still resolves to its own real directory. profile.Shim builds
// it and re-asserts it on every run.
//
// An agent may need more than one: opencode's config comes from XDG_CONFIG_HOME
// and its sessions from XDG_DATA_HOME, and neither has a private alternative.
//
// Only reach for this when the agent genuinely has no private variable. Verify by
// reading the code that computes the path, not the documentation.
type Shim struct {
	// Env is the shared variable this shim makes safe to set.
	Env string
	// Rel is the subdirectory of the profile that Env points at. Unique per
	// agent: two shims at one Rel would overwrite each other's links.
	Rel string
	// Entry is the name the agent looks for inside it, linked to the profile.
	Entry string
	// Fallback is the path under $HOME that Env resolves to when it is unset,
	// e.g. ".config" or ".local/share".
	Fallback string
}

// Layout says how an agent's session store is walked and where the metadata is.
// Four agents, four answers; none of them is a documented interface.
type Layout int

const (
	// LayoutClaudeProjects: projects/<encoded-cwd>/<uuid>.jsonl. The id is the
	// filename and the cwd is inside the file — the directory name encodes both
	// '/' and '.' as '-' and cannot be reversed.
	LayoutClaudeProjects Layout = iota
	// LayoutCodexRollouts: sessions/YYYY/MM/DD/rollout-<ISO>-<uuid>.jsonl, with a
	// session_meta object on the first line.
	LayoutCodexRollouts
	// LayoutPiSessions: sessions/<encoded-cwd>/<ISO>_<uuid>.jsonl, with a session
	// header on the first line.
	LayoutPiSessions
	// LayoutExec: not a file layout at all. opencode keeps sessions in sqlite,
	// which this repository cannot read without a dependency, so its own CLI is
	// asked instead.
	LayoutExec
)

// SessionStore says where an agent keeps past conversations and how to resume one.
//
// Verified by reading the real files and by running each binary's resume path.
// See docs/plans/2026-08-05-ap-sessions.md for the measurements.
type SessionStore struct {
	// Rel is the directory under the config dir holding sessions. Empty for
	// LayoutExec, which has no directory to walk.
	Rel string
	// Layout is how to read it.
	Layout Layout
	// ResumeArgs is the argv that resumes a session, with exactly one "{}" where
	// the id goes — the same placeholder a variant uses, and for the same reason:
	// the position is stated, never inferred.
	//
	// claude: --resume <id>   codex: resume <id>
	// pi:     --session <id>  opencode: -s <id>
	ResumeArgs []string
}

// Base is the directory Env normally resolves to, read exactly the way a
// freedesktop-following program reads it: Env when set, otherwise $HOME/Fallback.
//
// This must be evaluated against the environment ap inherited, before ap sets
// anything, or the shim would end up pointing at itself.
func (s Shim) Base() string {
	if d := os.Getenv(s.Env); d != "" {
		return d
	}
	h := home()
	if h == "" {
		return ""
	}
	return filepath.Join(h, s.Fallback)
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
	// Shims lists every shared variable this agent needs made safe. Empty for an
	// agent with a private config variable, which is three of the four.
	Shims []Shim
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
	// Sessions says where this agent keeps its past conversations and how to resume one.
	Sessions *SessionStore
}

func home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}

// ConfigBase is the directory freedesktop-config-following programs resolve
// their config root against: XDG_CONFIG_HOME when set, otherwise ~/.config.
//
// Used by opencode's Config below and by profile.ConfigBase.
func ConfigBase() string {
	return Shim{Env: "XDG_CONFIG_HOME", Fallback: ".config"}.Base()
}

// dataBase is the directory freedesktop-following programs resolve their data
// root against: XDG_DATA_HOME when set, otherwise ~/.local/share.
func dataBase() string {
	return Shim{Env: "XDG_DATA_HOME", Fallback: ".local/share"}.Base()
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
			Sessions: &SessionStore{
				Rel:        "projects",
				Layout:     LayoutClaudeProjects,
				ResumeArgs: []string{"--resume", "{}"},
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
			Sessions: &SessionStore{
				Rel:        "sessions",
				Layout:     LayoutCodexRollouts,
				ResumeArgs: []string{"resume", "{}"},
			},
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
				// Where pi's providers actually live: baseUrl, api and the custom model
				// list. Without it a fresh profile has no usable model — the same failure
				// as starting logged out, which is what Share is for. Verified: it holds
				// an $ENV reference, never a literal key.
				//
				// models-store.json is NOT shared: 72 KB of downloaded catalogue that pi
				// regenerates inside each profile on its own.
				{Rel: "models.json", From: filepath.Join(h, ".pi", "agent", "models.json")},
			},
			Unshared:       []string{"sessions"},
			State:          []string{"sessions", "models-store.json"},
			CloneAllow:     []string{"settings.json"},
			Sessions: &SessionStore{
				Rel:        "sessions",
				Layout:     LayoutPiSessions,
				ResumeArgs: []string{"--session", "{}"},
			},
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
			// Two shared variables, no private alternative for either. Config
			// comes from XDG_CONFIG_HOME; sessions, credentials and snapshots
			// come from XDG_DATA_HOME, where opencode keeps opencode.db.
			//
			// Both point Entry at the profile itself, so config and data resolve
			// to one directory. Verified: no name collision, opencode.jsonc and
			// opencode.db live side by side.
			//
			// XDG_STATE_HOME is deliberately NOT shimmed: prompt-history.jsonl,
			// model.json and locks/ stay shared. Measured — a run never wrote
			// there — so isolating it would be cost with no observed benefit.
			Shims: []Shim{
				{Env: "XDG_CONFIG_HOME", Rel: "xdg", Entry: "opencode", Fallback: ".config"},
				{Env: "XDG_DATA_HOME", Rel: "xdg-data", Entry: "opencode", Fallback: ".local/share"},
			},
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
			// opencode's credentials live under the data directory, which is now
			// shimmed, so they must be linked back or every profile starts logged
			// out. Measured: a run through these symlinks left all three files
			// byte-identical — opencode does not do claude's temp-file-plus-rename,
			// so none of this needs the Promote/orphan path Link has for
			// .credentials.json. If that ever changes, the machinery is already there.
			Shared: []Share{
				{Rel: "auth.json", From: filepath.Join(dataBase(), "opencode", "auth.json")},
				{Rel: "account.json", From: filepath.Join(dataBase(), "opencode", "account.json")},
				{Rel: "mcp-auth.json", From: filepath.Join(dataBase(), "opencode", "mcp-auth.json")},
			},
			// What the profile accumulates by being used, now that data is
			// shimmed: the session db and its sqlite sidecars, the git snapshots
			// opencode takes to support revert, and the logs. Skipped by
			// `ap create --from`, which copies configuration and never history.
			//
			// The -wal and -shm sidecars are listed explicitly. Copying the db
			// without its WAL yields a db missing whatever had not been
			// checkpointed, which is worse than not copying it at all.
			State: []string{"opencode.db", "opencode.db-wal", "opencode.db-shm", "snapshot", "log", "repos"},
			Sessions: &SessionStore{
				Layout:     LayoutExec,
				ResumeArgs: []string{"-s", "{}"},
			},
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
