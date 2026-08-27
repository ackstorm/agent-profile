//go:build unix

package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// lifecycleEnv seeds a repository and a throwaway home, and returns the repo
// URL and the data root.
func lifecycleEnv(t *testing.T) (repo, data string) {
	t.Helper()
	repo = seedRepo(t, map[string]string{
		"skills/pdf/SKILL.md":           "# pdf",
		"skills/pdf/scripts/convert.py": "print()",
	})
	data = t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	binDir := t.TempDir()
	stubExecutable(t, binDir)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return repo, data
}

func installPDF(t *testing.T, repo string) string {
	t.Helper()
	out, err := captureStdout(t, func() error {
		return dispatch([]string{"install", "claude:plan", "skill", "pdf", "--git", repo, "--subpath", "skills/pdf"})
	})
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	return out
}

// The exit criterion: install with no manifest ANYWHERE on disk.
func TestInstallNeedsNoManifestAnywhereOnDisk(t *testing.T) {
	repo, data := lifecycleEnv(t)
	installPDF(t, repo)

	root := filepath.Join(data, "agent-profile", "profiles", "claude", "plan")
	for _, rel := range []string{"skills/pdf/SKILL.md", "skills/pdf/scripts/convert.py", ".ap-ledger.json"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s was not written: %v", rel, err)
		}
	}
	// Nothing wrote a manifest. "A manifest is an input" is only true if the
	// imperative path does not quietly create one to remember things in.
	err := filepath.WalkDir(data, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if ext := filepath.Ext(p); ext == ".yaml" || ext == ".yml" {
			t.Errorf("install wrote a manifest at %s", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// `ap list <ref>` reads the LEDGER. A manifest says nothing about what is
// installed, which is why this is not `ap manifest render`.
func TestListOfAReferenceReportsWhatTheLedgerHolds(t *testing.T) {
	repo, _ := lifecycleEnv(t)

	out, err := captureStdout(t, func() error { return dispatch([]string{"list", "claude:plan"}) })
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out, "nothing is installed") {
		t.Errorf("an empty profile did not say so:\n%s", out)
	}

	installPDF(t, repo)
	out, err = captureStdout(t, func() error { return dispatch([]string{"list", "claude:plan"}) })
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, want := range []string{"skill", "pdf", "2 file(s)", "ap uninstall claude:plan"} {
		if !strings.Contains(out, want) {
			t.Errorf("the listing lacks %q:\n%s", want, out)
		}
	}
}

// The dry run prints the verdicts the real run acts on, and touches nothing.
func TestUninstallDryRunPreviewsAndThenRemoves(t *testing.T) {
	repo, data := lifecycleEnv(t)
	installPDF(t, repo)
	root := filepath.Join(data, "agent-profile", "profiles", "claude", "plan")

	out, err := captureStdout(t, func() error {
		return dispatch([]string{"uninstall", "claude:plan", "skill", "pdf", "--dry-run"})
	})
	if err != nil {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "would remove") || !strings.Contains(out, "nothing was removed") {
		t.Errorf("the dry run does not say it did nothing:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(root, "skills", "pdf", "SKILL.md")); err != nil {
		t.Fatalf("the dry run removed a file: %v", err)
	}

	out, err = captureStdout(t, func() error {
		return dispatch([]string{"uninstall", "claude:plan", "skill", "pdf"})
	})
	if err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(root, "skills", "pdf")); !os.IsNotExist(err) {
		t.Error("the skill survived the removal")
	}

	out, err = captureStdout(t, func() error { return dispatch([]string{"list", "claude:plan"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "nothing is installed") {
		t.Errorf("the ledger still claims the skill:\n%s", out)
	}
}

// §35.1 as amended by D4: v1 records no marketplace definition, so a bare ref
// has nothing to resolve against. Refused BY NAME — a ref quietly treated as a
// literal resource name would fetch nothing and report success.
func TestInstallRefusesAMarketplaceRefByName(t *testing.T) {
	repo, _ := lifecycleEnv(t)
	out, err := captureStdout(t, func() error {
		return dispatch([]string{"install", "claude:plan", "skill", "pdf@anthropic-skills", "--git", repo})
	})
	if err == nil {
		t.Fatalf("a marketplace ref was accepted:\n%s", out)
	}
	for _, want := range []string{"anthropic-skills", "pdf", "ap manifest apply"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
}

// An mcp server has no source; installing one imperatively would mean spelling
// a whole transport in flags, which is the manifest with worse syntax.
func TestInstallRefusesAKindWithNoLocator(t *testing.T) {
	lifecycleEnv(t)
	err := dispatch([]string{"install", "claude:plan", "mcp", "memory"})
	if err == nil || !strings.Contains(err.Error(), "ap manifest apply") {
		t.Fatalf("mcp was not refused with somewhere to go: %v", err)
	}
}

// The exit criterion: export a hand-built root, re-apply it, get the same root.
func TestManifestExportOfAHandBuiltRootReApplies(t *testing.T) {
	repo, data := lifecycleEnv(t)
	installPDF(t, repo)

	exported, err := captureStdout(t, func() error {
		return dispatch([]string{"manifest", "export", "claude:plan"})
	})
	if err != nil {
		t.Fatalf("export: %v\n%s", err, exported)
	}
	for _, want := range []string{"version:", "name: plan", "skills:", "pdf:", repo} {
		if !strings.Contains(exported, want) {
			t.Errorf("the exported manifest lacks %q:\n%s", want, exported)
		}
	}

	path := filepath.Join(t.TempDir(), "exported.yaml")
	if err := os.WriteFile(path, []byte(exported), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := captureStdout(t, func() error {
		return dispatch([]string{"manifest", "apply", path, "--target", "claude", "--profile", "copy"})
	})
	if err != nil {
		t.Fatalf("re-applying the export failed: %v\n%s\n%s", err, out, exported)
	}

	base := filepath.Join(data, "agent-profile", "profiles", "claude")
	for _, rel := range []string{"skills/pdf/SKILL.md", "skills/pdf/scripts/convert.py"} {
		a, err := os.ReadFile(filepath.Join(base, "plan", filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(base, "copy", filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("%s is missing from the re-applied root: %v", rel, err)
		}
		if string(a) != string(b) {
			t.Errorf("%s differs after export and re-apply", rel)
		}
	}
}
