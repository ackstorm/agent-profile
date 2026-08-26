package schema

import (
	"fmt"
	"os/exec"

	"github.com/ackstorm/agent-profile/pkg/agentreg"
)

// Preflight is §28: derived, never declared. It checks that the selected
// runtime CLI is executable and that every active stdio MCP transport's
// command resolves. A disabled MCP is not checked — the same "active only"
// rule Required applies to inputs.
func Preflight(p Profile, runtime string) error {
	agent, ok := agentreg.Lookup(runtime)
	if !ok {
		return fmt.Errorf("preflight: unknown runtime %q", runtime)
	}
	if _, err := exec.LookPath(agent.Bin); err != nil {
		return fmt.Errorf("preflight: runtime %q CLI %q is not executable: %w", runtime, agent.Bin, err)
	}

	for _, name := range sortedKeys(p.MCPs) {
		m := p.MCPs[name]
		if !m.Enabled || m.Transport.Type != "stdio" {
			continue
		}
		if _, err := exec.LookPath(m.Transport.Command); err != nil {
			return fmt.Errorf("preflight: mcp %q command %q is not executable: %w", name, m.Transport.Command, err)
		}
	}
	return nil
}
