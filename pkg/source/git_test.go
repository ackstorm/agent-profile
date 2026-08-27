package source

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var errStubbed = errors.New("stubbed")

// swapRunner replaces the git seam. runGit is a package-level variable, so a
// test using this must NOT call t.Parallel.
func swapRunner(r gitRunner) func() {
	old := runGit
	runGit = r
	return func() { runGit = old }
}

// recordArgv captures each invocation as a SLICE, never as a joined string. A
// check written against joined output cannot tell one argument from two, which
// is the whole property under test — CLAUDE.md records that exact mistake in
// the sandbox's arg:[…] versus argv: check.
func recordArgv(seen *[][]string, err error) gitRunner {
	return func(_ context.Context, _ string, args ...string) ([]byte, error) {
		*seen = append(*seen, slices.Clone(args))
		return nil, err
	}
}

func mustCache(t *testing.T) *Cache {
	t.Helper()
	c, err := NewCache(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// newBareRepo builds a real repository in a temp dir and returns a path git can
// clone from. No network, ever: a test that needs one goes red for reasons
// nobody controls, which is the lesson docs/references/SMOKE.md records from
// three such checks.
func newBareRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	work := t.TempDir()
	gitIn(t, work, "init", "--quiet", "--initial-branch=main")
	for rel, body := range files {
		writeIn(t, work, rel, body)
	}
	gitIn(t, work, "add", "-A")
	gitIn(t, work, "commit", "--quiet", "-m", "seed")
	return work
}

// mustGit runs git in repo and returns its output, failing the test if it
// errors. Same fixed identity and isolated HOME as newBareRepo built inline
// before the ref-matching tests needed to reach for it too.
func mustGit(t *testing.T, repo string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e",
		"GIT_CONFIG_NOSYSTEM=1", "HOME="+repo,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func gitIn(t *testing.T, repo string, args ...string) {
	t.Helper()
	mustGit(t, repo, args...)
}

func writeIn(t *testing.T, repo, rel, body string) {
	t.Helper()
	p := filepath.Join(repo, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// `git ls-remote <url> main` is a PATTERN match against the tail of every ref
// name, so a branch called `daisy/caffeinate/main` is one of the answers — and
// it sorts ahead of `refs/heads/main`. Taking the first line resolved `main` to
// that branch's SHA while the fetch resolved it properly, and the mismatch was
// reported as "main moved while fetching; re-run": a race that never happened,
// advising a re-run that could never help.
//
// Found against anthropics/claude-plugins-official, which has two such
// branches. Reproduced here with a local repository, because a test that needs
// the network goes red for reasons nobody controls.
func TestARefIsMatchedExactlyAndNotAsASuffix(t *testing.T) {
	repo := newBareRepo(t, map[string]string{"a.md": "main"})
	gitIn(t, repo, "checkout", "--quiet", "-b", "daisy/caffeinate/main")
	writeIn(t, repo, "a.md", "decoy")
	gitIn(t, repo, "commit", "--quiet", "-am", "decoy")
	gitIn(t, repo, "checkout", "--quiet", "main")

	got, err := FetchGit(t.Context(), mustCache(t), GitSpec{URL: repo, Ref: "main"})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(got.Dir, "a.md")); err != nil || string(b) != "main" {
		t.Errorf("a.md = %q %v, want the content of refs/heads/main", b, err)
	}
}

// An annotated tag reports its own object AND the commit it points at. The
// checkout lands on the commit, so recording the tag object would both disagree
// with the SHA check and put a hash naming no tree in the ledger.
func TestAnAnnotatedTagResolvesToTheCommitItPointsAt(t *testing.T) {
	repo := newBareRepo(t, map[string]string{"a.md": "tagged"})
	gitIn(t, repo, "tag", "-a", "v1.0", "-m", "release")

	got, err := FetchGit(t.Context(), mustCache(t), GitSpec{URL: repo, Ref: "v1.0"})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	head := strings.TrimSpace(string(mustGit(t, repo, "rev-parse", "v1.0^{commit}")))
	if got.ResolvedRef != head {
		t.Errorf("ResolvedRef = %q, want the commit %q", got.ResolvedRef, head)
	}
}

func TestFetchGitResolvesARefToASHAAndSlicesASubpath(t *testing.T) {
	repo := newBareRepo(t, map[string]string{
		"skills/pdf/SKILL.md": "# pdf",
		"README.md":           "top",
	})
	c := mustCache(t)
	got, err := FetchGit(t.Context(), c, GitSpec{URL: repo, Ref: "main", Subpath: "skills/pdf"})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if !shaPattern.MatchString(got.ResolvedRef) {
		t.Errorf("ResolvedRef = %q, want a 40-hex SHA", got.ResolvedRef)
	}
	if b, err := os.ReadFile(filepath.Join(got.Dir, "SKILL.md")); err != nil || string(b) != "# pdf" {
		t.Errorf("subpath was not re-rooted: %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(got.Dir, "README.md")); err == nil {
		t.Error("content outside the subpath leaked in")
	}
	// .git is not content. Leaving it would put an object store in the cache
	// and hand Phase 4 a directory it would have to filter anyway.
	if _, err := os.Stat(filepath.Join(c.Path(got.ResolvedRef), ".git")); !os.IsNotExist(err) {
		t.Errorf(".git survived into the cache entry: %v", err)
	}
}

// The SHA is the cache key, so every resource drawn from one repository at one
// ref shares a single clone. That — not a marketplace — is what makes fourteen
// skills from one repository cost one fetch, and it is why marketplaces could
// be cut from v1 without losing the property.
func TestTwoSubpathsOfOneRepoShareOneClone(t *testing.T) {
	repo := newBareRepo(t, map[string]string{
		"skills/pdf/SKILL.md":  "# pdf",
		"skills/xlsx/SKILL.md": "# xlsx",
	})
	c := mustCache(t)
	pdf, err := FetchGit(t.Context(), c, GitSpec{URL: repo, Ref: "main", Subpath: "skills/pdf"})
	if err != nil {
		t.Fatal(err)
	}
	xlsx, err := FetchGit(t.Context(), c, GitSpec{URL: repo, Ref: "main", Subpath: "skills/xlsx"})
	if err != nil {
		t.Fatal(err)
	}
	if pdf.ResolvedRef != xlsx.ResolvedRef {
		t.Fatalf("two fetches of one ref resolved differently: %s vs %s", pdf.ResolvedRef, xlsx.ResolvedRef)
	}
	ents, err := os.ReadDir(filepath.Join(c.Root, objectsDir))
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 {
		t.Errorf("cache holds %d entries, want 1 shared clone", len(ents))
	}
	if pdf.Dir == xlsx.Dir {
		t.Error("both subpaths re-rooted to the same directory")
	}
}

func TestFetchGitNeverPutsTheCredentialInTheURL(t *testing.T) {
	// The credential must reach git as an extraHeader. In the URL it would be
	// visible in /proc/<pid>/cmdline to every process on the machine AND
	// persisted on disk in remote.origin.url (§17.2).
	const token = "glpat-SECRET"
	var seen [][]string
	defer swapRunner(recordArgv(&seen, errStubbed))()

	_, _ = FetchGit(t.Context(), mustCache(t), GitSpec{
		URL: "https://gitlab.acme.internal/x.git", Ref: "main", Token: token,
	})
	if len(seen) == 0 {
		t.Fatal("git was never invoked")
	}
	var sawHeader bool
	for _, argv := range seen {
		for _, arg := range argv {
			if strings.HasPrefix(arg, "http.extraHeader=Authorization: ") {
				sawHeader = true
				continue
			}
			// Every OTHER argument must be free of the token. Asserting on a
			// joined string would pass even if the token were spliced into the
			// URL argument, because the header argument contains it legitimately.
			if strings.Contains(arg, token) {
				t.Errorf("the token reached git outside the header, in %q", arg)
			}
		}
	}
	if !sawHeader {
		t.Errorf("no http.extraHeader in argv: %v", seen)
	}
}

// An anonymous fetch must carry no -c at all, or a later reader would think a
// credential was involved.
func TestFetchGitSendsNoHeaderWithoutAToken(t *testing.T) {
	var seen [][]string
	defer swapRunner(recordArgv(&seen, errStubbed))()

	_, _ = FetchGit(t.Context(), mustCache(t), GitSpec{URL: "https://github.com/o/r.git", Ref: "main"})
	for _, argv := range seen {
		if slices.Contains(argv, "-c") {
			t.Errorf("anonymous fetch carried a -c override: %v", argv)
		}
	}
}

func TestFetchGitRefusesToSendACredentialOverPlaintext(t *testing.T) {
	_, err := FetchGit(t.Context(), mustCache(t), GitSpec{
		URL: "http://gitlab.acme.internal/x.git", Ref: "main", Token: "glpat-SECRET",
	})
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("err = %v; §17.2 refuses a credential over http://", err)
	}
	if strings.Contains(err.Error(), "glpat-SECRET") {
		t.Errorf("the refusal repeats the credential: %v", err)
	}
	// Anonymous over a non-https transport is not this guard's business, and
	// refusing it would break every local repository including these tests.
	repo := newBareRepo(t, map[string]string{"a": "b"})
	if _, err := FetchGit(t.Context(), mustCache(t), GitSpec{URL: repo, Ref: "main"}); err != nil {
		t.Errorf("anonymous fetch refused: %v", err)
	}
}

// §17.1 makes this mandatory: a 401 that does not name the scheme leaves the
// user unable to tell a wrong scheme from a wrong token, and a self-hosted
// GitLab inferred as bearer is exactly that situation.
func TestAFailedAuthenticatedFetchNamesTheSchemeItUsed(t *testing.T) {
	defer swapRunner(func(context.Context, string, ...string) ([]byte, error) {
		return []byte("remote: HTTP Basic: Access denied\nfatal: Authentication failed"), errStubbed
	})()

	_, err := FetchGit(t.Context(), mustCache(t), GitSpec{
		URL: "https://code.acme.internal/x.git", Ref: "main", Token: "glpat-SECRET",
	})
	if err == nil {
		t.Fatal("a failing fetch succeeded")
	}
	// code.acme.internal is named neither gitlab* nor git.*, so it infers
	// bearer — the case the field exists for.
	if !strings.Contains(err.Error(), "bearer") || !strings.Contains(err.Error(), "inferred") {
		t.Errorf("error does not name the inferred scheme: %v", err)
	}
	if strings.Contains(err.Error(), "glpat-SECRET") {
		t.Errorf("the error embeds the credential: %v", err)
	}
}

// A repository can ship a symlink pointing anywhere. Following one would read
// outside the tree that was fetched — the check pkg/schema cannot make,
// because it validates a string and has no filesystem.
func TestSubpathMayNotEscapeThroughASymlink(t *testing.T) {
	entry := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(entry, "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(entry, "real"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := subtree(entry, "escape"); err == nil {
		t.Error("a symlink out of the tree was followed")
	}
	for _, bad := range []string{"../outside", "..", "/etc"} {
		if _, err := subtree(entry, bad); err == nil {
			t.Errorf("subpath %q was accepted", bad)
		}
	}
	if got, err := subtree(entry, "real"); err != nil || filepath.Base(got) != "real" {
		t.Errorf("a legitimate subpath was refused: %q %v", got, err)
	}
}
