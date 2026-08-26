//go:build unix

package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ackstorm/agent-profile/internal/profile"
	"github.com/ackstorm/agent-profile/pkg/hydrate"
)

// listResources is `ap list <agent>:<profile>`: what the ledger holds.
//
// A resource is printed with the shape of what it wrote, because that is what
// uninstall will act on: a count of files for a whole-file resource, and the
// file plus the contributed keys for a merged one. A user asking "what is in
// this profile" and a user asking "what happens if I remove this" are looking
// at the same line.
func listResources(ref string, fs *flag.FlagSet, raw bool) error {
	agent, name, variant, err := profile.ParseVariantRefAllowDefault(ref)
	if err != nil {
		return err
	}
	if variant != "" {
		return fmt.Errorf("list takes a profile, not a variant: drop %q from %q", variant, ref)
	}
	if stop, err := parse(fs, fs.Args()[1:]); stop {
		return err
	}
	if extra := fs.Args(); len(extra) > 0 {
		return fmt.Errorf("unexpected argument %q\nusage: ap list [--raw] [<agent>[:<profile>]]", extra[0])
	}

	root := profile.Dir(agent, name)
	ledger, err := hydrate.LoadLedger(root)
	if err != nil {
		return err
	}
	return printLedger(os.Stdout, agent.Name, name, root, ledger, raw)
}

func printLedger(w io.Writer, agent, name, root string, l *hydrate.Ledger, raw bool) error {
	var b strings.Builder
	if !raw {
		fmt.Fprintf(&b, "%s:%s\n", agent, name)
		fmt.Fprintf(&b, "  %-10s %s\n", "root", root)
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
		fmt.Fprintf(&b, "\n  %d resource(s); `ap uninstall %s:%s <kind> <name>` removes one.\n",
			len(l.Resources), agent, name)
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
