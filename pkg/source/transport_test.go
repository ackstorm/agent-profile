package source

import (
	"strings"
	"testing"
)

// §17.2. A credential never crosses plaintext, and never rides in the URL.
func TestACredentialMayNotTravelOverNonTLS(t *testing.T) {
	if err := CheckCredentialTransport("https://gitlab.acme.internal/x.git"); err != nil {
		t.Errorf("https refused: %v", err)
	}
	for _, u := range []string{
		"http://gitlab.acme.internal/x.git",
		"http://127.0.0.1:8080/x.tgz",
		"git://gitlab.acme.internal/x.git",
	} {
		err := CheckCredentialTransport(u)
		if err == nil {
			t.Errorf("%q accepted; a credential must not cross plaintext", u)
			continue
		}
		if !strings.Contains(err.Error(), "https") {
			t.Errorf("error %q does not say what to use instead", err)
		}
	}
}

// The credential must reach git as a header, never as part of the URL: the URL
// position is visible in /proc/<pid>/cmdline to every process on the machine,
// and git persists it in remote.origin.url. A URL that already carries one is
// refused rather than silently rewritten — stripping it would leave the
// manifest looking correct while ap quietly edited what it said.
func TestACredentialInTheURLIsRefusedNotStripped(t *testing.T) {
	err := CheckCredentialTransport("https://oauth2:glpat-secret@gitlab.acme.internal/x.git")
	if err == nil {
		t.Fatal("a URL carrying a credential was accepted")
	}
	if strings.Contains(err.Error(), "glpat-secret") {
		t.Errorf("the error repeats the credential: %v", err)
	}
	if !strings.Contains(err.Error(), "gitlab.acme.internal") {
		t.Errorf("error %q does not name the host", err)
	}
}

// §21.2, tightened in v0.6.1. This is the specification's one mandatory
// security guard, and the table is its whole definition.
func TestSameEndpointComparesHostAndEffectivePort(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		// The effective port is the explicit one or the scheme's default, so
		// these name one endpoint.
		{"https://gl.acme.internal/a.git", "https://gl.acme.internal:443/b.git", true},
		{"https://GL.Acme.Internal/a.git", "https://gl.acme.internal/b.git", true},
		{"https://gl.acme.internal/a.git", "https://gl.acme.internal/b.git", true},
		// A different port on the same host can be a different service.
		{"https://gl.acme.internal/a.git", "https://gl.acme.internal:8443/b.git", false},
		{"https://gl.acme.internal/a.git", "https://github.com/b.git", false},
		// A subdomain is NOT the same host. This is the case the guard exists
		// for: a marketplace entry one label away reads as "ours" and is not.
		{"https://gl.acme.internal/a.git", "https://evil.gl.acme.internal/b.git", false},
		{"https://gl.acme.internal/a.git", "https://gl.acme.internal.evil.com/b.git", false},
		// A scheme change is a port change, so it is a different endpoint even
		// on one host.
		{"https://gl.acme.internal/a.git", "http://gl.acme.internal/b.git", false},
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

// SameEndpoint is currently unused: §21.2 was removed with the cross-repo
// second hop it guarded (v0.6.3, same-repo catalogues only). The tests stay,
// and so does the function.
//
// Keeping tested code that answers a question we will ask again is cheaper than
// rewriting it, and a guard rebuilt from memory under time pressure is how the
// subdomain case gets missed the second time. This test exists so the package
// does not look like it has dead weight nobody thought about.
func TestSameEndpointIsKeptForTheCrossRepoTrigger(t *testing.T) {
	same, err := SameEndpoint("https://gl.acme.internal/a.git", "https://evil.gl.acme.internal/b.git")
	if err != nil {
		t.Fatal(err)
	}
	if same {
		t.Error("the guard no longer rejects a subdomain; it is the case it exists for")
	}
}
