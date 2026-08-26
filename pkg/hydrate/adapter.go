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
