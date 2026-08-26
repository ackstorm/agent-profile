package source

import (
	"encoding/base64"
	"strings"
)

// Scheme is §17.1's transport scheme for a git or archive credential.
type Scheme string

const (
	SchemeBearer      Scheme = "bearer"
	SchemeBasicOAuth2 Scheme = "basic-oauth2"
)

// ResolveScheme returns the scheme to use and whether it was INFERRED.
//
// The bool is the point. §17.1 admits inference here only because it is a
// protocol default with a declared escape hatch AND a mandatory disclosure:
// callers must report the scheme they used, and MUST name it in a 401. An
// inference that is invisible when it succeeds is undebuggable when it fails,
// and a self-hosted GitLab on a host named neither gitlab* nor git.* is exactly
// that case — it infers bearer, gets 401, and only an explicit scheme fixes it.
//
// The rule is ach's, kept verbatim because it was derived against real
// instances rather than documentation.
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

// AuthHeader is the Authorization value for a scheme.
//
// basic-oauth2 is not a guess: measured against a real self-hosted GitLab,
// Bearer answers 401 and Basic base64("oauth2:"+token) answers 200. github.com,
// bitbucket.org and gitlab.com (>= 15.x) all honour bearer.
func AuthHeader(s Scheme, token string) string {
	if s == SchemeBasicOAuth2 {
		return "Basic " + base64.StdEncoding.EncodeToString([]byte("oauth2:"+token))
	}
	return "Bearer " + token
}

// Report is the disclosure §17.1 requires: what scheme was used, and whether
// anyone chose it. Callers put it in successful diagnostics and MUST put it in
// a 401 — see gitError.
func (s Scheme) Report(inferred bool) string {
	if inferred {
		return string(s) + " (scheme inferred from host)"
	}
	return string(s) + " (scheme declared in the manifest)"
}
