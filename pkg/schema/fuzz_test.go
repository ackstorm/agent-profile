package schema

import "testing"

func FuzzParseYAML(f *testing.F) {
	f.Add("version: 1\nname: execute\n")
	f.Add("a:\n  b: |\n    text\n")
	f.Add("a: null\nb: [1, 2]\n")
	f.Add("<<: *anchor\n")
	f.Add("a:\t b\n")
	f.Fuzz(func(t *testing.T, s string) {
		// The only contract is that a hostile file is refused, never a panic
		// and never a hang. What it parses to when it does parse is the table
		// tests' business.
		_, _ = ParseYAML([]byte(s))
	})
}
