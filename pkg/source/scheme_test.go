package source

import (
	"encoding/base64"
	"strings"
	"testing"
)

// §17.1. The table is ach's rule, and the `inferred` column is what makes the
// inference admissible at all: it must be reportable, or a 401 cannot say what
// was tried.
func TestSchemeIsInferredFromHostAndAlwaysReported(t *testing.T) {
	for _, tc := range []struct {
		declared, host string
		want           Scheme
		inferred       bool
	}{
		{"", "gitlab.com", SchemeBasicOAuth2, true},
		{"", "gitlab.acme.internal", SchemeBasicOAuth2, true},
		{"", "GitLab.ACME.internal", SchemeBasicOAuth2, true},
		{"", "git.acme.internal", SchemeBasicOAuth2, true},
		{"", "github.com", SchemeBearer, true},
		{"", "bitbucket.org", SchemeBearer, true},
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

// A report that does not distinguish inferred from declared is useless in the
// one situation it exists for: a 401 where the user must decide whether to
// override the scheme or fix the token.
func TestSchemeReportSaysWhoChose(t *testing.T) {
	if got := SchemeBearer.Report(true); !strings.Contains(got, "inferred") || !strings.Contains(got, "bearer") {
		t.Errorf("inferred report = %q", got)
	}
	if got := SchemeBasicOAuth2.Report(false); !strings.Contains(got, "declared") || !strings.Contains(got, "basic-oauth2") {
		t.Errorf("declared report = %q", got)
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
