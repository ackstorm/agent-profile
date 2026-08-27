package hydrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ackstorm/agent-profile/pkg/schema"
	"github.com/ackstorm/agent-profile/pkg/source"
)

func skillProfile(names ...string) schema.Profile {
	p := schema.Profile{Skills: map[string]schema.Resource{}}
	for _, n := range names {
		p.Skills[n] = schema.Resource{Enabled: true, Source: &schema.Source{}}
	}
	return p
}

func TestTheSkillContractNamesTheSkillAndTheResolvedPath(t *testing.T) {
	good, bad := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(good, "SKILL.md"), []byte("# ok"), 0o600); err != nil {
		t.Fatal(err)
	}

	p := skillProfile("ok", "broken")
	fetched := map[string]source.Resolved{
		"skill ok":     {Dir: good},
		"skill broken": {Dir: bad},
	}
	err := CheckContracts(p, fetched)
	if err == nil {
		t.Fatal("a skill with no SKILL.md passed")
	}
	// The usual cause is a subpath one directory too high or too low, and the
	// skill's name alone does not show that.
	for _, want := range []string{"broken", bad} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
	if strings.Contains(err.Error(), `"ok"`) {
		t.Errorf("a valid skill was reported: %v", err)
	}
}

// A manifest with three broken skills should say so once, not three times over
// three runs.
func TestEveryBrokenSkillIsReportedInOneRun(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	err := CheckContracts(skillProfile("alpha", "bravo"), map[string]source.Resolved{
		"skill alpha": {Dir: a},
		"skill bravo": {Dir: b},
	})
	if err == nil {
		t.Fatal("two broken skills passed")
	}
	for _, want := range []string{"alpha", "bravo"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q; the check stopped at the first failure", err, want)
		}
	}
}

// A disabled skill is not materialized, so its contract is not its problem
// (§4). Checking it anyway would make a disabled resource able to fail an
// apply, which is the opposite of what disabling means.
func TestADisabledSkillIsNotContractChecked(t *testing.T) {
	p := schema.Profile{Skills: map[string]schema.Resource{
		"off": {Enabled: false, Source: &schema.Source{}},
	}}
	if err := CheckContracts(p, map[string]source.Resolved{"skill off": {Dir: t.TempDir()}}); err != nil {
		t.Errorf("a disabled skill failed its contract: %v", err)
	}
}

// §26 makes artifacts the deliberately OPAQUE type: anything with a stronger
// semantic type uses that type instead, and an artifact is whatever the author
// says it is. Imposing a contract on one would make the opaque type not opaque.
func TestArtifactsHaveNoContract(t *testing.T) {
	p := schema.Profile{Artifacts: map[string]schema.Artifact{
		"agents-md": {Enabled: true, Source: &schema.Source{}, Destination: "AGENTS.md"},
	}}
	if err := CheckContracts(p, map[string]source.Resolved{"artifact agents-md": {Dir: t.TempDir()}}); err != nil {
		t.Errorf("an artifact was contract-checked: %v", err)
	}
}
