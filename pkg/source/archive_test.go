package source

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tarGz builds a gzipped tar in memory. Every archive test uses it, so no test
// needs a network or a fixture file on disk.
func tarGz(t testing.TB, headers []*tar.Header, bodies [][]byte) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for i, h := range headers {
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if len(bodies[i]) > 0 {
			if _, err := tw.Write(bodies[i]); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func tarFiles(t testing.TB, files map[string]string) []byte {
	hs := make([]*tar.Header, 0, len(files))
	bs := make([][]byte, 0, len(files))
	for name, body := range files {
		hs = append(hs, &tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
		bs = append(bs, []byte(body))
	}
	return tarGz(t, hs, bs)
}

func tarWithPath(t testing.TB, name string) []byte {
	return tarGz(t,
		[]*tar.Header{{Name: name, Mode: 0o644, Size: 1, Typeflag: tar.TypeReg}},
		[][]byte{[]byte("x")})
}

func tarWithSymlink(t testing.TB, name, target string) []byte {
	return tarGz(t,
		[]*tar.Header{{Name: name, Mode: 0o777, Typeflag: tar.TypeSymlink, Linkname: target}},
		[][]byte{nil})
}

func tarWithSize(t testing.TB, name string, size int) []byte {
	return tarGz(t,
		[]*tar.Header{{Name: name, Mode: 0o644, Size: int64(size), Typeflag: tar.TypeReg}},
		[][]byte{bytes.Repeat([]byte("a"), size)})
}

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// §18. The ORDER is the security property: verifying after extraction would
// mean the extractor had already parsed attacker-chosen bytes, and the
// extractor is where traversal, symlink and zip-bomb bugs live.
func TestFetchArchiveVerifiesTheDigestBeforeExtracting(t *testing.T) {
	body := tarFiles(t, map[string]string{"review/SKILL.md": "# review", "other/x": "no"})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	c := mustCache(t)

	got, err := FetchArchive(t.Context(), c, ArchiveSpec{
		URL: srv.URL, Digest: digestOf(body), Subpath: "review", HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(got.Dir, "SKILL.md")); err != nil || string(b) != "# review" {
		t.Errorf("subpath not re-rooted: %q %v", b, err)
	}
	// The digest is the receipt an archive has instead of a SHA.
	if got.ResolvedRef != digestOf(body) {
		t.Errorf("ResolvedRef = %q, want the digest", got.ResolvedRef)
	}

	// A mismatch must write nothing.
	_, err = FetchArchive(t.Context(), c, ArchiveSpec{
		URL: srv.URL, Digest: "sha256:" + strings.Repeat("0", 64), HTTPClient: srv.Client(),
	})
	if err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("err = %v; a digest mismatch must be refused by name", err)
	}
	if ents, _ := os.ReadDir(filepath.Join(c.Root, tmpDir)); len(ents) != 0 {
		t.Errorf("a rejected archive left %d staged entries behind", len(ents))
	}
	if c.Has("sha256-" + strings.Repeat("0", 64)) {
		t.Error("a rejected archive was published")
	}
}

// The assertions above do NOT prove the ORDER, and that was found by
// mutation: moving the digest check after extractTar left every one of them
// green, because Publish removes a failed fill either way. What distinguishes
// the two orders is whether the extractor ever RAN on unverified bytes.
//
// So: serve an archive that is hostile AND digest-mismatched. Verifying first
// means the error is about the digest and the extractor never saw the bytes.
// Verifying second means the extractor parsed them and failed on the
// traversal — which is the whole thing §18 exists to prevent, since the
// extractor is where traversal, symlink and zip-bomb bugs live.
func TestAHostileArchiveIsRejectedByDigestBeforeTheExtractorSeesIt(t *testing.T) {
	hostile := tarWithPath(t, "../../escape")
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(hostile)
	}))
	defer srv.Close()

	_, err := FetchArchive(t.Context(), mustCache(t), ArchiveSpec{
		URL: srv.URL, Digest: "sha256:" + strings.Repeat("0", 64), HTTPClient: srv.Client(),
	})
	if err == nil {
		t.Fatal("a hostile, mismatched archive was accepted")
	}
	if !strings.Contains(err.Error(), "digest mismatch") {
		t.Errorf("error = %v\nwant a digest mismatch: any extraction error here means the extractor parsed unverified bytes", err)
	}
	// Belt and braces: the same bytes with a CORRECT digest must reach the
	// extractor and be refused there, or the test above would pass for a
	// FetchArchive that never extracts at all.
	_, err = FetchArchive(t.Context(), mustCache(t), ArchiveSpec{
		URL: srv.URL, Digest: digestOf(hostile), HTTPClient: srv.Client(),
	})
	if err == nil || !strings.Contains(err.Error(), "escapes the archive root") {
		t.Errorf("err = %v; a verified but hostile archive must be refused by the extractor", err)
	}
}

// The digest is the cache key, so identical bytes are one entry however many
// URLs serve them.
func TestTwoURLsServingIdenticalBytesShareOneEntry(t *testing.T) {
	body := tarFiles(t, map[string]string{"a/f": "same"})
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) })
	a := httptest.NewTLSServer(h)
	defer a.Close()
	b := httptest.NewTLSServer(h)
	defer b.Close()
	c := mustCache(t)

	for _, srv := range []*httptest.Server{a, b} {
		if _, err := FetchArchive(t.Context(), c, ArchiveSpec{
			URL: srv.URL, Digest: digestOf(body), HTTPClient: srv.Client(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	ents, err := os.ReadDir(filepath.Join(c.Root, objectsDir))
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 {
		t.Errorf("cache holds %d entries, want 1", len(ents))
	}
}

func TestArchiveExtractionRefusesEscapes(t *testing.T) {
	for name, entry := range map[string]string{
		"traversal":    "../../etc/passwd",
		"absolute":     "/etc/passwd",
		"dotdot-inner": "a/../../b",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if err := extractTar(t.Context(), tarWithPath(t, entry), root, 1<<20); err == nil {
				t.Errorf("%q extracted; it must be refused", entry)
			}
		})
	}
	// A symlink pointing out of the tree is the same escape wearing a hat.
	for _, target := range []string{"../../etc", "/etc/passwd"} {
		if err := extractTar(t.Context(), tarWithSymlink(t, "esc", target), t.TempDir(), 1<<20); err == nil {
			t.Errorf("a symlink to %q was extracted", target)
		}
	}
	// A member larger than the cap stops the extraction rather than filling
	// the disk.
	if err := extractTar(t.Context(), tarWithSize(t, "big", 4<<20), t.TempDir(), 1<<20); err == nil {
		t.Error("an oversized member was extracted")
	}
	// A device node is not content, and skipping it silently would be §8's
	// silent drop.
	dev := tarGz(t, []*tar.Header{{Name: "d", Typeflag: tar.TypeChar, Mode: 0o666}}, [][]byte{nil})
	if err := extractTar(t.Context(), dev, t.TempDir(), 1<<20); err == nil {
		t.Error("a device member was extracted")
	}
	// And the happy path still works, or the guards above prove nothing.
	root := t.TempDir()
	if err := extractTar(t.Context(), tarFiles(t, map[string]string{"a/b.md": "hi"}), root, 1<<20); err != nil {
		t.Fatalf("a safe archive was refused: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(root, "a", "b.md")); err != nil || string(b) != "hi" {
		t.Errorf("safe member = %q %v", b, err)
	}
}

// A 401 on an authenticated archive fetch must name the scheme, the same rule
// §17.1 imposes on git — the failure mode is identical and so is the fix.
func TestArchive401NamesTheSchemeAndNotTheToken(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	_, err := FetchArchive(t.Context(), mustCache(t), ArchiveSpec{
		URL: srv.URL, Digest: digestOf([]byte("x")), Token: "ach-SECRET",
		DeclaredScheme: "bearer", HTTPClient: srv.Client(),
	})
	if err == nil {
		t.Fatal("a 401 succeeded")
	}
	if !strings.Contains(err.Error(), "bearer") {
		t.Errorf("error does not name the scheme: %v", err)
	}
	if strings.Contains(err.Error(), "ach-SECRET") {
		t.Errorf("the error embeds the credential: %v", err)
	}
}

func TestArchiveRefusesACredentialOverPlaintext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	_, err := FetchArchive(t.Context(), mustCache(t), ArchiveSpec{
		URL: srv.URL, Digest: digestOf([]byte("x")), Token: "ach-SECRET",
	})
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Errorf("err = %v; §17.2 applies to §18 too", err)
	}
}
