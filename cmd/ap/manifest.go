//go:build unix

package main

import (
	"fmt"
	"os"

	"github.com/ackstorm/agent-profile/internal/profile"
	"github.com/ackstorm/agent-profile/pkg/hydrate"
	"github.com/ackstorm/agent-profile/pkg/schema"
)

// cmdManifest is the `ap manifest` group: the operations whose input is a
// whole manifest, as opposed to `install`/`uninstall`, whose input is one
// loose capability. The split is the architecture's — two inputs reach a
// root, and the command surface is that split and nothing else.
//
// Its subcommand is the first positional, not a flag, and everything after
// it belongs to that subcommand. This is not `run`: nothing here passes
// arguments through to an agent, so parsing flags on either side of the
// subject is unambiguous.
func cmdManifest(args []string) error {
	const use = "manifest <render|apply|export> ..."
	if len(args) == 0 {
		return fmt.Errorf("usage: ap %s", use)
	}
	switch args[0] {
	case "render":
		return manifestRender(args[1:])
	case "apply":
		return manifestApply(args[1:])
	case "export":
		return manifestExport(args[1:])
	default:
		return fmt.Errorf("unknown manifest subcommand %q\nusage: ap %s", args[0], use)
	}
}

// manifestRender answers what a manifest MEANS, before any root is chosen.
// It is the only operation here with no reference: its subject is a file.
//
// --target is optional, and its absence is not a missing argument — it
// selects the other mode. With a target, the effective profile for that one
// runtime is printed. Without one, EVERY target the manifest declares is
// composed and nothing is printed: a manifest that composes for claude and
// not codex is broken, and the exit status is how a producer in another
// repository hears that in a single call. That is why there is no separate
// `validate` command; it was folded here rather than kept as a second name
// for the same pipeline.
func manifestRender(args []string) error {
	fs := flagSet("manifest")
	target := fs.String("target", "", "runtime to render for; omit to compose every declared target")
	quiet := fs.Bool("quiet", false, "check only: compose and print nothing")
	stop, path, err := parseAroundRef(fs, args, "manifest render [--target <runtime>] [--quiet] <manifest.yaml>")
	if stop {
		return err
	}

	targets := []string{*target}
	if *target == "" {
		if targets, err = schema.Targets(path); err != nil {
			return err
		}
	}

	for _, t := range targets {
		p, warns, err := schema.Effective(path, t)
		if err != nil {
			return fmt.Errorf("%s: %w", t, err)
		}
		for _, w := range warns {
			fmt.Fprintf(os.Stderr, "warning: %s: %s\n", t, w.Text)
		}
		// One target and not asked to be quiet is the only case that prints.
		// Composing every target and printing each would emit several
		// documents to one stream with nothing separating them, which is not
		// a format anything could consume.
		if *target != "" && !*quiet {
			if _, err := os.Stdout.Write(schema.Render(p)); err != nil {
				return err
			}
		}
	}
	return nil
}

// manifestExport turns a root's ledger back into a manifest (§35.2), closing
// the loop:
//
//	manifest → apply → ledger → export → manifest
//
// It is what makes "a manifest is an INPUT" workable: a profile assembled one
// `ap install` at a time becomes portable without ever having been written
// down. It prints to stdout and writes nothing, because nothing in this program
// writes a manifest — the user decides where it lands.
func manifestExport(args []string) error {
	const use = "manifest export <agent>:<profile> [--name <name>]"
	fs := flagSet("manifest")
	nameFlag := fs.String("name", "", "the manifest's name; defaults to the profile's")
	stop, ref, err := parseAroundRef(fs, args, use)
	if stop {
		return err
	}
	agent, name, variant, err := profile.ParseVariantRefAllowDefault(ref)
	if err != nil {
		return err
	}
	if variant != "" {
		return fmt.Errorf("export takes a profile, not a variant: drop %q from %q", variant, ref)
	}

	manifestName := *nameFlag
	if manifestName == "" {
		manifestName = name
	}
	p, err := hydrate.Export(profile.Dir(agent, name), manifestName, []string{agent.Name})
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(schema.Render(p))
	return err
}
