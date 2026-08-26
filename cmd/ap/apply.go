//go:build unix

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ackstorm/agent-profile/pkg/schema"
)

// cmdApply is the CLI's window onto the resolution phase: `--dry-run` runs
// exactly schema.Resolve and prints its result with secrets redacted, then
// stops. Materialization — actually applying a manifest — is Phase 4 and
// does not exist yet; without `--dry-run` this says so and exits non-zero
// rather than stubbing a fake success.
//
// It parses its own flags, like render and validate: apply has nothing to
// pass through to an agent, so there is no ambiguity a flag could create.
// Unlike render and validate, the manifest is not always a bare positional —
// `--manifest -` lets a caller (ach) pipe a generated manifest with no temp
// file of its own — so a zero-positional invocation must be valid when
// --manifest is given. parseAroundRef always requires one positional
// argument, so this runs the same two-pass parse it does with that one
// requirement relaxed, rather than reusing it unmodified.
func cmdApply(args []string) error {
	const use = "apply [--target <runtime>] [--dry-run] [--strict] [--manifest -] <manifest.yaml>"
	fs := flagSet("apply")
	target := fs.String("target", "", "runtime to resolve for")
	dryRun := fs.Bool("dry-run", false, "run the resolution phase and print it; touch nothing")
	strict := fs.Bool("strict", false, "promote every degradation warning (§7.2, §8) to an error")
	manifestFlag := fs.String("manifest", "", `manifest path, or "-" to read it from stdin`)

	if stop, err := parse(fs, args); stop {
		return err
	}
	var ref string
	if rest := fs.Args(); len(rest) > 0 {
		ref = rest[0]
		if stop, err := parse(fs, rest[1:]); stop {
			return err
		}
		if extra := fs.Args(); len(extra) > 0 {
			return fmt.Errorf("unexpected argument %q\nusage: ap %s", extra[0], use)
		}
	}
	switch {
	case ref == "" && *manifestFlag == "":
		return fmt.Errorf("usage: ap %s", use)
	case ref != "" && *manifestFlag != "":
		return fmt.Errorf("name the manifest once: a path or --manifest, not both")
	case *target == "":
		return fmt.Errorf("--target is required")
	}

	path := ref
	if *manifestFlag != "" {
		p, cleanup, err := manifestPath(*manifestFlag)
		if err != nil {
			return err
		}
		defer cleanup()
		path = p
	}

	res, resolved, err := schema.Resolve(path, *target, *strict)
	if err != nil {
		return err
	}

	if !*dryRun {
		return fmt.Errorf("materialization is Phase 4 and does not exist yet; rerun with --dry-run")
	}

	return printResolution(os.Stdout, res, resolved)
}

// manifestPath resolves --manifest's value to a real file path. "-" reads
// the manifest from stdin into a file inside its own MkdirTemp directory,
// which the caller removes; any other value is returned as-is, unchanged.
func manifestPath(v string) (path string, cleanup func(), err error) {
	if v != "-" {
		return v, func() {}, nil
	}
	dir, err := os.MkdirTemp("", "ap-apply-")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { _ = os.RemoveAll(dir) }
	b, err := io.ReadAll(os.Stdin)
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("reading manifest from stdin: %w", err)
	}
	path = filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		cleanup()
		return "", nil, err
	}
	return path, cleanup, nil
}

// printResolution is --dry-run's entire output: which runtime, every input
// the effective profile actually needs (by binding NAME only — Resolved's
// String method redacts, so even printing the value itself here could not
// leak one), and every degradation warning. Nothing below reads a resolved
// value.
func printResolution(w io.Writer, res *schema.Resolution, resolved *schema.Resolved) error {
	var b strings.Builder
	fmt.Fprintln(&b, "dry run: the resolution phase only — nothing is fetched (source fetching is Phase 3)")
	fmt.Fprintf(&b, "profile %q for %s\n", res.Profile.Name, res.Runtime)
	if len(res.Refs) == 0 {
		fmt.Fprintln(&b, "no inputs required")
	}
	for _, r := range res.Refs {
		fmt.Fprintf(&b, "  %s references %s %q\n", r.Resource, r.Kind, r.Name)
	}
	for _, warn := range res.Warnings {
		fmt.Fprintln(&b, "warning:", warn.Text)
	}
	fmt.Fprintln(&b, resolved)
	_, err := io.WriteString(w, b.String())
	return err
}

// cmdSchema prints the manifest's JSON Schema and exits 0. No flags: the
// schema does not vary by argument.
func cmdSchema(args []string) error {
	fs := flagSet("schema")
	if stop, err := parse(fs, args); stop {
		return err
	}
	if extra := fs.Args(); len(extra) > 0 {
		return fmt.Errorf("unexpected argument %q\nusage: ap schema", extra[0])
	}
	_, err := os.Stdout.Write(schema.JSONSchema())
	return err
}
