package source

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// ArchiveSpec is §18's source family. Digest is required by the schema, so it
// is never empty here.
type ArchiveSpec struct {
	URL, Digest, Subpath string
	Token                string
	DeclaredScheme       string
	// HTTPClient lets a test serve over httptest's TLS without a network. nil
	// means http.DefaultClient.
	HTTPClient *http.Client
}

// Caps. A download is held in memory to be hashed before anything parses it,
// so the download cap is also the memory cap; the member cap bounds what one
// tar entry can expand to.
const (
	maxArchiveBytes = 256 << 20
	maxMemberBytes  = 64 << 20
)

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
func FetchArchive(ctx context.Context, c *Cache, s ArchiveSpec) (Resolved, error) {
	scheme, inferred := ResolveScheme(s.DeclaredScheme, hostOf(s.URL))
	res := Resolved{
		ResolvedRef:    s.Digest,
		SchemeUsed:     scheme,
		SchemeInferred: inferred,
		Anonymous:      s.Token == "",
	}
	if s.Token != "" {
		if err := CheckCredentialTransport(s.URL); err != nil {
			return Resolved{}, err
		}
	}
	key, err := digestKey(s.Digest)
	if err != nil {
		return Resolved{}, err
	}

	entry, err := c.Publish(key, func(dir string) error {
		body, err := download(ctx, s, scheme)
		if err != nil {
			return err
		}
		if err := verifyDigest(s.URL, s.Digest, body); err != nil {
			return err
		}
		return extractTar(ctx, body, dir, maxMemberBytes)
	})
	if err != nil {
		return Resolved{}, err
	}
	if res.Dir, err = subtree(entry, s.Subpath); err != nil {
		return Resolved{}, err
	}
	return res, nil
}

// digestKey turns "sha256:<hex>" into a cache key. The schema already validated
// the shape; this refuses again rather than trusting a caller that skipped it,
// because the value reaches the filesystem through Cache.Path.
func digestKey(digest string) (string, error) {
	algo, hexsum, ok := strings.Cut(digest, ":")
	if !ok || algo != "sha256" || len(hexsum) != 64 {
		return "", fmt.Errorf("digest %q is not sha256:<64 hex>", digest)
	}
	if _, err := hex.DecodeString(hexsum); err != nil {
		return "", fmt.Errorf("digest %q is not hex: %w", digest, err)
	}
	return algo + "-" + hexsum, nil
}

func download(ctx context.Context, s ArchiveSpec, scheme Scheme) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL, nil)
	if err != nil {
		return nil, err
	}
	if s.Token != "" {
		req.Header.Set("Authorization", AuthHeader(scheme, s.Token))
	}
	client := s.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		// A transport error can carry the request URL; it never carries the
		// header, which is where the credential lives (§17.2, §34).
		return nil, fmt.Errorf("fetching %s: %w", s.URL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusUnauthorized && s.Token != "" {
			return nil, fmt.Errorf("fetching %s: %s\nauth %s", s.URL, resp.Status, scheme.Report(false))
		}
		return nil, fmt.Errorf("fetching %s: %s", s.URL, resp.Status)
	}
	// LimitReader with one spare byte, so hitting the cap is detectable rather
	// than silently producing a truncated body that would then fail its digest
	// with a misleading message.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxArchiveBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", s.URL, err)
	}
	if len(body) > maxArchiveBytes {
		return nil, fmt.Errorf("%s: archive exceeds %d bytes", s.URL, maxArchiveBytes)
	}
	return body, nil
}

// verifyDigest compares in constant time. The comparison is not secret-
// dependent, so this is belt and braces — but a digest check written with ==
// invites the next one to be written the same way.
func verifyDigest(url, want string, body []byte) error {
	sum := sha256.Sum256(body)
	got := "sha256:" + hex.EncodeToString(sum[:])
	if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		return fmt.Errorf("%s: digest mismatch\n  expected %s\n  computed %s\nnothing was written", url, want, got)
	}
	return nil
}

var errUnsafeEntry = errors.New("unsafe archive entry")

// extractTar writes a gzipped tar into root, refusing everything that could
// escape it.
//
// The rules are ach/internal/contentkit/tar_safety.go's: no absolute path, no
// "..", no symlink or hardlink whose target leaves the tree, no device or
// other special member, and a per-member size cap so one entry cannot fill the
// disk. Anything refused stops the extraction — a partially extracted archive
// is exactly the half-published entry Cache.Publish exists to prevent.
func extractTar(ctx context.Context, body []byte, root string, maxMember int64) error {
	zr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("archive is not gzip: %w", err)
	}
	defer func() { _ = zr.Close() }()

	tr := tar.NewReader(zr)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading archive: %w", err)
		}
		dest, err := safeJoin(root, h.Name)
		if err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if h.Size > maxMember {
				return fmt.Errorf("%w: %q is %d bytes, over the %d cap", errUnsafeEntry, h.Name, h.Size, maxMember)
			}
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return err
			}
			if err := writeMember(tr, dest, maxMember); err != nil {
				return err
			}
		case tar.TypeSymlink, tar.TypeLink:
			// A link is an escape wearing a hat: the entry's own path is
			// inside the tree, and its target is not. An absolute target is
			// checked on its own, because path.Join would quietly make
			// "/etc/passwd" relative to the entry's directory and pass.
			if path.IsAbs(h.Linkname) || filepath.IsAbs(h.Linkname) {
				return fmt.Errorf("%w: %q links to an absolute path", errUnsafeEntry, h.Name)
			}
			if _, err := safeJoin(root, path.Join(path.Dir(h.Name), h.Linkname)); err != nil {
				return fmt.Errorf("%w: %q links outside the archive root", errUnsafeEntry, h.Name)
			}
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(h.Linkname, dest); err != nil {
				return err
			}
		default:
			// Devices, fifos and the rest are not content. Skipping silently
			// would be §8's silent drop; refusing is the honest answer for a
			// member nothing in this system can use.
			return fmt.Errorf("%w: %q has unsupported type %q", errUnsafeEntry, h.Name, string(h.Typeflag))
		}
	}
}

// writeMember copies at most maxMember bytes. The header's Size is attacker
// chosen, so the cap is enforced on what is actually read, not on what the
// header claims.
func writeMember(r io.Reader, dest string, maxMember int64) error {
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	n, err := io.Copy(f, io.LimitReader(r, maxMember+1))
	if err != nil {
		return err
	}
	if n > maxMember {
		return fmt.Errorf("%w: %q exceeds the %d byte cap", errUnsafeEntry, dest, maxMember)
	}
	return f.Close()
}

// safeJoin resolves a member name against root and refuses anything that
// leaves it. The name is cleaned FIRST, because "a/../../b" is only visibly an
// escape once cleaned — the same reason §26.1 states for destinations.
func safeJoin(root, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("%w: empty name", errUnsafeEntry)
	}
	if path.IsAbs(name) || filepath.IsAbs(name) || strings.HasPrefix(name, `\`) {
		return "", fmt.Errorf("%w: %q is absolute", errUnsafeEntry, name)
	}
	clean := path.Clean(name)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("%w: %q escapes the archive root", errUnsafeEntry, name)
	}
	if clean == "." {
		return root, nil
	}
	return filepath.Join(root, filepath.FromSlash(clean)), nil
}
