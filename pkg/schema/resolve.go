package schema

import "fmt"

// Resolution is everything the resolution phase produced. Phase 4 consumes
// it and materializes; --dry-run prints it and stops.
type Resolution struct {
	Profile  Profile
	Runtime  string
	Refs     []Ref
	Warnings []Warning
}

// Resolve runs §37 steps 1-11 in order and stops before any source is
// fetched: effective profile (1-8) → required inputs (9) → resolve inputs
// (10) → preflight (11). The order is a contract, not a convenience — a
// profile with both an undeclared input and a missing preflight dependency
// must report the input error, because step 10 runs before step 11.
//
// strict promotes every Warning §7.2/§8 produced into an error.
func Resolve(path, runtime string, strict bool) (*Resolution, *Resolved, error) {
	p, warns, err := Effective(path, runtime)
	if err != nil {
		return nil, nil, err
	}
	if strict {
		for _, w := range warns {
			return nil, nil, fmt.Errorf("strict: %s", w.Text)
		}
	}

	refs := Required(p)

	resolved, err := ResolveInputs(p, refs)
	if err != nil {
		return nil, nil, err
	}

	if err := Preflight(p, runtime); err != nil {
		return nil, nil, err
	}

	return &Resolution{Profile: p, Runtime: runtime, Refs: refs, Warnings: warns}, resolved, nil
}
