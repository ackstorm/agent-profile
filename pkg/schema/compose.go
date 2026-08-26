package schema

import "slices"

// Merge composes overlay onto base. §3.1's three rules and nothing else:
// mappings merge recursively, non-mappings replace, marked unions and
// exclusive groups replace as a unit.
//
// This function must never name a resource type. Every decision that depends
// on WHICH key is being merged goes through Schema — that is what §3.5 means
// by "marked in the schema, never hardcoded per type in the merge engine".
func Merge(base, overlay *Node, s Schema) *Node {
	return mergeAt(base, overlay, s, nil)
}

func mergeAt(base, overlay *Node, s Schema, path []string) *Node {
	if base == nil {
		return overlay
	}
	if overlay == nil {
		return base
	}
	// Rule 2: a non-mapping on either side replaces. A Null overlay is handled
	// by the caller, which removes the key rather than storing a Null.
	if base.Kind != Mapping || overlay.Kind != Mapping {
		return overlay
	}
	// Rule 3, branch-keyed unions (§3.5.1): a different branch replaces the
	// whole node; the same branch falls through to the ordinary mapping merge
	// below, which recurses into it.
	if s.IsUnion(path) && !sameBranch(base, overlay) {
		return overlay
	}

	out := &Node{Kind: Mapping, Line: base.Line, Map: map[string]*Node{}, KeyLine: map[string]int{}}
	drop := discardedGroupMembers(base, overlay, s, path)

	for _, k := range base.Keys {
		if slices.Contains(drop, k) {
			continue
		}
		out.Keys = append(out.Keys, k)
		out.KeyLine[k] = base.KeyLine[k]
		out.Map[k] = base.Map[k]
	}
	for _, k := range overlay.Keys {
		ov := overlay.Map[k]
		// §3.7: null is reset-to-absent. Storing a Null instead would make
		// every consumer check for it, and render would emit a key the
		// effective profile does not have.
		if ov.Kind == Null {
			if i := slices.Index(out.Keys, k); i >= 0 {
				out.Keys = slices.Delete(out.Keys, i, i+1)
				delete(out.Map, k)
				delete(out.KeyLine, k)
			}
			continue
		}
		if _, ok := out.Map[k]; !ok {
			out.Keys = append(out.Keys, k)
		}
		out.KeyLine[k] = overlay.KeyLine[k]
		// slices.Concat, not append(path, k): append can reuse path's backing
		// array across sibling iterations of this loop, letting one child's
		// write clobber another's path and make the union oracle consult the
		// wrong key.
		out.Map[k] = mergeAt(out.Map[k], ov, s, slices.Concat(path, []string{k}))
	}
	return out
}

// sameBranch reports whether both sides of a branch-keyed union select the same
// branch. A union node holds exactly one key by construction (schema validation
// enforces that within a single document), so comparing the first key is the
// whole comparison.
func sameBranch(base, overlay *Node) bool {
	if len(base.Keys) == 0 || len(overlay.Keys) == 0 {
		return false
	}
	return base.Keys[0] == overlay.Keys[0]
}

// discardedGroupMembers returns the inherited exclusive-group members that this
// overlay displaces. Task 6 fills it in; until then nothing is discarded.
func discardedGroupMembers(base, overlay *Node, s Schema, path []string) []string {
	return nil
}
