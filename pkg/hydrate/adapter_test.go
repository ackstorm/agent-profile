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

// codex read skills only from ~/.agents/skills — outside CODEX_HOME — until
// codex-cli 0.149.1, so a profile could not isolate them and this case pinned
// the absence. It now reads $CODEX_HOME/skills too, verified by planting a
// marker skill there and watching `codex exec` list it back.
//
// Kept as a named case for the same reason it existed: the row is the one most
// likely to be changed on a guess, in either direction, and the evidence for
// its current value is not visible from the code.
func TestCodexSkillsLandInsideItsConfigDir(t *testing.T) {
	a, ok := agentreg.Lookup("codex")
	if !ok {
		t.Fatal("codex is not in the registry")
	}
	ad, err := AdapterFor(a)
	if err != nil {
		t.Fatal(err)
	}
	rel, supported := ad.SkillDir("pdf")
	if !supported {
		t.Fatal("codex reports no skills destination; it reads $CODEX_HOME/skills")
	}
	if want := filepath.Join("skills", "pdf"); rel != want {
		t.Errorf("codex skill destination = %q, want %q", rel, want)
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
