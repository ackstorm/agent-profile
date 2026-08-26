package schema

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveInputsErrorsOnAnUndeclaredBindingNamingBothSides(t *testing.T) {
	p := Profile{Inputs: Inputs{}}
	refs := []Ref{{Resource: "skills.company-review", Kind: "secret", Name: "gitlab-token"}}
	_, err := ResolveInputs(p, refs)
	if err == nil {
		t.Fatal("want an error for an undeclared binding")
	}
	want := `skill "company-review" references secret "gitlab-token": no binding declared in inputs`
	if err.Error() != want {
		t.Errorf("err = %q, want %q", err.Error(), want)
	}
}

func TestResolveInputsRejectsABindingWithBothEnvAndFile(t *testing.T) {
	p := Profile{Inputs: Inputs{Secrets: map[string]Binding{
		"gitlab-token": {Env: "GITLAB_TOKEN", File: "/run/secrets/gitlab-token"},
	}}}
	refs := []Ref{{Resource: "skills.company-review", Kind: "secret", Name: "gitlab-token"}}
	_, err := ResolveInputs(p, refs)
	if err == nil || !strings.Contains(err.Error(), "both env and file") {
		t.Errorf("err = %v, want a complaint about both env and file", err)
	}
}

func TestResolveInputsRejectsABindingWithNeitherEnvNorFile(t *testing.T) {
	p := Profile{Inputs: Inputs{Secrets: map[string]Binding{
		"gitlab-token": {},
	}}}
	refs := []Ref{{Resource: "skills.company-review", Kind: "secret", Name: "gitlab-token"}}
	_, err := ResolveInputs(p, refs)
	if err == nil || !strings.Contains(err.Error(), "neither env nor file") {
		t.Errorf("err = %v, want a complaint about neither env nor file", err)
	}
}

func TestResolveInputsErrorsOnAnUnsetEnvVarNamingIt(t *testing.T) {
	t.Setenv("AP_TEST_UNSET_TOKEN", "")
	os.Unsetenv("AP_TEST_UNSET_TOKEN")
	p := Profile{Inputs: Inputs{Secrets: map[string]Binding{
		"gitlab-token": {Env: "AP_TEST_UNSET_TOKEN"},
	}}}
	refs := []Ref{{Resource: "skills.company-review", Kind: "secret", Name: "gitlab-token"}}
	_, err := ResolveInputs(p, refs)
	if err == nil || !strings.Contains(err.Error(), "AP_TEST_UNSET_TOKEN") {
		t.Errorf("err = %v, want it to name the environment variable", err)
	}
}

func TestResolveInputsReadsAnEnvBindingAndAFileBinding(t *testing.T) {
	t.Setenv("AP_TEST_TOKEN", "s3cr3t")
	dir := t.TempDir()
	file := filepath.Join(dir, "token")
	if err := os.WriteFile(file, []byte("f1l3-v4lu3"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := Profile{Inputs: Inputs{
		Secrets: map[string]Binding{"env-token": {Env: "AP_TEST_TOKEN"}},
		Variables: map[string]Binding{
			"file-var": {File: file},
		},
	}}
	refs := []Ref{
		{Resource: "model", Kind: "secret", Name: "env-token"},
		{Resource: "model", Kind: "variable", Name: "file-var"},
	}
	resolved, err := ResolveInputs(p, refs)
	if err != nil {
		t.Fatalf("resolveinputs: %v", err)
	}
	if v, ok := resolved.Value("secret", "env-token"); !ok || v != "s3cr3t" {
		t.Errorf("secret env-token = %q, %v, want s3cr3t, true", v, ok)
	}
	if v, ok := resolved.Value("variable", "file-var"); !ok || v != "f1l3-v4lu3" {
		t.Errorf("variable file-var = %q, %v, want f1l3-v4lu3, true", v, ok)
	}
	if _, ok := resolved.Value("secret", "no-such-input"); ok {
		t.Error("Value should report false for an input never resolved")
	}
}

// TestResolvedNeverPrintsASecretValue is the guard against the accident this
// phase exists to prevent: a Resolved formatted with %v or %+v — the two
// verbs a stray log.Printf or fmt.Errorf("%v", ...) would reach for — must
// never contain the secret value.
func TestResolvedNeverPrintsASecretValue(t *testing.T) {
	const secretValue = "super-s3cret-v4lue"
	t.Setenv("AP_TEST_REDACT_TOKEN", secretValue)
	p := Profile{Inputs: Inputs{Secrets: map[string]Binding{
		"gitlab-token": {Env: "AP_TEST_REDACT_TOKEN"},
	}}}
	refs := []Ref{{Resource: "skills.company-review", Kind: "secret", Name: "gitlab-token"}}
	resolved, err := ResolveInputs(p, refs)
	if err != nil {
		t.Fatalf("resolveinputs: %v", err)
	}
	for _, format := range []string{"%v", "%+v"} {
		out := fmt.Sprintf(format, resolved)
		if strings.Contains(out, secretValue) {
			t.Errorf("fmt.Sprintf(%q, resolved) = %q: leaked the secret value", format, out)
		}
	}
}
