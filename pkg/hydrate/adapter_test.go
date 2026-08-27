package hydrate

import (
	"path/filepath"
	"testing"

	"github.com/ackstorm/agent-profile/pkg/agentreg"
)

// The destinations come from pkg/agentreg. This asserts against the registry
// rather than against a hand-written duplicate, because a duplicate is how two
// answers to "where does this agent read skills" stop agreeing.
func TestSkillDestinationsComeFromTheRegistry(t *testing.T) {
	for _, name := range agentreg.Names() {
		a, ok := agentreg.Lookup(name)
		if !ok {
			t.Fatalf("%s is in Names but not in Lookup", name)
		}
		ad, err := AdapterFor(a)
		if err != nil {
			t.Fatal(err)
		}
		got, supported := ad.SkillDir("pdf")
		if a.Skills == "" {
			if supported {
				t.Errorf("%s reports a skill destination but the registry has none", name)
			}
			continue
		}
		if !supported {
			t.Errorf("%s has a registry skill path %q but the adapter refuses it", name, a.Skills)
			continue
		}
		if want := filepath.Join(a.Skills, "pdf"); got != want {
			t.Errorf("%s SkillDir = %q, want %q", name, got, want)
		}
	}
}

// codex reads skills from ~/.agents/skills, OUTSIDE CODEX_HOME, so pointing
// that variable at a profile does not isolate them. Writing there anyway would
// leak one profile's skills into every other profile and into the user's bare
// codex — which is the opposite of what this tool is for.
//
// This is pinned as a named case rather than left to the table above, because
// the tempting "fix" is to give codex a skills path and make the table
// uniform, and the reason not to is not visible from the code.
func TestCodexHasNoConfigDirSkillDestination(t *testing.T) {
	a, ok := agentreg.Lookup("codex")
	if !ok {
		t.Fatal("codex is not in the registry")
	}
	ad, err := AdapterFor(a)
	if err != nil {
		t.Fatal(err)
	}
	if _, supported := ad.SkillDir("pdf"); supported {
		t.Error("codex reports a skills destination inside its config dir; ~/.agents/skills is outside it")
	}
}

// §26.1: relative to the root, never escaping, checked after cleaning.
func TestArtifactDestinationsStayInsideTheRoot(t *testing.T) {
	a, _ := agentreg.Lookup("claude")
	ad, _ := AdapterFor(a)

	for _, good := range []string{"AGENTS.md", "references/CODING.md", "./a/b.md"} {
		if _, err := ad.ArtifactDest(good); err != nil {
			t.Errorf("destination %q was refused: %v", good, err)
		}
	}
	for _, bad := range []string{"", "/etc/passwd", "../outside", "a/../../b"} {
		if got, err := ad.ArtifactDest(bad); err == nil {
			t.Errorf("destination %q was accepted as %q", bad, got)
		}
	}
}
