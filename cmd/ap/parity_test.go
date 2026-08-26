//go:build unix

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ackstorm/agent-profile/pkg/agentreg"
	"github.com/ackstorm/agent-profile/pkg/hydrate"
	"github.com/ackstorm/agent-profile/pkg/schema"
	"github.com/ackstorm/agent-profile/pkg/source"
)

// The zero-logic-CLI claim, made falsifiable.
//
// `ach` imports pkg/ and `ach-agent` drives the binary, so "one implementation,
// three ways in" is only true if the two ways produce the same thing. A golden
// text comparison would test the PRINTER; comparing the two ROOTS tests the
// product, which is what a consumer actually receives.
//
// The ledger is compared with it, minus its timestamps: a ledger that disagreed
// about what was written would make every later uninstall and export disagree
// too.
func TestTheCLIAndTheLibraryProduceTheSameRoot(t *testing.T) {
	repo := seedRepo(t, map[string]string{
		"skills/pdf/SKILL.md":           "# pdf",
		"skills/pdf/scripts/convert.py": "print()",
	})
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	binDir := t.TempDir()
	stubExecutable(t, binDir)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MEMORY_TOKEN", "unused-by-materialization")

	dir := t.TempDir()
	path := filepath.Join(dir, "p.yaml")
	manifest := `version: "1"
name: parity
targets:
  - claude
inputs:
  secrets:
    memory-token:
      env: MEMORY_TOKEN
skills:
  pdf:
    source:
      git:
        url: ` + repo + `
        subpath: skills/pdf
mcps:
  memory:
    transport:
      type: http
      url: https://memory.company.com/mcp
      headers:
        Authorization:
          prefix: "Bearer "
          value_from:
            secret: memory-token
`
	if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	// Way in #1: the binary.
	viaCLI := filepath.Join(t.TempDir(), "cli-root")
	out, err := captureStdout(t, func() error {
		return dispatch([]string{"manifest", "apply", "claude", "--root", viaCLI, path})
	})
	if err != nil {
		t.Fatalf("the CLI failed: %v\n%s", err, out)
	}

	// Way in #2: the library, exactly as `ach` would call it.
	viaLib := filepath.Join(t.TempDir(), "lib-root")
	res, resolved, err := schema.Resolve(path, "claude", false, true)
	if err != nil {
		t.Fatal(err)
	}
	cache, err := source.NewCache(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fetched, _, err := source.Resolve(t.Context(), res.Profile, source.Opts{
		Cache: cache, ManifestDir: dir,
		Secret: func(name string) (string, error) {
			v, ok := resolved.Value("secret", name)
			if !ok {
				return "", source.ErrSecretUnset
			}
			return v, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	agent, _ := agentreg.Lookup("claude")
	adapter, err := hydrate.AdapterFor(agent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hydrate.Apply(context.Background(), hydrate.Plan{
		Root: viaLib, Adapter: adapter, Profile: res.Profile, Fetched: fetched,
	}); err != nil {
		t.Fatal(err)
	}

	cliTree, libTree := treeOf(t, viaCLI), treeOf(t, viaLib)
	for rel, sum := range cliTree {
		got, ok := libTree[rel]
		if !ok {
			t.Errorf("%s: the CLI wrote it and the library did not", rel)
			continue
		}
		if got != sum {
			t.Errorf("%s differs between the CLI and the library", rel)
		}
	}
	for rel := range libTree {
		if _, ok := cliTree[rel]; !ok {
			t.Errorf("%s: the library wrote it and the CLI did not", rel)
		}
	}
	if len(cliTree) < 4 {
		t.Fatalf("the comparison covered almost nothing (%d files); it would pass on two empty roots", len(cliTree))
	}
}

// treeOf hashes every file under root. The ledger's installedAt stamps are the
// one thing the two runs cannot agree on, so they are elided rather than
// compared — everything else in it must match.
func treeOf(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if filepath.Base(p) == ".ap-ledger.json" {
			var kept []string
			for _, line := range strings.Split(string(body), "\n") {
				if !strings.Contains(line, `"installedAt"`) {
					kept = append(kept, line)
				}
			}
			body = []byte(strings.Join(kept, "\n"))
		}
		sum := sha256.Sum256(body)
		out[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// §33.2: a root is a parameter. Two spellings answering the same question with
// a silent precedence rule is how the wrong directory gets written.
func TestARootIsNamedOnce(t *testing.T) {
	dir := t.TempDir()
	err := dispatch([]string{"manifest", "apply", "claude:plan", "--root", dir, "/nonexistent.yaml"})
	if err == nil {
		t.Fatal("a reference and --root were both accepted")
	}
	for _, want := range []string{"claude:plan", dir} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}

	// And neither is not a default: nothing may fall back to a directory
	// nobody named.
	err = dispatch([]string{"manifest", "apply", "claude", "/nonexistent.yaml"})
	if err == nil || !strings.Contains(err.Error(), "--root") {
		t.Fatalf("a bare agent name with no --root was not refused with a way forward: %v", err)
	}
}

// The exit criterion the container image exists for, asserted here too, because
// this is where it can be asserted cheaply and deterministically.
func TestApplyWorksWithNoHomeAndNoProfileNamespace(t *testing.T) {
	repo := seedRepo(t, map[string]string{"skills/pdf/SKILL.md": "# pdf"})
	binDir := t.TempDir()
	stubExecutable(t, binDir)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	// No HOME, no XDG_DATA_HOME: nothing from which a profile root could be
	// inferred. The only root is the one on the command line.
	t.Setenv("HOME", "")
	t.Setenv("XDG_DATA_HOME", "")

	dir := t.TempDir()
	path := filepath.Join(dir, "p.yaml")
	body := "version: \"1\"\nname: hydrated\ntargets:\n  - claude\nskills:\n  pdf:\n    source:\n      git:\n        url: " +
		repo + "\n        subpath: skills/pdf\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(t.TempDir(), "config")
	out, err := captureStdout(t, func() error {
		return dispatch([]string{"manifest", "apply", "claude", "--root", root, path})
	})
	if err != nil {
		t.Fatalf("apply with no HOME failed: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(root, "skills", "pdf", "SKILL.md")); err != nil {
		t.Fatalf("nothing was materialized: %v", err)
	}
	if !strings.Contains(out, root) {
		t.Errorf("the report does not name the root it wrote to:\n%s", out)
	}
}

// /dev/null is a character device. So is /dev/zero, and so is /dev/urandom.
//
// stdinIsTerminal tested os.ModeCharDevice, so anything started by systemd,
// cron or a container runtime — all of which hand a process /dev/null on stdin
// by default — was reported as sitting at a terminal, and the
// real-configuration gate printed a question instead of refusing. `docker run`
// with no -t is what found it.
//
// This program's stated rule is that a pipe is not consent. A check that
// accepts any character device accepts one that can deliver a "y".
func TestStdinIsNotATerminalJustBecauseItIsACharacterDevice(t *testing.T) {
	for _, dev := range []string{os.DevNull, "/dev/zero"} {
		f, err := os.Open(dev)
		if err != nil {
			t.Skipf("%s is not openable here: %v", dev, err)
		}
		fi, err := f.Stat()
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode()&os.ModeCharDevice == 0 {
			t.Fatalf("%s is not a character device here, so this test proves nothing", dev)
		}

		saved := os.Stdin
		os.Stdin = f
		got := stdinIsTerminal()
		os.Stdin = saved
		_ = f.Close()

		if got {
			t.Errorf("%s was reported as a terminal", dev)
		}
	}
}
