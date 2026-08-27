//go:build unix

package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ackstorm/agent-profile/pkg/hydrate"
)

// listResources is `ap list <agent>:<profile>`: what the ledger holds.
//
// A resource is printed with the shape of what it wrote, because that is what
// uninstall will act on: a count of files for a whole-file resource, and the
// file plus the contributed keys for a merged one. A user asking "what is in
// this profile" and a user asking "what happens if I remove this" are looking
// at the same line.
func listResources(ref, rootFlag string, raw bool) error {
	tgt, err := resolveTarget(ref, rootFlag, "list")
	if err != nil {
		return err
	}
	ledger, err := hydrate.LoadLedger(tgt.Root)
	if err != nil {
		return err
	}
	return printLedger(os.Stdout, tgt, ledger, raw)
}

func printLedger(w io.Writer, tgt target, l *hydrate.Ledger, raw bool) error {
	var b strings.Builder
	if !raw {
		fmt.Fprintln(&b, tgt.Label())
		fmt.Fprintf(&b, "  %-10s %s\n", "root", tgt.Root)
	}
	if len(l.Resources) == 0 && !raw {
		fmt.Fprintln(&b, "\n  nothing is installed; `ap manifest apply` or `ap install` puts something here.")
		_, err := io.WriteString(w, b.String())
		return err
	}

	for _, r := range l.Resources {
		if raw {
			// Tab-separated for scripts, and the two fields a script needs to
			// call uninstall are the first two.
			fmt.Fprintf(&b, "%s\t%s\t%s\n", r.Kind, r.Name, r.ResolvedRef)
			continue
		}
		fmt.Fprintf(&b, "  %-9s %-16s %s\n", r.Kind, r.Name, describeFiles(r.Files))
	}
	if !raw {
		fmt.Fprintf(&b, "\n  %d resource(s); `ap uninstall %s <kind> <name>` removes one.\n",
			len(l.Resources), tgt.Label())
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// describeFiles says what a resource put where, in the terms uninstall uses.
func describeFiles(files []hydrate.FileRec) string {
	if len(files) == 1 && files[0].Merge != "" {
		return fmt.Sprintf("%s  %s", files[0].RelPath, strings.Join(files[0].Keys, ", "))
	}
	if len(files) == 1 {
		return files[0].RelPath
	}
	return fmt.Sprintf("%d file(s)", len(files))
}
