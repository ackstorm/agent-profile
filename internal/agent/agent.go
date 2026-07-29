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
	// Shim is non-nil only for an agent whose isolation needs a shared variable.
	Shim *Shim
	// Note is shown by `ap agents`, explaining any caveat.
	Note string
}

func home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
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
			// Verified. It was in Shared until this release, so the path is known good.
			Instructions: &Instructions{Name: "CLAUDE.md", Source: filepath.Join(h, ".claude", "CLAUDE.md")},
			Setup:        "ap run %s plugin install <plugin>   (or build it from a file: ap create --spec <file>)",
		},
		"codex": {
			Name:      "codex",
			Bin:       "codex",
			ConfigEnv: "CODEX_HOME",
			Mode:      Replace,
			Shared: []Share{
				{Rel: "auth.json", From: filepath.Join(h, ".codex", "auth.json")},
			},
			Unshared: []string{"sessions", "history.jsonl"},
			State:    []string{"sessions", "history.jsonl"},
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
			ConfigEnv: "PI_CODING_AGENT_DIR",
			Mode:      Replace,
			Shared: []Share{
				// Empty ({}) on the reference machine: pi reads provider keys from env
				// vars, which the child inherits anyway. Linked because it costs nothing
				// and covers the case where keys are stored here.
				{Rel: "auth.json", From: filepath.Join(h, ".pi", "agent", "auth.json")},
			},
			Unshared: []string{"sessions"},
			State:    []string{"sessions"},
			Setup:    "ap run %s config",
		},
		"opencode": {
			Name: "opencode",
			Bin:  "opencode",
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
			// Sessions AND auth live under XDG_DATA_HOME and XDG_STATE_HOME, which ap
			// never redirects — redirecting them is exactly what the shim exists to
			// avoid doing to every other program in the tree. So opencode cannot honour
			// the one-credential rule: its sessions stay global across profiles. Known
			// asymmetry, documented in docs/spec.md, not worth a second shim.
			Shared: nil,
			// State is nil for the same reason: its sessions are outside the profile
			// entirely, so a clone cannot carry them.
			Setup: "ap run %s providers   (a custom provider means editing opencode.json inside the profile)",
			Note:  "isolated through a config shim; sessions stay global - see the README",
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
