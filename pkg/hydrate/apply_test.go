package hydrate

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ackstorm/agent-profile/pkg/agentreg"
	"github.com/ackstorm/agent-profile/pkg/schema"
	"github.com/ackstorm/agent-profile/pkg/source"
)

func claudeAdapter(t *testing.T) Adapter {
	t.Helper()
	a, ok := agentreg.Lookup("claude")
	if !ok {
		t.Fatal("claude is not in the registry")
	}
	ad, err := AdapterFor(a)
	if err != nil {
		t.Fatal(err)
	}
	return ad
}

func writeTree(t *testing.T, root string, files map[string]string) string {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func fixedNow() time.Time { return time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC) }

// The five assertions in this test ARE Phase 4.
func TestApplyWritesRecordsAndLeavesHandAddedFilesAlone(t *testing.T) {
	src := writeTree(t, t.TempDir(), map[string]string{
		"SKILL.md":           "# pdf",
		"scripts/convert.py": "print()",
	})
	root := t.TempDir()

	// A file the user put there by hand, inside the very directory the skill
	// will be written into.
	handAdded := filepath.Join(root, "skills", "pdf", "notes.md")
	if err := os.MkdirAll(filepath.Dir(handAdded), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(handAdded, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	// And one the apply will overwrite.
	if err := os.WriteFile(filepath.Join(root, "skills", "pdf", "SKILL.md"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	p := Plan{
		Root: root, Adapter: claudeAdapter(t), Now: fixedNow,
		Profile: schema.Profile{Skills: map[string]schema.Resource{
			"pdf": {Enabled: true, Source: &schema.Source{}},
		}},
		Fetched: map[string]source.Resolved{"skill pdf": {Dir: src, ResolvedRef: "8a71c2e"}},
	}
	res, err := Apply(t.Context(), p)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	// 1. The files landed.
	if b, err := os.ReadFile(filepath.Join(root, "skills", "pdf", "SKILL.md")); err != nil || string(b) != "# pdf" {
		t.Errorf("SKILL.md = %q %v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(root, "skills", "pdf", "scripts", "convert.py")); err != nil || string(b) != "print()" {
		t.Errorf("convert.py = %q %v", b, err)
	}

	// 2. The hand-added file survived. Apply is additive (§33), and this is
	//    the assertion that proves it — the difference between apply and sync.
	if b, err := os.ReadFile(handAdded); err != nil || string(b) != "mine" {
		t.Errorf("a hand-added file did not survive: %q %v", b, err)
	}

	// 3. The overwrite was logged, the new file was not logged as one. §33
	//    requires it: a silent overwrite is the one thing an additive policy
	//    cannot afford.
	ops := map[string]string{}
	for _, c := range res.Changes {
		ops[filepath.ToSlash(c.Path)] = c.Op
	}
	if ops["skills/pdf/SKILL.md"] != "overwrite" {
		t.Errorf("SKILL.md op = %q, want overwrite: %+v", ops["skills/pdf/SKILL.md"], res.Changes)
	}
	if ops["skills/pdf/scripts/convert.py"] != "create" {
		t.Errorf("convert.py op = %q, want create", ops["skills/pdf/scripts/convert.py"])
	}
	if _, logged := ops["skills/pdf/notes.md"]; logged {
		t.Error("the hand-added file was reported as touched")
	}

	assertLedgerMatchesDisk(t, root)
}

// 4. The recorded hash equals the hash of what is on disk. §33.1's honesty
// property: every later verdict — remove, skip, report as modified — rests on
// this matching, so a ledger whose hashes drift from the files is worse than
// none.
func assertLedgerMatchesDisk(t *testing.T, root string) {
	t.Helper()
	l, err := LoadLedger(root)
	if err != nil {
		t.Fatal(err)
	}
	rec, ok := l.Resource("skill", "pdf")
	if !ok {
		t.Fatalf("the skill was not recorded: %+v", l)
	}
	if rec.ResolvedRef != "8a71c2e" || rec.InstalledAt != "2026-08-26T10:00:00Z" {
		t.Errorf("record = %+v", rec)
	}
	if len(rec.Files) != 2 {
		t.Fatalf("recorded %d files, want 2 (the hand-added one is not ours): %+v", len(rec.Files), rec.Files)
	}
	for _, f := range rec.Files {
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f.RelPath)))
		if err != nil {
			t.Fatalf("the ledger claims %s, which is not on disk: %v", f.RelPath, err)
		}
		sum := sha256.Sum256(body)
		if got := hex.EncodeToString(sum[:]); got != f.Hash {
			t.Errorf("%s: recorded hash %s, on-disk %s", f.RelPath, f.Hash, got)
		}
		if strings.Contains(f.RelPath, "notes.md") {
			t.Errorf("the ledger claims a file it did not write: %s", f.RelPath)
		}
	}
}

// 5. The ledger is written LAST (§37.2). A ledger claiming files that were
// never written is worse than no ledger: every later verdict would rest on a
// record that was never true.
func TestAFailedApplyLeavesNoLedger(t *testing.T) {
	root := t.TempDir()
	p := Plan{
		Root: root, Adapter: claudeAdapter(t), Now: fixedNow,
		Profile: schema.Profile{Skills: map[string]schema.Resource{
			"pdf": {Enabled: true, Source: &schema.Source{}},
		}},
		// A source directory that does not exist: materialization fails.
		Fetched: map[string]source.Resolved{"skill pdf": {Dir: filepath.Join(root, "nope")}},
	}
	if _, err := Apply(t.Context(), p); err == nil {
		t.Fatal("an apply with an unreadable source succeeded")
	}
	if _, err := os.Stat(filepath.Join(root, ledgerName)); !os.IsNotExist(err) {
		t.Errorf("a failed apply wrote a ledger: %v", err)
	}
}

// noSkillsAdapter is a runtime whose configuration directory has nowhere to put
// a skill.
//
// A fake, and deliberately so. This test used to ride on codex, which had no
// config-dir skills destination until 0.149.1 grew one — so the day that fact
// changed, the guard for §8 disappeared with it rather than merely needing an
// update. All four runtimes support skills today; the property must not depend
// on that staying true in either direction.
type noSkillsAdapter struct{ Adapter }

func (noSkillsAdapter) Name() string                   { return "codex" }
func (noSkillsAdapter) SkillDir(string) (string, bool) { return "", false }

// §8: an unsupported concept warns and is skipped, never dropped silently.
// "Why is my skill missing" has exactly one useful answer, and this is it.
func TestARuntimeWithNoSkillsDestinationWarnsRatherThanDropping(t *testing.T) {
	a, _ := agentreg.Lookup("codex")
	base, _ := AdapterFor(a)
	ad := noSkillsAdapter{base}
	src := writeTree(t, t.TempDir(), map[string]string{"SKILL.md": "#"})
	root := t.TempDir()

	res, err := Apply(t.Context(), Plan{
		Root: root, Adapter: ad, Now: fixedNow,
		Profile: schema.Profile{Skills: map[string]schema.Resource{"pdf": {Enabled: true, Source: &schema.Source{}}}},
		Fetched: map[string]source.Resolved{"skill pdf": {Dir: src}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) == 0 {
		t.Fatal("the runtime silently dropped a skill")
	}
	joined := strings.Join(res.Warnings, "\n")
	for _, want := range []string{"codex", "pdf"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warning does not name %q: %s", want, joined)
		}
	}
	// And nothing was recorded, because nothing was written.
	l, _ := LoadLedger(root)
	if _, ok := l.Resource("skill", "pdf"); ok {
		t.Error("a skipped skill was recorded in the ledger")
	}
}

// §4, and v0.6.1 made the detection LEDGER-driven: a merged file's contributed
// keys are invisible to the filesystem, so only the ledger knows a disabled
// resource put something in a file that still looks untouched.
func TestADisabledResourceWithLedgerStateWarns(t *testing.T) {
	root := t.TempDir()
	seed := &Ledger{Resources: []ResourceRec{{
		Name: "company-review", Kind: "skill", InstalledAt: "t",
		Files: []FileRec{{RelPath: "settings.json", Hash: "h", Merge: "deep", Keys: []string{"a.b"}}},
	}}}
	if err := seed.Save(root); err != nil {
		t.Fatal(err)
	}
	res, err := Apply(t.Context(), Plan{
		Root: root, Adapter: claudeAdapter(t), Now: fixedNow,
		Profile: schema.Profile{Skills: map[string]schema.Resource{
			"company-review": {Enabled: false, Source: &schema.Source{}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(res.Warnings, "\n")
	if !strings.Contains(joined, "company-review") || !strings.Contains(joined, "disabled") {
		t.Errorf("no ledger-driven warning for a disabled-but-materialized skill: %v", res.Warnings)
	}
}

// Re-applying replaces the record rather than appending, or the ledger grows a
// duplicate on every run and a later removal only finds the first.
func TestApplyTwiceIsIdempotentInTheLedger(t *testing.T) {
	src := writeTree(t, t.TempDir(), map[string]string{"SKILL.md": "#"})
	root := t.TempDir()
	p := Plan{
		Root: root, Adapter: claudeAdapter(t), Now: fixedNow,
		Profile: schema.Profile{Skills: map[string]schema.Resource{"pdf": {Enabled: true, Source: &schema.Source{}}}},
		Fetched: map[string]source.Resolved{"skill pdf": {Dir: src, ResolvedRef: "a"}},
	}
	if _, err := Apply(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	res, err := Apply(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	l, _ := LoadLedger(root)
	if len(l.Resources) != 1 {
		t.Errorf("the ledger grew a duplicate: %+v", l.Resources)
	}
	// The second run overwrites what the first wrote, and says so.
	for _, c := range res.Changes {
		if c.Op != "overwrite" {
			t.Errorf("second apply reported %q for %s, want overwrite", c.Op, c.Path)
		}
	}
}

// A server lives INSIDE a document the user also owns, so the ledger must
// record the dotted key and Merge: "deep". Recorded as a replace, Phase 7's
// uninstall would delete the user's whole config file.
func TestAnMCPServerIsRecordedAsADeepMergeWithItsKeys(t *testing.T) {
	root := t.TempDir()
	res, err := Apply(t.Context(), Plan{
		Root: root, Adapter: claudeAdapter(t), Now: fixedNow,
		Profile: schema.Profile{
			Inputs: schema.Inputs{Secrets: map[string]schema.Binding{
				"memory-token": {Env: "MEMORY_TOKEN"},
			}},
			MCPs: map[string]schema.MCP{"memory": specMemoryServer()},
		},
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(res.Changes) != 1 || res.Changes[0].Op != "merge" {
		t.Errorf("changes = %+v, want one merge", res.Changes)
	}

	l, err := LoadLedger(root)
	if err != nil {
		t.Fatal(err)
	}
	rec, ok := l.Resource("mcp", "memory")
	if !ok {
		t.Fatalf("the server was not recorded: %+v", l)
	}
	if len(rec.Files) != 1 {
		t.Fatalf("files = %+v", rec.Files)
	}
	f := rec.Files[0]
	if f.RelPath != ".claude.json" {
		t.Errorf("relPath = %q, want claude's user-scope file", f.RelPath)
	}
	if f.Merge != "deep" {
		t.Errorf("merge = %q, want deep — a replace would make uninstall delete the whole file", f.Merge)
	}
	if len(f.Keys) != 1 || f.Keys[0] != "mcpServers.memory" {
		t.Errorf("keys = %v, want [mcpServers.memory]", f.Keys)
	}

	// The document holds a REFERENCE, never the value.
	body, err := os.ReadFile(filepath.Join(root, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "${MEMORY_TOKEN}") {
		t.Errorf("no reference in the materialized config:\n%s", body)
	}
	// And the recorded hash matches what is there.
	if got, _ := hashFile(filepath.Join(root, ".claude.json")); got != f.Hash {
		t.Errorf("recorded hash %s, on-disk %s", f.Hash, got)
	}
}

// A hand-added server in the same file survives, which is the merge's additive
// property reaching all the way through apply.
func TestAHandAddedMCPServerSurvivesAnApply(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".claude.json"),
		[]byte(`{"mcpServers":{"mine":{"command":"my-server"}},"theme":"dark"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(t.Context(), Plan{
		Root: root, Adapter: claudeAdapter(t), Now: fixedNow,
		Profile: schema.Profile{
			Inputs: schema.Inputs{Secrets: map[string]schema.Binding{"memory-token": {Env: "MEMORY_TOKEN"}}},
			MCPs:   map[string]schema.MCP{"memory": specMemoryServer()},
		},
	}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(root, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"mine"`, `"theme"`, `"memory"`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("%s missing after apply:\n%s", want, body)
		}
	}
}

// codex's configuration is TOML, and the merge has to go through the TOML
// encoder rather than the JSON one. This is the only place the extension-based
// dispatch is exercised end to end.
func TestCodexMCPLandsInConfigTOML(t *testing.T) {
	a, _ := agentreg.Lookup("codex")
	ad, _ := AdapterFor(a)
	root := t.TempDir()
	if _, err := Apply(t.Context(), Plan{
		Root: root, Adapter: ad, Now: fixedNow,
		Profile: schema.Profile{
			Inputs: schema.Inputs{Secrets: map[string]schema.Binding{"memory-token": {Env: "MEMORY_TOKEN"}}},
			MCPs:   map[string]schema.MCP{"memory": specMemoryServer()},
		},
	}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(root, "config.toml"))
	if err != nil {
		t.Fatalf("no config.toml: %v", err)
	}
	// The variable NAME, never a placeholder codex cannot expand and never a
	// value.
	if !strings.Contains(string(body), `bearer_token_env_var = "MEMORY_TOKEN"`) {
		t.Errorf("config.toml lacks the bearer key:\n%s", body)
	}
	for _, forbidden := range []string{"${", "{env:"} {
		if strings.Contains(string(body), forbidden) {
			t.Errorf("config.toml carries a placeholder codex cannot expand:\n%s", body)
		}
	}
	l, _ := LoadLedger(root)
	rec, _ := l.Resource("mcp", "memory")
	if len(rec.Files) != 1 || rec.Files[0].Keys[0] != "mcp_servers.memory" {
		t.Errorf("recorded keys = %+v, want mcp_servers.memory", rec.Files)
	}
}

// A file-sourced secret references what the LAUNCHER exports, because runtimes
// do not read files. The cost — that such a profile requires launching through
// ap — is stated in APSecretVar's doc rather than discovered.
func TestAFileSourcedSecretReferencesTheLauncherExportedName(t *testing.T) {
	root := t.TempDir()
	if _, err := Apply(t.Context(), Plan{
		Root: root, Adapter: claudeAdapter(t), Now: fixedNow,
		Profile: schema.Profile{
			Inputs: schema.Inputs{Secrets: map[string]schema.Binding{
				"memory-token": {File: "/run/secrets/memory-token"},
			}},
			MCPs: map[string]schema.MCP{"memory": specMemoryServer()},
		},
	}); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(root, ".claude.json"))
	if !strings.Contains(string(body), "${AP_SECRET_MEMORY_TOKEN}") {
		t.Errorf("no launcher-exported reference:\n%s", body)
	}
	if strings.Contains(string(body), "/run/secrets") {
		t.Errorf("the file PATH reached the configuration:\n%s", body)
	}
}

// §9 lands in a FILE, so whoever starts the agent reads it — including a main
// container that execs the runtime directly after an init container hydrated.
// The env-file draft this replaced was read only by `ap run`.
func TestModelIsMaterializedIntoTheRuntimesFileAndRecorded(t *testing.T) {
	root := t.TempDir()
	res, err := Apply(t.Context(), Plan{
		Root: root, Adapter: claudeAdapter(t), Now: fixedNow,
		Profile: schema.Profile{
			Inputs: schema.Inputs{Secrets: map[string]schema.Binding{"llm-token": {Env: "LITELLM_TOKEN"}}},
			Model:  &schema.Model{Type: "anthropic", BaseURL: "https://llm.company.com", Model: "claude-opus-5"},
			Runtimes: map[string]schema.Runtime{"claude": {Environment: map[string]string{
				"ANTHROPIC_BASE_URL": "http://localhost:4000",
				"COMPANY_REGION":     "eu-west-1",
			}}},
		},
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(root, "settings.json"))
	if err != nil {
		t.Fatalf("no settings.json: %v", err)
	}
	// §15.1: the user's entry wins, and the notice says so.
	if !strings.Contains(string(body), "http://localhost:4000") {
		t.Errorf("the user's override lost:\n%s", body)
	}
	if !strings.Contains(string(body), "eu-west-1") {
		t.Errorf("a non-colliding user variable was dropped:\n%s", body)
	}
	if !strings.Contains(strings.Join(res.Warnings, " "), "ANTHROPIC_BASE_URL") {
		t.Errorf("no override notice: %v", res.Warnings)
	}

	// Recorded as a deep merge with its dotted key, like every other write
	// into a file the user also owns.
	l, _ := LoadLedger(root)
	rec, ok := l.Resource("model", "model")
	if !ok {
		t.Fatalf("model was not recorded: %+v", l)
	}
	f := rec.Files[0]
	if f.RelPath != "settings.json" || f.Merge != "deep" || len(f.Keys) == 0 {
		t.Errorf("record = %+v", f)
	}

	// And nothing writes an env file any more.
	if _, err := os.Stat(filepath.Join(root, ".ap-env")); !os.IsNotExist(err) {
		t.Error(".ap-env was written; model materializes into files now")
	}
}

// pi has no measured model destination. A guessed path writes to a name the
// agent never opens, so §8's warning is the honest answer.
func TestARuntimeWithNoModelDestinationWarns(t *testing.T) {
	a, _ := agentreg.Lookup("pi")
	ad, _ := AdapterFor(a)
	root := t.TempDir()
	res, err := Apply(t.Context(), Plan{
		Root: root, Adapter: ad, Now: fixedNow,
		Profile: schema.Profile{Model: &schema.Model{BaseURL: "https://llm.company.com"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(res.Warnings, " "), "pi") {
		t.Errorf("pi silently dropped the model block: %v", res.Warnings)
	}
}

// One plugin declaration, four runtimes, four different layouts. That is why
// plugins is one common type and not four artifacts with hand-written
// destinations: an artifact names ONE destination.
func TestAPluginRoutesPerRuntimeAndReportsWhatItCannot(t *testing.T) {
	src := writeTree(t, t.TempDir(), map[string]string{
		"skills/review/SKILL.md":     "# review",
		"commands/deploy.md":         "# deploy",
		"hooks/pre.sh":               "#!/bin/sh",
		"README.md":                  "docs",
		".claude-plugin/plugin.json": `{"name":"p"}`,
	})
	for _, tc := range []struct {
		runtime  string
		want     map[string]string // path under root -> content
		dropped  []string
		notThere []string
	}{
		{
			runtime: "claude",
			want: map[string]string{
				"skills/review/SKILL.md": "# review",
				"commands/deploy.md":     "# deploy",
			},
			dropped: []string{"hooks"},
		},
		{
			// pi puts commands under prompts/ and has no destination for hooks.
			runtime: "pi",
			want: map[string]string{
				"skills/review/SKILL.md": "# review",
				"prompts/deploy.md":      "# deploy",
			},
			dropped: []string{"hooks"},
		},
		{
			// codex puts commands under prompts/ like pi, and since 0.149.1
			// reads $CODEX_HOME/skills as well as the shared ~/.agents/skills.
			runtime: "codex",
			want: map[string]string{
				"skills/review/SKILL.md": "# review",
				"prompts/deploy.md":      "# deploy",
			},
			dropped: []string{"hooks"},
		},
	} {
		t.Run(tc.runtime, func(t *testing.T) {
			a, _ := agentreg.Lookup(tc.runtime)
			ad, _ := AdapterFor(a)
			root := t.TempDir()
			res, err := Apply(t.Context(), Plan{
				Root: root, Adapter: ad, Now: fixedNow,
				Profile: schema.Profile{Plugins: map[string]schema.Resource{
					"p": {Enabled: true, Source: &schema.Source{}},
				}},
				Fetched: map[string]source.Resolved{"plugin p": {Dir: src, ResolvedRef: "abc"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			for rel, want := range tc.want {
				got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
				if err != nil || string(got) != want {
					t.Errorf("%s = %q %v", rel, got, err)
				}
			}
			for _, rel := range tc.notThere {
				if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); !os.IsNotExist(err) {
					t.Errorf("%s was written; this runtime has no destination for it", rel)
				}
			}
			joined := strings.Join(res.Warnings, "\n")
			for _, kind := range tc.dropped {
				if !strings.Contains(joined, kind) {
					t.Errorf("%q was dropped with no report:\n%s", kind, joined)
				}
			}
			// Non-content is skipped SILENTLY: reporting a README would bury
			// the signal the drop warnings carry.
			for _, quiet := range []string{"README", "claude-plugin"} {
				if strings.Contains(joined, quiet) {
					t.Errorf("%q was reported; §24.1 skips non-content silently:\n%s", quiet, joined)
				}
			}
			// And the ledger records the files, not the drops.
			l, _ := LoadLedger(root)
			rec, ok := l.Resource("plugin", "p")
			if !ok {
				t.Fatalf("the plugin was not recorded: %+v", l)
			}
			if len(rec.Files) != len(tc.want) {
				t.Errorf("recorded %d files, want %d: %+v", len(rec.Files), len(tc.want), rec.Files)
			}
		})
	}
}

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
