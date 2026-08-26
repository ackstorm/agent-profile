package hydrate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/ackstorm/agent-profile/pkg/schema"
	"github.com/ackstorm/agent-profile/pkg/source"
)

// CheckContracts validates what a resolved source actually contains.
//
// It runs in the RESOLUTION phase (§37.1 step 14), before any materialization,
// and that placement is the point: apply overwrites, so a contract violation
// discovered halfway through leaves a root partly written. Running it here also
// means --dry-run reports it, which is what --dry-run is for.
//
// Errors are collected rather than returned on the first failure. A manifest
// with three broken skills should say so once, not three times over three runs.
func CheckContracts(p schema.Profile, fetched map[string]source.Resolved) error {
	var errs []error
	names := make([]string, 0, len(p.Skills))
	for n := range p.Skills {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, n := range names {
		if !p.Skills[n].Enabled {
			continue
		}
		res, ok := fetched["skill "+n]
		if !ok {
			// Not fetched: a marketplace ref, whose item resolution is Phase 6.
			// Its contract is checked there, against the item it resolves to.
			continue
		}
		if err := checkSkill(n, res.Dir); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// checkSkill is §23: the resolved skill root MUST contain SKILL.md.
//
// The error names the resolved PATH as well as the skill, because the usual
// cause is a subpath pointing one directory too high or too low, and the name
// alone does not show that.
func checkSkill(name, dir string) error {
	fi, err := os.Stat(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return fmt.Errorf("skill %q: no SKILL.md in the resolved source root %s", name, dir)
	}
	if fi.IsDir() {
		return fmt.Errorf("skill %q: SKILL.md in %s is a directory", name, dir)
	}
	return nil
}
