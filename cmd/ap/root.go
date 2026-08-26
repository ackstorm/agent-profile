//go:build unix

package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ackstorm/agent-profile/internal/profile"
	"github.com/ackstorm/agent-profile/pkg/agentreg"
)

// target is the resolved subject of every command that touches a root: which
// runtime's rules to materialize by, and which directory to materialize into.
type target struct {
	Agent agentreg.Agent
	// Name is what the reference called this root — a profile name, the
	// `default` sentinel, or "" when --root named a directory outright.
	Name string
	Root string
}

// Label is what a report calls this root. A reference prints as one; a literal
// directory prints as itself, because that is what the user typed and there is
// no shorter true name for it.
func (t target) Label() string {
	if t.Name == "" {
		return t.Agent.Name + " → " + t.Root
	}
	return t.Agent.Name + ":" + t.Name
}

// resolveTarget turns the subject and --root into one root (§33.2).
//
// A root is a PARAMETER and must not be inferred from the environment. The
// reference form satisfies that on a laptop: `claude:plan` names the root, and
// profile.Dir only computes where this machine keeps its profiles.
//
// It does not satisfy it in a container. An init container hydrating onto a
// mount the main container reads would have to arrange XDG_DATA_HOME so that
// <it>/agent-profile/profiles/claude/hydrated lands on that mount — which is
// inferring a root from the environment through two layers of indirection, and
// there is no $HOME there to infer from in the first place.
//
// So --root states the directory, and the subject is then a bare agent name.
// This is not a third root: it is the same one mechanism — point the agent's
// configuration-directory variable at a directory — with the directory stated
// instead of derived.
//
// Both spellings at once is an error. They answer the same question, and a
// silent precedence rule is how the wrong directory gets written.
func resolveTarget(subject, rootFlag, verb string) (target, error) {
	qualified := strings.Contains(subject, ":")

	if rootFlag == "" {
		if !qualified {
			return target{}, fmt.Errorf(
				"%s needs a root: either <agent>:<profile>, or an agent name with --root <dir>", verb)
		}
		agent, name, variant, err := profile.ParseVariantRefAllowDefault(subject)
		if err != nil {
			return target{}, err
		}
		if variant != "" {
			// A variant is a set of launch arguments over a profile. It is not
			// a root, and materializing "into" one would have no meaning.
			return target{}, fmt.Errorf("%s takes a profile, not a variant: drop %q from %q", verb, variant, subject)
		}
		return target{Agent: agent, Name: name, Root: profile.Dir(agent, name)}, nil
	}

	if qualified {
		return target{}, fmt.Errorf(
			"%q names a root and so does --root %s; use one: a reference for a profile, --root for a directory",
			subject, rootFlag)
	}
	agent, ok := agentreg.Lookup(subject)
	if !ok {
		return target{}, fmt.Errorf("unknown agent %q: supported are %s", subject, strings.Join(agentreg.Names(), ", "))
	}
	abs, err := filepath.Abs(rootFlag)
	if err != nil {
		return target{}, err
	}
	return target{Agent: agent, Root: abs}, nil
}

// gate asks the one question, and only for the one root that has no undo.
//
// A literal --root is not gated. It is neither the user's real configuration
// nor a profile ap manages, so there is nothing ap could claim to undo, and the
// resolved absolute path the gate exists to DISPLAY is the argument the user
// just typed. `--root ~/.claude` is that user pointing at their own
// configuration deliberately, in their own hand.
func (t target) gate(yes bool) error {
	if t.Name == "" {
		return nil
	}
	return gateRealConfig(t.Agent, t.Name, t.Root, yes)
}
