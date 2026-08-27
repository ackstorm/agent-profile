package hydrate

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ackstorm/agent-profile/pkg/agentreg"
)

// Adapter says WHERE each kind of thing lands for one runtime, relative to the
// root. It says nothing about HOW: copying, hashing and recording are identical
// for every runtime, and giving each adapter its own copy of that is how they
// drift.
//
// A destination that does not exist for a runtime returns ok=false. The caller
// WARNS and skips (§8) — it never drops silently, because "this runtime does
// not support X" is the one thing that tells a user why nothing happened.
type Adapter interface {
	Name() string
	// SkillDir is where a named skill's directory goes.
	SkillDir(name string) (rel string, ok bool)
	// ArtifactDest resolves a manifest's `destination` under the root.
	ArtifactDest(destination string) (rel string, err error)
	// MCPTarget is the configuration file MCP servers merge into and the
	// top-level key inside it. ok=false means this runtime has no config-dir
	// MCP surface, which is a §8 degradation, not something to invent.
	MCPTarget() (rel, key string, ok bool)
	// PackageTarget is the file a runtime's OWN package declarations live in
	// and the key holding the list. ok=false means this runtime has no such
	// list, which is a §8 degradation and not something to invent — claude
	// and codex declare plugins through a marketplace instead.
	PackageTarget() (rel, key string, ok bool)
	// ReconcileCommand is the command that materializes a declared package,
	// with pkg substituted in, or "" when there is nothing to run. ap prints
	// this rather than running it: ap does not run other people's installers.
	ReconcileCommand(pkg string) string
}

// AdapterFor returns the adapter for an agent. Destinations come from
// pkg/agentreg, which is already the single copy of "what does this agent
// read"; a second table here is how the two answers stop agreeing.
func AdapterFor(a agentreg.Agent) (Adapter, error) {
	if a.Name == "" {
		return nil, fmt.Errorf("adapter: the agent has no name")
	}
	return registryAdapter{a}, nil
}

type registryAdapter struct{ agent agentreg.Agent }

func (r registryAdapter) Name() string { return r.agent.Name }

func (r registryAdapter) SkillDir(name string) (string, bool) {
	if r.agent.Skills == "" {
		return "", false
	}
	return filepath.Join(r.agent.Skills, name), true
}

func (r registryAdapter) MCPTarget() (string, string, bool) {
	if r.agent.MCPFile == "" || r.agent.MCPKey == "" {
		return "", "", false
	}
	return r.agent.MCPFile, r.agent.MCPKey, true
}

func (r registryAdapter) PackageTarget() (string, string, bool) {
	if r.agent.PackageFile == "" || r.agent.PackageKey == "" {
		return "", "", false
	}
	return r.agent.PackageFile, r.agent.PackageKey, true
}

func (r registryAdapter) ReconcileCommand(pkg string) string {
	if r.agent.PackageReconcile == "" {
		return ""
	}
	return fmt.Sprintf(r.agent.PackageReconcile, pkg)
}

// ArtifactDest applies §26.1: a destination is relative to the root, must not
// escape it, and is checked AFTER cleaning — "a/../../b" is only visibly an
// escape once cleaned.
func (r registryAdapter) ArtifactDest(destination string) (string, error) {
	if destination == "" {
		return "", fmt.Errorf("artifact destination must not be empty")
	}
	if filepath.IsAbs(destination) || strings.HasPrefix(destination, "/") {
		return "", fmt.Errorf("artifact destination %q is absolute; §26.1 keeps them relative to the root", destination)
	}
	clean := filepath.Clean(filepath.FromSlash(destination))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("artifact destination %q escapes the root", destination)
	}
	return clean, nil
}
