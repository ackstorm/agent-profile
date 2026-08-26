package schema

import (
	"strings"
	"testing"
)

func TestDecodeRejectsAnUnknownKeyAndNamesItsLine(t *testing.T) {
	n, _ := ParseYAML([]byte("version: \"1\"\nname: x\ndependencies:\n  - foo\n"))
	_, err := Decode(n)
	if err == nil || !strings.Contains(err.Error(), "dependencies") {
		t.Fatalf("err = %v; an unknown top-level key must be named", err)
	}
	if !strings.Contains(err.Error(), "line 3") {
		t.Errorf("error %q does not point at the key's own line", err)
	}
}

func TestDecodeEnforcesTheModelAndPromptRules(t *testing.T) {
	// §9: endpoint authentication has exactly one representation — `auth`.
	n, _ := ParseYAML([]byte("version: \"1\"\nname: x\nmodel:\n  headers:\n    Authorization:\n      value: tok\n"))
	if _, err := Decode(n); err == nil || !strings.Contains(err.Error(), "Authorization") {
		t.Errorf("err = %v; Authorization inside model.headers must be refused", err)
	}
	// §10: exactly one of content | source.
	n, _ = ParseYAML([]byte("version: \"1\"\nname: x\nprompt:\n  content: hi\n  source:\n    local:\n      path: ./p\n"))
	if _, err := Decode(n); err == nil {
		t.Error("prompt declaring both content and source accepted")
	}
	n, _ = ParseYAML([]byte("version: \"1\"\nname: x\nprompt:\n  mode: sideways\n  content: hi\n"))
	if _, err := Decode(n); err == nil || !strings.Contains(err.Error(), "sideways") {
		t.Errorf("err = %v; mode must be append or replace", err)
	}
}

func TestDecodeEnforcesExactlyOneLocatorOnAnEnabledResource(t *testing.T) {
	// §22: an enabled resource defines exactly one external locator.
	n, _ := ParseYAML([]byte("version: \"1\"\nname: x\nskills:\n  pdf:\n    ref: pdf@m\n    source:\n      local:\n        path: ./p\n"))
	if _, err := Decode(n); err == nil {
		t.Error("a resource with both ref and source accepted")
	}
	n, _ = ParseYAML([]byte("version: \"1\"\nname: x\nskills:\n  pdf: {}\n"))
	if _, err := Decode(n); err == nil {
		t.Error("an enabled resource with no locator accepted")
	}
	// A DISABLED resource needs no locator: it is not materialized.
	n, _ = ParseYAML([]byte("version: \"1\"\nname: x\nskills:\n  pdf:\n    enabled: false\n"))
	if _, err := Decode(n); err != nil {
		t.Errorf("a disabled resource without a locator refused: %v", err)
	}
}

func TestDecodeRefusesALiteralAuthorizationInMcpHeaders(t *testing.T) {
	// §14: a literal there is a secret embedded in the manifest, which §13
	// prohibits outright.
	n, _ := ParseYAML([]byte("version: \"1\"\nname: x\nmcps:\n  m:\n    transport:\n      type: http\n      url: https://e/mcp\n      headers:\n        Authorization:\n          value: \"Bearer abc\"\n"))
	if _, err := Decode(n); err == nil || !strings.Contains(err.Error(), "value_from") {
		t.Errorf("err = %v; a literal Authorization must be refused and name value_from", err)
	}
}

// The four tests below address review feedback on the batch that landed just
// before this one: a union node can legitimately end up with zero branches
// after a merge, and nothing rejected that until now.

func TestDecodeRefusesASourceThatEndsWithNoBranchSelected(t *testing.T) {
	// `null` resets a singular to absent (§3.7), and source.git is not a
	// collection entry, so the merge deletes `git` and leaves `source` a
	// mapping with no keys at all. compose.go is not wrong to allow this —
	// Decode is where it must be caught.
	base, err := ParseYAML([]byte("version: \"1\"\nname: x\nskills:\n  pdf:\n    source:\n      git:\n        url: https://example.com/r.git\n"))
	if err != nil {
		t.Fatal(err)
	}
	overlay, err := ParseYAML([]byte("skills:\n  pdf:\n    source:\n      git: null\n"))
	if err != nil {
		t.Fatal(err)
	}
	merged, err := Merge(base, overlay, V1Schema())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(merged); err == nil {
		t.Fatal("a source selecting neither git nor local was accepted")
	} else if !strings.Contains(err.Error(), "skills.pdf.source") {
		t.Errorf("err = %q; does not name skills.pdf.source", err)
	}
}

func TestDecodeRefusesASourceThatSelectsBothBranches(t *testing.T) {
	// Schema validation enforces branch exclusivity WITHIN a single authored
	// document; the merge engine deliberately does not — it assumes
	// validation catches it. This is the only place that happens.
	n, err := ParseYAML([]byte("version: \"1\"\nname: x\nskills:\n  pdf:\n    source:\n      git:\n        url: https://example.com/r.git\n      local:\n        path: ./p\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(n); err == nil {
		t.Fatal("a source declaring both git and local was accepted")
	} else if !strings.Contains(err.Error(), "skills.pdf.source") {
		t.Errorf("err = %q; does not name skills.pdf.source", err)
	}
}

func TestDecodeRefusesAPromptThatEndsWithNoBranchSelected(t *testing.T) {
	// Same shape as the source case, at the prompt content|source group.
	base, err := ParseYAML([]byte("version: \"1\"\nname: x\nprompt:\n  content: hi\n"))
	if err != nil {
		t.Fatal(err)
	}
	overlay, err := ParseYAML([]byte("prompt:\n  content: null\n"))
	if err != nil {
		t.Fatal(err)
	}
	merged, err := Merge(base, overlay, V1Schema())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(merged); err == nil {
		t.Fatal("a prompt selecting neither content nor source was accepted")
	} else if !strings.Contains(err.Error(), "prompt") {
		t.Errorf("err = %q; does not name prompt", err)
	}
}

func TestDecodeRefusesAPromptSourceThatSelectsBothBranches(t *testing.T) {
	n, err := ParseYAML([]byte("version: \"1\"\nname: x\nprompt:\n  source:\n    git:\n      url: https://example.com/r.git\n    local:\n      path: ./p\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(n); err == nil {
		t.Fatal("a prompt source declaring both git and local was accepted")
	} else if !strings.Contains(err.Error(), "prompt.source") {
		t.Errorf("err = %q; does not name prompt.source", err)
	}
}

func TestSubpathAndDestinationMayNotEscapeTheirRoot(t *testing.T) {
	for _, p := range []string{
		"../etc/passwd", "a/../../b", "/absolute", "a/b/../../..", "..",
	} {
		if err := validRelPath("destination", p); err == nil {
			t.Errorf("%q accepted as a destination", p)
		}
	}
	for _, p := range []string{"AGENTS.md", "references/CODING.md", "a/b/c"} {
		if err := validRelPath("destination", p); err != nil {
			t.Errorf("%q refused: %v", p, err)
		}
	}
}
