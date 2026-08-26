package hydrate

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ackstorm/agent-profile/pkg/schema"
)

// specMemoryServer is SPEC §35's `memory` server: http, with an Authorization
// header sourced from the memory-token binding.
func specMemoryServer() schema.MCP {
	return schema.MCP{Enabled: true, Transport: schema.Transport{
		Type: "http", URL: "https://memory.company.com/mcp",
		Headers: map[string]schema.HeaderValue{
			"Authorization": {Prefix: "Bearer ", ValueFrom: &schema.ValueFrom{Secret: "memory-token"}},
			"X-Tenant":      {Value: "acme"},
		},
	}}
}

func specFilesystemServer() schema.MCP {
	return schema.MCP{Enabled: true, Transport: schema.Transport{
		Type: "stdio", Command: "npx",
		Args: []string{"-y", "@modelcontextprotocol/server-filesystem", "."},
	}}
}

var specBindings = map[string]string{"memory-token": "MEMORY_TOKEN"}

// One golden document per runtime, asserted WHOLE. A substring check passes for
// a document the runtime cannot load — which is the failure opencode's strict
// schema turns into a config-wide abort.
func TestEachRuntimeGetsItsOwnMCPDocument(t *testing.T) {
	for _, tc := range []struct{ runtime, want string }{
		{"claude", `{
  "headers": {
    "Authorization": "Bearer ${MEMORY_TOKEN}",
    "X-Tenant": "acme"
  },
  "type": "http",
  "url": "https://memory.company.com/mcp"
}`},
		// pi has NO type field for HTTP. Pi defines none, and an earlier shared
		// shape emitted a stray one.
		{"pi", `{
  "headers": {
    "Authorization": "Bearer {env:MEMORY_TOKEN}",
    "X-Tenant": "acme"
  },
  "url": "https://memory.company.com/mcp"
}`},
		// opencode: type is "remote", never "http", and enabled is carried.
		{"opencode", `{
  "enabled": true,
  "headers": {
    "Authorization": "Bearer {env:MEMORY_TOKEN}",
    "X-Tenant": "acme"
  },
  "type": "remote",
  "url": "https://memory.company.com/mcp"
}`},
		// codex has no generic syntax: a Bearer Authorization becomes
		// bearer_token_env_var carrying the variable NAME alone, and a literal
		// header stays in http_headers.
		{"codex", `{
  "bearer_token_env_var": "MEMORY_TOKEN",
  "http_headers": {
    "X-Tenant": "acme"
  },
  "url": "https://memory.company.com/mcp"
}`},
	} {
		t.Run(tc.runtime, func(t *testing.T) {
			got, err := MCPEntry(tc.runtime, "memory", specMemoryServer(), specBindings)
			if err != nil {
				t.Fatal(err)
			}
			out, err := json.MarshalIndent(got, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if string(out) != tc.want {
				t.Errorf("document differs:\ngot:\n%s\nwant:\n%s", out, tc.want)
			}
			// The value is never in the document, whatever the shape.
			if strings.Contains(string(out), "sk-") {
				t.Errorf("a value leaked: %s", out)
			}
		})
	}
}

func TestStdioServersPerRuntime(t *testing.T) {
	for _, tc := range []struct{ runtime, want string }{
		{"claude", `{
  "args": [
    "-y",
    "@modelcontextprotocol/server-filesystem",
    "."
  ],
  "command": "npx"
}`},
		// opencode's local shape takes command as an ARRAY with the binary
		// first, and the key is "type": "local".
		{"opencode", `{
  "command": [
    "npx",
    "-y",
    "@modelcontextprotocol/server-filesystem",
    "."
  ],
  "enabled": true,
  "type": "local"
}`},
	} {
		t.Run(tc.runtime, func(t *testing.T) {
			got, err := MCPEntry(tc.runtime, "filesystem", specFilesystemServer(), nil)
			if err != nil {
				t.Fatal(err)
			}
			out, _ := json.MarshalIndent(got, "", "  ")
			if string(out) != tc.want {
				t.Errorf("document differs:\ngot:\n%s\nwant:\n%s", out, tc.want)
			}
		})
	}
}

// A non-Bearer sourced header on codex keeps its name and travels as an
// env_http_headers entry. Folding it into bearer_token_env_var would silently
// drop the header name and any prefix.
func TestCodexKeepsANonBearerSourcedHeaderAsAnEnvHeader(t *testing.T) {
	m := schema.MCP{Enabled: true, Transport: schema.Transport{
		Type: "http", URL: "https://x/mcp",
		Headers: map[string]schema.HeaderValue{
			"X-Api-Key": {ValueFrom: &schema.ValueFrom{Secret: "memory-token"}},
		},
	}}
	got, err := MCPEntry("codex", "memory", m, specBindings)
	if err != nil {
		t.Fatal(err)
	}
	env, ok := got["env_http_headers"].(map[string]any)
	if !ok || env["X-Api-Key"] != "MEMORY_TOKEN" {
		t.Errorf("entry = %+v, want X-Api-Key in env_http_headers naming the variable", got)
	}
	if _, wrong := got["bearer_token_env_var"]; wrong {
		t.Error("a non-Authorization header became bearer_token_env_var")
	}
}

// An input with no binding is an error naming it, not a reference to nothing.
func TestAnUnboundInputIsAnErrorNamingIt(t *testing.T) {
	_, err := MCPEntry("claude", "memory", specMemoryServer(), map[string]string{})
	if err == nil {
		t.Fatal("an unbound input produced a document")
	}
	if !strings.Contains(err.Error(), "memory-token") {
		t.Errorf("error %q does not name the input", err)
	}
}

// §34's refusal, reached through a real document rather than through
// SecretRef alone: nothing between the manifest and the file may route around
// it.
func TestARuntimeWithoutASecretSyntaxRefusesRatherThanInventingOne(t *testing.T) {
	// A runtime with a generic-syntax path but no syntax would be the bug this
	// guards; codex is routed away from renderHeaders entirely, so it must not
	// produce a placeholder anywhere in its document.
	got, err := MCPEntry("codex", "memory", specMemoryServer(), specBindings)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(got)
	for _, forbidden := range []string{"${", "{env:"} {
		if strings.Contains(string(out), forbidden) {
			t.Errorf("codex's document carries a placeholder it cannot expand: %s", out)
		}
	}

	var target ErrNoSecretSyntax
	_, err = renderHeaders("codex", "memory", specMemoryServer().Transport.Headers, specBindings)
	if !errors.As(err, &target) {
		t.Errorf("renderHeaders for codex = %v, want ErrNoSecretSyntax", err)
	}
}
