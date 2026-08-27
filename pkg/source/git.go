package source

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Resolved is one locator turned into bytes on disk. Phase 4 materializes from
// Dir; the ledger records ResolvedRef.
//
// ResolvedRef is a RECEIPT, not a pin (§32.2). Nothing re-uses it as a
// resolution input — a ref re-resolves on every run — and it exists so drift is
// reportable without a lockfile.
type Resolved struct {
	Dir            string
	ResolvedRef    string
	SchemeUsed     Scheme
	SchemeInferred bool
	// Anonymous records that a credential was available but deliberately NOT
	// sent. §21.2 requires the withholding to be reported, and a 401 that
	// followed one must be distinguishable from an ordinary auth failure.
	Anonymous bool
}

// GitSpec is what FetchGit needs. Token is a resolved secret VALUE, held only
// for the duration of the fetch: §34 lets a resolution-time consumer read one
// transiently and forbids persisting, logging or embedding it.
type GitSpec struct {
	URL, Ref, Subpath string
	Token             string
	DeclaredScheme    string
}

// gitRunner is the seam that lets a test assert argv without a network. It is
// a package-level variable, so tests that swap it must not run in parallel.
type gitRunner func(ctx context.Context, dir string, args ...string) ([]byte, error)

var runGit gitRunner = execGit

func execGit(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	// A credential could otherwise reach a helper, a pager, or an interactive
	// prompt that blocks a headless run forever.
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=",
		"GIT_CONFIG_NOSYSTEM=1",
		"GCM_INTERACTIVE=never",
	)
	return cmd.CombinedOutput()
}

var shaPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// FetchGit resolves a ref to a SHA, publishes the tree at that SHA into the
// cache, and returns the subpath re-rooted.
//
// The SHA is the cache key, so every resource drawn from one repository at one
// ref shares a single clone. That — not a marketplace — is what makes fourteen
// skills from one repository cost one fetch.
func FetchGit(ctx context.Context, c *Cache, s GitSpec) (Resolved, error) {
	scheme, inferred := ResolveScheme(s.DeclaredScheme, hostOf(s.URL))
	res := Resolved{SchemeUsed: scheme, SchemeInferred: inferred, Anonymous: s.Token == ""}

	if s.Token != "" {
		if err := CheckCredentialTransport(s.URL); err != nil {
			return Resolved{}, err
		}
	}

	sha, err := lsRemote(ctx, s, scheme, inferred)
	if err != nil {
		return Resolved{}, err
	}
	res.ResolvedRef = sha

	entry, err := c.Publish(sha, func(dir string) error {
		return cloneAt(ctx, dir, s, scheme, sha, inferred)
	})
	if err != nil {
		return Resolved{}, err
	}

	if res.Dir, err = subtree(entry, s.Subpath); err != nil {
		return Resolved{}, err
	}
	return res, nil
}

// lsRemote turns a ref into a SHA without cloning anything. An empty ref means
// HEAD, which is the repository's own default branch — never a branch name
// guessed here.
func lsRemote(ctx context.Context, s GitSpec, scheme Scheme, inferred bool) (string, error) {
	ref := s.Ref
	if ref == "" {
		ref = "HEAD"
	}
	out, err := runGit(ctx, "", gitArgs(s, scheme, "ls-remote", "--", s.URL, ref)...)
	if err != nil {
		return "", gitError(fmt.Sprintf("resolving %s of %s", ref, s.URL), out, err, scheme, inferred, s.Token != "")
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 || !shaPattern.MatchString(fields[0]) {
		return "", fmt.Errorf("%s: ref %q not found", s.URL, ref)
	}
	return fields[0], nil
}

// cloneAt fills a staging directory with the tree at sha.
//
// It fetches the REF and then verifies the checkout landed on the SHA that
// ls-remote reported, rather than fetching the SHA directly: a server may
// refuse a by-SHA fetch (uploadpack.allowReachableSHA1InWant is off by
// default), and a local path transport ignores --depth entirely. A ref that
// moved in between is an error naming both SHAs, not a silently different
// tree.
func cloneAt(ctx context.Context, dir string, s GitSpec, scheme Scheme, sha string, inferred bool) error {
	ref := s.Ref
	if ref == "" {
		ref = "HEAD"
	}
	steps := [][]string{
		{"init", "--quiet"},
		{"remote", "add", "origin", "--", s.URL},
		{"fetch", "--quiet", "--depth", "1", "origin", ref},
		{"checkout", "--quiet", "--detach", "FETCH_HEAD"},
	}
	for _, step := range steps {
		// Only the network step carries the credential. init, remote add and
		// checkout are local and have nothing to authenticate to.
		args := step
		if step[0] == "fetch" {
			args = gitArgs(s, scheme, step...)
		}
		if out, err := runGit(ctx, dir, args...); err != nil {
			return gitError("git "+step[0]+" "+s.URL, out, err, scheme, inferred, s.Token != "")
		}
	}
	out, err := runGit(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("reading the checked-out commit: %w", err)
	}
	if got := strings.TrimSpace(string(out)); got != sha {
		return fmt.Errorf("%s: %s moved from %s to %s while fetching; re-run", s.URL, ref, sha, got)
	}
	// .git is not content. Leaving it would put a full object store in the
	// cache and hand Phase 4 a directory it would have to filter anyway.
	return os.RemoveAll(filepath.Join(dir, ".git"))
}

// gitArgs builds the argv for one git invocation. The credential goes in
// http.extraHeader and NOWHERE else.
//
// ach's comment records both reasons for keeping it out of the URL:
// /proc/<pid>/cmdline exposes it to every process on the machine, and git
// persists it on disk in remote.origin.url. Both survive the process.
func gitArgs(s GitSpec, scheme Scheme, sub ...string) []string {
	var args []string
	if s.Token != "" {
		args = append(args, "-c", "http.extraHeader=Authorization: "+AuthHeader(scheme, s.Token))
	}
	return append(args, sub...)
}

// gitError wraps a git failure. §17.1 makes naming the scheme MANDATORY on a
// 401: without it the user cannot tell a wrong scheme from a wrong token, and
// a self-hosted GitLab inferred as bearer is exactly that situation.
//
// git's own output is included, but only after being scanned for the
// credential — §34 forbids embedding one in an error, and an extraHeader can
// be echoed back by a sufficiently unhelpful remote.
func gitError(what string, out []byte, err error, scheme Scheme, inferred, hadToken bool) error {
	msg := strings.TrimSpace(string(out))
	if !hadToken {
		return fmt.Errorf("%s: %w\n%s", what, err, msg)
	}
	return fmt.Errorf("%s: %w\nauth %s\n%s", what, err, scheme.Report(inferred), msg)
}

// subtree re-roots a subpath inside a published cache entry.
//
// pkg/schema already rejected the obvious shapes at decode time; this is the
// check schema cannot make, because it has no filesystem: after resolving
// symlinks, the result must still be inside the entry. A repository can ship a
// symlink pointing anywhere, and following one would read outside the tree
// that was fetched.
func subtree(entry, subpath string) (string, error) {
	if subpath == "" {
		return entry, nil
	}
	clean := filepath.Clean(filepath.FromSlash(subpath))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("subpath %q escapes the source root", subpath)
	}
	joined := filepath.Join(entry, clean)

	real, err := filepath.EvalSymlinks(joined)
	if err != nil {
		return "", fmt.Errorf("subpath %q: %w", subpath, err)
	}
	root, err := filepath.EvalSymlinks(entry)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("subpath %q resolves outside the source root", subpath)
	}
	return real, nil
}

// hostOf is the host for scheme inference. A URL git accepts but net/url does
// not — scp-style user@host:path — yields "", which infers bearer; that is
// correct, because scp-style is SSH and carries no Authorization header at all.
func hostOf(rawURL string) string {
	i := strings.Index(rawURL, "://")
	if i < 0 {
		return ""
	}
	rest := rawURL[i+3:]
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		rest = rest[at+1:]
	}
	if slash := strings.IndexAny(rest, "/:"); slash >= 0 {
		rest = rest[:slash]
	}
	return rest
}
