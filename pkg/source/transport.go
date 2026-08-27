package source

import (
	"fmt"
	"net/url"
	"strings"
)

// This file holds BOTH credential guards on purpose. They are one question —
// may this credential go to this endpoint? — asked at two moments, and
// splitting them across files is how one gets updated and the other does not.

// CheckCredentialTransport refuses to let a credential cross plaintext (§17.2).
//
// It is called before a fetch that WOULD carry one, never on an anonymous
// fetch: an http:// URL with no credential is merely unwise, and refusing it
// outright would break local test servers for no security gain.
//
// The other half of §17.2 — never put the credential in the URL — is not
// checkable here, because it is a property of how the fetch is invoked. It
// lives in git.go, which passes the credential through http.extraHeader.
func CheckCredentialTransport(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parse %q: %w", rawURL, err)
	}
	if !strings.EqualFold(u.Scheme, "https") {
		scheme := u.Scheme
		if scheme == "" {
			scheme = "(none)"
		}
		return fmt.Errorf("%s: refusing to send a credential over %s:// — use https", u.Host, scheme)
	}
	// A credential written into the URL is already leaked by the time it gets
	// here: the caller's own argv carries it, and git would persist it in
	// remote.origin.url. Refuse rather than strip, or the manifest keeps
	// looking correct while ap quietly rewrites it.
	if u.User != nil {
		return fmt.Errorf("%s: the URL carries credentials; declare them under auth instead — a credential in a URL is visible in /proc/<pid>/cmdline and git persists it in remote.origin.url", u.Hostname())
	}
	return nil
}

// SameEndpoint reports whether two URLs name the same host AND effective port.
//
// CURRENTLY UNUSED, and deliberately kept. It was §21.2's guard, and §21.2 was
// removed in v0.6.3 along with the thing it guarded: a marketplace entry may
// only name a source INSIDE its own repository, so there is no cross-host
// second hop for a credential to reach. The guard is day-one machinery for the
// moment that trigger fires — cross-repo catalogue entries — and tested code
// that answers a question we will ask again is cheaper to keep than to rewrite. The effective port is the explicit one or the
// scheme's default, so https://h and https://h:443 are one endpoint — while a
// different port on the same host may be an entirely different service, and
// does not inherit the credential.
//
// Host comparison is case-insensitive and EXACT. A subdomain is not the same
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

// effectivePort is the explicit port or the scheme's default. An unknown
// scheme has no default, and two URLs with the same unknown scheme and no port
// compare equal on "" — which is correct: they are as alike as this function
// can tell, and SameEndpoint's other half still requires the hosts to match.
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
