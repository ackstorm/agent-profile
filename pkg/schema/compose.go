package schema

import (
	"fmt"
	"slices"
	"strings"
)

// Merge composes overlay onto base. §3.1's three rules and nothing else:
// mappings merge recursively, non-mappings replace, marked unions and
// exclusive groups replace as a unit.
//
// This function must never name a resource type. Every decision that depends
// on WHICH key is being merged goes through Schema — that is what §3.5 means
// by "marked in the schema, never hardcoded per type in the merge engine".
func Merge(base, overlay *Node, s Schema) (*Node, error) {
	return mergeAt(base, overlay, s, nil)
}

func mergeAt(base, overlay *Node, s Schema, path []string) (*Node, error) {
	if base == nil {
		return overlay, nil
	}
	if overlay == nil {
		return base, nil
	}
	// Rule 2: a non-mapping on either side replaces. A Null overlay is handled
	// by the caller, which removes the key rather than storing a Null.
	if base.Kind != Mapping || overlay.Kind != Mapping {
		return overlay, nil
	}
	// Rule 3, branch-keyed unions (§3.5.1): a different branch replaces the
	// whole node; the same branch falls through to the ordinary mapping merge
	// below, which recurses into it.
	if s.IsUnion(path) && !sameBranch(base, overlay) {
		return overlay, nil
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
		// slices.Concat, not append(path, k): append can reuse path's backing
		// array across sibling iterations of this loop, letting one child's
		// write clobber another's path and make the union oracle consult the
		// wrong key.
		childPath := slices.Concat(path, []string{k})
		// §3.7: null is reset-to-absent, except on a collection entry, which
		// already has `enabled: false` — a second, subtly different disable
		// semantics is not introduced.
		if ov.Kind == Null {
			if s.IsCollectionEntry(childPath) {
				return nil, fmt.Errorf("line %d: %s: null is not valid; use `enabled: false`",
					ov.Line, strings.Join(childPath, "."))
			}
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
		merged, err := mergeAt(out.Map[k], ov, s, childPath)
		if err != nil {
			return nil, err
		}
		out.Map[k] = merged
	}
	return out, nil
}

// sameBranch reports whether both sides of a branch-keyed union select the same
// branch. A union node is INTENDED to hold exactly one key — that is what
// makes comparing the first key the whole comparison — but this function does
// not itself enforce that: Merge runs before Decode, so an overlay reaches
// here before schema validation has ever looked at it. The len == 0 guards
// below exist precisely because that invariant is not enforced at this point;
// they are not defensive padding for a case schema validation already rules
// out.
func sameBranch(base, overlay *Node) bool {
	if len(base.Keys) == 0 || len(overlay.Keys) == 0 {
		return false
	}
	return base.Keys[0] == overlay.Keys[0]
}

// discardedGroupMembers returns the inherited exclusive-group members this
// overlay displaces. §3.5.2: declaring ANY member of a group discards every
// other inherited member; declaring the same member composes normally. Keys
// outside the group are untouched, which is what lets `enabled: false` leave a
// locator alone and a replaced prompt content inherit its mode.
func discardedGroupMembers(base, overlay *Node, s Schema, path []string) []string {
	members := s.Group(path)
	if members == nil {
		return nil
	}
	var declared bool
	for _, m := range members {
		if _, ok := overlay.Map[m]; ok {
			declared = true
			break
		}
	}
	if !declared {
		return nil
	}
	var drop []string
	for _, m := range members {
		if _, inOverlay := overlay.Map[m]; inOverlay {
			continue
		}
		if _, inBase := base.Map[m]; inBase {
			drop = append(drop, m)
		}
	}
	return drop
}
