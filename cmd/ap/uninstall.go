//go:build unix

package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ackstorm/agent-profile/internal/profile"
	"github.com/ackstorm/agent-profile/pkg/hydrate"
)

// cmdUninstall removes one capability from a root, bounded by the ledger
// (§33.3).
//
// It may not remove anything the ledger does not own, which is what makes it
// safe against `<agent>:default` — the agent's real configuration directory,
// the case SPEC v0.5 gave up on. The ledger can tell ap's writes from the
// user's, so there is no gate here: removal is bounded by a record, not by a
// question.
func cmdUninstall(args []string) error {
	const use = "uninstall <agent>:<profile> <kind> <name> [--dry-run]"
	fs := flagSet("uninstall")
	dryRun := fs.Bool("dry-run", false, "print what would be removed; touch nothing")

	stop, pos, err := parsePositionals(fs, args, use, 3)
	if stop {
		return err
	}
	if len(pos) != 3 {
		return fmt.Errorf("usage: ap %s", use)
	}
	agent, name, variant, err := profile.ParseVariantRefAllowDefault(pos[0])
	if err != nil {
		return err
	}
	if variant != "" {
		return fmt.Errorf("uninstall takes a profile, not a variant: drop %q from %q", variant, pos[0])
	}

	root := profile.Dir(agent, name)
	rm, err := hydrate.Remove(root, pos[1], pos[2], *dryRun)
	if err != nil {
		return err
	}
	return printRemoval(os.Stdout, agent.Name, name, rm, *dryRun)
}

// printRemoval prints the verdicts — the same values in both modes, because
// there is one classifier and the preview is literally what ran.
func printRemoval(w io.Writer, agent, name string, rm hydrate.Removal, dryRun bool) error {
	var b strings.Builder
	verb := "removed"
	if dryRun {
		verb = "would remove"
	}
	fmt.Fprintf(&b, "%s %s %q\n", verb, rm.Kind, rm.Name)
	fmt.Fprintf(&b, "  %-10s %s:%s\n", "root", agent, name)

	for _, v := range rm.Verdicts {
		switch v.Op {
		case "remove":
			fmt.Fprintf(&b, "  - %s\n", v.Path)
		case "keys":
			fmt.Fprintf(&b, "  ~ %-24s removed %s\n", v.Path, strings.Join(v.Keys, ", "))
		case "skip":
			fmt.Fprintf(&b, "  ! %-24s kept: %s\n", v.Path, v.Reason)
		case "gone":
			fmt.Fprintf(&b, "  · %-24s already absent\n", v.Path)
		}
	}

	switch {
	case dryRun:
		fmt.Fprintln(&b, "\n  --dry-run: nothing was removed.")
	case !rm.Removed():
		fmt.Fprintln(&b, "\n  nothing was removed; the record was dropped.")
	}
	_, err := io.WriteString(w, b.String())
	return err
}
