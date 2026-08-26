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
