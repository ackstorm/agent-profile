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
//
// runtimeIsLocal says whether the runtime will execute on THIS machine, and
// every check here is conditional on it. Both check for an executable, and
// both executables belong to the runtime's process rather than to ap: an init
// container hydrating onto a volume that the MAIN container's runtime reads has
// neither the agent binary nor the `npx` an stdio server needs, and must not —
// that separation is the topology. Requiring them would make it impossible to
// hydrate for anywhere but here.
//
// The runtime is local whenever the root was named by a reference, because a
// profile exists to be launched. It is not when --root named a directory
// outright (§33.2).
func Preflight(p Profile, runtime string, runtimeIsLocal bool) error {
	agent, ok := agentreg.Lookup(runtime)
	if !ok {
		return fmt.Errorf("preflight: unknown runtime %q", runtime)
	}
	if !runtimeIsLocal {
		return nil
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
