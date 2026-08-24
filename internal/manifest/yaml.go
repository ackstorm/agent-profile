//go:build unix

// Package manifest parses `ap sync` manifests: a restricted YAML subset,
// decoded into validated profile declarations.
//
// The subset is parsed here rather than decoded by a library because this
// repository takes no dependencies and the standard library has no YAML
// decoder. The same call was already made for TOML in
// internal/profile/settings.go, which slices blocks without a parser.
//
// The rule that makes a subset safe: it REJECTS everything it does not
// understand, naming the construct and the line. A manifest comes from a
// repository somebody else wrote, and silently misreading one — an anchor read
// as a scalar, a flow collection read as a string — is worse than refusing it.
package manifest

import (
	"fmt"
	"strings"
)

// kind is what a node is. Four, and no more: §4.2 makes every scalar a string,
// so there are no booleans, numbers or nulls to distinguish.
type kind int

const (
	// scalarNode is a value: bare or double-quoted, already unquoted here.
	scalarNode kind = iota
	// mapNode is a block mapping.
	mapNode
	// seqNode is a block sequence, every element a scalarNode.
	seqNode
	// emptyNode is a key with nothing after the colon and no indented block.
	// §4.2: the parser reports it and does not decide what it means.
	emptyNode
)

// node is one parsed value.
type node struct {
	kind kind
	// line is 1-based, and is what every error message points at. For a
	// mapping or a sequence it is the line of the first entry.
	line int
	// str is set on scalarNode only, unquoted and with any trailing comment
	// already removed.
	str string
	// seq is set on seqNode only. Every element is a scalarNode: §4.1 admits
	// exactly "- item", one per line.
	seq []*node
	// keys is a mapping's keys in FILE order. Callers that need determinism
	// sort a copy; sorting this in place would lose the order errors are
	// reported in.
	keys []string
	// keyLine is the line the key itself appeared on, which is not m[k].line
	// when the value is an indented block — that node's line is the first line
	// of the block. An "unknown key" error one line below the key is the kind
	// of small wrongness that costs ten minutes.
	keyLine map[string]int
	m       map[string]*node
}

// line is one significant source line: blank lines and whole-line comments
// never make it this far.
type srcLine struct {
	n      int    // 1-based
	indent int    // leading spaces
	text   string // everything after the indent, right-trimmed
}

// parseYAML parses the subset described in §4.1 of docs/specs/ap-sync-v1.md.
func parseYAML(b []byte) (*node, error) {
	ls, err := scan(string(b))
	if err != nil {
		return nil, err
	}
	if len(ls) == 0 {
		return nil, fmt.Errorf("the manifest is empty")
	}
	if ls[0].indent != 0 {
		return nil, fmt.Errorf("line %d: the document starts indented", ls[0].n)
	}
	root, i, err := parseBlock(ls, 0, 0)
	if err != nil {
		return nil, err
	}
	if i != len(ls) {
		return nil, fmt.Errorf("line %d: unexpected content after the document", ls[i].n)
	}
	return root, nil
}

// scan splits the source into significant lines, and is where the three
// whole-line rejections live: tabs in indentation, and the two multi-document
// markers.
func scan(s string) ([]srcLine, error) {
	var out []srcLine
	for i, raw := range strings.Split(s, "\n") {
		n := i + 1
		indent := 0
		for indent < len(raw) && raw[indent] == ' ' {
			indent++
		}
		if indent < len(raw) && raw[indent] == '\t' {
			// Named explicitly, because "did not parse" is a miserable answer
			// for a file that looks correct in an editor set to soft tabs.
			return nil, fmt.Errorf("line %d: tab in indentation: indent with spaces", n)
		}
		text := strings.TrimRight(raw[indent:], " \t\r")
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		if text == "---" || text == "..." {
			return nil, fmt.Errorf("line %d: multi-document marker %q: one document per file", n, text)
		}
		out = append(out, srcLine{n: n, indent: indent, text: text})
	}
	return out, nil
}

// parseBlock parses the run of lines at exactly indent. Which of the two block
// forms it is, is decided by the first line and never revisited: a sequence
// entry appearing among mapping keys is an error, not a switch.
func parseBlock(ls []srcLine, i, indent int) (*node, int, error) {
	if isSeqEntry(ls[i].text) {
		return parseSeq(ls, i, indent)
	}
	return parseMap(ls, i, indent)
}

func isSeqEntry(t string) bool { return t == "-" || strings.HasPrefix(t, "- ") }

func parseSeq(ls []srcLine, i, indent int) (*node, int, error) {
	n := &node{kind: seqNode, line: ls[i].n}
	for i < len(ls) && ls[i].indent == indent {
		cur := ls[i]
		if !isSeqEntry(cur.text) {
			return nil, 0, fmt.Errorf("line %d: expected a sequence entry (\"- item\"), got %q", cur.n, cur.text)
		}
		rest := ""
		if len(cur.text) > 2 {
			rest = strings.TrimLeft(cur.text[2:], " ")
		}
		if rest == "" || strings.HasPrefix(rest, "#") {
			return nil, 0, fmt.Errorf("line %d: empty sequence entry: one value per \"- \" line", cur.n)
		}
		s, err := scalar(cur.n, rest)
		if err != nil {
			return nil, 0, err
		}
		n.seq = append(n.seq, &node{kind: scalarNode, line: cur.n, str: s})
		i++
		if i < len(ls) && ls[i].indent > indent {
			return nil, 0, fmt.Errorf("line %d: indented block under a sequence entry: v1 sequences hold one value per line", ls[i].n)
		}
	}
	return n, i, nil
}

func parseMap(ls []srcLine, i, indent int) (*node, int, error) {
	n := &node{kind: mapNode, line: ls[i].n, keyLine: map[string]int{}, m: map[string]*node{}}
	for i < len(ls) && ls[i].indent == indent {
		cur := ls[i]
		key, rest, ok := cutKey(cur.text)
		if !ok {
			return nil, 0, fmt.Errorf("line %d: expected \"key: value\" or \"key:\", got %q", cur.n, cur.text)
		}
		if err := checkKey(cur.n, key); err != nil {
			return nil, 0, err
		}
		if _, dup := n.m[key]; dup {
			// Refused rather than last-wins: a manifest with two `install:`
			// blocks means somebody merged badly, and quietly keeping one of
			// them is how half a setup goes missing.
			return nil, 0, fmt.Errorf("line %d: duplicate key %q in this mapping", cur.n, key)
		}
		i++

		value := strings.TrimLeft(rest, " ")
		hasInline := value != "" && !strings.HasPrefix(value, "#")
		indented := i < len(ls) && ls[i].indent > indent

		var child *node
		switch {
		case hasInline:
			// The value is read BEFORE the indentation is judged, so `a: |`
			// reports the block scalar it is rather than the misindentation it
			// also is. The construct the author typed is the useful half of the
			// answer; "a: |" with an indented body is a block scalar every time.
			s, err := scalar(cur.n, value)
			if err != nil {
				return nil, 0, err
			}
			if indented {
				// One message for both shapes it can be — a value with a block
				// under it, and a sibling that missed its column — because they
				// are indistinguishable here: `a: 1` then a deeper `b: 2` is
				// either, depending only on what the author meant.
				return nil, 0, fmt.Errorf("line %d: unexpected indentation under %q, which already has a value on its own line: a sibling must match its siblings exactly", ls[i].n, key)
			}
			child = &node{kind: scalarNode, line: cur.n, str: s}
		case indented:
			var err error
			child, i, err = parseBlock(ls, i, ls[i].indent)
			if err != nil {
				return nil, 0, err
			}
		default:
			// §4.2: reported as empty, with no decision about what empty
			// means. `claude:` under platforms and `bootstrap:` want opposite
			// answers, and only the schema knows which one it is looking at.
			child = &node{kind: emptyNode, line: cur.n}
		}
		n.keys = append(n.keys, key)
		n.keyLine[key] = cur.n
		n.m[key] = child
	}
	if i < len(ls) && ls[i].indent > indent {
		// Deeper than this block but shallower than the child block that just
		// closed: a sibling that does not line up with its siblings.
		return nil, 0, fmt.Errorf("line %d: unexpected indentation: a sibling must match its siblings exactly", ls[i].n)
	}
	return n, i, nil
}

// cutKey splits "key: value" or "key:" at the first colon that is followed by a
// space or ends the line — which is exactly why §4.3 says a scalar containing
// ": " has to be quoted.
func cutKey(s string) (key, rest string, ok bool) {
	for i := range len(s) {
		if s[i] != ':' {
			continue
		}
		if i+1 == len(s) || s[i+1] == ' ' {
			return strings.TrimRight(s[:i], " "), s[i+1:], true
		}
	}
	return "", "", false
}

// checkKey refuses the constructs that can only appear as a key. The ones that
// appear as a value are refused in scalar, one error each.
func checkKey(n int, k string) error {
	switch {
	case k == "":
		return fmt.Errorf("line %d: empty key", n)
	case k == "<<":
		return fmt.Errorf("line %d: merge key \"<<\": not supported", n)
	case strings.HasPrefix(k, `"`), strings.HasPrefix(k, `'`):
		return fmt.Errorf("line %d: quoted key %s: keys are bare in this subset", n, k)
	case strings.HasPrefix(k, "&"), strings.HasPrefix(k, "*"), strings.HasPrefix(k, "!"):
		return fmt.Errorf("line %d: %q: anchors, aliases and tags are not supported", n, k)
	}
	return nil
}

// indicators maps the first character of a value to the construct it opens, so
// every rejection can name what it saw rather than saying "did not parse". §4.3
// tells the author to quote the value; the error has to be specific enough for
// that advice to be findable.
var indicators = map[byte]string{
	'[':  "flow sequence ([a, b]): use a block sequence, one \"- item\" per line",
	'{':  "flow mapping ({a: b}): use a block mapping, one \"key: value\" per line",
	'|':  "block scalar (|): write the value on one line, quoted if it needs to be",
	'>':  "block scalar (>): write the value on one line, quoted if it needs to be",
	'&':  "anchor (&): anchors, aliases and merge keys are not supported",
	'*':  "alias (*): anchors, aliases and merge keys are not supported",
	'!':  "tag (!): tags are not supported",
	'%':  "directive (%): not supported",
	'@':  "reserved indicator (@): quote the value if it is meant literally",
	'`':  "reserved indicator (`): quote the value if it is meant literally",
	'\'': "single-quoted scalar: use double quotes, so there is one escaping rule",
}

// scalar reads one value: double-quoted with two escapes, or bare and verbatim.
//
// s has already had its leading spaces removed and is known non-empty and not a
// comment.
func scalar(line int, s string) (string, error) {
	if what, bad := indicators[s[0]]; bad {
		return "", fmt.Errorf("line %d: %s", line, what)
	}
	if s[0] != '"' {
		// Bare. Quotes INSIDE it are ordinary characters — YAML only treats a
		// quote as special at the start of a scalar — which is what carries
		// `--effort=xhigh "/plan:run {}"` through to Tokenize intact.
		if i := strings.Index(s, " #"); i >= 0 {
			s = s[:i]
		}
		return strings.TrimRight(s, " "), nil
	}
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			// \\ and \" are the only escapes (§4.1). Every other backslash is
			// a literal backslash, so a Windows path or a regex in a prompt
			// survives being quoted.
			if i+1 < len(s) && (s[i+1] == '\\' || s[i+1] == '"') {
				b.WriteByte(s[i+1])
				i++
				continue
			}
			b.WriteByte('\\')
		case '"':
			after := strings.TrimLeft(s[i+1:], " ")
			if after != "" && !strings.HasPrefix(after, "#") {
				return "", fmt.Errorf("line %d: unexpected text after a quoted scalar: %q", line, after)
			}
			return b.String(), nil
		default:
			b.WriteByte(s[i])
		}
	}
	return "", fmt.Errorf("line %d: unterminated quoted scalar", line)
}
