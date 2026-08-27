package hydrate

import (
	"fmt"
	"strings"
)

// SecretRef renders a reference to an environment variable in one runtime's own
// expansion syntax, or reports that the runtime has none.
//
// §34's table, and it does not generalise. Three runtimes have a generic
// syntax; codex does not — it has two SPECIFIC keys, handled in mcp.go, which
// is why it returns false here rather than a made-up placeholder. A made-up one
// would be written into config.toml verbatim and the agent would send the
// literal characters as a credential.
//
// The reference names the DECLARED binding variable. There is no launcher
// re-export and no synthesized name for env-sourced secrets (§34), which is
// what makes invoking the runtime CLI directly work wherever the environment is
// already populated — containers, CI.
func SecretRef(runtime, bindingVar string) (string, bool) {
	if bindingVar == "" {
		return "", false
	}
	switch runtime {
	case "claude":
		return "${" + bindingVar + "}", true
	case "opencode", "pi":
		return "{env:" + bindingVar + "}", true
	default:
		return "", false
	}
}

// APSecretVar is the variable the launcher exports for a FILE-sourced secret.
//
// Runtimes do not read files, so the launcher reads it and exports the value;
// materialized configuration references this name. The value exists only in the
// child process environment, never on disk.
//
// The consequence is stated rather than discovered: a profile using
// file-sourced secrets in runtime configuration REQUIRES launching through ap.
// Running the agent's CLI directly, which works for every env-sourced secret,
// finds nothing under this name.
func APSecretVar(binding string) string {
	var b strings.Builder
	b.WriteString("AP_SECRET_")
	for _, r := range strings.ToUpper(binding) {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			// Anything else becomes '_'. A binding name is a YAML key and may
			// hold '-', which is not legal in an environment variable name on
			// every shell that will inherit it.
			b.WriteByte('_')
		}
	}
	return b.String()
}

// ErrNoSecretSyntax is the refusal §34 requires.
//
// If a runtime cannot express a reference for an ACTIVE resource, apply fails
// in the resolution phase — the same error class as a missing secret.
// Warn-and-skip is forbidden here, and the reason is specific: it would
// materialize the resource WITHOUT its authentication, which is silently broken
// configuration the user discovers at the first request.
type ErrNoSecretSyntax struct {
	Runtime, Resource, Binding string
}

func (e ErrNoSecretSyntax) Error() string {
	return fmt.Sprintf(
		"%s references secret %q, and runtime %q cannot express a secret reference in its configuration; "+
			"materializing it without the credential would leave a resource that fails at its first request",
		e.Resource, e.Binding, e.Runtime)
}
