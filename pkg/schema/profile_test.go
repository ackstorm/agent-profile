package schema

import (
	"path/filepath"
	"strings"
	"testing"
)

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

// The four tests below address review feedback on the batch that landed just
// before this one: a union node can legitimately end up with zero branches
// after a merge, and nothing rejected that until now.

func TestDecodeRefusesASourceThatEndsWithNoBranchSelected(t *testing.T) {
	// `null` resets a singular to absent (§3.7), and source.git is not a
	// collection entry, so the merge deletes `git` and leaves `source` a
	// mapping with no keys at all. compose.go is not wrong to allow this —
	// Decode is where it must be caught.
	base, err := ParseYAML([]byte("version: \"1\"\nname: x\nskills:\n  pdf:\n    source:\n      git:\n        url: https://example.com/r.git\n"))
	if err != nil {
		t.Fatal(err)
	}
	overlay, err := ParseYAML([]byte("skills:\n  pdf:\n    source:\n      git: null\n"))
	if err != nil {
		t.Fatal(err)
	}
	merged, err := Merge(base, overlay, V1Schema())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(merged); err == nil {
		t.Fatal("a source selecting neither git nor local was accepted")
	} else if !strings.Contains(err.Error(), "skills.pdf.source") {
		t.Errorf("err = %q; does not name skills.pdf.source", err)
	}
}

func TestDecodeRefusesASourceThatSelectsBothBranches(t *testing.T) {
	// Schema validation enforces branch exclusivity WITHIN a single authored
	// document; the merge engine deliberately does not — it assumes
	// validation catches it. This is the only place that happens.
	n, err := ParseYAML([]byte("version: \"1\"\nname: x\nskills:\n  pdf:\n    source:\n      git:\n        url: https://example.com/r.git\n      local:\n        path: ./p\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(n); err == nil {
		t.Fatal("a source declaring both git and local was accepted")
	} else if !strings.Contains(err.Error(), "skills.pdf.source") {
		t.Errorf("err = %q; does not name skills.pdf.source", err)
	}
}

func TestDecodeRefusesAPromptThatEndsWithNoBranchSelected(t *testing.T) {
	// Same shape as the source case, at the prompt content|source group.
	base, err := ParseYAML([]byte("version: \"1\"\nname: x\nprompt:\n  content: hi\n"))
	if err != nil {
		t.Fatal(err)
	}
	overlay, err := ParseYAML([]byte("prompt:\n  content: null\n"))
	if err != nil {
		t.Fatal(err)
	}
	merged, err := Merge(base, overlay, V1Schema())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(merged); err == nil {
		t.Fatal("a prompt selecting neither content nor source was accepted")
	} else if !strings.Contains(err.Error(), "prompt") {
		t.Errorf("err = %q; does not name prompt", err)
	}
}

func TestDecodeRefusesAPromptSourceThatSelectsBothBranches(t *testing.T) {
	n, err := ParseYAML([]byte("version: \"1\"\nname: x\nprompt:\n  source:\n    git:\n      url: https://example.com/r.git\n    local:\n      path: ./p\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(n); err == nil {
		t.Fatal("a prompt source declaring both git and local was accepted")
	} else if !strings.Contains(err.Error(), "prompt.source") {
		t.Errorf("err = %q; does not name prompt.source", err)
	}
}

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

// The six tests below cover validation rules the reviewer found implemented
// but untested: right today, but nothing shipped would catch a regression.
// Each case asserts the error names the offending path, not merely that an
// error occurred.

func TestDecodeEnforcesModelAuthTypeIsBearer(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			"unsupported type refused",
			"version: \"1\"\nname: x\nmodel:\n  auth:\n    type: basic\n    value_from:\n      variable: TOK\n",
			"model.auth.type",
		},
		{
			"bearer accepted",
			"version: \"1\"\nname: x\nmodel:\n  auth:\n    type: bearer\n    value_from:\n      variable: TOK\n",
			"",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			n, err := ParseYAML([]byte(c.yaml))
			if err != nil {
				t.Fatal(err)
			}
			_, err = Decode(n)
			if c.wantErr == "" {
				if err != nil {
					t.Errorf("err = %v; want no error", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("err = %v; want it to name %q", err, c.wantErr)
			}
		})
	}
}

func TestDecodeEnforcesTransportRequiredAndForbiddenFields(t *testing.T) {
	base := "version: \"1\"\nname: x\nmcps:\n  m:\n    transport:\n"
	cases := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			"unknown type refused",
			base + "      type: websocket\n",
			"mcps.m.transport.type",
		},
		{
			"http requires url",
			base + "      type: http\n",
			"url is required",
		},
		{
			"http forbids command",
			base + "      type: http\n      url: https://e/mcp\n      command: foo\n",
			"command/args are not valid",
		},
		{
			"http forbids args",
			base + "      type: http\n      url: https://e/mcp\n      args:\n        - --flag\n",
			"command/args are not valid",
		},
		{
			"http accepted with url",
			base + "      type: http\n      url: https://e/mcp\n",
			"",
		},
		{
			"stdio requires command",
			base + "      type: stdio\n",
			"command is required",
		},
		{
			"stdio forbids url",
			base + "      type: stdio\n      command: foo\n      url: https://e/mcp\n",
			"url/headers are not valid",
		},
		{
			"stdio forbids headers",
			base + "      type: stdio\n      command: foo\n      headers:\n        X:\n          value: v\n",
			"url/headers are not valid",
		},
		{
			"stdio accepted with command",
			base + "      type: stdio\n      command: foo\n",
			"",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			n, err := ParseYAML([]byte(c.yaml))
			if err != nil {
				t.Fatal(err)
			}
			_, err = Decode(n)
			if c.wantErr == "" {
				if err != nil {
					t.Errorf("err = %v; want no error", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("err = %v; want it to name %q", err, c.wantErr)
			}
		})
	}
}

func TestDecodeEnforcesMarketplaceTypeAndSource(t *testing.T) {
	base := "version: \"1\"\nname: x\nmarketplaces:\n  m:\n"
	cases := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			"unknown type refused",
			base + "    type: registries\n    source:\n      local:\n        path: ./p\n",
			"marketplaces.m.type",
		},
		{
			"missing source refused",
			base + "    type: plugins\n",
			"marketplaces.m: source is required",
		},
		{
			"plugins with source accepted",
			base + "    type: plugins\n    source:\n      local:\n        path: ./p\n",
			"",
		},
		{
			"skills with source accepted",
			base + "    type: skills\n    source:\n      local:\n        path: ./p\n",
			"",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			n, err := ParseYAML([]byte(c.yaml))
			if err != nil {
				t.Fatal(err)
			}
			_, err = Decode(n)
			if c.wantErr == "" {
				if err != nil {
					t.Errorf("err = %v; want no error", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("err = %v; want it to name %q", err, c.wantErr)
			}
		})
	}
}

func TestDecodeEnforcesInputsBindingExactlyOneOfEnvFile(t *testing.T) {
	base := "version: \"1\"\nname: x\ninputs:\n  variables:\n    v:\n"
	cases := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			"env accepted",
			base + "      env: FOO\n",
			"",
		},
		{
			"file accepted",
			base + "      file: ./foo\n",
			"",
		},
		{
			"both refused",
			base + "      env: FOO\n      file: ./foo\n",
			"inputs.variables.v: both env and file set",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			n, err := ParseYAML([]byte(c.yaml))
			if err != nil {
				t.Fatal(err)
			}
			_, err = Decode(n)
			if c.wantErr == "" {
				if err != nil {
					t.Errorf("err = %v; want no error", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("err = %v; want it to name %q", err, c.wantErr)
			}
		})
	}
	// "Neither env nor file" needs a zero-key mapping, which this restricted
	// YAML has no literal syntax for ({} is a rejected flow mapping) — the
	// same reason the union-branch tests above go through Merge instead of a
	// single parsed document.
	t.Run("neither refused", func(t *testing.T) {
		b, err := ParseYAML([]byte(base + "      env: FOO\n"))
		if err != nil {
			t.Fatal(err)
		}
		o, err := ParseYAML([]byte("inputs:\n  variables:\n    v:\n      env: null\n"))
		if err != nil {
			t.Fatal(err)
		}
		merged, err := Merge(b, o, V1Schema())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Decode(merged); err == nil || !strings.Contains(err.Error(), "inputs.variables.v: neither env nor file") {
			t.Errorf("err = %v; want it to name inputs.variables.v and say neither env nor file", err)
		}
	})
}

func TestDecodeEnforcesTargetsNameKnownAgents(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			"unknown agent refused",
			"version: \"1\"\nname: x\ntargets:\n  - nonesuch\n",
			"targets: unknown agent \"nonesuch\"",
		},
		{
			"known agent accepted",
			"version: \"1\"\nname: x\ntargets:\n  - claude\n",
			"",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			n, err := ParseYAML([]byte(c.yaml))
			if err != nil {
				t.Fatal(err)
			}
			_, err = Decode(n)
			if c.wantErr == "" {
				if err != nil {
					t.Errorf("err = %v; want no error", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("err = %v; want it to name %q", err, c.wantErr)
			}
		})
	}
}

func TestDecodeEnforcesVersionAndNameRules(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			"missing version refused",
			"name: x\n",
			"version is required",
		},
		{
			"unsupported version refused",
			"version: \"2\"\nname: x\n",
			"version 2",
		},
		{
			"missing name refused",
			"version: \"1\"\n",
			"name is required",
		},
		{
			"invalid name refused",
			"version: \"1\"\nname: ../escape\n",
			"name",
		},
		{
			"the default sentinel bypasses ValidName",
			"version: \"1\"\nname: default\n",
			"",
		},
		{
			"an ordinary valid name is accepted",
			"version: \"1\"\nname: work\n",
			"",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			n, err := ParseYAML([]byte(c.yaml))
			if err != nil {
				t.Fatal(err)
			}
			_, err = Decode(n)
			if c.wantErr == "" {
				if err != nil {
					t.Errorf("err = %v; want no error", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("err = %v; want it to name %q", err, c.wantErr)
			}
		})
	}
}

// §17.1: the two v1 schemes, and — the part that matters — absent must stay
// absent. A default written here would make the host inference unreachable,
// and §17.1 requires the scheme actually used to be reportable, which it
// cannot be if the decoder already picked one.
func TestGitAuthSchemeAcceptsOnlyTheTwoV1Values(t *testing.T) {
	manifest := func(auth string) []byte {
		return []byte("version: \"1\"\nname: x\ntargets:\n  - claude\n" +
			"inputs:\n  secrets:\n    t:\n      env: T\n" +
			"skills:\n  s:\n    source:\n      git:\n        url: https://gl/x.git\n" +
			"        auth:\n" + auth)
	}
	for _, scheme := range []string{"bearer", "basic-oauth2"} {
		n, err := ParseYAML(manifest("          scheme: " + scheme + "\n          value_from:\n            secret: t\n"))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		p, err := Decode(n)
		if err != nil {
			t.Fatalf("decode %s: %v", scheme, err)
		}
		if got := p.Skills["s"].Source.Git.Auth.Scheme; got != scheme {
			t.Errorf("scheme = %q, want %q", got, scheme)
		}
	}

	n, err := ParseYAML(manifest("          value_from:\n            secret: t\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	p, err := Decode(n)
	if err != nil {
		t.Fatalf("decode without scheme: %v", err)
	}
	if got := p.Skills["s"].Source.Git.Auth.Scheme; got != "" {
		t.Errorf("scheme = %q, want empty — the schema must not default it", got)
	}

	n, err = ParseYAML(manifest("          scheme: ntlm\n          value_from:\n            secret: t\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Decode(n); err == nil || !strings.Contains(err.Error(), "ntlm") {
		t.Errorf("err = %v; an unknown scheme must be refused by name", err)
	}
}

// §18: an archive's digest is REQUIRED and is not a checksum bolted on. A git
// source is content-addressed — fetching by SHA is verified by git itself —
// and an archive is not, so the digest is its only integrity claim and there
// is no flag to skip it.
func TestArchiveSourceRequiresASha256Digest(t *testing.T) {
	manifest := func(extra string) []byte {
		return []byte("version: \"1\"\nname: x\ntargets:\n  - claude\n" +
			"skills:\n  s:\n    source:\n      archive:\n" +
			"        url: https://ach/c/9f2a/skill.tar.gz\n" + extra)
	}
	want := "sha256:" + strings.Repeat("a", 64)
	n, err := ParseYAML(manifest("        digest: " + want + "\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	p, err := Decode(n)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := p.Skills["s"].Source.Archive.Digest; got != want {
		t.Errorf("digest = %q, want %q", got, want)
	}

	for _, bad := range []struct{ name, extra string }{
		{"absent", ""},
		{"wrong algorithm", "        digest: md5:" + strings.Repeat("a", 32) + "\n"},
		{"short hex", "        digest: sha256:abc\n"},
		{"uppercase hex", "        digest: sha256:" + strings.Repeat("A", 64) + "\n"},
	} {
		n, err := ParseYAML(manifest(bad.extra))
		if err != nil {
			t.Fatalf("%s: parse: %v", bad.name, err)
		}
		if _, err := Decode(n); err == nil || !strings.Contains(err.Error(), "digest") {
			t.Errorf("%s: err = %v; want an error naming digest", bad.name, err)
		}
	}
}

// §3.5.1: a different branch replaces the union wholesale, and the union is
// now one of three rather than one of two. The branch-keyed union is driven by
// the schema oracle, not by a per-type rule, so this is what proves adding a
// branch needed no change to the merge engine.
func TestArchiveIsAThirdBranchOfTheSourceUnion(t *testing.T) {
	got := mergeYAML(t,
		"skills:\n  s:\n    source:\n      git:\n        url: https://e/r.git\n        ref: main\n",
		"skills:\n  s:\n    source:\n      archive:\n        url: https://e/a.tgz\n"+
			"        digest: sha256:"+strings.Repeat("b", 64)+"\n")
	src := got.Map["skills"].Map["s"].Map["source"]
	if _, ok := src.Map["git"]; ok {
		t.Error("the git branch survived a switch to archive")
	}
	if _, ok := src.Map["archive"]; !ok {
		t.Fatal("the archive branch is missing")
	}

	// And back the other way, because a union that only replaces in one
	// direction would pass a one-way test.
	got = mergeYAML(t,
		"skills:\n  s:\n    source:\n      archive:\n        url: https://e/a.tgz\n"+
			"        digest: sha256:"+strings.Repeat("b", 64)+"\n",
		"skills:\n  s:\n    source:\n      local:\n        path: ./here\n")
	src = got.Map["skills"].Map["s"].Map["source"]
	if _, ok := src.Map["archive"]; ok {
		t.Error("the archive branch survived a switch to local")
	}
}

// §24: plugins is a common resource type, with the same two locators as a
// skill — a source or a marketplace ref.
func TestPluginsIsACommonResourceCollection(t *testing.T) {
	n, err := ParseYAML([]byte("version: \"1\"\nname: p\ntargets:\n  - claude\n" +
		"marketplaces:\n  acme:\n    type: plugins\n    source:\n      git:\n        url: https://e/m.git\n" +
		"plugins:\n  code-review:\n    ref: code-review@acme\n" +
		"  ponytail:\n    source:\n      git:\n        url: https://e/p.git\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	p, err := Decode(n)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(p.Plugins) != 2 {
		t.Fatalf("plugins = %+v", p.Plugins)
	}
	if p.Plugins["code-review"].Ref != "code-review@acme" {
		t.Errorf("ref = %q", p.Plugins["code-review"].Ref)
	}
	if p.Plugins["ponytail"].Source.Git.URL != "https://e/p.git" {
		t.Errorf("source = %+v", p.Plugins["ponytail"].Source)
	}
	// Same exclusive group as every other named resource: one locator.
	n, _ = ParseYAML([]byte("version: \"1\"\nname: p\ntargets:\n  - claude\n" +
		"plugins:\n  x:\n    ref: a@b\n    source:\n      git:\n        url: https://e/p.git\n"))
	if _, err := Decode(n); err == nil {
		t.Error("a plugin with both locators was accepted")
	}
}

// A runtime block's `plugins` is the RUNTIME-NATIVE mechanism (§24.3), not the
// common collection. It must not be lifted to the root: §30's worked example
// disables the common skill for opencode and uses opencode's own package
// instead, which only works because these two never merge.
func TestARuntimePluginsBlockIsNotLiftedToTheCommonCollection(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "p.yaml", `version: "1"
name: p
targets:
  - opencode
skills:
  ponytail:
    source:
      git:
        url: https://e/p.git
runtimes:
  opencode:
    skills:
      ponytail:
        enabled: false
    plugins:
      ponytail:
        package: "@dietrichgebert/ponytail"
`)
	p, _, err := Effective(filepath.Join(dir, "p.yaml"), "opencode")
	if err != nil {
		t.Fatalf("effective: %v", err)
	}
	if len(p.Plugins) != 0 {
		t.Errorf("a runtime-native package declaration reached the common collection: %+v", p.Plugins)
	}
	if p.Skills["ponytail"].Enabled {
		t.Error("the common skill was not disabled for opencode")
	}
	if _, ok := p.Runtimes["opencode"].Plugins["ponytail"]; !ok {
		t.Errorf("the native declaration was lost: %+v", p.Runtimes["opencode"])
	}
}

// mustLoad parses src and returns whatever node resulted, error or not — same
// as every other fixture in this file, which passes a parse failure straight
// through to Decode rather than stopping the test early. "ponytail: {}" is a
// flow mapping this subset refuses at the parser, one line before decode ever
// sees it; the test only needs SOME error to come back.
func mustLoad(t *testing.T, src string) *Node {
	t.Helper()
	n, _ := ParseYAML([]byte(src))
	return n
}

func decodeOrFail(t *testing.T, src string) Profile {
	t.Helper()
	p, err := Decode(mustLoad(t, src))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

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
