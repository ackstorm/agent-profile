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
	// normal one. Nothing forks, but the global config still applies.
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

// Agent is one supported CLI.
type Agent struct {
	Name string
	// Bin is the executable name looked up on PATH.
	Bin string
	// ConfigEnv is the variable set to the profile directory. Always agent
	// specific: we never set a generic variable such as XDG_CONFIG_HOME, because
	// every child process the agent spawns would inherit it.
	ConfigEnv string
	Mode      Mode
	Shared    []Share
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
			Name:      "opencode",
			Bin:       "opencode",
			ConfigEnv: "OPENCODE_CONFIG_DIR",
			Mode:      Additive,
			// Sessions and auth live in ~/.local/share/opencode, outside the
			// config root, so nothing needs linking.
			Shared: nil,
			Note:   "additive: your global ~/.config/opencode still loads; use `ap run --pure` to suppress it",
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
