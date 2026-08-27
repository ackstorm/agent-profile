# Phase 1 — Manifest and Composition Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: `superpowers:subagent-driven-development`
> (recommended) or `superpowers:executing-plans` to implement this plan
> task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn a declarative profile YAML file plus its `extends` chain into a
validated, runtime-specific effective profile, printable with `ap manifest render`, with
no filesystem mutation and no network access.

**Architecture:** One new **exported** package `pkg/schema`, layered bottom-up: a typed
YAML subset parser produces a `*Node` tree; a schema-supplied union oracle drives
a generic merge engine (the merge engine never names a resource type); `extends`
folds parent trees left-to-right; the composed tree is decoded into a typed
`Profile` and validated; `Effective` selects a runtime and overlays its block.
`internal/manifest` (`ap sync`) is left untouched and dies in Phase 7.

**Tech Stack:** Go 1.25, standard library only. `pkg/schema` carries **no build
tag** and must compile for windows — `ach` imports it and `ach-cli` ships windows.
Only `cmd/ap/manifest.go` keeps `//go:build unix`.

## Global Constraints

Copied verbatim from `docs/superpowers/plans/2026-08-26-declarative-v1-roadmap.md`
§Global Constraints; every task's requirements implicitly include them.

- **Go 1.25 floor is a security floor.** `go.mod` stays at `1.25.8` or above.
- **Build tags split by layer.** `pkg/**` carries **no** build tag and must
  compile for `windows/amd64`. `internal/**` and `cmd/ap/**` keep `//go:build unix`.
- **`pkg/` is a published API.** Every exported symbol is a compatibility promise
  to `ach` and `ach-agent`. Export what a consumer needs, nothing more.
- **`pkg/` never reads `$HOME` implicitly.** Every root arrives as a parameter.
- **No host toolchain.** Every Go command goes through a `make` target, which runs
  it inside `Dockerfile.devtools` via `scripts/dev.sh`. `make test` is
  `go test -race -shuffle=on -count=1 ./...`.
- **Standard library only.** Phase 1 adds no dependency. `go.mod` gains no `require`.
- **Anything turning user input into a path calls `agentreg.ValidName`.**
- **Guards get mutation-tested.** After adding a guard: revert it, run its test,
  confirm the test fails, restore.
- **`run` parses no flags after its reference.** `render` has no passthrough, so
  it uses `parseAroundRef` — never extend flag parsing into `run`.
- **Commits are frequent, conventional, short imperative subject <72 chars.**

---

## File Structure

| File | Responsibility |
|---|---|
| `pkg/schema/doc.go` | Package doc: what the subset admits, why it rejects the rest, and that this package is a published API |
| `pkg/schema/yaml.go` | Typed YAML subset parser → `*Node` |
| `pkg/schema/yaml_test.go` | Parser table tests, one per admitted and rejected construct |
| `pkg/schema/fuzz_test.go` | `FuzzParseYAML` — must never panic |
| `pkg/schema/schema.go` | Union oracle: path patterns for branch unions and exclusive groups |
| `pkg/schema/schema_test.go` | Pattern matcher tests |
| `pkg/schema/compose.go` | `Merge` — the three rules, unions, exclusive groups, `null` reset |
| `pkg/schema/compose_test.go` | Every §3 example from the spec as a table case |
| `pkg/schema/extends.go` | `Load` — parent resolution, cycle detection, traversal guard |
| `pkg/schema/extends_test.go` | Chain, cycle, traversal, missing-parent cases |
| `pkg/schema/profile.go` | `Profile` types + `Decode` + validation |
| `pkg/schema/profile_test.go` | Schema validation cases |
| `pkg/schema/effective.go` | `Effective` — runtime selection, overlay, enabled state, warnings |
| `pkg/schema/effective_test.go` | Runtime overlay and §7.1/§7.2 warning cases |
| `pkg/schema/render.go` | Deterministic YAML emit with secret redaction |
| `cmd/ap/manifest.go` | `ap manifest render <path> [--target <runtime>]` dispatch |

Also in this phase, as a mechanical move with no behaviour change (Task 0):
`internal/agent` → `pkg/agentreg`. `ach` needs that table — it keeps a second
copy today in `internal/cli/adapter/globalpath.go`, and two copies of "what does
`GEMINI_CLI_HOME` mean" is how they drift. The move is import-path-only; the
registry's contents, its verified-by-running-the-binary rule and its tests are
untouched.

Three design decisions locked here, the first two from the spec:

1. **The merge engine never names a resource type.** §3.5: unions are
   "marked in the schema — never hardcoded per type in the merge engine".
   `compose.go` receives a `Schema` and asks it; it must contain no string
   literal like `"skills"` or `"source"`.
2. **`pkg/schema` must not import `internal/`.** It is imported by another
   module; an `internal/` import would not compile there. The compiler enforces
   this for `ach`, but not for `ap`, so Task 0 adds a test that walks the import
   graph. `profile.ValidName` (used by `extends`) therefore moves too — see
   Task 8's note.
3. **`mcps.*.transport` is NOT a marked union.** §3.5 lists exactly three v1
   union points and `transport` is not among them: it is discriminated by an
   inner `type` field, not by a branch key. So a base declaring `stdio` and a
   child declaring `type: http` leaves `command` and `args` inherited, and
   *validation* rejects the mix. This is per spec. Do not "fix" it by adding
   `transport` to the oracle.

---

### Task 0: Make the reusable half exportable — ✅ DONE (`bb5987b`)

> Already implemented and committed: `pkg/agentreg` (registry + `ValidName` +
> `Default`) and `pkg/boundary_test.go`. Verify with `make test` and
> `make crossbuild`, then skip to Task 1. Steps kept for the record.

`ach` cannot import `internal/`. This task moves what a consumer needs and pins
the boundary with a test, before any new code is written against the old layout.

**Files:**
- Move: `internal/agent/` → `pkg/agentreg/` (package renamed `agentreg`)
- Move: `profile.ValidName` and `profile.Default` → `pkg/agentreg/name.go`
- Modify: every import site (`internal/profile`, `internal/run`, `internal/session`,
  `internal/manifest`, `cmd/ap`)
- Create: `pkg/boundary_test.go`
- Modify: `Makefile` — add `crossbuild`, both halves

**Interfaces:**
- Produces: `agentreg.Agent`, `agentreg.Lookup`, `agentreg.Names`,
  `agentreg.ValidName`, `agentreg.Default`. Every later task and phase uses these
  names; `profile.ValidName` no longer exists.

- [ ] **Step 1: Write the failing boundary test**

```go
package pkg_test

import (
	"go/build"
	"path/filepath"
	"strings"
	"testing"
)

// pkg/ is imported by ackstorm/ach, which is a different module, so an
// internal/ import here would simply not compile there. Go enforces that for
// ach and not for us, which means we would find out in the wrong repository.
// This test finds out here.
func TestPkgNeverImportsInternal(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	pkgs, err := filepath.Glob(filepath.Join(root, "pkg", "*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) == 0 {
		t.Fatal("no packages under pkg/")
	}
	for _, dir := range pkgs {
		p, err := build.ImportDir(dir, 0)
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		for _, imp := range append(p.Imports, p.TestImports...) {
			if strings.Contains(imp, "/internal/") {
				t.Errorf("%s imports %s; pkg/ must not import internal/", dir, imp)
			}
		}
	}
}
```

- [ ] **Step 2: Run it, verify it fails**
  Run: `make test-one T=TestPkgNeverImportsInternal P=./pkg/`
  Expected: FAIL — `no packages under pkg/`

- [ ] **Step 3: Move the packages**

```bash
git mv internal/agent pkg/agentreg
sed -i 's/^package agent$/package agentreg/' pkg/agentreg/*.go
sed -i 's|"github.com/ackstorm/agent-profile/internal/agent"|"github.com/ackstorm/agent-profile/pkg/agentreg"|' $(grep -rl 'internal/agent' --include='*.go' .)
sed -i 's/agent\.\([A-Z]\)/agentreg./g' $(grep -rl 'agentreg' --include='*.go' internal cmd)
```

Then move `ValidName` and `Default` out of `internal/profile` into
`pkg/agentreg/name.go` verbatim, keeping their doc comments — `--from` once
skipped `ValidName` and became a path traversal, and that comment is the reason
anyone keeps calling it. `internal/profile` imports them back from `agentreg`.

Delete the `//go:build unix` line from every moved file. The registry is a table
of paths and strings; it has no syscall in it. `agent.go`'s `home()` uses
`os.UserHomeDir`, which is portable.

- [ ] **Step 4: Run the full suite, verify nothing changed**
  Run: `make test`
  Expected: PASS, including all three untouchable tests
  (`TestDeleteDoesNotFollowTheConfigShim`, `TestDeleteDoesNotFollowSymlinks`,
  `TestEnvOnlySetsPathsInsideTheProfile`). This is a pure move: a single
  behavioural failure here means the move was not pure. Do not adjust a test.
  Run: `make test-one T=TestPkgNeverImportsInternal P=./pkg/`
  Expected: PASS

- [ ] **Step 5: Add the crossbuild gate**

In the Makefile, next to `verify`, both halves per the `in_container` rule:

```makefile
.PHONY: crossbuild
crossbuild: ## Build the exported library for windows — ach imports it and ships there.
	$(call in_container,_crossbuild)
_crossbuild:
	GOOS=windows GOARCH=amd64 go build ./pkg/...
	GOOS=darwin  GOARCH=arm64 go build ./pkg/...
```

Add `_crossbuild` to `_verify`'s prerequisite list.

- [ ] **Step 6: Run it**
  Run: `make crossbuild`
  Expected: both builds succeed. A failure here names a syscall or a build tag
  that has to move back to `internal/`.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "refactor: export the agent registry as pkg/agentreg"
```

---

### Task 1: Typed scalar nodes

Evolve the value model so `null`, booleans and numbers survive parsing. Today
`internal/manifest/yaml.go` makes every scalar a string, which cannot express
`enabled: false` or emit `temperature: 0.2` as a JSON number.

**Files:**
- Create: `pkg/schema/doc.go`
- Create: `pkg/schema/yaml.go`
- Test: `pkg/schema/yaml_test.go`

**Interfaces:**
- Produces: `Kind`, `Node`, `ParseYAML(b []byte) (*Node, error)`,
  `(*Node).Bool() (bool, error)`, `(*Node).Number() (string, error)`,
  `(*Node).Text() (string, error)`. Every later task consumes these.

- [ ] **Step 1: Write failing tests**

```go
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
```

- [ ] **Step 2: Run test, verify it fails**
  Run: `make test-one T=TestParseDistinguishesNullBoolAndNumberFromString P=./pkg/schema/`
  (If that target does not exist, add it next to `_test` in the Makefile as
  `go test -race -run '$(T)' $(P)`, both halves, per the `in_container` rule.)
  Expected: FAIL — package `decl` does not exist.

- [ ] **Step 3: Write the parser**

Copy `internal/manifest/yaml.go` to `pkg/schema/yaml.go` verbatim first, then
change these three things and nothing else:

```go
// Kind is what a node is. Five, because unlike the ap-sync subset this one
// must tell `enabled: false` from `enabled: "false"` and must emit
// `temperature: 0.2` into JSON as a number rather than a string.
type Kind int

const (
	Scalar Kind = iota // a value: bare or quoted, already unquoted here
	Mapping            // a block mapping
	Sequence           // a block sequence, every element a Scalar
	Null               // an explicit `null` or `~`
	Empty              // a key with nothing after the colon and no block
)

type Node struct {
	Kind    Kind
	Line    int
	Str     string // Scalar only, unquoted, trailing comment removed
	Quoted  bool   // Scalar only: it was written with double quotes
	Seq     []*Node
	Keys    []string // mapping keys in FILE order
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

// Number validates the lexeme and returns it VERBATIM. The lexeme is what gets
// emitted into materialized JSON, so 0.20 stays 0.20 and no float formatting
// ever alters a value the author typed.
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
```

In the scalar-construction path, set `Quoted` when the value was double-quoted,
and return a `Null` node when an unquoted value is exactly `null` or `~`.

- [ ] **Step 4: Run tests, verify they pass**
  Run: `make test-one T=TestParseDistinguishesNullBoolAndNumberFromString P=./pkg/schema/`
  Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/schema/doc.go pkg/schema/yaml.go pkg/schema/yaml_test.go
git commit -m "feat(decl): typed scalars — null, bool and number survive parsing"
```

---

### Task 2: Block scalars

`prompt.content: |` appears throughout the spec. The ap-sync subset rejects `|`
outright.

**Files:**
- Modify: `pkg/schema/yaml.go`
- Test: `pkg/schema/yaml_test.go`

**Interfaces:**
- Consumes: `ParseYAML`, `Node` (Task 1).
- Produces: nothing new — `|` and `|-` now yield a `Scalar` whose `Str` holds
  the block body and whose `Quoted` is true (it is a string, never a bool).

- [ ] **Step 1: Write failing test**

```go
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
```

- [ ] **Step 2: Run test, verify it fails**
  Run: `make test-one T=TestParseReadsLiteralBlockScalarsAndRejectsFoldedOnes P=./pkg/schema/`
  Expected: FAIL — `block scalar (|): write value on one line`

- [ ] **Step 3: Implement**

In the value dispatch, before the existing indicator rejection table:

```go
// A literal block keeps every newline the author typed, which is the whole
// reason prompt content uses one. `|-` strips the final newline. An
// indentation indicator (`|2`) and a folded block (`>`) are refused by the
// table below: neither is needed and both change text in ways a reader of the
// file would not predict.
if v == "|" || v == "|-" {
	body, next := readBlockBody(ls, i+1, indent)
	if v == "|-" {
		body = strings.TrimRight(body, "\n")
	}
	return &Node{Kind: Scalar, Line: ls[i].n, Str: body, Quoted: true}, next, nil
}
```

```go
// readBlockBody consumes every line indented deeper than the key, strips that
// common indent, and returns the body plus the index of the first line that is
// not part of it. A blank line inside the block is kept as an empty line.
func readBlockBody(ls []line, i, keyIndent int) (string, int) {
	var body []string
	blockIndent := -1
	for ; i < len(ls); i++ {
		if strings.TrimSpace(ls[i].text) == "" {
			body = append(body, "")
			continue
		}
		ind := indentOf(ls[i].text)
		if ind <= keyIndent {
			break
		}
		if blockIndent < 0 {
			blockIndent = ind
		}
		body = append(body, strings.TrimRight(ls[i].text[blockIndent:], "\r"))
	}
	for len(body) > 0 && body[len(body)-1] == "" {
		body = body[:len(body)-1]
	}
	if len(body) == 0 {
		return "", i
	}
	return strings.Join(body, "\n") + "\n", i
}
```

Remove `'|'` from the rejection table; leave `'>'` in it.

- [ ] **Step 4: Run tests, verify they pass**
  Run: `make test-one T=TestParse P=./pkg/schema/`
  Expected: PASS (both parser tests)

- [ ] **Step 5: Commit**

```bash
git add pkg/schema/yaml.go pkg/schema/yaml_test.go
git commit -m "feat(decl): literal block scalars for prompt content"
```

---

### Task 3: Fuzz the parser and pin the rejections

The traversal bug in this repository was found by fuzzing. A parser that reads
files fetched by `git clone` from somebody else's repository gets the same
treatment.

**Files:**
- Create: `pkg/schema/fuzz_test.go`
- Modify: `pkg/schema/yaml_test.go`

**Interfaces:**
- Consumes: `ParseYAML` (Task 1).
- Produces: nothing — this task adds only tests.

- [ ] **Step 1: Write the fuzz target and the rejection table**

```go
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
```

```go
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
```

- [ ] **Step 2: Run both, verify the table passes and fuzz finds nothing**
  Run: `make test-one T=TestParseNamesEveryConstructItRefuses P=./pkg/schema/`
  Expected: PASS
  Run: `make fuzz` after adding `-fuzz=FuzzParseYAML ./pkg/schema/` to the
  `_fuzz` target's list, alongside the existing path-validation targets.
  Expected: 30s, no crashers.

- [ ] **Step 3: Commit**

```bash
git add pkg/schema/fuzz_test.go pkg/schema/yaml_test.go Makefile
git commit -m "test(decl): fuzz the parser and pin every named rejection"
```

---

### Task 4: The union oracle

§3.5 requires unions to be marked in the schema and never hardcoded per type in
the merge engine. This task builds the marking; Task 6 consumes it.

**Files:**
- Create: `pkg/schema/schema.go`
- Test: `pkg/schema/schema_test.go`

**Interfaces:**
- Produces: `type Schema struct{ ... }`, `V1Schema() Schema`,
  `(Schema) IsUnion(path []string) bool`,
  `(Schema) Group(path []string) []string`. Task 6 consumes both methods.

- [ ] **Step 1: Write failing test**

```go
func TestSchemaMarksTheThreeV1UnionPointsAndNothingElse(t *testing.T) {
	s := V1Schema()
	for _, p := range [][]string{
		{"skills", "pdf", "source"},
		{"artifacts", "agents-md", "source"},
		{"marketplaces", "anthropic-skills", "source"},
		{"prompt", "source"},
		{"runtimes", "claude", "skills", "pdf", "source"},
	} {
		if !s.IsUnion(p) {
			t.Errorf("%v: not marked as a union", p)
		}
	}
	// transport is discriminated by an inner `type` field, not a branch key.
	// §3.5 lists exactly three v1 union points and this is not one of them.
	if s.IsUnion([]string{"mcps", "memory", "transport"}) {
		t.Error("mcps.*.transport is marked as a union; §3.5 does not list it")
	}
	if got := s.Group([]string{"skills", "pdf"}); !slices.Equal(got, []string{"ref", "source"}) {
		t.Errorf("skills.* group = %v, want [ref source]", got)
	}
	if got := s.Group([]string{"prompt"}); !slices.Equal(got, []string{"content", "source"}) {
		t.Errorf("prompt group = %v, want [content source]", got)
	}
	if s.Group([]string{"mcps", "memory"}) != nil {
		t.Error("mcps.* has an exclusive group; it has no locator")
	}
}
```

- [ ] **Step 2: Run test, verify it fails**
  Run: `make test-one T=TestSchemaMarksTheThreeV1UnionPointsAndNothingElse P=./pkg/schema/`
  Expected: FAIL — undefined: `V1Schema`

- [ ] **Step 3: Implement**

```go
package schema

import "slices"

// Schema marks the points where composition stops being an ordinary mapping
// merge. §3.5 requires exactly this: the marking lives here, and compose.go
// asks. Nothing in compose.go may name a resource type.
type Schema struct {
	// unions are branch-keyed union NODES: the value under this path selects
	// exactly one branch, and an overlay choosing a different branch replaces
	// the whole node.
	unions [][]string
	// groups are exclusive groups, keyed by the CONTAINER path. An overlay
	// declaring any member discards every other inherited member.
	groups []group
}

type group struct {
	at      []string
	members []string
}

// V1Schema is §3.5's three union points and no others. Adding a row is a spec
// change, not an implementation detail.
func V1Schema() Schema {
	return Schema{
		unions: [][]string{
			{"skills", "*", "source"},
			{"artifacts", "*", "source"},
			{"marketplaces", "*", "source"},
			{"prompt", "source"},
			{"runtimes", "*", "skills", "*", "source"},
			{"runtimes", "*", "artifacts", "*", "source"},
			{"runtimes", "*", "marketplaces", "*", "source"},
			{"runtimes", "*", "plugins", "*", "source"},
		},
		groups: []group{
			// Members sorted, so Group's result is stable and comparable.
			{at: []string{"skills", "*"}, members: []string{"ref", "source"}},
			{at: []string{"runtimes", "*", "skills", "*"}, members: []string{"ref", "source"}},
			{at: []string{"prompt"}, members: []string{"content", "source"}},
		},
	}
}

func (s Schema) IsUnion(path []string) bool {
	for _, p := range s.unions {
		if matchPath(p, path) {
			return true
		}
	}
	return false
}

// Group returns the exclusive-group members at this container path, or nil.
func (s Schema) Group(path []string) []string {
	for _, g := range s.groups {
		if matchPath(g.at, path) {
			return slices.Clone(g.members)
		}
	}
	return nil
}

// matchPath compares segment by segment; "*" in the pattern matches exactly one
// segment. There is no "**": every marked point in v1 is at a known depth, and
// a wildcard that swallowed depth would mark paths nobody wrote down.
func matchPath(pattern, path []string) bool {
	if len(pattern) != len(path) {
		return false
	}
	for i, seg := range pattern {
		if seg != "*" && seg != path[i] {
			return false
		}
	}
	return true
}
```

Note the deliberate omission: `runtimes.*.plugins.*` gets no exclusive group.
`package:` (opencode) is a third locator form and §24 makes the plugin schema
adapter-owned, so Phase 6 decides it. A group added here now would refuse
`package` before anything can materialize it.

- [ ] **Step 4: Run test, verify it passes**
  Run: `make test-one T=TestSchemaMarksTheThreeV1UnionPointsAndNothingElse P=./pkg/schema/`
  Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/schema/schema.go pkg/schema/schema_test.go
git commit -m "feat(decl): mark the three v1 union points in the schema"
```

---

### Task 5: The three merge rules

§3.1: mappings merge recursively, non-mappings replace, and (Task 6) unions
replace as a unit. This task does the first two plus `null` reset.

**Files:**
- Create: `pkg/schema/compose.go`
- Test: `pkg/schema/compose_test.go`

**Interfaces:**
- Consumes: `Node`, `ParseYAML` (Task 1); `Schema` (Task 4).
- Produces: `Merge(base, overlay *Node, s Schema) *Node`. Tasks 6, 8 and 12
  consume it.

- [ ] **Step 1: Write failing tests**

```go
// mergeYAML is the helper every composition test uses: parse two documents,
// merge them, render the result. Written once here so a case is three lines.
func mergeYAML(t *testing.T, base, overlay string) *Node {
	t.Helper()
	b, err := ParseYAML([]byte(base))
	if err != nil {
		t.Fatalf("base: %v", err)
	}
	o, err := ParseYAML([]byte(overlay))
	if err != nil {
		t.Fatalf("overlay: %v", err)
	}
	return Merge(b, o, V1Schema())
}

func TestMergeRecursesIntoMappingsAndReplacesEverythingElse(t *testing.T) {
	// §3.3: a child overriding one parameter keeps its siblings.
	got := mergeYAML(t,
		"model:\n  type: anthropic\n  parameters:\n    effort: high\n    temperature: 0.2\n",
		"model:\n  parameters:\n    effort: xhigh\n")
	params := got.Map["model"].Map["parameters"]
	if params.Map["effort"].Str != "xhigh" {
		t.Errorf("effort = %q, want xhigh", params.Map["effort"].Str)
	}
	if params.Map["temperature"].Str != "0.2" {
		t.Errorf("temperature = %q, want 0.2 — a sibling was lost", params.Map["temperature"].Str)
	}
	if got.Map["model"].Map["type"].Str != "anthropic" {
		t.Error("model.type was lost")
	}

	// §3.4: lists replace wholesale. There is no implicit append.
	got = mergeYAML(t, "targets:\n  - claude\n  - codex\n", "targets:\n  - opencode\n")
	if len(got.Map["targets"].Seq) != 1 || got.Map["targets"].Seq[0].Str != "opencode" {
		t.Errorf("targets = %v, want exactly [opencode]", got.Map["targets"].Seq)
	}
}

func TestMergeTreatsNullAsResetToAbsent(t *testing.T) {
	// §3.7: `model: null` in a child yields an effective profile without model,
	// which is the subscription case — runtime-native defaults and credentials.
	got := mergeYAML(t, "model:\n  type: anthropic\n  model: claude-opus-5\n", "model: null\n")
	if _, ok := got.Map["model"]; ok {
		t.Error("model survived `model: null`; §3.7 makes it absent")
	}
	if slices.Contains(got.Keys, "model") {
		t.Error("model still listed in Keys; render would emit it")
	}
}
```

- [ ] **Step 2: Run tests, verify they fail**
  Run: `make test-one T='TestMerge' P=./pkg/schema/`
  Expected: FAIL — undefined: `Merge`

- [ ] **Step 3: Implement**

```go
package schema

import "slices"

// Merge composes overlay onto base. §3.1's three rules and nothing else:
// mappings merge recursively, non-mappings replace, marked unions and
// exclusive groups replace as a unit.
//
// This function must never name a resource type. Every decision that depends
// on WHICH key is being merged goes through Schema — that is what §3.5 means
// by "marked in the schema, never hardcoded per type in the merge engine".
func Merge(base, overlay *Node, s Schema) *Node {
	return mergeAt(base, overlay, s, nil)
}

func mergeAt(base, overlay *Node, s Schema, path []string) *Node {
	if base == nil {
		return overlay
	}
	if overlay == nil {
		return base
	}
	// Rule 2: a non-mapping on either side replaces. A Null overlay is handled
	// by the caller, which removes the key rather than storing a Null.
	if base.Kind != Mapping || overlay.Kind != Mapping {
		return overlay
	}
	// Rule 3, branch-keyed unions (§3.5.1): a different branch replaces the
	// whole node; the same branch falls through to the ordinary mapping merge
	// below, which recurses into it.
	if s.IsUnion(path) && !sameBranch(base, overlay) {
		return overlay
	}

	out := &Node{Kind: Mapping, Line: base.Line, Map: map[string]*Node{}, KeyLine: map[string]int{}}
	drop := discardedGroupMembers(base, overlay, s, path)

	for _, k := range base.Keys {
		if slices.Contains(drop, k) {
			continue
		}
		out.Keys = append(out.Keys, k)
		out.KeyLine[k] = base.KeyLine[k]
		out.Map[k] = base.Map[k]
	}
	for _, k := range overlay.Keys {
		ov := overlay.Map[k]
		// §3.7: null is reset-to-absent. Storing a Null instead would make
		// every consumer check for it, and render would emit a key the
		// effective profile does not have.
		if ov.Kind == Null {
			if i := slices.Index(out.Keys, k); i >= 0 {
				out.Keys = slices.Delete(out.Keys, i, i+1)
				delete(out.Map, k)
				delete(out.KeyLine, k)
			}
			continue
		}
		if _, ok := out.Map[k]; !ok {
			out.Keys = append(out.Keys, k)
		}
		out.KeyLine[k] = overlay.KeyLine[k]
		out.Map[k] = mergeAt(out.Map[k], ov, s, append(path, k))
	}
	return out
}

// sameBranch reports whether both sides of a branch-keyed union select the same
// branch. A union node holds exactly one key by construction (schema validation
// enforces that within a single document), so comparing the first key is the
// whole comparison.
func sameBranch(base, overlay *Node) bool {
	if len(base.Keys) == 0 || len(overlay.Keys) == 0 {
		return false
	}
	return base.Keys[0] == overlay.Keys[0]
}

// discardedGroupMembers returns the inherited exclusive-group members that this
// overlay displaces. Task 6 fills it in; until then nothing is discarded.
func discardedGroupMembers(base, overlay *Node, s Schema, path []string) []string {
	return nil
}
```

Note: `append(path, k)` inside a loop can alias. Build the child path with
`slices.Concat(path, []string{k})` if the union tests in Task 6 show
cross-contamination — the fix is one line and the test will name it.

- [ ] **Step 4: Run tests, verify they pass**
  Run: `make test-one T='TestMerge' P=./pkg/schema/`
  Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/schema/compose.go pkg/schema/compose_test.go
git commit -m "feat(decl): mappings merge, non-mappings replace, null resets"
```

---

### Task 6: Unions and exclusive groups

Without §3.5.2 an effective resource carries both `ref` and `source` and fails
locator validation, making it impossible to override a marketplace-backed
resource with a direct source. That is the bug this task prevents.

**Files:**
- Modify: `pkg/schema/compose.go:discardedGroupMembers`
- Test: `pkg/schema/compose_test.go`

**Interfaces:**
- Consumes: `Schema.IsUnion`, `Schema.Group` (Task 4); `mergeAt` (Task 5).
- Produces: no new symbols — `Merge`'s behaviour changes only.

- [ ] **Step 1: Write failing tests**

```go
func TestMergeReplacesAUnionWholesaleWhenTheBranchChanges(t *testing.T) {
	// §3.5.1: git → local replaces the node. Branches never coexist.
	got := mergeYAML(t,
		"skills:\n  pdf:\n    source:\n      git:\n        url: https://example.com/r.git\n        ref: main\n",
		"skills:\n  pdf:\n    source:\n      local:\n        path: ./repo\n")
	src := got.Map["skills"].Map["pdf"].Map["source"]
	if _, ok := src.Map["git"]; ok {
		t.Error("git branch survived a switch to local; branches never coexist")
	}
	if src.Map["local"].Map["path"].Str != "./repo" {
		t.Error("local branch missing")
	}

	// Same branch composes: the base's url survives the child's ref override.
	got = mergeYAML(t,
		"skills:\n  pdf:\n    source:\n      git:\n        url: https://example.com/r.git\n        ref: main\n",
		"skills:\n  pdf:\n    source:\n      git:\n        ref: v2\n")
	git := got.Map["skills"].Map["pdf"].Map["source"].Map["git"]
	if git.Map["url"].Str != "https://example.com/r.git" {
		t.Error("url lost on a same-branch override")
	}
	if git.Map["ref"].Str != "v2" {
		t.Errorf("ref = %q, want v2", git.Map["ref"].Str)
	}
}

func TestMergeDiscardsTheOtherLocatorWhenAnOverlayPicksOne(t *testing.T) {
	// §3.5.2's worked example. Without this the resource carries ref AND
	// source and fails locator validation, so a marketplace-backed skill could
	// never be overridden with a direct source.
	got := mergeYAML(t,
		"skills:\n  pdf:\n    ref: pdf@anthropic-skills\n",
		"skills:\n  pdf:\n    source:\n      git:\n        url: https://example.com/fork.git\n")
	pdf := got.Map["skills"].Map["pdf"]
	if _, ok := pdf.Map["ref"]; ok {
		t.Error("inherited ref survived an overlay declaring source")
	}
	if _, ok := pdf.Map["source"]; !ok {
		t.Fatal("source missing")
	}

	// Keys OUTSIDE the group compose normally: `enabled: false` must not
	// disturb the locator, and a replaced prompt content inherits mode.
	got = mergeYAML(t,
		"skills:\n  pdf:\n    ref: pdf@anthropic-skills\n",
		"skills:\n  pdf:\n    enabled: false\n")
	if _, ok := got.Map["skills"].Map["pdf"].Map["ref"]; !ok {
		t.Error("`enabled: false` discarded the locator; it is outside the group")
	}
	got = mergeYAML(t,
		"prompt:\n  mode: replace\n  source:\n    local:\n      path: ./p.md\n",
		"prompt:\n  content: |\n    inline\n")
	p := got.Map["prompt"]
	if _, ok := p.Map["source"]; ok {
		t.Error("prompt source survived an overlay declaring content")
	}
	if p.Map["mode"].Str != "replace" {
		t.Error("mode lost; it is outside the content|source group")
	}
}
```

- [ ] **Step 2: Run tests, verify they fail**
  Run: `make test-one T='TestMergeDiscards|TestMergeReplacesAUnion' P=./pkg/schema/`
  Expected: FAIL — the ref survives, and `source` is present alongside it.

- [ ] **Step 3: Implement**

```go
// discardedGroupMembers returns the inherited exclusive-group members this
// overlay displaces. §3.5.2: declaring ANY member of a group discards every
// other inherited member; declaring the same member composes normally. Keys
// outside the group are untouched, which is what lets `enabled: false` leave a
// locator alone and a replaced prompt content inherit its mode.
func discardedGroupMembers(base, overlay *Node, s Schema, path []string) []string {
	members := s.Group(path)
	if members == nil {
		return nil
	}
	var declared bool
	for _, m := range members {
		if _, ok := overlay.Map[m]; ok {
			declared = true
			break
		}
	}
	if !declared {
		return nil
	}
	var drop []string
	for _, m := range members {
		if _, inOverlay := overlay.Map[m]; inOverlay {
			continue
		}
		if _, inBase := base.Map[m]; inBase {
			drop = append(drop, m)
		}
	}
	return drop
}
```

- [ ] **Step 4: Run tests, verify they pass**
  Run: `make test-one T='TestMerge' P=./pkg/schema/`
  Expected: PASS (all four merge tests)

- [ ] **Step 5: Mutation-test the guard**
  Make `discardedGroupMembers` return `nil` unconditionally again. Run
  `make test-one T=TestMergeDiscardsTheOtherLocatorWhenAnOverlayPicksOne P=./pkg/schema/`.
  Expected: FAIL. Restore. If it passed, the test is wrong — fix the test first.

- [ ] **Step 6: Commit**

```bash
git add pkg/schema/compose.go pkg/schema/compose_test.go
git commit -m "feat(decl): exclusive groups discard the displaced locator"
```

---

### Task 7: `null` on a collection entry is an error

§3.7 makes `skills.x: null` a validation error rather than a quiet delete,
because named resources already have `enabled: false` and a second, subtly
different disable semantics is not introduced.

**Files:**
- Modify: `pkg/schema/compose.go`, `pkg/schema/schema.go`
- Test: `pkg/schema/compose_test.go`

**Interfaces:**
- Consumes: `Schema` (Task 4).
- Produces: `Schema.IsCollection(path []string) bool`;
  `Merge` gains an error return: `Merge(base, overlay *Node, s Schema) (*Node, error)`.
  Update Task 5's and Task 6's call sites and the `mergeYAML` helper.

- [ ] **Step 1: Write failing test**

```go
func TestNullOnACollectionEntryIsRefusedAndNamesEnabledFalse(t *testing.T) {
	b, _ := ParseYAML([]byte("skills:\n  x:\n    ref: x@m\n"))
	o, _ := ParseYAML([]byte("skills:\n  x: null\n"))
	_, err := Merge(b, o, V1Schema())
	if err == nil {
		t.Fatal("`skills.x: null` accepted; §3.7 makes it a validation error")
	}
	if !strings.Contains(err.Error(), "skills.x") || !strings.Contains(err.Error(), "enabled: false") {
		t.Errorf("error %q must name the path and point at `enabled: false`", err)
	}
	// A singular still resets.
	b, _ = ParseYAML([]byte("model:\n  type: anthropic\n"))
	o, _ = ParseYAML([]byte("model: null\n"))
	if _, err := Merge(b, o, V1Schema()); err != nil {
		t.Errorf("`model: null` refused: %v — §3.7 allows it on singulars", err)
	}
}
```

- [ ] **Step 2: Run test, verify it fails**
  Run: `make test-one T=TestNullOnACollectionEntry P=./pkg/schema/`
  Expected: FAIL — `Merge` returns one value.

- [ ] **Step 3: Implement**

Add to `schema.go`:

```go
// collections are the named-resource collections: their ENTRIES are the things
// `null` may not reset. §3.7.
var collections = [][]string{
	{"skills", "*"}, {"mcps", "*"}, {"artifacts", "*"}, {"marketplaces", "*"},
	{"runtimes", "*", "skills", "*"}, {"runtimes", "*", "mcps", "*"},
	{"runtimes", "*", "artifacts", "*"}, {"runtimes", "*", "marketplaces", "*"},
	{"runtimes", "*", "plugins", "*"}, {"runtimes", "*", "variants", "*"},
}

// IsCollectionEntry reports whether path names an entry of a named resource
// collection.
func (s Schema) IsCollectionEntry(path []string) bool {
	for _, p := range collections {
		if matchPath(p, path) {
			return true
		}
	}
	return false
}
```

In `mergeAt`, in the `ov.Kind == Null` arm, before deleting:

```go
if ov.Kind == Null {
	// §3.7: a named resource already has `enabled: false`, which keeps it
	// visible in diagnostics. A second, subtly different disable semantics
	// is not introduced.
	if s.IsCollectionEntry(append(slices.Clone(path), k)) {
		return nil, fmt.Errorf("line %d: %s: null is not valid; use `enabled: false`",
			ov.Line, strings.Join(append(slices.Clone(path), k), "."))
	}
	...
}
```

Thread the error out of `mergeAt` and `Merge`.

- [ ] **Step 4: Run tests, verify they pass**
  Run: `make test-one T='TestMerge|TestNull' P=./pkg/schema/`
  Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/schema/compose.go pkg/schema/schema.go pkg/schema/compose_test.go
git commit -m "feat(decl): refuse null on a collection entry, name enabled: false"
```

---

### Task 8: `extends` resolution

§5: single parent, chains allowed, resolution is local and deterministic, no
search paths, no implicit remote lookup.

**Files:**
- Create: `pkg/schema/extends.go`
- Test: `pkg/schema/extends_test.go`

**Interfaces:**
- Consumes: `ParseYAML` (Task 1), `Merge` (Tasks 5–7), `agentreg.ValidName`
  (Task 0 — `pkg/` cannot import `internal/profile`, which is why it moved).
- Produces: `Load(path string) (composed *Node, leafRuntimes []string, err error)`.
  `leafRuntimes` is the runtime names the LEAF document declared itself, which
  Task 11 needs to tell §7.1 (inherited, silent) from §7.2 (local, warn).

- [ ] **Step 1: Write failing tests**

```go
func TestLoadFoldsAnExtendsChainParentFirst(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "base.yaml", "version: \"1\"\nname: base\nmodel:\n  type: anthropic\n  parameters:\n    effort: high\n")
	write(t, dir, "mid.yaml", "version: \"1\"\nname: mid\nextends: base\nmodel:\n  parameters:\n    effort: xhigh\n")
	write(t, dir, "leaf.yaml", "version: \"1\"\nname: leaf\nextends: ./mid.yaml\ntargets:\n  - claude\n")

	got, _, err := Load(filepath.Join(dir, "leaf.yaml"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Map["model"].Map["type"].Str != "anthropic" {
		t.Error("grandparent's model.type was lost")
	}
	if got.Map["model"].Map["parameters"].Map["effort"].Str != "xhigh" {
		t.Error("parent's override was lost")
	}
	if got.Map["name"].Str != "leaf" {
		t.Errorf("name = %q, want leaf — the leaf's own name must win", got.Map["name"].Str)
	}
}

func TestLoadRefusesACycleAndNamesIt(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.yaml", "version: \"1\"\nname: a\nextends: b\n")
	write(t, dir, "b.yaml", "version: \"1\"\nname: b\nextends: a\n")
	_, _, err := Load(filepath.Join(dir, "a.yaml"))
	if err == nil {
		t.Fatal("cycle accepted")
	}
	if !strings.Contains(err.Error(), "a.yaml") {
		t.Errorf("error %q does not name the file that closes the cycle", err)
	}
}

func TestLoadRefusesAParentOutsideTheManifestDirectory(t *testing.T) {
	// --from once skipped ValidName and became a path traversal that copied the
	// user's real ~/.claude into a profile. `extends` turns manifest text into a
	// path the same way and gets the same guard.
	dir := t.TempDir()
	write(t, dir, "leaf.yaml", "version: \"1\"\nname: leaf\nextends: ../../etc/passwd\n")
	if _, _, err := Load(filepath.Join(dir, "leaf.yaml")); err == nil {
		t.Fatal("`extends: ../../etc/passwd` accepted")
	}
	write(t, dir, "leaf2.yaml", "version: \"1\"\nname: leaf\nextends: ../base\n")
	if _, _, err := Load(filepath.Join(dir, "leaf2.yaml")); err == nil {
		t.Fatal("a bare name escaping via .. was accepted")
	}
}
```

- [ ] **Step 2: Run tests, verify they fail**
  Run: `make test-one T=TestLoad P=./pkg/schema/`
  Expected: FAIL — undefined: `Load`

- [ ] **Step 3: Implement**

```go
package schema

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ackstorm/agent-profile/pkg/agentreg"
)

// Load reads a manifest and folds its extends chain, parent first. It returns
// the composed tree and the runtime names the LEAF document declared for
// itself — §7.2 warns about a locally declared non-target block, while §7.1
// ignores an inherited one silently, and only the leaf's own keys tell them
// apart.
//
// Resolution is local and deterministic (§5.2): a bare name is
// <manifest dir>/<name>.yaml, a relative path is joined to the manifest
// directory, and nothing else is searched. There is no GitHub lookup, no
// registry and no configured search path.
func Load(path string) (*Node, []string, error) {
	return load(path, nil)
}

func load(path string, seen []string) (*Node, []string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, nil, err
	}
	for _, s := range seen {
		if s == abs {
			return nil, nil, fmt.Errorf("%s: extends cycle: %s is already in the chain",
				filepath.Base(path), filepath.Base(abs))
		}
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return nil, nil, err
	}
	doc, err := ParseYAML(b)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", filepath.Base(abs), err)
	}
	leafRuntimes := runtimeNames(doc)

	ex, ok := doc.Map["extends"]
	if !ok {
		return doc, leafRuntimes, nil
	}
	parentPath, err := resolveExtends(filepath.Dir(abs), ex)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", filepath.Base(abs), err)
	}
	parent, _, err := load(parentPath, append(seen, abs))
	if err != nil {
		return nil, nil, err
	}
	// The child's own extends key is not part of the effective profile: it has
	// been consumed. Removing it here keeps render honest.
	delete(doc.Map, "extends")
	doc.Keys = removeKey(doc.Keys, "extends")

	composed, err := Merge(parent, doc, V1Schema())
	if err != nil {
		return nil, nil, err
	}
	return composed, leafRuntimes, nil
}

// resolveExtends turns manifest text into a path. Every branch stays inside the
// manifest directory: a bare name goes through agentreg.ValidName, and a
// relative path is rejected if it climbs out. `extends` is user input becoming
// a path, which is the class of thing --from got wrong.
func resolveExtends(dir string, ex *Node) (string, error) {
	name, err := ex.Text()
	if err != nil {
		return "", fmt.Errorf("extends: %w", err)
	}
	if strings.ContainsRune(name, '/') || strings.HasSuffix(name, ".yaml") {
		p := filepath.Join(dir, filepath.Clean(name))
		rel, err := filepath.Rel(dir, p)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("extends %q: a parent must live in the manifest directory", name)
		}
		return p, nil
	}
	if err := agentreg.ValidName(name); err != nil {
		return "", fmt.Errorf("extends %q: %w", name, err)
	}
	return filepath.Join(dir, name+".yaml"), nil
}

func runtimeNames(doc *Node) []string {
	r, ok := doc.Map["runtimes"]
	if !ok || r.Kind != Mapping {
		return nil
	}
	return slices.Clone(r.Keys)
}

func removeKey(keys []string, k string) []string {
	if i := slices.Index(keys, k); i >= 0 {
		return slices.Delete(keys, i, i+1)
	}
	return keys
}
```

Add the `write` helper to the test file:

```go
func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 4: Run tests, verify they pass**
  Run: `make test-one T=TestLoad P=./pkg/schema/`
  Expected: PASS

- [ ] **Step 5: Mutation-test the traversal guard**
  Delete the `filepath.Rel` check and the `agentreg.ValidName` call from
  `resolveExtends`. Run
  `make test-one T=TestLoadRefusesAParentOutsideTheManifestDirectory P=./pkg/schema/`.
  Expected: FAIL on both sub-cases. Restore.

- [ ] **Step 6: Add extends to the fuzz surface**
  Add `resolveExtends` to `pkg/schema/fuzz_test.go` as `FuzzResolveExtends`,
  asserting the returned path never escapes the given directory, and add it to
  the `_fuzz` target.

- [ ] **Step 7: Commit**

```bash
git add pkg/schema/extends.go pkg/schema/extends_test.go pkg/schema/fuzz_test.go Makefile
git commit -m "feat(decl): fold extends chains, refuse cycles and traversal"
```

---

### Task 9: Typed profile and schema validation

§36's Profile, Composition, Resources, Model, Prompt and Skills validation
sections, minus everything Phases 2–6 own (input resolution, source resolution,
contract checks).

**Files:**
- Create: `pkg/schema/profile.go`
- Test: `pkg/schema/profile_test.go`

**Interfaces:**
- Consumes: `Node` and its typed accessors (Task 1).
- Produces: `Profile`, `Model`, `Prompt`, `Resource`, `MCP`, `Transport`,
  `Artifact`, `Marketplace`, `Runtime`, `Inputs`, `HeaderValue`,
  `Decode(n *Node) (Profile, error)`. Tasks 11 and 12 consume `Profile`.

- [ ] **Step 1: Write failing tests**

```go
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
```

- [ ] **Step 2: Run tests, verify they fail**
  Run: `make test-one T=TestDecode P=./pkg/schema/`
  Expected: FAIL — undefined: `Decode`

- [ ] **Step 3: Implement**

Write the types and a decoder built on one helper, `onlyKeys(n, "a", "b", ...)`,
copied from `internal/manifest/manifest.go` — it is the mechanism that makes an
unknown key an error naming its own line.

```go
// Profile is one composed manifest, decoded and shape-checked. It is NOT yet
// runtime-specific: Effective produces that.
type Profile struct {
	Version      string
	Name         string
	Targets      []string
	Model        *Model
	Prompt       *Prompt
	Inputs       Inputs
	Marketplaces map[string]Marketplace
	Skills       map[string]Resource
	MCPs         map[string]MCP
	Artifacts    map[string]Artifact
	Runtimes     map[string]Runtime
}

// IsBase reports whether this profile can only be inherited from. §5.3: a base
// is a profile without targets, and there is no abstract flag.
func (p Profile) IsBase() bool { return len(p.Targets) == 0 }

// Resource is a named common resource with an exclusive locator group.
// Enabled defaults to true (§4) and is a pointer only so `enabled: false` on a
// resource that declares nothing else is distinguishable from an absent key.
type Resource struct {
	Enabled bool
	Ref     string  // <item>@<marketplace>, §22
	Source  *Source // §16
}

type Source struct {
	Git   *GitSource
	Local *LocalSource
}

type GitSource struct {
	URL, Ref, Subpath string
	Auth              *ValueFrom
}

type LocalSource struct{ Path, Subpath string }

type Model struct {
	Type, BaseURL, Model string
	Auth                 *Auth
	Headers              map[string]HeaderValue
	Parameters           map[string]string // lexemes, emitted verbatim
}

type Auth struct {
	Type      string // v1: "bearer"
	ValueFrom ValueFrom
}

// HeaderValue is §14: exactly one of Value or ValueFrom, and Prefix is valid
// only with ValueFrom.
type HeaderValue struct {
	Value     string
	Prefix    string
	ValueFrom *ValueFrom
}

type ValueFrom struct{ Secret, Variable string }

type Prompt struct {
	Mode    string // "append" (default) or "replace"
	Content string
	Source  *Source
}

type MCP struct {
	Enabled   bool
	Transport Transport
}

type Transport struct {
	Type    string // "http" or "stdio"
	URL     string
	Headers map[string]HeaderValue
	Command string
	Args    []string
}

type Artifact struct {
	Enabled     bool
	Source      *Source
	Destination string
}

type Marketplace struct {
	Enabled bool
	Type    string // "plugins" or "skills"
	Source  *Source
}

// Runtime holds one runtime's overlay. Common-vocabulary keys overlay the root
// in Effective; the rest are runtime-native and stay here.
type Runtime struct {
	Skills       map[string]Resource
	MCPs         map[string]MCP
	Artifacts    map[string]Artifact
	Marketplaces map[string]Marketplace
	Model        *Model
	Prompt       *Prompt
	Inputs       Inputs
	Plugins      map[string]*Node // adapter-owned, §24 — kept as a tree for Phase 6
	Environment  map[string]string
	Variants     map[string][]string
}

type Inputs struct {
	Variables map[string]Binding
	Secrets   map[string]Binding
}

// Binding is §13: exactly one of Env or File.
type Binding struct{ Env, File string }
```

Validation rules this task must enforce, each with its own error naming the
offending line:

- `version` is required and equals `"1"`.
- `name` is required and passes `agentreg.ValidName`, or is `agentreg.Default`.
- unknown key at any level → error naming the key and its line.
- `model.headers` must not contain `Authorization` (§9).
- `model.auth.type` is `bearer` and carries `value_from` (§9).
- `prompt`: exactly one of `content` | `source`; `mode` ∈ {`append`, `replace`},
  default `append` (§10).
- each header value: exactly one of `value` | `value_from`; `prefix` only with
  `value_from` (§14).
- MCP `transport.headers.Authorization` must use `value_from` (§14).
- `transport.type` ∈ {`http`, `stdio`}; `http` requires `url` and forbids
  `command`/`args`; `stdio` requires `command` and forbids `url`/`headers`.
- each enabled `skills` entry: exactly one of `ref` | `source` (§22).
- each enabled `artifacts` entry: `source` and a `destination` that is relative
  and does not escape via `..` (§26.1).
- each `marketplaces` entry: `type` ∈ {`plugins`, `skills`} and a `source` (§21).
- each `inputs.*` binding: exactly one of `env` | `file` (§13).
- `targets` names known agents (`agent.Names()`).

- [ ] **Step 4: Run tests, verify they pass**
  Run: `make test-one T=TestDecode P=./pkg/schema/`
  Expected: PASS (all four)

- [ ] **Step 5: Commit**

```bash
git add pkg/schema/profile.go pkg/schema/profile_test.go
git commit -m "feat(decl): typed profile with §36 schema validation"
```

---

### Task 10: Destination and subpath path rules

§20 and §26.1 both say the same thing: relative, and must not escape via `..`.
This is the same class of bug `--from` had, so it gets its own guard and its own
fuzz target rather than an inline check nobody can mutation-test.

**Files:**
- Modify: `pkg/schema/profile.go`
- Modify: `pkg/schema/fuzz_test.go`
- Test: `pkg/schema/profile_test.go`

**Interfaces:**
- Produces: `validRelPath(field, p string) error`. Task 9's decoder calls it for
  `subpath` and `destination`; Phases 3, 4 and 6 call it too.

- [ ] **Step 1: Write failing test**

```go
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
```

- [ ] **Step 2: Run test, verify it fails**
  Run: `make test-one T=TestSubpathAndDestination P=./pkg/schema/`
  Expected: FAIL — undefined: `validRelPath`

- [ ] **Step 3: Implement**

```go
// validRelPath enforces the one path rule §20 and §26.1 share: relative, and it
// may not escape its root with "..". Checked on the CLEANED path, because
// "a/../../b" is only visibly an escape after cleaning — and an escape here
// would write outside a profile namespace, which is the whole containment
// boundary.
func validRelPath(field, p string) error {
	if p == "" {
		return fmt.Errorf("%s: must not be empty", field)
	}
	if filepath.IsAbs(p) {
		return fmt.Errorf("%s %q: must be relative", field, p)
	}
	c := filepath.Clean(p)
	if c == ".." || strings.HasPrefix(c, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s %q: must not escape its root with %q", field, p, "..")
	}
	return nil
}
```

- [ ] **Step 4: Run test, verify it passes**
  Run: `make test-one T=TestSubpathAndDestination P=./pkg/schema/`
  Expected: PASS

- [ ] **Step 5: Add the fuzz target and mutation-test the guard**

```go
func FuzzValidRelPath(f *testing.F) {
	f.Add("a/b")
	f.Add("../x")
	f.Fuzz(func(t *testing.T, p string) {
		if err := validRelPath("destination", p); err != nil {
			return
		}
		// Anything accepted must stay under the root when joined to it.
		root := "/profile"
		got := filepath.Join(root, p)
		if !strings.HasPrefix(got, root+string(filepath.Separator)) && got != root {
			t.Fatalf("accepted %q escapes: %q", p, got)
		}
	})
}
```

Then delete the `..` prefix check, run `make test-one
T=TestSubpathAndDestinationMayNotEscapeTheirRoot P=./pkg/schema/`, confirm
FAIL, and restore. Add `FuzzValidRelPath` to the `_fuzz` target.

- [ ] **Step 6: Commit**

```bash
git add pkg/schema/profile.go pkg/schema/profile_test.go pkg/schema/fuzz_test.go Makefile
git commit -m "feat(decl): one guard for subpath and destination containment"
```

---

### Task 11: Runtime selection and the effective profile

§7, §30 and §31: select a runtime, overlay its common-vocabulary keys onto the
root, keep its native keys, evaluate `enabled`, and produce §7.1/§7.2's warnings.

**Files:**
- Create: `pkg/schema/effective.go`
- Test: `pkg/schema/effective_test.go`

**Interfaces:**
- Consumes: `Load` (Task 8), `Merge` (Tasks 5–7), `Decode` (Task 9).
- Produces: `type Warning struct{ Text string }`,
  `Effective(path, runtime string) (Profile, []Warning, error)`. Task 12 and
  every later phase consume it.

- [ ] **Step 1: Write failing tests**

```go
func TestEffectiveOverlaysTheRuntimeBlockOntoTheCommonVocabulary(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "p.yaml", `version: "1"
name: p
targets:
  - opencode
skills:
  ponytail:
    source:
      local:
        path: ./ponytail
runtimes:
  opencode:
    skills:
      ponytail:
        enabled: false
    variants:
      brainstorm:
        args:
          - "/superpowers:brainstorming {}"
`)
	p, warns, err := Effective(filepath.Join(dir, "p.yaml"), "opencode")
	if err != nil {
		t.Fatalf("effective: %v", err)
	}
	// §4: the resource stays visible for diagnostics, but disabled.
	s, ok := p.Skills["ponytail"]
	if !ok {
		t.Fatal("disabled resource dropped; §4 keeps it for render and explain")
	}
	if s.Enabled {
		t.Error("runtime overlay did not disable the skill")
	}
	if s.Source == nil {
		t.Error("the inherited locator was lost by an enabled-only overlay")
	}
	// Runtime-native keys stay under the runtime.
	if got := p.Runtimes["opencode"].Variants["brainstorm"]; len(got) != 1 {
		t.Errorf("variants = %v, want one arg", got)
	}
	if len(warns) != 0 {
		t.Errorf("unexpected warnings: %v", warns)
	}
}

func TestEffectiveIgnoresAnInheritedNonTargetBlockAndWarnsOnALocalOne(t *testing.T) {
	dir := t.TempDir()
	// §7.1: inherited, ignored SILENTLY.
	write(t, dir, "base.yaml", "version: \"1\"\nname: base\nruntimes:\n  opencode:\n    environment:\n      A: b\n")
	write(t, dir, "leaf.yaml", "version: \"1\"\nname: leaf\nextends: base\ntargets:\n  - claude\n")
	if _, warns, err := Effective(filepath.Join(dir, "leaf.yaml"), "claude"); err != nil || len(warns) != 0 {
		t.Errorf("warns = %v, err = %v; an inherited non-target block is ignored silently", warns, err)
	}
	// §7.2: locally declared, WARNS.
	write(t, dir, "local.yaml", "version: \"1\"\nname: local\ntargets:\n  - claude\nruntimes:\n  opencode:\n    environment:\n      A: b\n")
	_, warns, err := Effective(filepath.Join(dir, "local.yaml"), "claude")
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 1 || !strings.Contains(warns[0].Text, "opencode") {
		t.Errorf("warns = %v; a locally declared non-target block must warn and name the runtime", warns)
	}
}

func TestEffectiveRefusesABaseProfileAndNamesWhy(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "base.yaml", "version: \"1\"\nname: coding-base\n")
	_, _, err := Effective(filepath.Join(dir, "base.yaml"), "claude")
	if err == nil || !strings.Contains(err.Error(), "base profile") {
		t.Errorf("err = %v; §5.3 requires the message to say it is a base profile", err)
	}
}
```

- [ ] **Step 2: Run tests, verify they fail**
  Run: `make test-one T=TestEffective P=./pkg/schema/`
  Expected: FAIL — undefined: `Effective`

- [ ] **Step 3: Implement**

```go
package schema

// Warning is a degradation notice. §8 forbids silent drops, so everything this
// package schemaines to act on comes back here and the caller prints it. --strict
// (Phase 2) promotes these to errors.
type Warning struct{ Text string }

// Effective runs §31's lifecycle up to the point where inputs are computed:
// load, fold extends, select the runtime, overlay it, validate, evaluate
// enabled. Phase 2 continues from here with inputs and preflight.
func Effective(path, runtime string) (Profile, []Warning, error) {
	tree, leafRuntimes, err := Load(path)
	if err != nil {
		return Profile{}, nil, err
	}
	p, err := Decode(tree)
	if err != nil {
		return Profile{}, nil, err
	}
	// §5.3: a base participates in inheritance but cannot be applied.
	if p.IsBase() {
		return Profile{}, nil, fmt.Errorf("profile %q declares no targets — it is a base profile", p.Name)
	}
	if !slices.Contains(p.Targets, runtime) {
		return Profile{}, nil, fmt.Errorf("profile %q does not target %q; targets are %v", p.Name, runtime, p.Targets)
	}

	var warns []Warning
	// §7.1 vs §7.2: an inherited non-target block is ignored silently; one the
	// LEAF declared warns. leafRuntimes is the only thing that tells them apart.
	for _, r := range leafRuntimes {
		if !slices.Contains(p.Targets, r) {
			warns = append(warns, Warning{fmt.Sprintf(
				"runtimes.%s is declared but %q is not a target of profile %q", r, r, p.Name)})
		}
	}

	// The runtime overlay is applied on the TREE, not on the typed struct, so
	// it goes through exactly the same three rules — including the exclusive
	// groups that let `enabled: false` leave a locator alone.
	if rt, ok := tree.Map["runtimes"]; ok {
		if block, ok := rt.Map[runtime]; ok {
			overlay := commonKeysOnly(block)
			merged, err := Merge(tree, overlay, V1Schema())
			if err != nil {
				return Profile{}, nil, err
			}
			if p, err = Decode(merged); err != nil {
				return Profile{}, nil, err
			}
		}
	}
	// Every runtime block other than the selected one is dropped from the
	// effective profile: it is runtime-specific and this profile now has a
	// runtime.
	p.Runtimes = map[string]Runtime{runtime: p.Runtimes[runtime]}
	return p, warns, nil
}

// commonKeysOnly lifts a runtime block's common-vocabulary keys to the root, so
// runtimes.<r>.skills overlays skills. Runtime-native keys — plugins, variants,
// environment — are left where they are; they have no root counterpart and
// lifting them would invent one.
func commonKeysOnly(block *Node) *Node {
	out := &Node{Kind: Mapping, Line: block.Line, Map: map[string]*Node{}, KeyLine: map[string]int{}}
	for _, k := range []string{"model", "prompt", "inputs", "marketplaces", "skills", "mcps", "artifacts"} {
		if v, ok := block.Map[k]; ok {
			out.Keys = append(out.Keys, k)
			out.Map[k] = v
			out.KeyLine[k] = block.KeyLine[k]
		}
	}
	return out
}
```

- [ ] **Step 4: Run tests, verify they pass**
  Run: `make test-one T=TestEffective P=./pkg/schema/`
  Expected: PASS (all three)

- [ ] **Step 5: Commit**

```bash
git add pkg/schema/effective.go pkg/schema/effective_test.go
git commit -m "feat(decl): runtime overlay, base refusal and §7 warnings"
```

---

### Task 12: `ap manifest render`

§31: expose the effective profile without materializing it, with sensitive
values redacted. With **no** `--target` it composes every target the manifest
declares and says nothing on success — that is the contract check `ach` and
`ach-agent` run against a manifest they generated, and the exit code is the whole
interface. There is deliberately no separate `validate` command: it would be the
same pipeline behind a second name.

**Files:**
- Create: `pkg/schema/render.go`
- Create: `cmd/ap/manifest.go`
- Modify: `cmd/ap/main.go` (dispatch table and usage text)
- Test: `pkg/schema/render_test.go`, `cmd/ap/main_test.go`

**Interfaces:**
- Consumes: `Effective`, `Profile` (Task 11).
- Produces: `Render(p Profile) []byte`, `Targets(path string) ([]string, error)`;
  the `manifest` dispatch case with its `render` subcommand.

- [ ] **Step 1: Write failing tests**

```go
// pkg/schema/render_test.go
func TestRenderNeverEmitsASecretBinding(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "p.yaml", `version: "1"
name: p
targets:
  - claude
inputs:
  secrets:
    llm-token:
      env: LITELLM_TOKEN
model:
  type: anthropic
  auth:
    type: bearer
    value_from:
      secret: llm-token
`)
	t.Setenv("LITELLM_TOKEN", "sk-do-not-print-me")
	p, _, err := Effective(filepath.Join(dir, "p.yaml"), "claude")
	if err != nil {
		t.Fatal(err)
	}
	out := string(Render(p))
	if strings.Contains(out, "sk-do-not-print-me") {
		t.Fatal("render emitted a secret VALUE")
	}
	// The binding name is structure, not a secret, and is what makes render
	// useful for debugging. The variable NAME is the reference §34 materializes.
	if !strings.Contains(out, "llm-token") {
		t.Error("render dropped the secret's logical name; §31 asks for the effective profile")
	}
}

func TestRenderIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "p.yaml", "version: \"1\"\nname: p\ntargets:\n  - claude\nskills:\n  b:\n    ref: b@m\n  a:\n    ref: a@m\n")
	p, _, _ := Effective(filepath.Join(dir, "p.yaml"), "claude")
	first := string(Render(p))
	for i := 0; i < 20; i++ {
		if got := string(Render(p)); got != first {
			t.Fatalf("render is not deterministic:\n%s\n---\n%s", first, got)
		}
	}
	if strings.Index(first, "a:") > strings.Index(first, "b:") {
		t.Error("collection keys are not sorted; map order leaked into the output")
	}
}
```

```go
// cmd/ap/main_test.go
func TestDispatchRenderWithoutATargetValidatesEveryTarget(t *testing.T) {
	// No --target is not an error: it means "check them all", which is the
	// contract check ach and ach-agent run. The exit code is the whole answer,
	// so a broken second target must fail even though the first composes.
	dir := t.TempDir()
	write(t, dir, "ok.yaml", "version: \"1\"\nname: p\ntargets:\n  - claude\n  - codex\n")
	if code, _, _ := runAP(t, "manifest", "render", filepath.Join(dir, "ok.yaml")); code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	// codex is not a declared target, so composing for it must fail.
	write(t, dir, "bad.yaml", "version: \"1\"\nname: p\ntargets:\n  - claude\nskills:\n  x: {}\n")
	code, _, stderr := runAP(t, "manifest", "render", filepath.Join(dir, "bad.yaml"))
	if code == 0 {
		t.Fatal("a manifest with a locator-less enabled resource validated clean")
	}
	if !strings.Contains(stderr, "x") {
		t.Errorf("stderr %q does not name the offending resource", stderr)
	}
}
```

- [ ] **Step 2: Run tests, verify they fail**
  Run: `make test-one T='TestRender|TestDispatchRender' P='./pkg/schema/ ./cmd/ap/'`
  Expected: FAIL — undefined: `Render`; unknown command `render`.

- [ ] **Step 3: Implement**

`Render` walks `Profile` and emits the subset's own YAML: two-space indent,
collection keys sorted, block scalars for multi-line prompt content, numbers as
their stored lexemes. It emits `value_from.secret: <name>` and never reads a
binding's value — Phase 2 owns resolution, and render must work before it.

`cmd/ap/manifest.go`:

```go
//go:build unix

package main

// manifestRender prints the effective profile for one runtime and mutates nothing.
// It parses its own flags with parseAroundRef, like create and unlike run:
// there is no passthrough here, so `ap manifest render ./p.yaml --target claude`
// is unambiguous. Do not give this treatment to run.
//
// It lives under "manifest" because its subject is a FILE. Every other ap
// command takes a reference first; this one would be the only exception, and
// the group states that rather than leaving it to be discovered.
func manifestRender(args []string) int {
	fs := flag.NewFlagSet("manifest render", flag.ContinueOnError)
	target := fs.String("target", "", "runtime to render for")
	path, rest, err := parseAroundRef(fs, args)
	...
}
```

With no `--target`, `render` validates instead of printing: run `Effective` for
every target the profile declares, print warnings to stderr, exit 0 on success
and 1 on the first error. Running it for *every* target is what makes it a
contract check — a manifest that composes for claude and not for codex is broken,
and the producer needs to hear that in one call.

```go
func renderAll(fs *flag.FlagSet, args []string) int {
	path, _, err := parseAroundRef(fs, args)
	if err != nil {
		return usage(err)
	}
	targets, err := schema.Targets(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	for _, t := range targets {
		_, warns, err := schema.Effective(path, t)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", t, err)
			return 1
		}
		for _, w := range warns {
			fmt.Fprintf(os.Stderr, "warning: %s: %s\n", t, w.Text)
		}
	}
	return 0
}
```

`schema.Targets(path string) ([]string, error)` loads and decodes far enough to
read `targets`, and is exported because `ach` needs it for the same reason.

Register `manifest` in `main.go`'s dispatch and usage text.

- [ ] **Step 4: Run tests, verify they pass**
  Run: `make test-one T='TestRender|TestDispatchRender' P='./pkg/schema/ ./cmd/ap/'`
  Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/schema/render.go pkg/schema/render_test.go cmd/ap/manifest.go cmd/ap/main.go cmd/ap/main_test.go
git commit -m "feat(ap): ap manifest render, redacted and deterministic"
```

---

### Task 13: Spec examples as end-to-end fixtures, and the full gate

The spec's §35 full example is the acceptance test for this phase: it must
parse, compose and render for both of its targets.

**Files:**
- Create: `pkg/schema/testdata/spec-35-execute.yaml` (copied verbatim from
  SPEC §35), `pkg/schema/testdata/spec-35-coding-base.yaml` (a minimal parent
  so `extends: coding-base` resolves)
- Test: `pkg/schema/effective_test.go`
- Modify: `docs/references/` — add `DECLARATIVE.md` recording what the subset
  admits, why `transport` is not a marked union, and where the phases live
- Modify: `CLAUDE.md` — add the `pkg/schema` row to the MANDATORY reading
  table and record the standing rules this phase establishes

- [ ] **Step 1: Write the acceptance test**

```go
func TestTheSpecsFullExampleComposesForBothOfItsTargets(t *testing.T) {
	for _, rt := range []string{"claude", "opencode"} {
		t.Run(rt, func(t *testing.T) {
			p, warns, err := Effective("testdata/spec-35-execute.yaml", rt)
			if err != nil {
				t.Fatalf("effective: %v", err)
			}
			for _, w := range warns {
				t.Logf("warning: %s", w.Text)
			}
			if p.Name != "execute" {
				t.Errorf("name = %q", p.Name)
			}
			if _, ok := p.Skills["pdf"]; !ok {
				t.Error("marketplace-backed skill missing")
			}
			// §35's closing note: for opencode company-review is inactive, so
			// gitlab-token is not required. Phase 2 asserts the requirement
			// calculation; here we assert the state it reads.
			cr := p.Skills["company-review"]
			if want := rt != "opencode"; cr.Enabled != want {
				t.Errorf("company-review enabled = %v, want %v for %s", cr.Enabled, want, rt)
			}
		})
	}
}
```

- [ ] **Step 2: Run it, verify it fails**
  Run: `make test-one T=TestTheSpecsFullExample P=./pkg/schema/`
  Expected: FAIL — testdata missing.

- [ ] **Step 3: Add the fixtures**
  Copy SPEC §35's YAML verbatim into `spec-35-execute.yaml`. Write
  `spec-35-coding-base.yaml` as `version: "1"` plus `name: coding-base` and
  nothing else — the spec does not give the parent's body, and inventing one
  would make this test assert something the spec does not say.

- [ ] **Step 4: Run it, verify it passes**
  Run: `make test-one T=TestTheSpecsFullExample P=./pkg/schema/`
  Expected: PASS for both subtests

- [ ] **Step 5: Run the full gate**
  Run: `make verify` — Expected: fmt-check, shellcheck, vet, lint, test (race +
  shuffle), vulncheck all pass.
  Run: `make secrets` — Expected: gitleaks clean over full history.
  Run: `make fuzz` — Expected: 30s per target, no crashers.
  Run: `make sandbox` — Expected: pass; Phase 1 adds no materialization, so this
  must be unchanged from before the phase.
  Run: `make smoke` — Expected: pass, unchanged.
  If any command fails, fix it before the commit. Do not report the phase done
  with a red gate; quote the failing output instead.

- [ ] **Step 6: Write the reference doc and update CLAUDE.md**
  `docs/references/DECLARATIVE.md` records: the subset's admitted and rejected
  constructs with the reason for each; why the merge engine may not name a
  resource type; why `mcps.*.transport` is deliberately not a marked union; the
  `Quoted` flag's role in telling `false` from `"false"`; and the phase map with
  a pointer to the roadmap. `CLAUDE.md` gains the row
  `pkg/schema/*, composition, the YAML subset → docs/references/DECLARATIVE.md`.

- [ ] **Step 7: Commit**

```bash
git add pkg/schema/testdata pkg/schema/effective_test.go docs/references/DECLARATIVE.md CLAUDE.md
git commit -m "test(decl): the spec's full example composes for both targets"
```

---

## Self-Review

**Spec coverage for Phase 1's declared scope:** §2 (Task 9), §3.1–3.4 (Task 5),
§3.5.1 (Task 6), §3.5.2 (Task 6), §3.6 (nothing to build — no operators exist),
§3.7 (Tasks 5 and 7), §4 (Tasks 9 and 11), §5.1–5.3 (Tasks 8, 9, 11), §6 (Task 9
accepts `name: default` via `agentreg.Default`; the namespace behaviour is Phase 7),
§7.1–7.2 (Task 11), §20 and §26.1 path rules (Task 10), §31 (Tasks 11, 12), §36's
Profile/Composition/Resources/Model/Prompt sections (Task 9).

**Reframe coverage:** `pkg/schema` is exported and import-boundary-tested
(Task 0); `pkg/agentreg` is the single copy of the agent table (Task 0);
`GOOS=windows` builds (Task 0 step 6); `ap manifest render` is the cross-repository
contract check (Task 12). `ap manifest schema` (deferred) is not in v1 at all;
`ap manifest apply --manifest -` are Phase 2 — they need the input model, which does not
exist yet.

**Blocked on decisions outside this phase, and unaffected by them:** the six spec
deltas in the roadmap's §0.1 and the vocabulary question in §0.2 all land in
Phases 3, 5 and 6. Phase 1 builds the parser, the composition engine and the
validation frame, none of which depends on which resource types exist or where
their content comes from. Adding a resource type later is a row in `V1Schema`
and a field on `Profile`.

**Known gaps, deliberately deferred, each with its owning phase:** §8 adapter
degradation and `--strict` (Phase 2 — `Warning` exists here, the promotion does
not); §11–§14 input *resolution* (Phase 2 — the shapes are validated here, no
binding is read); §16–§19 source *fetching* (Phase 3); §21–§22 marketplace item
*resolution* (Phase 6 — the `ref` grammar and type matching are validated here);
§23's `SKILL.md` contract check (Phase 4 — it needs a resolved source);
§24 runtime-native plugin schema (Phase 6 — `Runtime.Plugins` is kept as an
unvalidated tree on purpose, open question §40.1); §32–§34 (Phases 3 and 5).

**Type consistency:** `Node`/`Kind` (Task 1) are used unchanged by Tasks 2, 4–9,
11. `Schema` gains `IsUnion` and `Group` in Task 4 and `IsCollectionEntry` in
Task 7 — Task 7 explicitly restates the signature change. `Merge`'s signature
changes in Task 7 from one return value to two; Task 7's interfaces block names
every call site to update (`mergeAt`, the `mergeYAML` helper, `load`).
`Effective` (Task 11) is the single entry point Task 12 and every later phase
consume.

## Execution Handoff

**Plan complete and saved to
`docs/superpowers/plans/2026-08-26-phase-1-manifest-and-composition.md`.
Two execution options:**

**1. Subagent-Driven (recommended)** — dispatch a fresh subagent per task, review
between tasks, fast iteration.

**2. Inline Execution** — execute tasks in this session with
`superpowers:executing-plans`, batched with review checkpoints.

**Which approach?**


---

## Phase 1 catch-up — three tasks added after v0.6.1

Tasks 1–11 are committed (`742fe9a`). These close the gap between what was
planned in v0.5 terms and what SPEC v0.6.2 requires, and Phase 3 consumes all
three.

**All three are done** (`242fce3`, `59da074`, `7377dbd`), plus one removal the
audit called for: the JSON Schema emitter and `ap schema` are gone (`5c924dc`)
— a second, hand-written description of the manifest contract that nothing
forced to agree with the decoder. Four things the tasks did not anticipate came
out of doing them, each recorded below its task.

### Task 14: `auth.scheme` on the git source

**Files:** Modify `pkg/schema/profile.go`, `pkg/schema/profile_test.go`

**Interfaces:** `GitSource.Auth` changes from `*ValueFrom` to `*GitAuth`.
Phase 3 consumes `GitAuth.Scheme`.

- [x] **Step 1: Write the failing test**

```go
func TestGitAuthSchemeAcceptsOnlyTheTwoV1Values(t *testing.T) {
	n, _ := ParseYAML([]byte("version: \"1\"\nname: x\ntargets:\n  - claude\n" +
		"inputs:\n  secrets:\n    t: {env: T}\n" +
		"skills:\n  s:\n    source:\n      git:\n        url: https://gl/x.git\n" +
		"        auth:\n          scheme: basic-oauth2\n          value_from: {secret: t}\n"))
	p, err := Decode(n)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := p.Skills["s"].Source.Git.Auth.Scheme; got != "basic-oauth2" {
		t.Errorf("scheme = %q", got)
	}
	// Absent is legal: §17.1 infers it from the host at fetch time, which is
	// Phase 3's job. The schema must not invent a default here, or the
	// inference would be unreachable.
	n, _ = ParseYAML([]byte("version: \"1\"\nname: x\ntargets:\n  - claude\n" +
		"inputs:\n  secrets:\n    t: {env: T}\n" +
		"skills:\n  s:\n    source:\n      git:\n        url: https://gl/x.git\n" +
		"        auth:\n          value_from: {secret: t}\n"))
	p, err = Decode(n)
	if err != nil {
		t.Fatalf("decode without scheme: %v", err)
	}
	if got := p.Skills["s"].Source.Git.Auth.Scheme; got != "" {
		t.Errorf("scheme = %q, want empty — the schema must not default it", got)
	}
	// Anything else is refused by name.
	n, _ = ParseYAML([]byte("version: \"1\"\nname: x\ntargets:\n  - claude\n" +
		"inputs:\n  secrets:\n    t: {env: T}\n" +
		"skills:\n  s:\n    source:\n      git:\n        url: https://gl/x.git\n" +
		"        auth:\n          scheme: ntlm\n          value_from: {secret: t}\n"))
	if _, err := Decode(n); err == nil || !strings.Contains(err.Error(), "ntlm") {
		t.Errorf("err = %v; an unknown scheme must be refused by name", err)
	}
}
```

- [x] **Step 2: Run it, verify it fails**
  `make test-one T=TestGitAuthScheme P=./pkg/schema/` — Expected: FAIL, `Auth.Scheme` undefined.

- [x] **Step 3: Implement**

```go
// GitAuth is §17's credential plus §17.1's transport scheme.
//
// Scheme is deliberately NOT defaulted here. Absent means "infer from the host
// at fetch time" (§17.1), and a default written in the decoder would make that
// inference unreachable — and unreportable, which §17.1 requires it to be.
type GitAuth struct {
	// Scheme is "bearer", "basic-oauth2", or "" for inferred.
	Scheme    string
	ValueFrom ValueFrom
}
```

Point `GitSource.Auth` at it, and in the decoder accept `scheme` alongside
`value_from`, refusing anything outside the two values by name.

- [x] **Step 4: Run it, verify it passes.** `make test-one T=TestGitAuthScheme P=./pkg/schema/`
- [x] **Step 5: Commit**

```bash
git add pkg/schema/profile.go pkg/schema/profile_test.go
git commit -m "feat(schema): git auth.scheme, never defaulted in the decoder"
```

### Task 15: the `archive` source branch

**Files:** Modify `pkg/schema/profile.go`, `pkg/schema/schema.go`, `pkg/schema/profile_test.go`

**Interfaces:** `Source` gains `Archive *ArchiveSource`. Phase 3 consumes it.

- [x] **Step 1: Write the failing test**

```go
func TestArchiveSourceRequiresADigest(t *testing.T) {
	yaml := func(extra string) string {
		return "version: \"1\"\nname: x\ntargets:\n  - claude\n" +
			"skills:\n  s:\n    source:\n      archive:\n" +
			"        url: https://ach/c/9f2a/skill.tar.gz\n" + extra
	}
	n, _ := ParseYAML([]byte(yaml("        digest: sha256:" + strings.Repeat("a", 64) + "\n")))
	if _, err := Decode(n); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// §18: a git source is content-addressed and an archive is not, so the
	// digest is its only integrity claim and there is no flag to skip it.
	n, _ = ParseYAML([]byte(yaml("")))
	if _, err := Decode(n); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Errorf("err = %v; an archive without a digest must be refused", err)
	}
	n, _ = ParseYAML([]byte(yaml("        digest: md5:abc\n")))
	if _, err := Decode(n); err == nil {
		t.Error("a non-sha256 digest was accepted")
	}
}

func TestArchiveIsAThirdBranchOfTheSourceUnion(t *testing.T) {
	// §3.5.1: a different branch replaces the union wholesale, and archive is
	// now one of three rather than one of two.
	got := mergeYAML(t,
		"skills:\n  s:\n    source:\n      git:\n        url: https://e/r.git\n",
		"skills:\n  s:\n    source:\n      archive:\n        url: https://e/a.tgz\n"+
			"        digest: sha256:"+strings.Repeat("b", 64)+"\n")
	src := got.Map["skills"].Map["s"].Map["source"]
	if _, ok := src.Map["git"]; ok {
		t.Error("git branch survived a switch to archive")
	}
}
```

- [x] **Step 2: Run it, verify it fails.** Expected: `archive` is an unknown key.
- [x] **Step 3: Implement**

```go
// ArchiveSource is §18. Digest is REQUIRED and is not a checksum bolted on: an
// archive is not content-addressed the way a git SHA is, so the digest is the
// only integrity claim it has, and it participates in cache identity.
type ArchiveSource struct {
	URL     string
	Digest  string // "sha256:<64 hex>", required
	Subpath string
	Auth    *GitAuth
}
```

Add `Archive` to `Source`, extend the union's branch validation to three, and
validate the digest shape: `sha256:` followed by exactly 64 lowercase hex.
`V1Schema` needs no change — `source` is already the marked union node, and
`sameBranch` compares the first key whatever it is.

- [x] **Step 4: Run it, verify it passes**, plus the whole suite: `make test`.
- [x] **Step 5: Commit**

```bash
git add pkg/schema/profile.go pkg/schema/profile_test.go
git commit -m "feat(schema): archive source with a mandatory sha256 digest"
```

### Task 16: regroup `ap render` under `ap manifest`

`render.go` was written before the command surface was settled. `manifest render`
is the shape (`2026-08-26-command-surface.md`): its subject is a **file**, and it
would otherwise be the only command in the tool whose first argument is a path
rather than a reference.

**Files:** Rename `cmd/ap/render.go` → `cmd/ap/manifest.go`; modify
`cmd/ap/main.go`, `cmd/ap/main_test.go`

- [x] **Step 1: Update the dispatch test**

```go
func TestDispatchManifestRendersAndValidates(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "ok.yaml", "version: \"1\"\nname: p\ntargets:\n  - claude\n")
	// No --target: compose every declared target, exit code is the answer.
	if code, _, _ := runAP(t, "manifest", "render", filepath.Join(dir, "ok.yaml")); code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	// The bare verb is gone: the group is the contract other repositories call.
	if code, _, _ := runAP(t, "render", filepath.Join(dir, "ok.yaml")); code == 0 {
		t.Error("bare `ap render` still dispatches; it must be `ap manifest render`")
	}
}
```

- [x] **Step 2: Run it, verify it fails.** Expected: `ap render` still succeeds.
- [x] **Step 3: `git mv cmd/ap/render.go cmd/ap/manifest.go`**, rename the entry
  point to `manifestCmd`, dispatch on `manifest` with `render` as its first
  positional, and remove the `render` case. Update `usage` and `commandHelp`:
  the entry is `manifest  Apply, render or export a whole manifest`, with only
  `render` implemented in Phase 1.
- [x] **Step 4: Run it, verify it passes**, then `make verify`.
- [x] **Step 5: Commit**

```bash
git add cmd/ap/manifest.go cmd/ap/main.go cmd/ap/main_test.go
git commit -m "refactor(ap): group render under ap manifest"
```

### What the catch-up tasks turned up that the plan did not predict

- **The `auth` block's shape was wrong, and the fixture hid it.** The decoder
  read `auth: {secret: x}`; §17 and §35 both write
  `auth: {value_from: {secret: x}}`. `spec-35-execute.yaml` had been written to
  match the code rather than the spec it is named after, so the one fixture
  whose whole job is to be §35's bytes agreed with the bug. Task 14's test
  could not have caught this on its own — it was the fixture going red that
  did. A fixture named after a document must be diffed against that document,
  not against what the parser accepts.

- **A new source family silently opened two holes elsewhere.** `Required` and
  `Render` both switched on `src.Git` and `src.Local` by name, so `archive`
  contributed no required input (§12) and printed nothing at all (§8's silent
  drop, in the output another repository reads). Neither is in Task 15's steps.
  Adding a union branch is not local to the decoder; every exhaustive switch
  over that union is part of the change.

- **`ap render` and `ap validate` shipped before the surface was settled, and
  `validate` was never a command.** Its whole job — compose every declared
  target, exit status is the answer — is `manifest render` with no `--target`.
  Two names for one pipeline is what §35.1 says a reference implementation's
  spelling must not become.

- **A blind line-range edit deleted `env` and `which`'s help entries.** They sat
  between `render` and `delete` in `commandHelp`, and replacing that range by
  line number took them with it. `TestEachCommandHasItsOwnHelp` caught it; no
  compiler could have. Anchor an edit on the text it means to replace, never on
  the lines it currently occupies.
