//go:build unix

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ackstorm/agent-profile/internal/profile"
	"github.com/ackstorm/agent-profile/pkg/agentreg"
	"github.com/ackstorm/agent-profile/pkg/hydrate"
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
	const use = "manifest apply <agent>:<profile>|<agent> --root <dir> [--dry-run] [--strict] [--yes] [--manifest -] <manifest.yaml>"
	fs := flagSet("manifest")
	dryRun := fs.Bool("dry-run", false, "run the resolution phase and print it; touch nothing")
	strict := fs.Bool("strict", false, "promote every degradation warning (§7.2, §8) to an error")
	yes := fs.Bool("yes", false, "materialize into the agent's real configuration without asking")
	fs.BoolVar(yes, "y", false, "shorthand for --yes")
	manifestFlag := fs.String("manifest", "", `manifest path, or "-" to read it from stdin`)
	rootFlag := fs.String("root", "", "materialize into this directory instead of a profile's (§33.2)")

	stop, pos, err := parsePositionals(fs, args, use, 2)
	if stop {
		return err
	}
	if len(pos) == 0 {
		return fmt.Errorf("usage: ap %s", use)
	}

	// Default is allowed here and gated at materialization, not now: the
	// resolution phase writes nothing to gate.
	tgt, err := resolveTarget(pos[0], *rootFlag, "apply")
	if err != nil {
		return err
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

	// Ask BEFORE doing any work. The question is whether ap may touch this
	// root at all, and it does not depend on the manifest resolving — asking
	// after a long fetch would spend the user's time on a run they were about
	// to decline.
	if !*dryRun {
		if err := tgt.gate(*yes); err != nil {
			return err
		}
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

	res, resolved, fetched, reports, err := resolvePhase(path, tgt.Agent.Name, *strict, tgt.Name != "")
	if err != nil {
		return err
	}
	if *dryRun {
		return printResolution(os.Stdout, res, resolved, tgt.Label(), fetched, reports)
	}

	adapter, err := hydrate.AdapterFor(tgt.Agent)
	if err != nil {
		return err
	}
	applied, err := hydrate.Apply(context.Background(), hydrate.Plan{
		Root: tgt.Root, Adapter: adapter, Profile: res.Profile, Fetched: fetched,
	})
	if err != nil {
		return err
	}
	return printApplied(os.Stdout, res, tgt, applied, reports)
}

// resolvePhase is §37.1 steps 1-14: compose, resolve inputs, preflight, fetch
// every active source, and validate contracts. It mutates no root. Split out of
// manifestApply so the command stays argument handling and the phase stays one
// readable sequence — the boundary gocyclo was pointing at.
func resolvePhase(path, runtime string, strict, runtimeIsLocal bool) (
	*schema.Resolution, *schema.Resolved, map[string]source.Resolved, []source.Report, error,
) {
	res, resolved, err := schema.Resolve(path, runtime, strict, runtimeIsLocal)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return fetchPhase(res, resolved, filepath.Dir(path))
}

// fetchPhase is the half of §37.1 that does not care where the profile came
// from: acquire every active source, then validate contracts. `ap install`
// builds its profile in memory and joins here.
func fetchPhase(res *schema.Resolution, resolved *schema.Resolved, manifestDir string) (
	*schema.Resolution, *schema.Resolved, map[string]source.Resolved, []source.Report, error,
) {
	// The cache root is a parameter, like every root in pkg/. It is the one
	// thing an apply may create outside the target root: a cache is not a
	// materialization root, and §37.1 says source acquisition during
	// resolution is not mutation.
	cacheRoot, err := profile.CacheDir()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	cache, err := source.NewCache(cacheRoot)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	fetched, reports, err := source.Resolve(context.Background(), res.Profile, source.Opts{
		Cache:       cache,
		ManifestDir: manifestDir,
		Secret: func(name string) (string, error) {
			v, ok := resolved.Value("secret", name)
			if !ok {
				return "", source.ErrSecretUnset
			}
			return v, nil
		},
	})
	if err != nil {
		return nil, nil, nil, nil, err
	}

	// §37.1 step 14: contracts are validated in the RESOLUTION phase, before
	// anything is written. Apply overwrites, so a violation found halfway
	// through leaves a root partly written — and running the check here is
	// what makes --dry-run report it.
	if err := hydrate.CheckContracts(res.Profile, fetched); err != nil {
		return nil, nil, nil, nil, err
	}
	return res, resolved, fetched, reports, nil
}

// gateRealConfig is the one question, and only for the one root that has no
// undo.
//
// `<agent>:default` is the configuration the developer's own agent reads. ap
// cannot undo a write there by deleting a profile, so the resolved ABSOLUTE
// path is displayed and named for what it is — that display is the only thing
// distinguishing the two blast radii on one screen. A named profile needs no
// gate: `ap delete` removes it whole.
//
// Off a terminal it refuses. A pipe is not consent, checked with
// stdinIsTerminal and never with the answer to a question nobody was asked.
func gateRealConfig(agent agentreg.Agent, name, root string, yes bool) error {
	if name != agentreg.Default {
		return nil
	}
	if yes {
		return nil
	}
	if !stdinIsTerminal() {
		return fmt.Errorf("%s:%s is the real configuration %s already uses (%s), and there is no terminal to confirm on; pass --yes",
			agent.Name, agentreg.Default, agent.Name, root)
	}
	fmt.Fprintf(os.Stderr, "\n%s:%s is the real configuration %s already uses:\n  %s\n",
		agent.Name, agentreg.Default, agent.Name, root)
	fmt.Fprintln(os.Stderr, "This is not a profile. ap cannot undo it.")
	if !askYes("materialize into it?") {
		return errors.New("cancelled — nothing was written")
	}
	return nil
}

// printApplied is the house report style: two-space indent, a mark, a padded
// label. Every overwrite appears, because §33 requires it and because the
// user's evidence that apply did not quietly eat something is this list.
func printApplied(w io.Writer, res *schema.Resolution, tgt target,
	applied hydrate.Result, reports []source.Report,
) error {
	var b strings.Builder
	fmt.Fprintln(&b, tgt.Label())
	fmt.Fprintf(&b, "  %-10s %s\n", "root", tgt.Root)
	for _, rep := range reports {
		fmt.Fprintf(&b, "  ✓ %-10s %s\n", rep.Resource, rep.Text)
	}
	for _, c := range applied.Changes {
		mark := "+"
		if c.Op == "overwrite" {
			mark = "~"
		}
		fmt.Fprintf(&b, "  %s %s\n", mark, c.Path)
	}
	for _, warn := range res.Warnings {
		fmt.Fprintln(&b, "  ! warning:", warn.Text)
	}
	for _, warn := range applied.Warnings {
		fmt.Fprintln(&b, "  ! warning:", warn)
	}
	fmt.Fprintf(&b, "\n  %d file(s) written; the ledger records what landed.\n", len(applied.Changes))
	_, err := io.WriteString(w, b.String())
	return err
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
	label string, fetched map[string]source.Resolved, reports []source.Report,
) error {
	var b strings.Builder
	fmt.Fprintln(&b, label)
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

// askYes asks one question. Only ever reached after stdinIsTerminal, so a pipe
// never gets here — see gate, and see the sandbox check that feeds a literal
// "1" down one.
//
// readLine rather than bufio, for the reason readLine documents: buffering
// reads past the newline, and everything it swallowed would be missing from the
// stdin an install command or an agent inherits moments later.
func askYes(q string) bool {
	fmt.Fprintf(os.Stderr, "\n  %s [y/N] ", q)
	s := strings.ToLower(strings.TrimSpace(readLine(os.Stdin)))
	return s == "y" || s == "yes"
}
