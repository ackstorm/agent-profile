package schema

import (
	"strings"
	"testing"
)

func TestParseDistinguishesNullBoolAndNumberFromString(t *testing.T) {
	root, err := ParseYAML([]byte("a: null\nb: false\nc: 0.2\nd: \"false\"\ne: plain\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := root.Map["a"].Kind; got != Null {
		t.Errorf("a: kind = %v, want Null", got)
	}
	b, err := root.Map["b"].Bool()
	if err != nil || b {
		t.Errorf("b: Bool() = %v, %v; want false, nil", b, err)
	}
	n, err := root.Map["c"].Number()
	if err != nil || n != "0.2" {
		t.Errorf("c: Number() = %q, %v; want \"0.2\", nil", n, err)
	}
	// A quoted scalar is a string even when it spells a bool. Asking for a
	// bool must fail rather than silently agreeing.
	if _, err := root.Map["d"].Bool(); err == nil {
		t.Error(`d: "false" accepted as a bool; a quoted scalar is a string`)
	}
	if _, err := root.Map["e"].Bool(); err == nil {
		t.Error("e: plain non-bool accepted as a bool")
	}
}

// TestQuotedNullAndTildeStayScalarWhilePlainVariantsBecomeNull guards the one
// distinction the reviewer confirmed by inspection but that nothing tested:
// quoting always wins over the null sentinel, in both spellings YAML admits.
func TestQuotedNullAndTildeStayScalarWhilePlainVariantsBecomeNull(t *testing.T) {
	root, err := ParseYAML([]byte("a: \"null\"\nb: \"~\"\nc: null\nd: ~\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, tc := range []struct {
		key  string
		want string
	}{{"a", "null"}, {"b", "~"}} {
		n := root.Map[tc.key]
		if n.Kind != Scalar || !n.Quoted || n.Str != tc.want {
			t.Errorf("%s: got Kind=%v Quoted=%v Str=%q, want Scalar/true/%q", tc.key, n.Kind, n.Quoted, n.Str, tc.want)
		}
	}
	for _, key := range []string{"c", "d"} {
		if got := root.Map[key].Kind; got != Null {
			t.Errorf("%s: kind = %v, want Null", key, got)
		}
	}
}

// TestNumberAcceptsOnlyJSONGrammar guards against strconv.ParseFloat's wider
// grammar leaking non-JSON lexemes (Inf, NaN, a leading +, hex floats,
// leading zeros, bare decimal points) into text later emitted verbatim as
// JSON.
func TestNumberAcceptsOnlyJSONGrammar(t *testing.T) {
	bad := []string{"Inf", "NaN", "+1", "0x1p0", "01", ".5", "5."}
	for _, s := range bad {
		t.Run(s, func(t *testing.T) {
			root, err := ParseYAML([]byte("x: " + s + "\n"))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got, err := root.Map["x"].Number(); err == nil {
				t.Errorf("Number() = %q, nil; want an error", got)
			}
		})
	}

	good := []string{"0", "-1", "0.20", "1e3", "-2.5E-3"}
	for _, s := range good {
		t.Run(s, func(t *testing.T) {
			root, err := ParseYAML([]byte("x: " + s + "\n"))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			got, err := root.Map["x"].Number()
			if err != nil {
				t.Fatalf("Number() = _, %v; want nil", err)
			}
			if got != s {
				t.Errorf("Number() = %q, want %q (byte-identical to the lexeme)", got, s)
			}
		})
	}
}

func TestParseReadsLiteralBlockScalarsAndRejectsFoldedOnes(t *testing.T) {
	root, err := ParseYAML([]byte("prompt:\n  content: |\n    line one\n    line two\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := root.Map["prompt"].Map["content"].Str
	if got != "line one\nline two\n" {
		t.Errorf("content = %q, want %q", got, "line one\nline two\n")
	}
	// A folded block reflows text. Nothing in the spec needs it, and silently
	// joining a user's prompt lines is exactly the misreading this subset exists
	// to refuse.
	if _, err := ParseYAML([]byte("a: >\n  one\n  two\n")); err == nil {
		t.Error("folded block scalar (>) accepted; the subset must name and refuse it")
	}
}

func TestParseNamesEveryConstructItRefuses(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"flow sequence", "a: [x, y]\n", "flow sequence"},
		{"flow mapping", "a: {x: y}\n", "flow mapping"},
		{"folded block", "a: >\n  x\n", "block scalar (>)"},
		{"anchor", "a: &x y\n", "anchors"},
		{"alias", "a: *x\n", "anchors"},
		{"tag", "a: !!str y\n", "tags"},
		{"merge key", "<<: *x\n", `merge key "<<"`},
		{"tab indent", "a:\n\tb: c\n", "tab"},
		{"second document", "a: b\n---\nc: d\n", "document"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseYAML([]byte(tc.in))
			if err == nil {
				t.Fatalf("%q was accepted; the subset must refuse it", tc.in)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not name %q", err, tc.want)
			}
		})
	}
}
