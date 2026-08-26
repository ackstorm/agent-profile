//go:build unix

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ackstorm/agent-profile/internal/profile"
	"github.com/ackstorm/agent-profile/pkg/schema"
	"github.com/ackstorm/agent-profile/pkg/source"
)

// manifestApply is the CLI's window onto the resolution phase: `--dry-run`
// runs exactly schema.Resolve and prints its result with secrets redacted,
// then stops. Materialization — actually applying a manifest — is Phase 4 and
// does not exist yet; without `--dry-run` this says so and exits non-zero
// rather than stubbing a fake success.
//
// The first argument is a REFERENCE, like every other command that changes or
// inspects a root, and there is no --target: the reference already names the
// agent. `ach-cli` needs that flag because its verbs address a tool; here
// `claude:plan` states the runtime and the root in one token. render is the
// exception that keeps --target, because a file has no root to read it from.
//
// The manifest is the second positional, or `--manifest -` to read it from
// stdin so a caller (ach, ach-agent) can pipe a generated one with no temp
// file of its own. Exactly one of the two.
func manifestApply(args []string) error {
	const use = "manifest apply <agent>:<profile> [--dry-run] [--strict] [--manifest -] <manifest.yaml>"
	fs := flagSet("manifest")
	dryRun := fs.Bool("dry-run", false, "run the resolution phase and print it; touch nothing")
	strict := fs.Bool("strict", false, "promote every degradation warning (§7.2, §8) to an error")
	manifestFlag := fs.String("manifest", "", `manifest path, or "-" to read it from stdin`)

	stop, pos, err := parsePositionals(fs, args, use, 2)
	if stop {
		return err
	}
	if len(pos) == 0 {
		return fmt.Errorf("usage: ap %s", use)
	}

	// A two-segment reference only. A variant is a set of launch arguments
	// over a profile; it is not a root, and applying "into" one would have no
	// meaning. Default is allowed here and gated at materialization, not now:
	// the resolution phase writes nothing to gate.
	agent, name, variant, err := profile.ParseVariantRefAllowDefault(pos[0])
	if err != nil {
		return err
	}
	if variant != "" {
		return fmt.Errorf("apply takes a profile, not a variant: drop %q from %q", variant, pos[0])
	}

	var ref string
	if len(pos) > 1 {
		ref = pos[1]
	}
	switch {
	case ref == "" && *manifestFlag == "":
		return fmt.Errorf("usage: ap %s", use)
	case ref != "" && *manifestFlag != "":
		return fmt.Errorf("name the manifest once: a path or --manifest, not both")
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

	res, resolved, err := schema.Resolve(path, agent.Name, *strict)
	if err != nil {
		return err
	}

	if !*dryRun {
		return fmt.Errorf("materialization is Phase 4 and does not exist yet; rerun with --dry-run")
	}

	// The cache root is a parameter, like every root in pkg/. It is the one
	// thing a dry run may create: a cache is not a materialization root, and
	// §37.1 says source acquisition during resolution is not mutation.
	cacheRoot, err := profile.CacheDir()
	if err != nil {
		return err
	}
	cache, err := source.NewCache(cacheRoot)
	if err != nil {
		return err
	}

	fetched, reports, err := source.Resolve(context.Background(), res.Profile, source.Opts{
		Cache:       cache,
		ManifestDir: filepath.Dir(path),
		Secret: func(name string) (string, error) {
			v, ok := resolved.Value("secret", name)
			if !ok {
				return "", source.ErrSecretUnset
			}
			return v, nil
		},
	})
	if err != nil {
		return err
	}

	return printResolution(os.Stdout, res, resolved, agent.Name, name, fetched, reports)
}

// parsePositionals is parseAroundRef generalised to at most max positional
// arguments, because apply takes a reference AND a manifest path, and may
// take only the reference when --manifest supplies the second.
//
// flag.Parse stops at the first non-flag argument, so this alternates: take a
// positional, reparse whatever followed it, repeat. Same rule as
// parseAroundRef and for the same reason — nothing here passes through to an
// agent, so a flag after the subject is unambiguous. Do not give this
// treatment to run.
func parsePositionals(fs *flag.FlagSet, args []string, use string, max int) (stop bool, pos []string, err error) {
	if stop, err := parse(fs, args); stop {
		return true, nil, err
	}
	for range max {
		rest := fs.Args()
		if len(rest) == 0 {
			return false, pos, nil
		}
		pos = append(pos, rest[0])
		if stop, err := parse(fs, rest[1:]); stop {
			return true, nil, err
		}
	}
	if extra := fs.Args(); len(extra) > 0 {
		return true, nil, fmt.Errorf("unexpected argument %q\nusage: ap %s", extra[0], use)
	}
	return false, pos, nil
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

// printResolution is --dry-run's entire output: which root it would have
// written to, every input the effective profile actually needs (by binding
// NAME only — Resolved's String method redacts, so even printing the value
// itself here could not leak one), what each locator resolved to, and every
// degradation warning. Nothing below reads a resolved value.
func printResolution(w io.Writer, res *schema.Resolution, resolved *schema.Resolved,
	agent, root string, fetched map[string]source.Resolved, reports []source.Report,
) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%s:%s\n", agent, root)
	fmt.Fprintf(&b, "  %-10s %s\n", "profile", res.Profile.Name)

	if len(res.Refs) == 0 {
		fmt.Fprintf(&b, "  %-10s %s\n", "inputs", "none required")
	}
	for _, r := range res.Refs {
		fmt.Fprintf(&b, "  ✓ %-10s %s %q → %s\n", "input", r.Kind, r.Name, r.Resource)
	}

	// Reports come back in resolution order, which is sorted: a report nobody
	// can diff is a report nobody reads.
	for _, rep := range reports {
		mark := "✓"
		if _, ok := fetched[rep.Resource]; !ok {
			// Resolved but not fetched — a marketplace ref, whose item lookup
			// is Phase 6. Marked as a skip rather than omitted, because §8
			// forbids a silent drop.
			mark = "–"
		}
		fmt.Fprintf(&b, "  %s %-10s %s\n", mark, rep.Resource, rep.Text)
	}
	for _, warn := range res.Warnings {
		fmt.Fprintln(&b, "  ! warning:", warn.Text)
	}
	// Printing Resolved itself is deliberate, not decoration: its String
	// method is the guard that stops a resolved secret reaching a log, and a
	// guard nothing exercises is a guard nobody notices losing.
	fmt.Fprintf(&b, "  %-10s %v\n", "resolved", resolved)

	// §37.1 makes this line mandatory. "Dry run" reads as "does nothing", and
	// this one resolves secrets, authenticates to private sources and fetches
	// content — because that is the only way to check a contract.
	fmt.Fprintln(&b, "\n  --dry-run authenticated and fetched; nothing was written.")
	_, err := io.WriteString(w, b.String())
	return err
}
