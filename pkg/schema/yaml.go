package schema

import (
	"fmt"
	"strconv"
	"strings"
)

// Kind is what a Node is. Five, because unlike the ap-sync subset this one
// must tell `enabled: false` from `enabled: "false"` and must emit
// `temperature: 0.2` into JSON as a number rather than a string.
type Kind int

const (
	// Scalar is a value: bare or double-quoted, already unquoted here.
	Scalar Kind = iota
	// Mapping is a block mapping.
	Mapping
	// Sequence is a block sequence, every element a Scalar.
	Sequence
	// Null is an explicit `null` or `~`.
	Null
	// Empty is a key with nothing after the colon and no indented block.
	// §4.2: the parser reports it and does not decide what it means.
	Empty
)

// Node is one parsed value.
type Node struct {
	Kind Kind
	// Line is 1-based, and is what every error message points at. For a
	// mapping or a sequence it is the line of the first entry.
	Line int
	// Str is set on Scalar only, unquoted and with any trailing comment
	// already removed.
	Str string
	// Quoted is set on Scalar only: the value was written with double quotes.
	// `enabled: false` and `enabled: "false"` parse to the same Str but must
	// stay distinguishable — a quoted scalar is a string by construction and
	// Bool/Number refuse it rather than silently agreeing.
	Quoted bool
	// Seq is set on Sequence only. Every element is a Scalar: §4.1 admits
	// exactly "- item", one per line.
	Seq []*Node
	// Keys is a mapping's keys in FILE order. Callers that need determinism
	// sort a copy; sorting this in place would lose the order errors are
	// reported in.
	Keys []string
	// KeyLine is the line the key itself appeared on, which is not
	// Map[k].Line when the value is an indented block — that node's Line is
	// the first line of the block. An "unknown key" error one line below the
	// key is the kind of small wrongness that costs ten minutes.
	KeyLine map[string]int
	Map     map[string]*Node
}

// Bool reads a plain `true` or `false`. A quoted scalar is a string by
// construction and is refused here, so `enabled: "false"` names its own line
// instead of quietly disabling a resource.
func (n *Node) Bool() (bool, error) {
	if n.Kind != Scalar || n.Quoted {
		return false, fmt.Errorf("line %d: expected true or false", n.Line)
	}
	switch n.Str {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return false, fmt.Errorf("line %d: %q is not true or false", n.Line, n.Str)
}

// Number validates the lexeme and returns it VERBATIM. The lexeme is what
// gets emitted into materialized JSON, so 0.20 stays 0.20 and no float
// formatting ever alters a value the author typed.
func (n *Node) Number() (string, error) {
	if n.Kind != Scalar || n.Quoted {
		return "", fmt.Errorf("line %d: expected a number", n.Line)
	}
	if _, err := strconv.ParseFloat(n.Str, 64); err != nil {
		return "", fmt.Errorf("line %d: %q is not a number", n.Line, n.Str)
	}
	return n.Str, nil
}

// Text reads a string. A plain `null` is not one — §3.7 gives it its own
// meaning, so silently reading it as the four letters would be wrong.
func (n *Node) Text() (string, error) {
	if n.Kind != Scalar {
		return "", fmt.Errorf("line %d: expected a string", n.Line)
	}
	return n.Str, nil
}

// line is one significant source line: blank lines and whole-line comments
// never make it this far.
type srcLine struct {
	n      int    // 1-based
	indent int    // leading spaces
	text   string // everything after the indent, right-trimmed
}

// ParseYAML parses the subset described in §4.1 of docs/specs/ap-sync-v1.md.
func ParseYAML(b []byte) (*Node, error) {
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
func parseBlock(ls []srcLine, i, indent int) (*Node, int, error) {
	if isSeqEntry(ls[i].text) {
		return parseSeq(ls, i, indent)
	}
	return parseMap(ls, i, indent)
}

func isSeqEntry(t string) bool { return t == "-" || strings.HasPrefix(t, "- ") }

func parseSeq(ls []srcLine, i, indent int) (*Node, int, error) {
	n := &Node{Kind: Sequence, Line: ls[i].n}
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
		child, err := scalarNode(cur.n, rest)
		if err != nil {
			return nil, 0, err
		}
		n.Seq = append(n.Seq, child)
		i++
		if i < len(ls) && ls[i].indent > indent {
			return nil, 0, fmt.Errorf("line %d: indented block under a sequence entry: v1 sequences hold one value per line", ls[i].n)
		}
	}
	return n, i, nil
}

func parseMap(ls []srcLine, i, indent int) (*Node, int, error) {
	n := &Node{Kind: Mapping, Line: ls[i].n, KeyLine: map[string]int{}, Map: map[string]*Node{}}
	for i < len(ls) && ls[i].indent == indent {
		cur := ls[i]
		key, rest, ok := cutKey(cur.text)
		if !ok {
			return nil, 0, fmt.Errorf("line %d: expected \"key: value\" or \"key:\", got %q", cur.n, cur.text)
		}
		if err := checkKey(cur.n, key); err != nil {
			return nil, 0, err
		}
		if _, dup := n.Map[key]; dup {
			// Refused rather than last-wins: a manifest with two `install:`
			// blocks means somebody merged badly, and quietly keeping one of
			// them is how half a setup goes missing.
			return nil, 0, fmt.Errorf("line %d: duplicate key %q in this mapping", cur.n, key)
		}
		i++

		value := strings.TrimLeft(rest, " ")
		hasInline := value != "" && !strings.HasPrefix(value, "#")
		indented := i < len(ls) && ls[i].indent > indent

		var child *Node
		switch {
		case hasInline:
			// The value is read BEFORE the indentation is judged, so `a: |`
			// reports the block scalar it is rather than the misindentation it
			// also is. The construct the author typed is the useful half of the
			// answer; "a: |" with an indented body is a block scalar every time.
			var err error
			child, err = scalarNode(cur.n, value)
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
			child = &Node{Kind: Empty, Line: cur.n}
		}
		n.Keys = append(n.Keys, key)
		n.KeyLine[key] = cur.n
		n.Map[key] = child
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

// scalarNode reads one value into a Node: double-quoted with two escapes, or
// bare and verbatim. A bare value that is exactly `null` or `~` becomes a
// Null node rather than a Scalar, per §3.7; everything else double-quoted
// becomes a Scalar with Quoted set, and everything else bare becomes a Scalar
// with Quoted false.
//
// s has already had its leading spaces removed and is known non-empty and not a
// comment.
func scalarNode(line int, s string) (*Node, error) {
	if what, bad := indicators[s[0]]; bad {
		return nil, fmt.Errorf("line %d: %s", line, what)
	}
	if s[0] != '"' {
		// Bare. Quotes INSIDE it are ordinary characters — YAML only treats a
		// quote as special at the start of a scalar — which is what carries
		// `--effort=xhigh "/plan:run {}"` through to Tokenize intact.
		if i := strings.Index(s, " #"); i >= 0 {
			s = s[:i]
		}
		str := strings.TrimRight(s, " ")
		if str == "null" || str == "~" {
			return &Node{Kind: Null, Line: line}, nil
		}
		return &Node{Kind: Scalar, Line: line, Str: str}, nil
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
				return nil, fmt.Errorf("line %d: unexpected text after a quoted scalar: %q", line, after)
			}
			return &Node{Kind: Scalar, Line: line, Str: b.String(), Quoted: true}, nil
		default:
			b.WriteByte(s[i])
		}
	}
	return nil, fmt.Errorf("line %d: unterminated quoted scalar", line)
}
