//go:build unix

package main

import (
	"fmt"
	"os"

	"github.com/ackstorm/agent-profile/pkg/schema"
)

// cmdRenderOrValidate holds the one decision dispatch would otherwise make
// itself, so `render` and `validate` cost dispatch a single combined case
// instead of two — the difference between 20 and 21 against gocyclo's limit.
func cmdRenderOrValidate(cmd string, args []string) error {
	if cmd == "render" {
		return cmdRender(args)
	}
	return cmdValidate(args)
}

// cmdRender prints the effective profile for one runtime and mutates
// nothing. It parses its own flags with parseAroundRef, like create and
// unlike run: there is no passthrough here, so
// `ap render ./p.yaml --target claude` is unambiguous. Do not give this
// treatment to run.
func cmdRender(args []string) error {
	fs := flagSet("render")
	target := fs.String("target", "", "runtime to render for")
	stop, path, err := parseAroundRef(fs, args, "render [--target <runtime>] <manifest.yaml>")
	if stop {
		return err
	}
	if *target == "" {
		return fmt.Errorf("--target is required")
	}
	p, warns, err := schema.Effective(path, *target)
	if err != nil {
		return err
	}
	for _, w := range warns {
		fmt.Fprintln(os.Stderr, "warning:", w.Text)
	}
	_, err = os.Stdout.Write(schema.Render(p))
	return err
}

// cmdValidate is render without the printing, run for EVERY target the
// manifest declares rather than one: a manifest that composes for claude and
// not codex is broken, and the producer has to hear that in a single call.
// It is the contract check ach and ach-agent run against a manifest they
// generated, so its exit code — through dispatch's normal error handling —
// is the whole interface.
func cmdValidate(args []string) error {
	fs := flagSet("validate")
	stop, path, err := parseAroundRef(fs, args, "validate <manifest.yaml>")
	if stop {
		return err
	}
	targets, err := schema.Targets(path)
	if err != nil {
		return err
	}
	for _, t := range targets {
		_, warns, err := schema.Effective(path, t)
		if err != nil {
			return fmt.Errorf("%s: %w", t, err)
		}
		for _, w := range warns {
			fmt.Fprintf(os.Stderr, "warning: %s: %s\n", t, w.Text)
		}
	}
	return nil
}
