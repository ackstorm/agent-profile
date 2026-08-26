package schema

import "testing"

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
