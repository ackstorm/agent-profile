# Runtime-native plugins Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: `superpowers:subagent-driven-development`
> (recommended) or `superpowers:executing-plans` to implement this plan task-by-task.
> Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `runtimes.<rt>.plugins.<name>.package` materialize for real, and
override the same-named common plugin for that runtime, closing SPEC §24.3 and
the half of §40.1 that two runtimes can answer today.

**Architecture:** ap writes the runtime's own package DECLARATION into the
runtime's own settings file and records the element it added; it never runs the
runtime's installer. A native entry for a name suppresses the common plugin of
that name for that runtime, which is what makes the override expressible. Two
runtimes have a native package list (pi, opencode); the other two are an honest
§8 degradation rather than an invented mechanism.

**Tech Stack:** Go 1.25, standard library only, Unix. Everything runs through
`scripts/dev.sh` inside `Dockerfile.devtools`.

## Global Constraints

- **Standard library only.** No new dependencies, ever.
- **Never run the toolchain on the host.** `make test`, `make verify` and every
  Go command wrap themselves into the devtools container.
- **ap does not run other people's installers.** `ap sync` was removed for this
  reason. ap writes a declaration and NAMES the reconcile command; the user runs
  it. Precedent: the `codex plugin add` warning in `internal/profile/clone.go`.
- **§8: nothing is dropped silently.** A declared thing ap cannot act on
  produces a warning naming the runtime and the resource.
- **A merged record is bounded by its recorded KEYS, never by a hash.** See
  `CLAUDE.md`, "The ledger is the only state". A whole-container key must never
  be recorded, or uninstall takes the user's entries with ours.
- **Every guard is mutation-tested.** Revert it, run its test, confirm the test
  fails, restore. A green test that passes with the guard removed is worse than
  no test.
- **The registry describes other people's software.** Every field is verified by
  running the real binary. The measurements this plan rests on are in Task 0.

---

## Task 0: The measurements this plan rests on

Already taken, on codex-cli 0.149.1 and the pi build on this machine. Recorded
here because every later task depends on them and none of them may be assumed.

- `pi install git:github.com/DietrichGebert/ponytail` does four things: clones
  into `<PI_CODING_AGENT_DIR>/git/github.com/DietrichGebert/ponytail`, writes
  `<dir>/git/.gitignore` containing `*` and `!.gitignore`, appends the source
  string to `settings.json` under `packages`, and runs `npm install` in the
  clone.
- `settings.json` after that install is exactly:
  ```json
  {
    "packages": [
      "git:github.com/DietrichGebert/ponytail"
    ]
  }
  ```
- **`pi update <source>` reconciles a declaration with nothing on disk.** With a
  `settings.json` holding only the `packages` array and no `git/` directory at
  all, `pi update git:github.com/DietrichGebert/ponytail` cloned it and reported
  `Updated`. `pi list` shows the package from the declaration alone, before any
  clone exists.
- opencode's native form is an npm package name in `opencode.json` under
  `plugin` (SPEC §24.3's own example: `package: "@dietrichgebert/ponytail"`).
- `pkg/agentreg`: pi's settings file is `settings.json`, opencode's is
  `opencode.json`, both `JSON`.

**The fork this closes, stated:** ap could instead clone the tree itself. It
must not. ap cannot run `npm install`, so a package with JavaScript dependencies
would be left half-installed and looking finished — a worse failure than an
honest "run `pi update`". Cloning would also make ap the owner of pi's on-disk
layout, which is someone else's software.

---

## Task 1: Append to a string array without owning it

`MergeInto` deep-merges MAPS and records dotted keys. A package list is an
ARRAY, and `mergeMap` replaces a non-map value whole (`doc[k] = v`) and records
the container key — so uninstalling ap's package would delete the user's too.
That is precisely the failure `mergeMap`'s own comment warns about for
`mcpServers`. Arrays need their own primitive.

**Files:**
- Modify: `pkg/hydrate/merge.go`
- Test: `pkg/hydrate/merge_test.go`

**Interfaces:**
- Consumes: `readDoc`, `encodeDoc`, `writeDoc` from `pkg/hydrate/merge.go`.
- Produces:
  - `func AppendInto(path, key, element string) (added bool, err error)`
  - `func RemoveFrom(path, key, element string) error`

- [ ] **Step 1: Write failing tests**

```go
// A package list is a list, and it is SHARED: the user put entries in it by
// hand and expects them to survive both our install and our uninstall. A whole
// -array write would take them with it, which is why MergeInto is wrong here.
func TestAppendIntoAddsOnceAndLeavesTheUsersEntriesAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"packages":["git:example.com/theirs"],"other":1}`), 0o600); err != nil {
		t.Fatal(err)
	}

	added, err := AppendInto(path, "packages", "git:example.com/ours")
	if err != nil || !added {
		t.Fatalf("AppendInto = %v %v, want true nil", added, err)
	}
	// Idempotent: applying twice must converge, never duplicate.
	added, err = AppendInto(path, "packages", "git:example.com/ours")
	if err != nil || added {
		t.Fatalf("second AppendInto = %v %v, want false nil", added, err)
	}

	var doc map[string]any
	raw, _ := os.ReadFile(path)
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	got := doc["packages"].([]any)
	if len(got) != 2 || got[0] != "git:example.com/theirs" || got[1] != "git:example.com/ours" {
		t.Errorf("packages = %v", got)
	}
	if doc["other"] != float64(1) {
		t.Errorf("an unrelated key was disturbed: %v", doc["other"])
	}

	// And removal takes exactly ours.
	if err := RemoveFrom(path, "packages", "git:example.com/ours"); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(path)
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	got = doc["packages"].([]any)
	if len(got) != 1 || got[0] != "git:example.com/theirs" {
		t.Errorf("removal took the user's entry: %v", got)
	}
}

// An absent file is created, and an emptied array is LEFT in place — the same
// rule MergeOut applies to an emptied container, for the same reason: the user
// may have created it and an empty list costs them nothing.
func TestAppendIntoCreatesTheFileAndRemovalLeavesAnEmptyList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if _, err := AppendInto(path, "packages", "p"); err != nil {
		t.Fatal(err)
	}
	if err := RemoveFrom(path, "packages", "p"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if got, ok := doc["packages"].([]any); !ok || len(got) != 0 {
		t.Errorf("packages = %v, want an empty list left in place", doc["packages"])
	}
}

// A non-list where the list belongs is the user's data. Refuse rather than
// overwrite: readDoc already refuses a file that does not parse, for the same
// reason.
func TestAppendIntoRefusesANonListAtTheKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"packages":"not-a-list"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := AppendInto(path, "packages", "p"); err == nil {
		t.Fatal("a non-list at the key was overwritten")
	}
}
```

- [ ] **Step 2: Run the tests and verify they fail**

Run: `./scripts/dev.sh go test ./pkg/hydrate/ -run 'TestAppendInto' -count=1`
Expected: FAIL — `undefined: AppendInto`

- [ ] **Step 3: Implement both functions**

```go
// AppendInto adds element to the string list at key in the structured document
// at path, creating the file and the list if absent, and reports whether it
// added anything.
//
// MergeInto is deliberately not used here. It descends into MAPS and records
// dotted keys; a list is neither, so mergeMap would replace the whole array and
// record the container key — and uninstall, bounded by recorded keys, would
// then delete every package in the file including the user's. That is the exact
// failure mergeMap's own comment describes for mcpServers.
//
// Idempotent, because apply must converge: an element already present is left
// where it is rather than appended again.
func AppendInto(path, key, element string) (bool, error) {
	isTOML := strings.EqualFold(filepath.Ext(path), ".toml")
	doc, err := readDoc(path, isTOML)
	if err != nil {
		return false, err
	}
	var list []any
	switch v := doc[key].(type) {
	case nil:
		if _, present := doc[key]; present {
			return false, fmt.Errorf("%s: %q is null, not a list", path, key)
		}
	case []any:
		list = v
	default:
		// The user's data, in the place ours goes. Replacing it would destroy
		// work this program did not create.
		return false, fmt.Errorf("%s: %q is not a list", path, key)
	}
	for _, e := range list {
		if s, ok := e.(string); ok && s == element {
			return false, nil
		}
	}
	doc[key] = append(list, element)
	return true, writeDoc(path, doc, isTOML)
}

// RemoveFrom deletes exactly one element from the string list at key, and
// nothing else. It is AppendInto's inverse, and the bound is the same one
// MergeOut applies to keys: a list holding four packages, three of them the
// user's, loses one.
//
// An emptied list is LEFT in place, like an emptied container in MergeOut: the
// user may have created it, and an empty list costs them nothing.
func RemoveFrom(path, key, element string) error {
	isTOML := strings.EqualFold(filepath.Ext(path), ".toml")
	doc, err := readDoc(path, isTOML)
	if err != nil {
		return err
	}
	list, ok := doc[key].([]any)
	if !ok {
		return nil
	}
	out := make([]any, 0, len(list))
	for _, e := range list {
		if s, ok := e.(string); ok && s == element {
			continue
		}
		out = append(out, e)
	}
	doc[key] = out
	return writeDoc(path, doc, isTOML)
}
```

If `readDoc` returns an error for a missing file rather than an empty document,
mirror `MergeInto`'s handling of that case exactly; if `writeDoc` does not exist
under that name, factor the encode-and-write tail of `MergeInto` into one and
call it from both. Do not duplicate the encoding.

- [ ] **Step 4: Run the tests and verify they pass**

Run: `./scripts/dev.sh go test ./pkg/hydrate/ -run 'TestAppendInto' -count=1`
Expected: PASS

- [ ] **Step 5: Mutation-test the bound**

Replace `AppendInto`'s body with `doc[key] = []any{element}` and run
`TestAppendIntoAddsOnceAndLeavesTheUsersEntriesAlone`. Expected: FAIL on
"packages =". Restore.

- [ ] **Step 6: Commit**

```bash
git add pkg/hydrate/merge.go pkg/hydrate/merge_test.go
git commit -m "feat(hydrate): append to a shared string list without owning it"
```

---

## Task 2: Decode a runtime-native plugin

`decodePlugins` keeps each entry as a raw `*Node` because §24 made the schema
adapter-owned. Two runtimes now have a schema, so the entry gets a type.

**Files:**
- Modify: `pkg/schema/profile.go` (`decodePlugins` at :1302, `Runtime` at :151)
- Modify: `pkg/schema/render.go` (:356 emits `r.Plugins[k]` as a raw node)
- Test: `pkg/schema/profile_test.go`, `pkg/schema/render_test.go`

**Interfaces:**
- Produces:
  ```go
  // NativePlugin is a runtime's OWN packaging mechanism (§24.3), not §24's
  // common plugin contract. Package is the locator in the runtime's own
  // syntax — "git:github.com/owner/repo" for pi, "@scope/name" for opencode —
  // and ap never parses it.
  type NativePlugin struct {
      Enabled bool
      Package string
  }
  ```
  `Runtime.Plugins` becomes `map[string]NativePlugin`.
  `func decodeNativePlugins(path string, n *Node) (map[string]NativePlugin, error)`

- [ ] **Step 1: Write failing tests**

```go
func TestARuntimeNativePluginDecodesItsPackage(t *testing.T) {
	src := `version: "1"
name: p
targets:
  - pi
runtimes:
  pi:
    plugins:
      ponytail:
        package: "git:github.com/DietrichGebert/ponytail"
`
	p := decodeOrFail(t, src)
	got := p.Runtimes["pi"].Plugins["ponytail"]
	if got.Package != "git:github.com/DietrichGebert/ponytail" {
		t.Errorf("package = %q", got.Package)
	}
	if !got.Enabled {
		t.Error("a native plugin defaults to disabled; every other resource defaults to enabled")
	}
}

// enabled: false is how one runtime opts OUT of a plugin the root declares.
// It needs no package, because nothing is materialized for it.
func TestADisabledNativePluginNeedsNoPackage(t *testing.T) {
	src := `version: "1"
name: p
targets:
  - pi
runtimes:
  pi:
    plugins:
      ponytail:
        enabled: false
`
	p := decodeOrFail(t, src)
	if got := p.Runtimes["pi"].Plugins["ponytail"]; got.Enabled {
		t.Error("enabled: false was not read")
	}
}

// An enabled entry with no locator is refused, exactly as §22 refuses one for a
// common resource. Materializing nothing while reporting success is the failure
// this prevents.
func TestAnEnabledNativePluginNeedsAPackage(t *testing.T) {
	src := `version: "1"
name: p
targets:
  - pi
runtimes:
  pi:
    plugins:
      ponytail: {}
`
	if _, err := Decode(mustLoad(t, src)); err == nil {
		t.Fatal("an enabled native plugin with no package was accepted")
	}
}
```

Use whatever decode helper `pkg/schema/profile_test.go` already has; if it has
none, write `decodeOrFail`/`mustLoad` as three-line helpers beside these tests
rather than inlining `Load`+`Decode` three times.

- [ ] **Step 2: Run the tests and verify they fail**

Run: `./scripts/dev.sh go test ./pkg/schema/ -run 'NativePlugin' -count=1`
Expected: FAIL — `Runtimes["pi"].Plugins["ponytail"].Package undefined`

- [ ] **Step 3: Implement the decoder**

```go
// decodeNativePlugins reads runtimes.<rt>.plugins: a runtime's OWN packaging
// mechanism (§24.3), which is not §24's common plugin contract and shares only
// the word. The locator is in the runtime's own syntax and ap never parses it —
// ap writes it into the runtime's settings and the runtime resolves it.
func decodeNativePlugins(path string, n *Node) (map[string]NativePlugin, error) {
	if n.Kind != Mapping {
		return nil, fmt.Errorf("line %d: %s: expected a mapping of name to plugin", n.Line, path)
	}
	out := map[string]NativePlugin{}
	for _, k := range n.Keys {
		p := fmt.Sprintf("%s.%s", path, k)
		e := n.Map[k]
		np := NativePlugin{Enabled: true}
		if e.Kind == Empty {
			e = &Node{Kind: Mapping, Line: e.Line, Map: map[string]*Node{}}
		}
		if e.Kind != Mapping {
			return nil, fmt.Errorf("line %d: %s: expected a mapping", e.Line, p)
		}
		if err := onlyKeys(e, "enabled", "package"); err != nil {
			return nil, err
		}
		if en, ok := e.Map["enabled"]; ok {
			b, err := en.Bool()
			if err != nil {
				return nil, fmt.Errorf("line %d: %s.enabled: %w", en.Line, p, err)
			}
			np.Enabled = b
		}
		if pk, ok := e.Map["package"]; ok {
			v, err := pk.Text()
			if err != nil {
				return nil, fmt.Errorf("line %d: %s.package: %w", pk.Line, p, err)
			}
			np.Package = v
		} else if np.Enabled {
			// §22's rule, one collection over: an enabled resource defines
			// exactly one locator. A disabled one needs none.
			return nil, fmt.Errorf("line %d: %s: no package; an enabled runtime-native plugin needs one", e.Line, p)
		}
		out[k] = np
	}
	return out, nil
}
```

Point `decodeRuntimePluginsField` at it, change `Runtime.Plugins` to
`map[string]NativePlugin`, and update `render.go`'s plugins block to emit
`enabled` and `package` as scalars through the existing writer rather than
re-emitting a raw node.

- [ ] **Step 4: Run the schema suite**

Run: `./scripts/dev.sh go test ./pkg/schema/ -count=1`
Expected: PASS. `TestRenderRoundTripsThroughTheParser` covers the new emitter —
if it does not exercise a runtime-native plugin, add one to its fixture. An
emitter whose output the parser refuses is the defect that test exists for.

- [ ] **Step 5: Commit**

```bash
git add pkg/schema/
git commit -m "feat(schema): a runtime-native plugin has a package, not a raw node"
```

---

## Task 3: Where a native package is declared, per runtime

**Files:**
- Modify: `pkg/agentreg/agent.go` (pi and opencode rows)
- Modify: `pkg/hydrate/adapter.go`
- Test: `pkg/hydrate/adapter_test.go`, `pkg/agentreg/agent_test.go`

**Interfaces:**
- Consumes: `agentreg.Agent`.
- Produces:
  - `agentreg.Agent` gains `PackageFile, PackageKey string`.
  - `Adapter` gains `PackageTarget() (rel, key string, ok bool)`, the same shape
    as `MCPTarget`.
  - pi: `PackageFile: "settings.json"`, `PackageKey: "packages"`.
  - opencode: `PackageFile: "opencode.json"`, `PackageKey: "plugin"`.
  - claude, codex: both empty, so `ok` is false.

- [ ] **Step 1: Write failing tests**

```go
// Measured, not read: `pi install git:github.com/DietrichGebert/ponytail`
// against a throwaway PI_CODING_AGENT_DIR left settings.json holding exactly
// {"packages":["git:github.com/DietrichGebert/ponytail"]}.
func TestPiDeclaresPackagesInItsSettings(t *testing.T) {
	a, _ := agentreg.Lookup("pi")
	ad, _ := AdapterFor(a)
	rel, key, ok := ad.PackageTarget()
	if !ok || rel != "settings.json" || key != "packages" {
		t.Errorf("pi PackageTarget = %q %q %v", rel, key, ok)
	}
}

// claude and codex declare plugins through a marketplace, not a package list.
// Inventing a key for them would write a file neither reads (§8: warn, never
// invent).
func TestClaudeAndCodexHaveNoNativePackageList(t *testing.T) {
	for _, name := range []string{"claude", "codex"} {
		a, _ := agentreg.Lookup(name)
		ad, _ := AdapterFor(a)
		if _, _, ok := ad.PackageTarget(); ok {
			t.Errorf("%s reports a native package list; it uses a marketplace", name)
		}
	}
}
```

- [ ] **Step 2: Run and verify they fail**

Run: `./scripts/dev.sh go test ./pkg/hydrate/ -run 'PackageTarget|NativePackage|DeclaresPackages' -count=1`
Expected: FAIL — `ad.PackageTarget undefined`

- [ ] **Step 3: Implement**

```go
// PackageTarget is the file a runtime's OWN package declarations live in and
// the key holding the list. ok=false means this runtime has no such list, which
// is a §8 degradation and not something to invent — claude and codex declare
// plugins through a marketplace instead.
func (r registryAdapter) PackageTarget() (string, string, bool) {
	if r.agent.PackageFile == "" || r.agent.PackageKey == "" {
		return "", "", false
	}
	return r.agent.PackageFile, r.agent.PackageKey, true
}
```

On the pi row, with the measurement in the comment as every row here carries
one:

```go
// Measured: `pi install git:github.com/DietrichGebert/ponytail` against a
// throwaway PI_CODING_AGENT_DIR appends the source string here and clones into
// <dir>/git/<host>/<owner>/<repo>. ap writes only the declaration — `pi update
// <source>` materializes it from that alone, verified against a settings.json
// with no clone on disk.
PackageFile: "settings.json",
PackageKey:  "packages",
```

- [ ] **Step 4: Run and verify they pass**

Run: `./scripts/dev.sh go test ./pkg/hydrate/ ./pkg/agentreg/ -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/agentreg/agent.go pkg/hydrate/adapter.go pkg/hydrate/adapter_test.go
git commit -m "feat(agentreg): pi and opencode declare native packages in a list"
```

---

## Task 4: Materialize it, and let it override the common plugin

This is the task the user asked for. Two behaviours land together because
neither is testable without the other: a native entry is written, and the common
plugin of the same name is not.

**Files:**
- Modify: `pkg/hydrate/apply.go` (`Apply` at :50, `applyPlugins` at :145)
- Test: `pkg/hydrate/apply_test.go`

**Interfaces:**
- Consumes: `AppendInto` (Task 1), `NativePlugin` (Task 2), `PackageTarget`
  (Task 3).
- Produces: `func applyNativePlugins(p Plan, l *Ledger, res *Result, stamp string) error`,
  and a ledger record of kind `"native-plugin"` whose single `FileRec` carries
  `Merge: "list"` and `Keys: []string{key + "." + pkg}`.

- [ ] **Step 1: Write failing tests**

```go
// §24.3: a runtime block's plugins entry OVERRIDES the common one for that
// runtime. Before this, the native entry was parsed, rendered, and dropped in
// silence while the common plugin materialized anyway — so `runtimes.pi` could
// not express "not this one, mine instead".
func TestANativePluginOverridesTheCommonOneOfTheSameName(t *testing.T) {
	a, _ := agentreg.Lookup("pi")
	ad, _ := AdapterFor(a)
	src := writeTree(t, t.TempDir(), map[string]string{"skills/ponytail/SKILL.md": "# common"})
	root := t.TempDir()

	res, err := Apply(t.Context(), Plan{
		Root: root, Adapter: ad, Now: fixedNow,
		Profile: schema.Profile{
			Plugins: map[string]schema.Resource{"ponytail": {Enabled: true, Source: &schema.Source{}}},
			Runtimes: map[string]schema.Runtime{"pi": {Plugins: map[string]schema.NativePlugin{
				"ponytail": {Enabled: true, Package: "git:github.com/DietrichGebert/ponytail"},
			}}},
		},
		Fetched: map[string]source.Resolved{"plugin ponytail": {Dir: src, ResolvedRef: "abc"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	// The common plugin's files are NOT there.
	if _, err := os.Stat(filepath.Join(root, "skills", "ponytail", "SKILL.md")); !os.IsNotExist(err) {
		t.Error("the common plugin materialized despite the runtime-native override")
	}
	// The declaration IS.
	raw, err := os.ReadFile(filepath.Join(root, "settings.json"))
	if err != nil {
		t.Fatalf("settings.json was not written: %v", err)
	}
	if !strings.Contains(string(raw), "git:github.com/DietrichGebert/ponytail") {
		t.Errorf("the package was not declared:\n%s", raw)
	}
	// And the user is told the one command that reconciles it, because ap does
	// not run other people's installers.
	if !strings.Contains(strings.Join(res.Warnings, "\n"), "pi update") {
		t.Errorf("the reconcile command was not reported: %v", res.Warnings)
	}

	// The ledger bounds removal by the ELEMENT, never by the container key: a
	// recorded "packages" would take the user's packages with ours.
	l, _ := LoadLedger(root)
	rec, ok := l.Resource("native-plugin", "ponytail")
	if !ok {
		t.Fatalf("not recorded: %+v", l)
	}
	if len(rec.Files) != 1 || len(rec.Files[0].Keys) != 1 ||
		rec.Files[0].Keys[0] == "packages" {
		t.Errorf("record is not bounded by the element: %+v", rec.Files)
	}
}

// enabled: false on a native entry is how ONE runtime opts out entirely. The
// override still applies — the common plugin does not come back — which is what
// makes "ponytail everywhere except pi" expressible.
func TestADisabledNativePluginSuppressesTheCommonOneAndWritesNothing(t *testing.T) {
	a, _ := agentreg.Lookup("pi")
	ad, _ := AdapterFor(a)
	src := writeTree(t, t.TempDir(), map[string]string{"skills/ponytail/SKILL.md": "# common"})
	root := t.TempDir()

	if _, err := Apply(t.Context(), Plan{
		Root: root, Adapter: ad, Now: fixedNow,
		Profile: schema.Profile{
			Plugins: map[string]schema.Resource{"ponytail": {Enabled: true, Source: &schema.Source{}}},
			Runtimes: map[string]schema.Runtime{"pi": {Plugins: map[string]schema.NativePlugin{
				"ponytail": {Enabled: false},
			}}},
		},
		Fetched: map[string]source.Resolved{"plugin ponytail": {Dir: src, ResolvedRef: "abc"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "skills", "ponytail", "SKILL.md")); !os.IsNotExist(err) {
		t.Error("the common plugin materialized for a runtime that opted out")
	}
	if _, err := os.Stat(filepath.Join(root, "settings.json")); !os.IsNotExist(err) {
		t.Error("a disabled native plugin still wrote a declaration")
	}
}

// §8: a runtime with no native package list says so. claude declares plugins
// through a marketplace, and writing a packages array into its config would
// produce a file nothing reads.
func TestARuntimeWithNoPackageListWarnsRatherThanInventingOne(t *testing.T) {
	a, _ := agentreg.Lookup("claude")
	ad, _ := AdapterFor(a)
	root := t.TempDir()

	res, err := Apply(t.Context(), Plan{
		Root: root, Adapter: ad, Now: fixedNow,
		Profile: schema.Profile{Runtimes: map[string]schema.Runtime{"claude": {
			Plugins: map[string]schema.NativePlugin{"ponytail": {Enabled: true, Package: "x"}},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(res.Warnings, "\n")
	for _, want := range []string{"claude", "ponytail"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warning does not name %q: %s", want, joined)
		}
	}
}
```

- [ ] **Step 2: Run and verify they fail**

Run: `./scripts/dev.sh go test ./pkg/hydrate/ -run 'NativePlugin|PackageList' -count=1`
Expected: FAIL — `applyNativePlugins` does not exist and the common plugin
materializes.

- [ ] **Step 3: Implement**

Add to `Apply`, immediately BEFORE `applyPlugins`, so the suppression set exists
when the common plugins are walked:

```go
	if err := applyNativePlugins(p, ledger, &res, stamp); err != nil {
		return res, err
	}
```

```go
// nativePlugins is the selected runtime's own plugin block. Effective narrows
// Runtimes to one entry, so there is at most one to read.
func nativePlugins(p Plan) map[string]schema.NativePlugin {
	return p.Profile.Runtimes[p.Adapter.Name()].Plugins
}

// applyNativePlugins writes each declared native package into the runtime's own
// settings list (§24.3), and reports the command that materializes it.
//
// ap writes the DECLARATION and nothing else. Running the runtime's installer
// is what `ap sync` did and why it was removed; the honest replacement is to
// name the command, exactly as the clone path names `codex plugin add`.
// Measured: `pi update <source>` clones a package that exists only in
// settings.json, so the declaration alone is enough to act on.
func applyNativePlugins(p Plan, l *Ledger, res *Result, stamp string) error {
	native := nativePlugins(p)
	names := sortedKeys(native)
	active := make([]string, 0, len(names))
	for _, n := range names {
		if native[n].Enabled {
			active = append(active, n)
		}
	}
	if len(active) == 0 {
		return nil
	}
	rel, key, ok := p.Adapter.PackageTarget()
	if !ok {
		for _, n := range active {
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"runtime %q has no native package list; skipping plugin %q — it declares plugins through a marketplace",
				p.Adapter.Name(), n))
		}
		return nil
	}
	path := filepath.Join(p.Root, rel)
	for _, name := range active {
		pkg := native[name].Package
		if _, err := AppendInto(path, key, pkg); err != nil {
			return fmt.Errorf("plugin %q: %w", name, err)
		}
		hash, err := hashFile(path)
		if err != nil {
			return err
		}
		l.Put(ResourceRec{
			Name: name, Kind: "native-plugin", InstalledAt: stamp,
			// Bounded by the ELEMENT. Recording the container key would have
			// uninstall delete every package in the list, the user's included —
			// the same rule a merged MCP record follows.
			Files: []FileRec{{RelPath: filepath.ToSlash(rel), Hash: hash, Merge: "list", Keys: []string{key + "." + pkg}}},
		})
		res.Changes = append(res.Changes, Change{Path: filepath.Join(rel, key+"."+pkg), Op: "merge"})
		if cmd := p.Adapter.ReconcileCommand(pkg); cmd != "" {
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"plugin %q is declared but not materialized; ap does not run a runtime's installer — run: %s", name, cmd))
		}
	}
	return nil
}
```

`ReconcileCommand` is one more `Adapter` method backed by a registry string
(`Agent.PackageReconcile`, e.g. `"pi update %s"` and `""` for opencode, which
resolves its npm plugin at start-up). Add it in this task alongside the rest;
splitting it out would leave Task 3 unable to compile its own test.

Then, in `applyPlugins`, skip a common plugin the runtime overrode:

```go
	native := nativePlugins(p)
	for _, name := range sortedKeys(p.Profile.Plugins) {
		r := p.Profile.Plugins[name]
		if !r.Enabled {
			continue
		}
		// §24.3: a runtime-native entry of the same name OVERRIDES the common
		// one for this runtime — including a disabled one, which is how a
		// single runtime opts out of a plugin the root declares. Materializing
		// both would install ponytail twice by two mechanisms.
		if _, overridden := native[name]; overridden {
			continue
		}
```

- [ ] **Step 4: Run and verify they pass**

Run: `./scripts/dev.sh go test ./pkg/hydrate/ -count=1`
Expected: PASS

- [ ] **Step 5: Mutation-test both guards**

Delete the `if _, overridden := native[name]` block, run
`TestANativePluginOverridesTheCommonOneOfTheSameName`, confirm it fails on "the
common plugin materialized". Restore. Then change the recorded `Keys` to
`[]string{key}`, run the same test, confirm it fails on "not bounded by the
element". Restore.

- [ ] **Step 6: Commit**

```bash
git add pkg/hydrate/apply.go pkg/hydrate/apply_test.go pkg/hydrate/adapter.go pkg/agentreg/agent.go
git commit -m "feat(hydrate): materialize runtime-native plugins and let them override"
```

---

## Task 5: Remove it and export it

A record nothing can remove is a leak, and `ap manifest export` is the other
half of the ledger's loop.

**Files:**
- Modify: `pkg/hydrate/remove.go` (`classifyFile` at :135, `execute` at :170)
- Modify: `pkg/hydrate/export.go` (:96)
- Test: `pkg/hydrate/remove_test.go`, `pkg/hydrate/export_test.go`

**Interfaces:**
- Consumes: `RemoveFrom` (Task 1), the `"native-plugin"` record (Task 4).
- Produces: `Merge: "list"` handled in `classifyFile` and `execute`; export
  emits the record back under `runtimes.<rt>.plugins`.

- [ ] **Step 1: Write failing tests**

```go
// The bound, end to end: the user's package survives ours being removed.
func TestUninstallingANativePluginLeavesTheUsersPackagesIntact(t *testing.T) {
	a, _ := agentreg.Lookup("pi")
	ad, _ := AdapterFor(a)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "settings.json"),
		[]byte(`{"packages":["git:example.com/theirs"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(t.Context(), Plan{
		Root: root, Adapter: ad, Now: fixedNow,
		Profile: schema.Profile{Runtimes: map[string]schema.Runtime{"pi": {
			Plugins: map[string]schema.NativePlugin{"ponytail": {Enabled: true, Package: "git:example.com/ours"}},
		}}},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := Remove(root, "native-plugin", "ponytail", false); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(root, "settings.json"))
	if strings.Contains(string(raw), "ours") {
		t.Errorf("ours survived the removal:\n%s", raw)
	}
	if !strings.Contains(string(raw), "theirs") {
		t.Errorf("the user's package was removed with ours:\n%s", raw)
	}
}
```

- [ ] **Step 2: Run and verify it fails**

Run: `./scripts/dev.sh go test ./pkg/hydrate/ -run 'UninstallingANativePlugin' -count=1`
Expected: FAIL — the `"list"` merge is unhandled, so nothing is removed.

- [ ] **Step 3: Implement**

In `classifyFile`, treat `Merge == "list"` like `"deep"`: a verdict of `Op:
"keys"` carrying `f.Keys`. In `execute`, when the verdict came from a `"list"`
record, split each key on the FIRST `.` into container and element and call
`RemoveFrom(path, container, element)`. Carry the merge kind on the `Verdict`
rather than re-reading the ledger.

- [ ] **Step 4: Extend export**

```go
	case "native-plugin":
		// Back under the runtime block it came from: a native plugin is not a
		// common one and round-tripping it to the root would change what the
		// manifest means.
		setNative(&p.Runtimes, runtime, rec.Name, schema.NativePlugin{Enabled: true, Package: packageOf(rec)})
```

`packageOf` reads the element back out of the single recorded key — the same
place `execute` reads it — so there is one parser for that string and not two.

- [ ] **Step 5: Run the suite**

Run: `./scripts/dev.sh go test ./pkg/hydrate/ -count=1`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add pkg/hydrate/
git commit -m "feat(hydrate): remove and export a runtime-native plugin"
```

---

## Task 6: The two gates that drive real binaries, and the docs

**Files:**
- Modify: `scripts/sandbox.sh`
- Modify: `scripts/smoke.sh`
- Modify: `CLAUDE.md`
- Modify: `docs/references/DECLARATIVE.md`
- Modify: `examples/agent-profiles/base.yaml`

- [ ] **Step 1: Add the sandbox check**

Deterministic half, with the stub agents: apply a manifest declaring a common
plugin and a `runtimes.pi.plugins` override, then assert `settings.json` holds
the package, the common plugin's files are absent, and `ap uninstall pi:<p>
native-plugin ponytail` leaves a hand-added package in the list. Follow the
existing checks' `pass`/`bad` shape.

- [ ] **Step 2: Add the smoke check**

Against the real pi: apply the manifest, then run `pi list` under the profile
and assert the package is listed from the declaration alone — which is what the
measurement in Task 0 proves is possible. Do NOT assert the clone: ap did not
make one, and asserting `pi update` would put a network fetch inside the check.

- [ ] **Step 3: Run both gates**

```bash
make sandbox
make smoke
```
Expected: both green, with the new checks listed.

- [ ] **Step 4: Write the doctrine into CLAUDE.md**

A section under the ledger rules stating: a native entry overrides the common
plugin of the same name for that runtime including when disabled; ap writes the
declaration and names the reconcile command because it does not run other
people's installers; the record is bounded by the ELEMENT and a container key
must never be recorded; claude and codex have no package list and warn.

- [ ] **Step 5: Show it in a shipped example**

Add the pi override to `examples/agent-profiles/base.yaml`, so `make examples`
composes it every run and a manifest that stops parsing is caught.

- [ ] **Step 6: Full verification**

```bash
make verify
make sandbox
make walkthrough
make smoke
make hydrate
```
Expected: all green.

- [ ] **Step 7: Commit**

```bash
git add scripts/ CLAUDE.md docs/references/DECLARATIVE.md examples/
git commit -m "docs: runtime-native plugins, and the two gates that drive them"
```

---

## Deferred, deliberately

- **claude and codex native packages.** Both declare plugins through a
  marketplace (`extraKnownMarketplaces`/`enabledPlugins` for claude,
  `[plugins."<p>@<m>"]` for codex). Expressible, but it is a second mechanism
  with its own reconcile story, and §40.1 leaves it open. They warn today.
- **ap running `pi update`.** Not a gap to close later; it is the line `ap sync`
  was removed to draw.
- **npm dependencies.** A pi package needing `npm install` is materialized by
  `pi update`, not by ap. Stated here rather than discovered by a user.
