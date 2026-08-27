package hydrate

import (
	"strings"
	"testing"
)

// §34's table. It does not generalise, which is the point of testing it as a
// table rather than deriving it.
func TestSecretReferencesUseEachRuntimesOwnSyntax(t *testing.T) {
	for _, tc := range []struct {
		runtime, want string
		ok            bool
	}{
		{"claude", "${MEMORY_TOKEN}", true},
		{"opencode", "{env:MEMORY_TOKEN}", true},
		{"pi", "{env:MEMORY_TOKEN}", true},
		// codex has no generic syntax — only bearer_token_env_var and
		// env_http_headers, which mcp.go handles. Returning a made-up
		// placeholder here would put literal characters into config.toml and
		// codex would send them AS the credential.
		{"codex", "", false},
	} {
		got, ok := SecretRef(tc.runtime, "MEMORY_TOKEN")
		if got != tc.want || ok != tc.ok {
			t.Errorf("SecretRef(%q) = %q,%v want %q,%v", tc.runtime, got, ok, tc.want, tc.ok)
		}
	}
}

// The whole point: a reference, never a value. This asserts with a value in
// hand, because the failure mode is a helper that "conveniently" resolves.
func TestASecretReferenceNeverContainsTheValue(t *testing.T) {
	const value = "sk-do-not-materialize-me"
	t.Setenv("MEMORY_TOKEN", value)
	for _, runtime := range []string{"claude", "opencode", "pi", "codex"} {
		got, _ := SecretRef(runtime, "MEMORY_TOKEN")
		if strings.Contains(got, value) {
			t.Errorf("%s: the reference resolved to the value: %q", runtime, got)
		}
	}
}

// A binding name is a YAML key and may hold '-', which is not a legal
// environment variable name on every shell that will inherit it.
func TestAPSecretVarIsAUsableEnvironmentName(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"memory-token", "AP_SECRET_MEMORY_TOKEN"},
		{"gitlab_token", "AP_SECRET_GITLAB_TOKEN"},
		{"tok3n", "AP_SECRET_TOK3N"},
		{"a.b c", "AP_SECRET_A_B_C"},
	} {
		if got := APSecretVar(tc.in); got != tc.want {
			t.Errorf("APSecretVar(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The refusal must say WHY, or the obvious workaround is to remove the auth
// block and ship a server that fails at its first request.
func TestTheNoSyntaxRefusalNamesTheResourceTheRuntimeAndTheBinding(t *testing.T) {
	err := ErrNoSecretSyntax{Runtime: "codex", Resource: `mcp "memory"`, Binding: "memory-token"}
	msg := err.Error()
	for _, want := range []string{"codex", "memory", "memory-token", "first request"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal %q lacks %q", msg, want)
		}
	}
}
