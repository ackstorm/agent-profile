package hydrate

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ackstorm/agent-profile/pkg/schema"
)

// MCPEntry builds one server's native shape for one runtime.
//
// There is no shared shape and no base struct with per-runtime tweaks. Each of
// the four is a different document, and the two that look alike differ in the
// one field that matters — pi's HTTP entry has NO type key, because pi defines
// none, and emitting one is how a stray key reaches a strict schema.
//
// bindings maps a manifest input name to the environment variable a reference
// should name. Nothing here reads a value; §34 forbids it outside the
// --allow-plaintext-secrets path, which lives in the caller.
func MCPEntry(runtime, id string, m schema.MCP, bindings map[string]string) (map[string]any, error) {
	switch runtime {
	case "claude":
		return claudeMCP(runtime, id, m, bindings)
	case "pi":
		return piMCP(runtime, id, m, bindings)
	case "opencode":
		return opencodeMCP(runtime, id, m, bindings)
	case "codex":
		return codexMCP(id, m, bindings)
	}
	return nil, fmt.Errorf("runtime %q has no MCP surface", runtime)
}

func claudeMCP(runtime, id string, m schema.MCP, b map[string]string) (map[string]any, error) {
	if m.Transport.Type == "stdio" {
		return map[string]any{
			"command": m.Transport.Command,
			"args":    argsOrEmpty(m.Transport.Args),
		}, nil
	}
	h, err := renderHeaders(runtime, id, m.Transport.Headers, b)
	if err != nil {
		return nil, err
	}
	e := map[string]any{"type": "http", "url": m.Transport.URL}
	if len(h) > 0 {
		e["headers"] = h
	}
	return e, nil
}

// piMCP omits `type` for an HTTP server. Pi's schema is url (presence implies
// StreamableHTTP with SSE fallback) plus headers, and it defines no type field
// for HTTP — an earlier shared shape emitted a stray one. Measured by ach.
func piMCP(runtime, id string, m schema.MCP, b map[string]string) (map[string]any, error) {
	if m.Transport.Type == "stdio" {
		return map[string]any{
			"command": m.Transport.Command,
			"args":    argsOrEmpty(m.Transport.Args),
		}, nil
	}
	h, err := renderHeaders(runtime, id, m.Transport.Headers, b)
	if err != nil {
		return nil, err
	}
	e := map[string]any{"url": m.Transport.URL}
	if len(h) > 0 {
		e["headers"] = h
	}
	return e, nil
}

// opencodeMCP converts to opencode's STRICT per-entry schema.
//
// The conversion is mandatory, not cosmetic: opencode validates each entry and
// aborts the whole configuration on a mismatch — a real ConfigInvalidError
// observed against 1.16.0. So `type` is "remote", never "http"; `command` is an
// ARRAY with the binary first; environment lives under "environment"; and every
// other field is dropped rather than passed through, because a closed schema
// rejects what it does not know.
func opencodeMCP(runtime, id string, m schema.MCP, b map[string]string) (map[string]any, error) {
	if m.Transport.Type == "stdio" {
		cmd := append([]string{m.Transport.Command}, m.Transport.Args...)
		return map[string]any{"type": "local", "command": cmd, "enabled": true}, nil
	}
	h, err := renderHeaders(runtime, id, m.Transport.Headers, b)
	if err != nil {
		return nil, err
	}
	e := map[string]any{"type": "remote", "url": m.Transport.URL, "enabled": true}
	if len(h) > 0 {
		e["headers"] = h
	}
	return e, nil
}

// codexMCP is the one that has no generic expansion syntax, only two specific
// keys — which is why SecretRef returns false for codex.
//
//   - a Bearer Authorization becomes bearer_token_env_var, the variable NAME
//     alone;
//   - any other sourced header becomes an env_http_headers entry, again the
//     name alone;
//   - a literal header value becomes an http_headers entry.
//
// A LITERAL Authorization never reaches here: §14 makes it a validation error,
// because a literal there is a secret embedded in the manifest.
func codexMCP(id string, m schema.MCP, b map[string]string) (map[string]any, error) {
	if m.Transport.Type == "stdio" {
		return map[string]any{
			"command": m.Transport.Command,
			"args":    argsOrEmpty(m.Transport.Args),
		}, nil
	}
	e := map[string]any{"url": m.Transport.URL}
	envHeaders := map[string]any{}
	literalHeaders := map[string]any{}

	for _, name := range sortedHeaderNames(m.Transport.Headers) {
		hv := m.Transport.Headers[name]
		if hv.ValueFrom == nil {
			literalHeaders[name] = hv.Value
			continue
		}
		v, err := bindingVar(b, hv.ValueFrom.Secret, hv.ValueFrom.Variable)
		if err != nil {
			return nil, fmt.Errorf("mcp %q: header %q: %w", id, name, err)
		}
		// Only a "Bearer " prefix maps to codex's dedicated key. Anything else
		// keeps its header name and travels as an env_http_headers entry, or
		// the prefix would be silently dropped.
		if name == "Authorization" && strings.TrimSpace(hv.Prefix) == "Bearer" {
			e["bearer_token_env_var"] = v
			continue
		}
		envHeaders[name] = v
	}
	if len(envHeaders) > 0 {
		e["env_http_headers"] = envHeaders
	}
	if len(literalHeaders) > 0 {
		e["http_headers"] = literalHeaders
	}
	return e, nil
}

// renderHeaders turns §14 header values into strings for a runtime WITH a
// generic expansion syntax. A runtime without one never reaches here.
func renderHeaders(runtime, id string, headers map[string]schema.HeaderValue, b map[string]string) (map[string]any, error) {
	if len(headers) == 0 {
		return nil, nil
	}
	out := map[string]any{}
	for _, name := range sortedHeaderNames(headers) {
		hv := headers[name]
		if hv.ValueFrom == nil {
			out[name] = hv.Value
			continue
		}
		v, err := bindingVar(b, hv.ValueFrom.Secret, hv.ValueFrom.Variable)
		if err != nil {
			return nil, fmt.Errorf("mcp %q: header %q: %w", id, name, err)
		}
		ref, ok := SecretRef(runtime, v)
		if !ok {
			return nil, ErrNoSecretSyntax{Runtime: runtime, Resource: fmt.Sprintf("mcp %q", id), Binding: v}
		}
		out[name] = hv.Prefix + ref
	}
	return out, nil
}

// bindingVar resolves an input NAME to the environment variable a reference
// should name. Never to a value.
func bindingVar(b map[string]string, secret, variable string) (string, error) {
	name := secret
	if name == "" {
		name = variable
	}
	if name == "" {
		return "", fmt.Errorf("value_from names neither a secret nor a variable")
	}
	v, ok := b[name]
	if !ok {
		return "", fmt.Errorf("input %q has no binding", name)
	}
	return v, nil
}

func sortedHeaderNames(h map[string]schema.HeaderValue) []string {
	out := make([]string, 0, len(h))
	for k := range h {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// argsOrEmpty keeps an empty args list as [] rather than dropping it, so a
// re-render is byte-identical whether or not a server declared arguments.
func argsOrEmpty(a []string) []string {
	if a == nil {
		return []string{}
	}
	return a
}
