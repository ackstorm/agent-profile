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

// Kind distinguishes a directory link from a file link.
type Kind int

const (
	Dir Kind = iota
	File
)

func (k Kind) String() string {
	if k == File {
		return "file"
	}
	return "directory"
}

// Share is one piece of state that must stay common to every profile: a link at
// Rel (relative to the profile dir) pointing at From (absolute, in the real
// home).
type Share struct {
	Rel  string
	From string
	Kind Kind
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
	return map[string]Agent{
		"claude": {
			Name:      "claude",
			Bin:       "claude",
			ConfigEnv: "CLAUDE_CONFIG_DIR",
			Mode:      Replace,
			Shared: []Share{
				{Rel: "projects", From: filepath.Join(h, ".claude", "projects"), Kind: Dir},
				{Rel: ".credentials.json", From: filepath.Join(h, ".claude", ".credentials.json"), Kind: File},
				// hasTrustDialogAccepted lives here, so sharing it stops the
				// workspace-trust prompt reappearing in every profile. Cost: MCP
				// servers added with `claude mcp add` are per project in this file
				// and become visible everywhere. Put per-profile MCP in the
				// profile's settings.json under mcpServers instead.
				{Rel: ".claude.json", From: filepath.Join(h, ".claude.json"), Kind: File},
				{Rel: "CLAUDE.md", From: filepath.Join(h, ".claude", "CLAUDE.md"), Kind: File},
				// Content-addressed by marketplace/name/version: safe to share and
				// saves re-downloading every plugin per profile.
				{Rel: "plugins/cache", From: filepath.Join(h, ".claude", "plugins", "cache"), Kind: Dir},
			},
		},
		"codex": {
			Name:      "codex",
			Bin:       "codex",
			ConfigEnv: "CODEX_HOME",
			Mode:      Replace,
			Shared: []Share{
				{Rel: "sessions", From: filepath.Join(h, ".codex", "sessions"), Kind: Dir},
				{Rel: "auth.json", From: filepath.Join(h, ".codex", "auth.json"), Kind: File},
				{Rel: "history.jsonl", From: filepath.Join(h, ".codex", "history.jsonl"), Kind: File},
			},
		},
		"pi": {
			Name:      "pi",
			Bin:       "pi",
			ConfigEnv: "PI_CODING_AGENT_DIR",
			Mode:      Replace,
			Shared: []Share{
				{Rel: "sessions", From: filepath.Join(h, ".pi", "agent", "sessions"), Kind: Dir},
				// Empty ({}) on the reference machine: pi reads provider keys from
				// env vars, which the child inherits anyway. Linked because it
				// costs nothing and covers the case where keys are stored here.
				{Rel: "auth.json", From: filepath.Join(h, ".pi", "agent", "auth.json"), Kind: File},
			},
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
			// Sessions, auth and state live under XDG_DATA_HOME and XDG_STATE_HOME,
			// which we never touch, so nothing needs linking back.
			Shared: nil,
			Note:   "isolated through a config shim; see `ap which` and the README",
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
