package manifest

import (
	"strings"
	"testing"
)

// The example from the spec, minus the schema: every shape a manifest uses,
// parsed by structure alone.
const exampleYAML = `version: 1
name: execute

bootstrap:
  - "curl -fsSL https://example.test/install.sh | sh"

platforms:
  claude:
    install:
      - ach-cli skill install pdf@anthropics --global
    variants:
      opus:
        args: --model=claude-opus-5 --effort=xhigh
      execute-plan:
        args: --effort=xhigh "/superpowers:executing-plans {}"
  codex:
`

func TestParseYAMLReadsEveryShapeAManifestUses(t *testing.T) {
	root, err := parseYAML([]byte(exampleYAML))
	if err != nil {
		t.Fatalf("parseYAML: %v", err)
	}
	if root.kind != mapNode {
		t.Fatalf("root kind = %v, want mapNode", root.kind)
	}
	if got := root.m["version"].str; got != "1" {
		t.Errorf("version = %q, want %q", got, "1")
	}
	if got := root.m["name"].str; got != "execute" {
		t.Errorf("name = %q, want %q", got, "execute")
	}

	boot := root.m["bootstrap"]
	if boot.kind != seqNode || len(boot.seq) != 1 {
		t.Fatalf("bootstrap = %v with %d entries, want one seqNode entry", boot.kind, len(boot.seq))
	}
	// The quotes are removed and what was inside them survives verbatim,
	// pipe included.
	if got, want := boot.seq[0].str, "curl -fsSL https://example.test/install.sh | sh"; got != want {
		t.Errorf("bootstrap[0] = %q, want %q", got, want)
	}

	// An agent with nothing under it is empty, not an error and not a guess:
	// §4.2 leaves "empty mapping or empty string" to the schema.
	if got := root.m["platforms"].m["codex"].kind; got != emptyNode {
		t.Errorf("platforms.codex kind = %v, want emptyNode", got)
	}

	// A bare scalar keeps quotes that are INSIDE it. This is what lets a
	// variant's args carry a grouped token through to Tokenize.
	v := root.m["platforms"].m["claude"].m["variants"].m["execute-plan"].m["args"]
	if got, want := v.str, `--effort=xhigh "/superpowers:executing-plans {}"`; got != want {
		t.Errorf("args = %q, want %q", got, want)
	}
}

// keys is file order, and keyLine points at the key rather than at whatever
// block follows it — Task 3's "unknown key" errors are only useful if they
// name the right line.
func TestParseYAMLRecordsKeyOrderAndKeyLines(t *testing.T) {
	root, err := parseYAML([]byte(exampleYAML))
	if err != nil {
		t.Fatalf("parseYAML: %v", err)
	}
	want := []string{"version", "name", "bootstrap", "platforms"}
	if len(root.keys) != len(want) {
		t.Fatalf("keys = %v, want %v", root.keys, want)
	}
	for i := range want {
		if root.keys[i] != want[i] {
			t.Fatalf("keys = %v, want %v", root.keys, want)
		}
	}
	// "platforms:" is line 7 of exampleYAML; its child block starts on line 8.
	if got := root.keyLine["platforms"]; got != 7 {
		t.Errorf("keyLine[platforms] = %d, want 7", got)
	}
}

func TestParseYAMLStripsCommentsButNotFromInsideAQuotedScalar(t *testing.T) {
	root, err := parseYAML([]byte(`# a whole-line comment
name: execute   # a trailing one
note: "keeps # this"
`))
	if err != nil {
		t.Fatalf("parseYAML: %v", err)
	}
	if got := root.m["name"].str; got != "execute" {
		t.Errorf("name = %q, want %q", got, "execute")
	}
	if got := root.m["note"].str; got != "keeps # this" {
		t.Errorf("note = %q, want %q", got, "keeps # this")
	}
}

func TestParseYAMLUnquotesOnlyTheTwoEscapesThereAre(t *testing.T) {
	// \\ and \" are escapes; every other backslash is a literal character,
	// which is what "everything else inside quotes is literal" means.
	root, err := parseYAML([]byte(`a: "say \"hi\""
b: "back\\slash"
c: "not\nan\tescape"
`))
	if err != nil {
		t.Fatalf("parseYAML: %v", err)
	}
	for _, tc := range []struct{ key, want string }{
		{"a", `say "hi"`},
		{"b", `back\slash`},
		{"c", `not\nan\tescape`},
	} {
		if got := root.m[tc.key].str; got != tc.want {
			t.Errorf("%s = %q, want %q", tc.key, got, tc.want)
		}
	}
}

// One case per rejected construct in §4.1, and each asserts an ERROR rather
// than a wrong value. A subset parser that quietly produces something for an
// anchor is the failure mode the whole rejection list exists to prevent.
func TestParseYAMLRejectsEveryConstructOutsideTheSubset(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"tab indentation", "a:\n\tb: c\n", "tab in indentation"},
		{"flow sequence", "a: [1, 2]\n", "flow sequence"},
		{"flow mapping", "a: {b: c}\n", "flow mapping"},
		{"literal block scalar", "a: |\n  text\n", "block scalar (|)"},
		{"folded block scalar", "a: >\n  text\n", "block scalar (>)"},
		{"anchor", "a: &x 1\n", "anchor (&)"},
		{"alias", "a: *x\n", "alias (*)"},
		{"merge key", "a:\n  <<: b\n", "merge key"},
		{"tag", "a: !!str 1\n", "tag (!)"},
		{"document start", "---\na: 1\n", "multi-document marker"},
		{"document end", "a: 1\n...\n", "multi-document marker"},
		{"single quotes", "a: 'x'\n", "single-quoted scalar"},
		{"duplicate key", "a: 1\na: 2\n", "duplicate key"},
		{"not a mapping line", "a: 1\nnope\n", "expected \"key: value\""},
		{"unterminated quote", "a: \"x\n", "unterminated quoted scalar"},
		{"junk after a quote", "a: \"x\" y\n", "unexpected text after a quoted scalar"},
		{"empty sequence entry", "a:\n  -\n", "empty sequence entry"},
		{"nested under an entry", "a:\n  - x\n    b: c\n", "indented block under a sequence entry"},
		{"value and block both", "a: 1\n  b: 2\n", "already has a value"},
		{"misaligned sibling", "a:\n  b: 1\n   c: 2\n", "unexpected indentation"},
		{"starts indented", "  a: 1\n", "starts indented"},
		{"empty document", "\n# nothing\n", "empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseYAML([]byte(tc.src))
			if err == nil {
				t.Fatalf("parseYAML(%q) = nil error, want one naming %q", tc.src, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("parseYAML(%q) error = %q, want it to name %q", tc.src, err, tc.want)
			}
		})
	}
}
