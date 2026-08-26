# Phase 3 — Sources and Cache Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: `superpowers:subagent-driven-development`
> (recommended) or `superpowers:executing-plans` to implement this plan
> task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** Turn every locator in an effective profile into verified bytes in a
content-addressed cache — git, archive and local — with no lockfile, no
materialization, and the two credential rules of SPEC v0.6.1 enforced.

**Architecture:** One new package `pkg/source`, built bottom-up: a cache that
stages and publishes atomically; a git fetcher that shells out to `git` and
never puts a credential in a URL; an archive fetcher that verifies a digest
before extracting; and a resolver that walks an effective profile and returns
one resolved tree per active locator. `pkg/schema` is consumed, never modified.

**Spec of record:** `docs/specs/agent-profile-declarative-spec-v0.6.2.md`

**Status: COMPLETE.** All nine tasks landed. Gates: `verify`, `crossbuild`,
`secrets`, `fuzz` (with the new `FuzzExtractTar`), `sandbox` — all green;
`sandbox` unchanged, as required, because Phase 3 materializes nothing.
Fourteen mutation tests recorded across the phase. What the plan did not
predict is written up at the end of this file.

**Tech Stack:** Go 1.25, standard library only. `pkg/source` carries **no build
tag** and must compile for windows.

## Global Constraints

Every task's requirements implicitly include these.

- **Go 1.25 floor is a security floor.** Never lowered.
- **`pkg/**` carries no build tag** and must compile for `windows/amd64`;
  `make crossbuild` is the gate.
- **`pkg/` never imports `internal/`** — `pkg/boundary_test.go` enforces it.
- **`pkg/` never reads `$HOME` implicitly.** The cache root is a parameter.
- **Standard library only.** Phase 3 adds no dependency. `git` is invoked as a
  subprocess, not linked.
- **Copied `ach` code keeps attribution** — a comment naming the source path,
  and its `// SPDX-License-Identifier: Apache-2.0` header.
- **Guards get mutation-tested.** After adding a guard: revert it, run its test,
  confirm the test fails, restore.
- **No host toolchain.** Every Go command goes through a `make` target.
- **Commits are conventional**, imperative subject <72 chars.

## What Phase 3 does NOT do

Stated so no task grows one: no lockfile, no `--frozen`, no pinning (§32 — a
`ref` re-resolves on every run); no materialization into any root (Phase 4); no
ledger (Phase 4); no marketplace item resolution (Phase 6 — §21.2's guard is
*specified* here in Task 8 and *applied* there).

---

## File Structure

| File | Responsibility |
|---|---|
| `pkg/source/doc.go` | Package doc: what a resolved source is, and the two credential rules |
| `pkg/source/cache.go` | Content-addressed cache: stage to tmp, publish by rename, sweep |
| `pkg/source/cache_test.go` | Atomicity, reuse, sweep |
| `pkg/source/transport.go` | §17.2 TLS rule and §21.2 endpoint comparison — the two credential guards, together, because they answer one question |
| `pkg/source/transport_test.go` | Both guards, both mutation-tested |
| `pkg/source/scheme.go` | §17.1 inference and the mandatory report |
| `pkg/source/scheme_test.go` | Inference table, and that the choice is always reported |
| `pkg/source/git.go` | Clone at a ref, resolve to a SHA, slice a subpath, header auth |
| `pkg/source/git_test.go` | Against local bare repositories — no network |
| `pkg/source/archive.go` | Fetch, verify digest **before** extract, safe extract |
| `pkg/source/archive_test.go` | Digest mismatch, traversal, symlink, size cap |
| `pkg/source/local.go` | A directory on disk, with `subpath` |
| `pkg/source/resolve.go` | `Resolve(schema.Profile, Opts) (map[string]Resolved, error)` |
| `pkg/source/resolve_test.go` | Ordering, secret resolution, error surfaces |

One decision locked here: **`transport.go` holds both credential guards.** They
are one question — "may this credential go to this endpoint?" — asked at two
moments, and splitting them is how one gets updated and the other does not.

---

### Task 1: The cache

**Files:** Create `pkg/source/doc.go`, `pkg/source/cache.go`, `pkg/source/cache_test.go`

**Interfaces:**
- Produces: `type Cache struct{ Root string }`, `NewCache(root string) (*Cache, error)`,
  `(*Cache).Path(key string) string`, `(*Cache).Has(key string) bool`,
  `(*Cache).Publish(key string, fill func(dir string) error) (string, error)`.
  Every later task consumes `Publish`.

- [x] **Step 1: Write the failing test**

```go
package source

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPublishIsAtomicAndReusesAnExistingEntry(t *testing.T) {
	c, err := NewCache(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	fill := func(dir string) error {
		calls++
		return os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o600)
	}
	p1, err := c.Publish("k", fill)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := c.Publish("k", fill)
	if err != nil {
		t.Fatal(err)
	}
	if p1 != p2 || calls != 1 {
		t.Errorf("second Publish refilled: calls = %d", calls)
	}
	// A fill that fails must leave NOTHING behind. A half-written entry is
	// worse than none: the next run would find Has(key) true and serve a
	// truncated tree as if it were verified content.
	if _, err := c.Publish("bad", func(dir string) error {
		_ = os.WriteFile(filepath.Join(dir, "half"), []byte("y"), 0o600)
		return os.ErrInvalid
	}); err == nil {
		t.Fatal("a failing fill was published")
	}
	if c.Has("bad") {
		t.Error("a failed fill left a published entry")
	}
	if ents, _ := os.ReadDir(filepath.Join(c.Root, "tmp")); len(ents) != 0 {
		t.Errorf("tmp not swept: %d entries", len(ents))
	}
}
```

- [x] **Step 2: Run it, verify it fails**
  `make test-one T=TestPublishIsAtomic P=./pkg/source/` — Expected: FAIL, package does not exist.

- [x] **Step 3: Implement**

Port the shape of `ach/internal/cachefs` (`bootstrap.go`, `stage.go`, `sweep.go`),
which is dependency-free.

```go
// Publish fills a fresh temporary directory and moves it into place under key.
// The rename is what makes an entry atomic: a reader either sees no entry or a
// complete one, never a partial fill.
//
// A failed fill removes the temporary directory and publishes nothing. This is
// not tidiness: Has(key) is the only thing distinguishing verified content from
// bytes that arrived, so a half-published entry would be served as if it had
// passed its digest check.
func (c *Cache) Publish(key string, fill func(dir string) error) (string, error) {
	final := c.Path(key)
	if c.Has(key) {
		return final, nil
	}
	tmp, err := os.MkdirTemp(filepath.Join(c.Root, "tmp"), "stage-")
	if err != nil {
		return "", err
	}
	if err := fill(tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return "", err
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.RemoveAll(tmp)
		// A concurrent publisher winning the race is success, not failure:
		// the entry it wrote is the same content under the same key.
		if c.Has(key) {
			return final, nil
		}
		return "", err
	}
	return final, nil
}
```

`NewCache` creates `<root>/objects` and `<root>/tmp` at `0o700` and sweeps any
`stage-*` left by a killed process.

- [x] **Step 4: Run it, verify it passes.** `make test-one T=TestPublishIsAtomic P=./pkg/source/`
- [x] **Step 5: Commit**

```bash
git add pkg/source/doc.go pkg/source/cache.go pkg/source/cache_test.go
git commit -m "feat(source): atomic content-addressed cache"
```

---

### Task 2: The two credential guards

§17.2 (never over non-TLS) and §21.2 (never to a foreign endpoint). Both are
mutation-tested; §21.2 is the specification's one mandatory security guard.

**Files:** Create `pkg/source/transport.go`, `pkg/source/transport_test.go`

**Interfaces:**
- Produces: `CheckCredentialTransport(rawURL string) error`,
  `SameEndpoint(a, b string) (bool, error)`. Tasks 4, 5 and Phase 6 consume both.

- [x] **Step 1: Write the failing tests**

```go
func TestACredentialMayNotTravelOverNonTLS(t *testing.T) {
	if err := CheckCredentialTransport("https://gitlab.acme.internal/x.git"); err != nil {
		t.Errorf("https refused: %v", err)
	}
	for _, u := range []string{
		"http://gitlab.acme.internal/x.git",
		"http://127.0.0.1:8080/x.tgz",
	} {
		err := CheckCredentialTransport(u)
		if err == nil {
			t.Errorf("%q accepted; a credential must not cross plaintext", u)
			continue
		}
		if !strings.Contains(err.Error(), "http://") {
			t.Errorf("error %q does not name the scheme", err)
		}
	}
}

func TestSameEndpointComparesHostAndEffectivePort(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		// v0.6.1: the effective port is the explicit one or the scheme's
		// default, so these are one endpoint.
		{"https://gl.acme.internal/a.git", "https://gl.acme.internal:443/b.git", true},
		{"https://GL.Acme.Internal/a.git", "https://gl.acme.internal/b.git", true},
		// A different port on the same host can be a different service.
		{"https://gl.acme.internal/a.git", "https://gl.acme.internal:8443/b.git", false},
		{"https://gl.acme.internal/a.git", "https://github.com/b.git", false},
		// A subdomain is not the same host. This is the case the guard exists
		// for: a marketplace entry one label away reads as "ours" and is not.
		{"https://gl.acme.internal/a.git", "https://evil.gl.acme.internal/b.git", false},
	} {
		got, err := SameEndpoint(tc.a, tc.b)
		if err != nil {
			t.Fatalf("%s vs %s: %v", tc.a, tc.b, err)
		}
		if got != tc.want {
			t.Errorf("SameEndpoint(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
```

- [x] **Step 2: Run them, verify they fail**
  `make test-one T='TestACredential|TestSameEndpoint' P=./pkg/source/` — Expected: FAIL, undefined.

- [x] **Step 3: Implement**

```go
// CheckCredentialTransport refuses to let a credential cross plaintext (§17.2).
// It is called before a fetch that WOULD carry one, never on an anonymous
// fetch: an http:// URL with no credential is merely unwise, and refusing it
// would break local test servers for no security gain.
func CheckCredentialTransport(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parse %q: %w", rawURL, err)
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return fmt.Errorf("%s: refusing to send a credential over %s:// — use https", u.Host, u.Scheme)
	}
	return nil
}

// SameEndpoint reports whether two URLs name the same host AND effective port
// (§21.2, tightened in v0.6.1). The effective port is the explicit one or the
// scheme's default, so https://h and https://h:443 are one endpoint — while a
// different port on the same host may be an entirely different service, and
// does not inherit the credential.
//
// Host comparison is case-insensitive and exact. A subdomain is NOT the same
// host: "evil.gl.acme.internal" reads as ours to a human skimming a catalogue,
// which is exactly the confusion this guard exists to refuse.
func SameEndpoint(a, b string) (bool, error) {
	ua, err := url.Parse(a)
	if err != nil {
		return false, fmt.Errorf("parse %q: %w", a, err)
	}
	ub, err := url.Parse(b)
	if err != nil {
		return false, fmt.Errorf("parse %q: %w", b, err)
	}
	return strings.EqualFold(ua.Hostname(), ub.Hostname()) &&
		effectivePort(ua) == effectivePort(ub), nil
}

func effectivePort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return "443"
	case "http":
		return "80"
	case "ssh":
		return "22"
	}
	return ""
}
```

- [x] **Step 4: Run them, verify they pass.**
- [x] **Step 5: Mutation-test both guards**
  - Make `CheckCredentialTransport` return `nil` unconditionally. Run
    `make test-one T=TestACredentialMayNotTravelOverNonTLS P=./pkg/source/`.
    Expected: FAIL. Restore.
  - Make `effectivePort` return `""` always (so port is ignored). Run
    `make test-one T=TestSameEndpointComparesHostAndEffectivePort P=./pkg/source/`.
    Expected: FAIL on the `:8443` case. Restore.
  - Change `EqualFold(Hostname…)` to `strings.HasSuffix`. Expected: FAIL on the
    subdomain case. Restore.

  If any of the three still passes, the test is wrong — fix the test first.

- [x] **Step 6: Commit**

```bash
git add pkg/source/transport.go pkg/source/transport_test.go
git commit -m "feat(source): refuse a credential over plaintext or to a foreign endpoint"
```

---

### Task 3: Scheme inference, and the mandatory report

§17.1. The rule that makes inference acceptable is that it is always disclosed —
so the report is not a nicety, it is the condition.

**Files:** Create `pkg/source/scheme.go`, `pkg/source/scheme_test.go`

**Interfaces:**
- Consumes: `schema.GitAuth` (Phase 1 Task 14).
- Produces: `type Scheme string`, `SchemeBearer`, `SchemeBasicOAuth2`,
  `ResolveScheme(declared, host string) (Scheme, bool)` — the bool is
  `inferred`. `AuthHeader(s Scheme, token string) string`.

- [x] **Step 1: Write the failing test**

```go
func TestSchemeIsInferredFromHostAndAlwaysReported(t *testing.T) {
	for _, tc := range []struct {
		declared, host string
		want           Scheme
		inferred       bool
	}{
		{"", "gitlab.com", SchemeBasicOAuth2, true},
		{"", "gitlab.acme.internal", SchemeBasicOAuth2, true},
		{"", "git.acme.internal", SchemeBasicOAuth2, true},
		{"", "github.com", SchemeBearer, true},
		// The case that made the field necessary: a self-hosted GitLab named
		// neither gitlab* nor git.* infers wrong, and only an explicit value
		// can fix it.
		{"", "code.acme.internal", SchemeBearer, true},
		{"basic-oauth2", "code.acme.internal", SchemeBasicOAuth2, false},
		{"bearer", "gitlab.com", SchemeBearer, false},
	} {
		got, inferred := ResolveScheme(tc.declared, tc.host)
		if got != tc.want || inferred != tc.inferred {
			t.Errorf("ResolveScheme(%q,%q) = %v,%v want %v,%v",
				tc.declared, tc.host, got, inferred, tc.want, tc.inferred)
		}
	}
}

func TestAuthHeaderMatchesEachProvidersMeasuredForm(t *testing.T) {
	if got := AuthHeader(SchemeBearer, "tok"); got != "Bearer tok" {
		t.Errorf("bearer = %q", got)
	}
	// Measured against a real self-hosted GitLab: Bearer -> 401,
	// Basic base64("oauth2:"+token) -> 200.
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("oauth2:tok"))
	if got := AuthHeader(SchemeBasicOAuth2, "tok"); got != want {
		t.Errorf("basic-oauth2 = %q, want %q", got, want)
	}
}
```

- [x] **Step 2: Run it, verify it fails.**
- [x] **Step 3: Implement**

```go
// ResolveScheme returns the scheme to use and whether it was inferred.
//
// The bool is the point. §17.1 admits inference here only because it is a
// protocol default with a declared escape hatch AND a mandatory disclosure:
// callers must report the scheme they used, and MUST name it in a 401. An
// inference that is invisible when it succeeds is undebuggable when it fails,
// and a self-hosted GitLab on a host named neither gitlab* nor git.* is exactly
// that case.
func ResolveScheme(declared, host string) (Scheme, bool) {
	if declared != "" {
		return Scheme(declared), false
	}
	h := strings.ToLower(host)
	if strings.Contains(h, "gitlab") || strings.HasPrefix(h, "git.") {
		return SchemeBasicOAuth2, true
	}
	return SchemeBearer, true
}
```

- [x] **Step 4: Run it, verify it passes.**
- [x] **Step 5: Commit**

```bash
git add pkg/source/scheme.go pkg/source/scheme_test.go
git commit -m "feat(source): infer the auth scheme from host, and report it"
```

---

### Task 4: The git fetcher

**Files:** Create `pkg/source/git.go`, `pkg/source/git_test.go`

**Interfaces:**
- Consumes: `Cache.Publish` (Task 1), `CheckCredentialTransport` (Task 2),
  `ResolveScheme`/`AuthHeader` (Task 3).
- Produces: `type GitSpec struct{ URL, Ref, Subpath, Token, DeclaredScheme string }`,
  `FetchGit(ctx, c *Cache, s GitSpec) (Resolved, error)`,
  `type Resolved struct{ Dir, ResolvedRef string; SchemeUsed Scheme; SchemeInferred bool; Anonymous bool }`.
  Tasks 7, 8 and Phase 4 consume `Resolved`.

- [x] **Step 1: Write the failing test**

Tests run against a local bare repository created with `git init --bare` in a
`t.TempDir()`. **No network, ever** — a test that needs one is a test that goes
red for reasons nobody controls, which is the lesson `docs/references/SMOKE.md`
records from three such checks.

```go
func TestFetchGitResolvesARefToASHAAndSlicesASubpath(t *testing.T) {
	repo := newBareRepo(t, map[string]string{
		"skills/pdf/SKILL.md": "# pdf",
		"README.md":           "top",
	})
	c, _ := NewCache(t.TempDir())
	got, err := FetchGit(t.Context(), c, GitSpec{URL: repo, Ref: "main", Subpath: "skills/pdf"})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(got.ResolvedRef) != 40 {
		t.Errorf("ResolvedRef = %q, want a 40-hex SHA", got.ResolvedRef)
	}
	if _, err := os.Stat(filepath.Join(got.Dir, "SKILL.md")); err != nil {
		t.Errorf("subpath was not re-rooted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(got.Dir, "README.md")); err == nil {
		t.Error("content outside the subpath leaked in")
	}
}

func TestFetchGitNeverPutsTheCredentialInTheURL(t *testing.T) {
	// The credential must reach git as an extraHeader. In the URL it would be
	// visible in /proc/<pid>/cmdline to every process on the machine AND
	// persisted on disk in remote.origin.url (§17.2).
	var seen []string
	restore := swapRunner(func(_ context.Context, dir string, args ...string) ([]byte, error) {
		seen = append(seen, strings.Join(args, " "))
		return nil, errStubbed
	})
	defer restore()
	_, _ = FetchGit(t.Context(), mustCache(t), GitSpec{
		URL: "https://gitlab.acme.internal/x.git", Ref: "main", Token: "glpat-SECRET",
	})
	joined := strings.Join(seen, "\n")
	if strings.Contains(joined, "glpat-SECRET@") || strings.Contains(joined, "://glpat-SECRET") {
		t.Fatal("the token reached git in the URL position")
	}
	if !strings.Contains(joined, "http.extraHeader") {
		t.Errorf("no extraHeader in argv:\n%s", joined)
	}
}

func TestFetchGitRefusesToSendACredentialOverPlaintext(t *testing.T) {
	_, err := FetchGit(t.Context(), mustCache(t), GitSpec{
		URL: "http://gitlab.acme.internal/x.git", Ref: "main", Token: "glpat-SECRET",
	})
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Errorf("err = %v; §17.2 refuses a credential over http://", err)
	}
	// Anonymous over http is not this guard's business.
	repo := newBareRepo(t, map[string]string{"a": "b"})
	if _, err := FetchGit(t.Context(), mustCache(t), GitSpec{URL: repo, Ref: "main"}); err != nil {
		t.Errorf("anonymous fetch refused: %v", err)
	}
}
```

- [x] **Step 2: Run them, verify they fail.**
- [x] **Step 3: Implement**

Port the shape of `ach/internal/gitfetch`. It shells out to `git` — no
dependency — and its argv is the security boundary:

```go
// gitArgs builds the argv for one git invocation. The credential goes in
// http.extraHeader and NOWHERE else. `ach`'s comment records both reasons for
// keeping it out of the URL: /proc/<pid>/cmdline exposes it to every process on
// the machine, and git persists it on disk in remote.origin.url.
func gitArgs(s GitSpec, scheme Scheme, sub string) []string {
	args := []string{}
	if s.Token != "" {
		args = append(args, "-c",
			"http.extraHeader=Authorization: "+AuthHeader(scheme, s.Token))
	}
	return append(args, sub)
}
```

Sequence: `ls-remote` the ref to a SHA (that SHA is the cache key), then
`Publish` a shallow clone checked out at it, then re-root `Subpath` — rejecting
`..`, absolute paths and symlinks that leave the tree, reusing
`schema.ValidRelPath`'s rule.

The `swapRunner` seam is package-private and exists so the argv can be asserted
without a network. Assert on the **argv slice**, never on a joined string that a
stub echoed: a check written against joined output cannot tell one argument from
two, which is the property under test. That mistake is recorded in
`CLAUDE.md` for the sandbox's `arg:[…]` versus `argv:` check.

- [x] **Step 4: Run them, verify they pass.**
- [x] **Step 5: Mutation-test the URL guard.** Change `gitArgs` to embed the
  token in the URL. Run `make test-one T=TestFetchGitNeverPutsTheCredentialInTheURL P=./pkg/source/`.
  Expected: FAIL. Restore.
- [x] **Step 6: Commit**

```bash
git add pkg/source/git.go pkg/source/git_test.go
git commit -m "feat(source): git fetch with header auth and subpath re-rooting"
```

---

### Task 5: The archive fetcher

§18. The digest is verified **before** extraction, because a digest checked on
extracted bytes has already run the extractor over untrusted input.

**Files:** Create `pkg/source/archive.go`, `pkg/source/archive_test.go`

**Interfaces:**
- Consumes: `Cache.Publish`, `CheckCredentialTransport`, `AuthHeader`.
- Produces: `type ArchiveSpec struct{ URL, Digest, Subpath, Token, DeclaredScheme string }`,
  `FetchArchive(ctx, c *Cache, s ArchiveSpec) (Resolved, error)`.

- [x] **Step 1: Write the failing tests**

```go
func TestFetchArchiveVerifiesTheDigestBeforeExtracting(t *testing.T) {
	body := tarGz(t, map[string]string{"review/SKILL.md": "# review"})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	sum := sha256.Sum256(body)
	c, _ := NewCache(t.TempDir())

	got, err := FetchArchive(t.Context(), c, ArchiveSpec{
		URL: srv.URL, Digest: "sha256:" + hex.EncodeToString(sum[:]),
		Subpath: "review", HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if _, err := os.Stat(filepath.Join(got.Dir, "SKILL.md")); err != nil {
		t.Errorf("subpath not re-rooted: %v", err)
	}

	// A mismatch must write nothing — and must be caught before the extractor
	// ever sees the bytes.
	_, err = FetchArchive(t.Context(), c, ArchiveSpec{
		URL: srv.URL, Digest: "sha256:" + strings.Repeat("0", 64),
		HTTPClient: srv.Client(),
	})
	if err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("err = %v; a digest mismatch must be refused by name", err)
	}
	if ents, _ := os.ReadDir(filepath.Join(c.Root, "tmp")); len(ents) != 0 {
		t.Error("a rejected archive left staged bytes behind")
	}
}

func TestArchiveExtractionRefusesEscapes(t *testing.T) {
	for name, entry := range map[string]string{
		"traversal":    "../../etc/passwd",
		"absolute":     "/etc/passwd",
		"dotdot-inner": "a/../../b",
	} {
		t.Run(name, func(t *testing.T) {
			if err := extractTar(t.Context(), tarWithPath(t, entry), t.TempDir(), 1<<20); err == nil {
				t.Errorf("%q extracted; it must be refused", entry)
			}
		})
	}
	// A symlink pointing out of the tree is the same escape wearing a hat.
	if err := extractTar(t.Context(), tarWithSymlink(t, "esc", "../../etc"), t.TempDir(), 1<<20); err == nil {
		t.Error("an escaping symlink was extracted")
	}
	// And a member larger than the cap stops the extraction rather than
	// filling the disk.
	if err := extractTar(t.Context(), tarWithSize(t, "big", 4<<20), t.TempDir(), 1<<20); err == nil {
		t.Error("an oversized member was extracted")
	}
}
```

- [x] **Step 2: Run them, verify they fail.**
- [x] **Step 3: Implement**

Port the tar-safety rules from `ach/internal/contentkit/tar_safety.go`.

```go
// FetchArchive downloads, verifies, then extracts — in that order, and the
// order is the security property (§18).
//
// A digest is not a checksum bolted on: a git source is content-addressed and
// verified by git itself, an archive is not, so the digest is its ONLY
// integrity claim. Verifying AFTER extraction would mean the extractor had
// already parsed attacker-chosen bytes — and the extractor is exactly where
// traversal, symlink and zip-bomb bugs live.
//
// The digest is also the cache key: identical bytes are one entry however many
// URLs serve them.
```

Download to memory bounded by a cap, hash, compare in constant time, and only
then extract inside `Publish`'s temporary directory.

- [x] **Step 4: Run them, verify they pass.**
- [x] **Step 5: Mutation-test the ordering.** Move the digest comparison to
  after extraction. Run `make test-one T=TestFetchArchiveVerifiesTheDigestBeforeExtracting P=./pkg/source/`.
  Expected: FAIL on the "left staged bytes behind" assertion. Restore.
- [x] **Step 6: Add a fuzz target**

```go
func FuzzExtractTar(f *testing.F) {
	f.Add(tarWithPath(nil, "a/b"))
	f.Fuzz(func(t *testing.T, b []byte) {
		root := t.TempDir()
		_ = extractTar(context.Background(), b, root, 1<<16)
		// Whatever it did, nothing may exist outside root.
		assertNothingOutside(t, root)
	})
}
```

Add it to the `_fuzz` target.

- [x] **Step 7: Commit**

```bash
git add pkg/source/archive.go pkg/source/archive_test.go pkg/source/fuzz_test.go Makefile
git commit -m "feat(source): archive fetch, digest verified before extraction"
```

---

### Task 6: The local source

**Files:** Create `pkg/source/local.go`; modify `pkg/source/resolve_test.go`

**Interfaces:**
- Produces: `ResolveLocal(base string, s schema.LocalSource) (Resolved, error)`.

- [x] **Step 1: Write the failing test**

```go
func TestLocalResolvesRelativeToTheManifestAndRefusesEscapes(t *testing.T) {
	dir := t.TempDir()
	mkdirAll(t, filepath.Join(dir, "assets", "review"))
	got, err := ResolveLocal(dir, schema.LocalSource{Path: "./assets", Subpath: "review"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Dir != filepath.Join(dir, "assets", "review") {
		t.Errorf("Dir = %q", got.Dir)
	}
	// A local source is not a licence to read the filesystem: a manifest from
	// a cloned repository must not reach outside it.
	if _, err := ResolveLocal(dir, schema.LocalSource{Path: "../../etc"}); err == nil {
		t.Error("a path escaping the manifest directory was accepted")
	}
	// Local sources are never cached and never carry a resolved ref: they are
	// mutable development content by definition (§32).
	if got.ResolvedRef != "" {
		t.Errorf("ResolvedRef = %q, want empty for a local source", got.ResolvedRef)
	}
}
```

- [x] **Step 2: Run it, verify it fails.**
- [x] **Step 3: Implement.** Join against the manifest directory, clean, refuse
  an escape with `schema.ValidRelPath`'s rule, and stat the result.
- [x] **Step 4: Run it, verify it passes.**
- [x] **Step 5: Mutation-test the escape guard.** Remove the containment check,
  confirm the test fails, restore.
- [x] **Step 6: Commit**

```bash
git add pkg/source/local.go pkg/source/resolve_test.go
git commit -m "feat(source): local sources, contained to the manifest directory"
```

---

### Task 7: Resolve an effective profile

**Files:** Create `pkg/source/resolve.go`, `pkg/source/resolve_test.go`

**Interfaces:**
- Consumes: `schema.Profile`, `FetchGit`, `FetchArchive`, `ResolveLocal`.
- Produces: `type Opts struct{ Cache *Cache; ManifestDir string; Secret func(name string) (string, error) }`,
  `Resolve(ctx, p schema.Profile, o Opts) (map[string]Resolved, []Report, error)`,
  `type Report struct{ Resource, Text string }`. Phase 4 consumes both.

- [x] **Step 1: Write the failing tests**

```go
func TestResolveSkipsDisabledResourcesAndTheirSecrets(t *testing.T) {
	// §12: only active resources contribute requirements. A disabled resource
	// whose source needs a token must not make that token required.
	p := profileFrom(t, `version: "1"
name: p
targets: [claude]
inputs:
  secrets:
    gl: {env: GL}
skills:
  off:
    enabled: false
    source: {git: {url: "https://gitlab.x/a.git", auth: {value_from: {secret: gl}}}}
`)
	got, _, err := Resolve(t.Context(), p, Opts{Cache: mustCache(t), Secret: func(string) (string, error) {
		t.Fatal("a disabled resource's secret was read")
		return "", nil
	}})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("resolved %d entries, want 0", len(got))
	}
}

func TestResolveReportsTheSchemeItUsed(t *testing.T) {
	// §17.1's condition: inference is acceptable only because it is disclosed.
	// The report exists on SUCCESS, not only in a 401 — an inference that is
	// invisible when it works is undebuggable when it stops working.
	repo := newBareRepo(t, map[string]string{"SKILL.md": "#"})
	p := profileFrom(t, `version: "1"
name: p
targets: [claude]
skills:
  s: {source: {git: {url: "`+repo+`"}}}
`)
	_, reports, err := Resolve(t.Context(), p, Opts{Cache: mustCache(t)})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(reports, func(r Report) bool {
		return r.Resource == "skill s" && strings.Contains(r.Text, "inferred")
	}) {
		t.Errorf("no scheme report for skill s: %+v", reports)
	}
}

func TestResolveNamesTheResourceWhenASecretIsMissing(t *testing.T) {
	p := profileFrom(t, `version: "1"
name: p
targets: [claude]
inputs:
  secrets:
    gl: {env: GL_UNSET}
skills:
  company-review:
    source: {git: {url: "https://gitlab.x/a.git", auth: {value_from: {secret: gl}}}}
`)
	_, _, err := Resolve(t.Context(), p, Opts{Cache: mustCache(t),
		Secret: func(string) (string, error) { return "", ErrSecretUnset }})
	if err == nil {
		t.Fatal("a missing secret resolved")
	}
	// The referrer is what turns a ten-minute hunt into a two-minute fix, and
	// the spec's own §12 example omits it.
	for _, want := range []string{"company-review", "gl"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}
```

- [x] **Step 2: Run them, verify they fail.**
- [x] **Step 3: Implement.** Walk `Skills`, `Artifacts`, `Marketplaces` and
  `Prompt` in sorted key order — determinism is what makes a report diffable —
  skipping `Enabled == false`. Read a secret only when an active resource's
  locator references it, wrapping any failure with the resource name. Dispatch
  on the source branch. Collect a `Report` per resolved locator carrying the
  scheme and whether it was inferred.
- [x] **Step 4: Run them, verify they pass.**
- [x] **Step 5: Commit**

```bash
git add pkg/source/resolve.go pkg/source/resolve_test.go
git commit -m "feat(source): resolve an effective profile's active locators"
```

---

### Task 8: `ap manifest apply --dry-run`

The resolution phase, wired to a command, mutating nothing. This is what makes
Phase 3 shippable on its own.

**Files:** Modify `cmd/ap/manifest.go`, `cmd/ap/main.go`, `cmd/ap/main_test.go`

**Interfaces:**
- Consumes: `schema.Effective`, `source.Resolve`.
- Produces: the `apply` subcommand, `--dry-run` only. `--yes` and `--manifest -`
  are Phases 4 and 7. `--prune` is **deferred past v1** — do not add the flag.

- [x] **Step 1: Write the failing test**

```go
func TestManifestApplyDryRunResolvesAndWritesNothing(t *testing.T) {
	home := t.TempDir()
	repo := newBareRepo(t, map[string]string{"skills/pdf/SKILL.md": "# pdf"})
	dir := t.TempDir()
	write(t, dir, "p.yaml", `version: "1"
name: plan
targets: [claude]
skills:
  pdf: {source: {git: {url: "`+repo+`", subpath: skills/pdf}}}
`)
	code, stdout, _ := runAPWithHome(t, home, "manifest", "apply", "claude:plan",
		filepath.Join(dir, "p.yaml"), "--dry-run")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	for _, want := range []string{"skill", "pdf", "SKILL.md ok", "nothing was written"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	// §37.1: a dry run is not offline, but it IS non-mutating. The profile
	// namespace must not exist afterwards — apply overwrites, and a dry run
	// that half-created a root would be worse than no dry run.
	if _, err := os.Stat(filepath.Join(home, ".local/share/agent-profile/profiles/claude/plan")); !os.IsNotExist(err) {
		t.Error("--dry-run created the profile namespace")
	}
}

func TestManifestApplyDryRunSaysItAuthenticated(t *testing.T) {
	// §37.1 requires this to be said: "dry run" reads as "does nothing", and
	// here it resolves secrets and fetches over the network.
	_, stdout, _ := runAP(t, "manifest", "apply", "claude:plan", fixture(t), "--dry-run")
	if !strings.Contains(stdout, "fetched") {
		t.Errorf("the dry run does not disclose that it fetched:\n%s", stdout)
	}
}
```

- [x] **Step 2: Run them, verify they fail.**
- [x] **Step 3: Implement**

`manifestApply` parses with `parseAroundRef` (no passthrough, like `create`),
resolves the reference to a root **without creating it**, runs `schema.Effective`
then `source.Resolve`, prints the report in the house style — `  ✓ <label 10>
<value>`, `  – ` for a skip, `  ✗ ` for a failure — and stops. The closing line
is mandatory and states both facts:

```
  --dry-run authenticated and fetched; nothing was written.
```

Without `--dry-run`, exit 2 with "materialization is Phase 4". A stub that
silently does nothing is worse than an unimplemented command.

- [x] **Step 4: Run them, verify they pass.**
- [x] **Step 5: Commit**

```bash
git add cmd/ap/manifest.go cmd/ap/main.go cmd/ap/main_test.go
git commit -m "feat(ap): ap manifest apply --dry-run runs the resolution phase"
```

---

### Task 9: Gates, docs, and the phase's own fixture

**Files:** Create `pkg/source/testdata/`; modify `docs/references/DECLARATIVE.md`,
`CLAUDE.md`, `Makefile`

- [x] **Step 1: Write the end-to-end fixture test**

```go
func TestTheSpecsFullExampleResolvesOffline(t *testing.T) {
	// SPEC §35 with every remote URL rewritten to a local bare repository, so
	// the shape is the spec's and the network is not involved.
	p, _, err := schema.Effective("testdata/spec-35-local.yaml", "claude")
	if err != nil {
		t.Fatal(err)
	}
	got, reports, err := source.Resolve(t.Context(), p, offlineOpts(t))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("nothing resolved")
	}
	for _, r := range reports {
		t.Logf("%s: %s", r.Resource, r.Text)
	}
}
```

- [x] **Step 2: Run it, verify it fails**, then add the fixture and pass.
- [x] **Step 3: Run every gate**
  - `make verify` — fmt-check, shellcheck, vet, lint, test (race + shuffle), vulncheck
  - `make crossbuild` — `pkg/` must build for windows and darwin
  - `make secrets` — gitleaks over full history
  - `make fuzz` — including the new `FuzzExtractTar`
  - `make sandbox` — must be **unchanged**: Phase 3 materializes nothing
  - `make smoke` — must be unchanged

  A red gate is reported with its output, not worked around.

- [x] **Step 4: Document what was learned, not what was built**

Add to `docs/references/DECLARATIVE.md`: why the digest is verified before
extraction and not after; why the scheme report exists on success and not only
on 401; why `SameEndpoint` compares the effective port and rejects subdomains;
and why no test in this package touches the network.

Add to `CLAUDE.md`'s MANDATORY reading table:
`pkg/source/transport.go`, credential guards → `docs/references/DECLARATIVE.md`.

- [x] **Step 5: Commit**

```bash
git add pkg/source/testdata docs/references/DECLARATIVE.md CLAUDE.md
git commit -m "test(source): the spec's example resolves with no network"
```

---

## Self-Review

**Spec coverage:** §16 (Task 7), §17 + §17.1 + §17.2 (Tasks 2–4), §18 (Task 5),
§19 (Task 6), §20 (Tasks 4–6), §21.2's primitives (Task 2 — *applied* in Phase 6),
§32's "no lockfile" (nothing built, asserted by Task 6's empty `ResolvedRef` for
local sources and by the absence of any pin), §37.1 steps 10–12 (Tasks 7–8).

**Deferred with owners:** §21.1 marketplace item resolution and the *application*
of the §21.2 guard (Phase 6); the ledger (Phase 4); `--yes` and `--manifest -`
(Phases 4 and 7); §13.1's `--auth-secret-env` (Phase 7 — Phase 3 takes a `Secret`
function, so the binding's origin is the caller's business). `--prune` is
deferred past v1 entirely (SPEC §38).

**One lock, not two.** Task 1's `Publish` is atomic — temp dir, fill, rename,
sweep — and its test asserts a failed fill leaves nothing. That is what makes the
cache safe to leave **unlocked**, and Phase 4 must not port `ach`'s workspace
lock on top of it (SPEC §37.3). Concurrent resolvers waste one fetch and cannot
corrupt anything; a second lock would buy that fetch back for a lock-ordering
rule and the deadlock the rule exists to exclude.

**Type consistency:** `Resolved` is introduced in Task 4 and consumed unchanged
by Tasks 5, 6, 7 and Phase 4. `Scheme` (Task 3) is carried on `Resolved` and read
by Task 7's `Report`. `Cache` (Task 1) is consumed by Tasks 4, 5 and 7 and never
by 6 — local sources are not cached, which Task 6's test pins.

**Mutation-tested guards, five:** non-TLS credential (Task 2), effective-port
comparison (Task 2), exact-host comparison (Task 2), credential-not-in-URL
(Task 4), digest-before-extract (Task 5). Each has the revert-run-restore step
written into the task.

## Execution Handoff

**Plan complete and saved to
`docs/superpowers/plans/2026-08-26-phase-3-sources-and-cache.md`.**

Prerequisite: Phase 1 Tasks 14 and 15 (`auth.scheme`, the `archive` branch) —
Task 3 and Task 5 consume them.

**1. Subagent-Driven (recommended)** — a fresh subagent per task, review between.
**2. Inline Execution** — batched with checkpoints.

---

## What Phase 3 turned up that the plan did not predict

Written down because the plan was wrong about three of these, and re-deriving
them costs the same mistakes twice.

### The plan's own digest-ordering test was vacuous

Task 5 specified asserting that a digest mismatch leaves no staged bytes and
publishes nothing. Both hold **in either order**, because `Cache.Publish`
removes a failed fill whether the failure came from the digest check or from
the extractor. Moving `verifyDigest` after `extractTar` left the test green.

The order is only observable through whether the extractor RAN. The test now
serves an archive that is hostile AND digest-mismatched: verify-first fails on
the digest, verify-second fails on the traversal — proving the extractor parsed
unverified bytes. It carries a second fetch with a correct digest so it cannot
pass for a `FetchArchive` that never extracts at all.

**Two of the fourteen mutations initially failed to COMPILE rather than failing
a test.** An invalid mutation proves nothing: `if false ||` left a variable
unused, and Go refused the build, which greps as neither pass nor fail. Both
were rewritten to keep the variable live. When a mutation produces no test
output at all, that is the signal — not a passing guard.

### Asserting on joined argv would have hidden the credential-in-URL bug

The plan's own example test joined git's arguments with a space before
searching for the token. That check passes with the token spliced into the URL,
because the `http.extraHeader` argument legitimately contains the token. The
test asserts on the argv **slice**, skipping the header argument and requiring
every other argument to be token-free. `CLAUDE.md` already records this exact
vacuity for the sandbox's `arg:[…]` versus `argv:` check; it recurred here.

### Adding a source family is not local to the decoder

Phase 1 Task 15 added `archive` to the union. `Required` and `Render` both
switched on `Git`/`Local` by name, so an archive contributed no required input
(§12 — Phase 3 would have fetched a private archive with no credential and
reported a 401 naming nothing) and rendered as a resource with `source:` and
nothing under it (§8's silent drop). Neither was in the task's steps. Every
exhaustive switch over a union is part of adding a branch to it.

### `--target` was the wrong shape for apply, and it took the surface to see it

`ap apply --target claude <path>` shipped in Phase 2. The reference form
(`ap manifest apply claude:plan <path>`) names the agent and the root in one
token, which is why use case 1 notes that ap needs no flag `ach-cli` needs.
`render` keeps `--target` because a file has no root to read one from — that
asymmetry is the design, not an inconsistency to tidy away.

### The cache belongs under XDG_CACHE_HOME, and beside the profiles

Not inside one. A source fetched for `claude:plan` is the same bytes when
`codex:review` asks for it, and content addressed by SHA or digest cannot
collide between them. `XDG_CACHE_HOME` rather than `XDG_DATA_HOME` because a
cache is reconstructible by definition — deleting it costs a re-fetch — which
keeps it out of whatever the user backs up.

### A `ref` is reported, not skipped

Marketplace item resolution is Phase 6, so `Resolve` cannot turn
`pdf@anthropic-skills` into bytes. Producing nothing for it silently would be
§8's silent drop in the one output that tells a user what apply is about to do,
so it comes back as a `Report` marked `–`. The marketplace's own catalogue IS
fetched here: it is an ordinary locator, and only the item lookup inside it is
deferred.

### The §35 fixture is read, never copied

`pkg/source`'s end-to-end test reads `pkg/schema/testdata/spec-35-execute.yaml`
and rewrites its remote URLs to local repositories at test time. A second copy
is exactly how the `auth` block drifted the first time. The rewrite strips the
auth blocks — a credential may not cross a non-TLS transport and a local path is
not https — and a sibling test keeps them intact to assert that the guard fires
end to end through a real profile, not only through a hand-built `GitSpec`.
