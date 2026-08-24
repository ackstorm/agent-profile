//go:build unix

package manifest

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Tokenize splits a variant's `args` string into argv elements.
//
// Three things are recognised and everything else is a literal character:
//
//	whitespace   separates tokens; runs of spaces and tabs collapse
//	"…" '…'      group one token; the quotes are removed and the other style
//	             inside them is literal
//	\            escapes the next character, outside single quotes only
//
// Nothing is expanded. No variables, no command substitution, no globbing, no
// tilde, and no `;` `&&` `|` or redirection — `--prompt $HOME` reaches the agent
// as the characters `$HOME`, exactly as typed.
//
// That is the deliberate opposite of an `install` command, which really does go
// to `sh -c`. The two fields are both strings and look alike, so the difference
// is stated rather than inferred: an install command is a shell one-liner by
// nature, an agent's argv must arrive verbatim. A prompt is the most likely
// thing in a manifest to contain `$`, a backtick or an asterisk, and having ap
// interpret any of them would corrupt the prompt in a way that is invisible
// until the agent answers the wrong question.
//
// The list form of `args` exists for what this cannot express — an argument
// holding both quote styles, or one that is a bare quote character — and is the
// canonical form when the two disagree, because the list IS the storage format.
func Tokenize(s string) ([]string, error) {
	var (
		out     []string
		cur     strings.Builder
		started bool
	)
	flush := func() {
		if started {
			out = append(out, cur.String())
			cur.Reset()
			started = false
		}
	}
	for i := 0; i < len(s); {
		r, w := utf8.DecodeRuneInString(s[i:])
		switch r {
		case ' ', '\t':
			flush()
			i += w
		case '\\':
			i += w
			if i >= len(s) {
				return nil, fmt.Errorf("args ends with a backslash, which has nothing to escape")
			}
			r2, w2 := utf8.DecodeRuneInString(s[i:])
			cur.WriteRune(r2)
			started = true
			i += w2
		case '"', '\'':
			// started is set BEFORE the loop, so `--p ""` is one empty
			// argument rather than no argument at all. An empty string is
			// legal argv and a variant may want one.
			started = true
			quote := r
			i += w
			closed := false
			for i < len(s) {
				r2, w2 := utf8.DecodeRuneInString(s[i:])
				i += w2
				if r2 == quote {
					closed = true
					break
				}
				if r2 == '\\' && quote == '"' {
					if i >= len(s) {
						return nil, fmt.Errorf("args ends with a backslash, which has nothing to escape")
					}
					r3, w3 := utf8.DecodeRuneInString(s[i:])
					cur.WriteRune(r3)
					i += w3
					continue
				}
				cur.WriteRune(r2)
			}
			if !closed {
				return nil, fmt.Errorf("args has an unterminated %c quote", quote)
			}
		default:
			cur.WriteRune(r)
			started = true
			i += w
		}
	}
	flush()
	return out, nil
}
