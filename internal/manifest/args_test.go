package manifest

import (
	"strings"
	"testing"
)

func TestTokenizeSplitsOnWhitespaceAndGroupsOnQuotes(t *testing.T) {
	for _, tc := range []struct {
		name, in string
		want     []string
	}{
		{"plain flags", "--model=claude-opus-5 --effort=xhigh",
			[]string{"--model=claude-opus-5", "--effort=xhigh"}},
		{"runs of space and tab collapse", "-s  danger-full-access\t-a never",
			[]string{"-s", "danger-full-access", "-a", "never"}},
		{"double quotes group one token", `--effort=xhigh "/plan:run a b"`,
			[]string{"--effort=xhigh", "/plan:run a b"}},
		{"single quotes group one token", `--system 'be brief'`,
			[]string{"--system", "be brief"}},
		{"the other style inside is literal", `--p "it's fine"`,
			[]string{"--p", "it's fine"}},
		{"a backslash escapes the next character", `a\ b c`,
			[]string{"a b", "c"}},
		{"a backslash is literal inside single quotes", `'a\b'`,
			[]string{`a\b`}},
		{"quotes may abut, producing one token", `--p"x"y`,
			[]string{"--px" + "y"}},
		{"leading and trailing space is not a token", "  a  ",
			[]string{"a"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Tokenize(tc.in)
			if err != nil {
				t.Fatalf("Tokenize(%q): %v", tc.in, err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("Tokenize(%q) = %q, want %q", tc.in, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("Tokenize(%q) = %q, want %q", tc.in, got, tc.want)
				}
			}
		})
	}
}

// TestTokenizeExpandsNothing is the property the whole feature rests on. Every
// one of these characters means something to a shell and none of them may mean
// anything here: install goes to sh -c, args goes to argv.
func TestTokenizeExpandsNothing(t *testing.T) {
	got, err := Tokenize("--p $HOME *.go `id` ~ a;b a&&b a|b >out")
	if err != nil {
		t.Fatalf("Tokenize: %v", err)
	}
	want := []string{"--p", "$HOME", "*.go", "`id`", "~", "a;b", "a&&b", "a|b", ">out"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("Tokenize expanded something: got %q, want %q", got, want)
	}
}

func TestTokenizeRefusesWhatItCannotFinish(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"unterminated double quote", `--p "no end`, "unterminated"},
		{"unterminated single quote", `--p 'no end`, "unterminated"},
		{"trailing backslash", `--p a\`, "backslash"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Tokenize(tc.in); err == nil {
				t.Fatalf("Tokenize(%q) = nil error, want one naming %q", tc.in, tc.want)
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to name %q", err, tc.want)
			}
		})
	}
}

// An empty string is zero tokens and not an error here: the schema turns that
// into "a variant with no arguments behaves identically to its profile", which
// is a better message than anything this function could say.
func TestTokenizeOfNothingIsNoTokens(t *testing.T) {
	got, err := Tokenize("   ")
	if err != nil || len(got) != 0 {
		t.Fatalf("Tokenize(spaces) = %q, %v; want no tokens and no error", got, err)
	}
}
