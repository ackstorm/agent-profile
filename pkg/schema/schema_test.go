package schema

import (
	"slices"
	"testing"
)

func TestSchemaMarksTheThreeV1UnionPointsAndNothingElse(t *testing.T) {
	s := V1Schema()
	for _, p := range [][]string{
		{"skills", "pdf", "source"},
		{"artifacts", "agents-md", "source"},
		{"marketplaces", "anthropic-skills", "source"},
		{"prompt", "source"},
		{"runtimes", "claude", "skills", "pdf", "source"},
	} {
		if !s.IsUnion(p) {
			t.Errorf("%v: not marked as a union", p)
		}
	}
	// Why mcps.*.transport is not a union: see the comment in schema.go, next
	// to V1Schema's unions list.
	if s.IsUnion([]string{"mcps", "memory", "transport"}) {
		t.Error("mcps.*.transport is marked as a union; §3.5 does not list it")
	}
	if got := s.Group([]string{"skills", "pdf"}); !slices.Equal(got, []string{"ref", "source"}) {
		t.Errorf("skills.* group = %v, want [ref source]", got)
	}
	if got := s.Group([]string{"prompt"}); !slices.Equal(got, []string{"content", "source"}) {
		t.Errorf("prompt group = %v, want [content source]", got)
	}
	if s.Group([]string{"mcps", "memory"}) != nil {
		t.Error("mcps.* has an exclusive group; it has no locator")
	}
}
