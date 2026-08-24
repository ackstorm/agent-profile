//go:build unix

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/ackstorm/agent-profile/internal/agent"
	"github.com/ackstorm/agent-profile/internal/manifest"
	"github.com/ackstorm/agent-profile/internal/profile"
	"github.com/ackstorm/agent-profile/internal/run"
)

// installTimeout bounds every bootstrap and install command.
//
// Unbounded waits are banned in this repository. Ten minutes is long enough for
// an `npm install -g` on a slow link and short enough that a hung command fails
// the manifest rather than the afternoon. There is no flag for it in v1: add one
// when ten minutes is measurably wrong, not before.
const installTimeout = 10 * time.Minute

// runCommand executes one of a manifest's shell commands.
//
// Through `sh -c`, so `npx …@latest --flag` works as typed. That is the
// deliberate asymmetry with a variant's `args`, which is tokenized and never
// evaluated: an install command is a shell one-liner by nature, an agent's argv
// must arrive verbatim. It is also the escape hatch for anything this feature
// does not provide — `PATH=$HOME/.local/bin:$PATH ach-cli …` is a command, not a
// feature request.
//
// In a fresh empty temporary directory, removed afterwards: not the manifest's
// directory and not the user's cwd. scripts/smoke.sh learned that one the hard
// way, where a command quietly resolving against the wrong cwd was
// indistinguishable from a command that ran correctly.
//
// With stdout and stderr streamed straight through. A five-minute silent npx is
// indistinguishable from a hang, and silencing output is exactly what made a
// failure to RUN indistinguishable from a failure to PASS in smoke.sh.
func runCommand(cmdline string, env []string) error {
	dir, err := os.MkdirTemp("", "ap-sync-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	ctx, cancel := context.WithTimeout(context.Background(), installTimeout)
	defer cancel()

	// #nosec G204 -- running the manifest's own commands IS the feature. It is
	// arbitrary code execution by design and cannot be engineered away; what
	// can be done is make it impossible by surprise, which is what the gate in
	// gateCommands does before anything reaches here.
	c := exec.CommandContext(ctx, "sh", "-c", cmdline)
	c.Dir = dir
	c.Env = env
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	// No stdin, deliberately. ap has already asked everything it is going to
	// ask; a command that read from the terminal here would be consuming input
	// nobody offered it, and in a script it would block until the timeout
	// killed it ten minutes later.
	c.Stdin = nil

	err = c.Run()
	if ctx.Err() == context.DeadlineExceeded {
		// Named, because "signal: killed" is a miserable thing to read at the
		// end of a ten-minute wait. Note the limit: CommandContext kills the
		// shell, not its process group, so a child that outlived its parent is
		// still out there — bounding ap's own wait is what this promises.
		return fmt.Errorf("timed out after %s", installTimeout)
	}
	return err
}

// syncPlan is everything a sync would do, resolved and checked before any of it
// happens. It exists so --dry-run and the run itself cannot disagree: they are
// the same value, printed or executed.
type syncPlan struct {
	files []filePlan
}

// filePlan is one manifest. bootstrap is hoisted here rather than onto each
// target because it runs ONCE, before any of them — §8.2 — and a list attached
// per platform is the exact shape that leaked into the real home.
type filePlan struct {
	path      string
	bootstrap []string
	targets   []targetPlan
}

// targetPlan is one <platform>:<name> identity.
type targetPlan struct {
	a    agent.Agent
	name string // the profile name, or profile.Default
	ref  string // "<agent>:<name>", as the report and every error name it
	// path is the directory install writes into: the profile, or — for a
	// default target — the agent's real config directory. Printed by
	// --dry-run precisely so what is at stake is visible before anything runs.
	path string
	// isDefault is carried explicitly and never inferred by comparing path
	// against the real config directory. run.Env takes the same fact the same
	// way, for the same reason: inferring it would be one moved directory away
	// from silently doing the opposite.
	isDefault bool
	// exists is read before anything runs, so "reused" in the report is a
	// statement about what was found rather than about what happened.
	exists   bool
	install  []string
	variants []variantPlan
}

// variantPlan carries the verb the report will print. Sync writes variants with
// replace = true, so re-syncing converges — and OVERWRITES a hand-made variant
// of the same name. That is the only place v1 destroys anything a user typed,
// which is why the verb is resolved here and shown in both --dry-run and the
// report rather than left as a silent "written".
type variantPlan struct {
	name string
	args []string
	verb string
}

func buildPlan(ms []manifest.Manifest) syncPlan {
	var p syncPlan
	for _, m := range ms {
		f := filePlan{path: m.Path, bootstrap: m.Bootstrap}
		for _, pl := range m.Platforms {
			t := targetPlan{
				a:         pl.Agent,
				name:      m.Name,
				ref:       pl.Agent.Name + ":" + m.Name,
				path:      profile.Dir(pl.Agent, m.Name),
				isDefault: m.IsDefault(),
				install:   pl.Install,
			}
			t.exists = t.isDefault || profile.Exists(pl.Agent, m.Name)
			for _, v := range pl.Variants {
				verb := "created"
				if _, err := profile.VariantArgs(pl.Agent, m.Name, v.Name); err == nil {
					verb = "updated"
				}
				t.variants = append(t.variants, variantPlan{name: v.Name, args: v.Args, verb: verb})
			}
			f.targets = append(f.targets, t)
		}
		p.files = append(p.files, f)
	}
	return p
}

// hasCommands reports whether this plan would run any third-party code at all.
// A manifest with no bootstrap and no install never prompts: creating profiles
// and writing variants runs nothing.
func (p syncPlan) hasCommands() bool {
	for _, f := range p.files {
		if len(f.bootstrap) > 0 {
			return true
		}
		for _, t := range f.targets {
			if len(t.install) > 0 {
				return true
			}
		}
	}
	return false
}

// defaultTargets is every target that would run commands against the agent's
// real config directory. Only the ones with commands: a default platform with
// no install does nothing at all, and asking about nothing teaches people to
// dismiss the prompt that matters.
func (p syncPlan) defaultTargets() []targetPlan {
	var out []targetPlan
	for _, f := range p.files {
		for _, t := range f.targets {
			if t.isDefault && len(t.install) > 0 {
				out = append(out, t)
			}
		}
	}
	return out
}

// print renders the plan: what would run, where, and what would be written.
//
// Straight to stdout rather than to an io.Writer, like receipt.print: the only
// caller is --dry-run, and a writer parameter with one argument is indirection
// nobody asked for.
func (p syncPlan) print() {
	for _, f := range p.files {
		fmt.Printf("\n%s\n", f.path)
		for _, c := range f.bootstrap {
			fmt.Printf("  bootstrap  %s\n", c)
		}
		for _, t := range f.targets {
			where := tilde(t.path)
			switch {
			case t.isDefault:
				// The resolved absolute path, unabbreviated. This is the one
				// place a manifest can reach the configuration the developer
				// uses every day, and a "~" in front of it is exactly the kind
				// of familiarity that stops someone reading the line.
				fmt.Printf("  %-24s %s   (the agent's real config)\n", t.ref, t.path)
			case t.exists:
				fmt.Printf("  %-24s %s   (reuse)\n", t.ref, where)
			default:
				fmt.Printf("  %-24s %s   (create)\n", t.ref, where)
			}
			for _, c := range t.install {
				fmt.Printf("    install  %s\n", c)
			}
			for _, v := range t.variants {
				fmt.Printf("    variant  %s (%s)  %s\n", v.name, v.verb, strings.Join(v.args, " "))
			}
		}
	}
}

// gate is everything ap asks before it runs somebody else's shell commands.
//
// `git clone` a repository and `ap sync` runs the commands inside it as you.
// That is arbitrary code execution by design and cannot be engineered away —
// it IS the feature. What can be done is make it impossible by surprise, and
// that is all this function does.
//
// ONE gate: --yes, covering every command in the plan, including the ones a
// `name: default` manifest runs against the agent's real configuration. There
// was a second flag for those — --allow-default — on the grounds that a mistake
// there is the developer's working environment and ap has no undo, where a
// mistake anywhere else is a stray package or a directory `ap delete` removes.
// It was removed on request: two questions for one run was one question too
// many. What survives of it is the display — a default target is shown with its
// resolved absolute path and its own heading, so the one prompt that remains
// says plainly which of the two it is about to touch.
func gate(p syncPlan, yes bool) error {
	if !p.hasCommands() || yes {
		return nil
	}
	if !stdinIsTerminal() {
		// A pipe is not consent. Same rule as askToPromote, and checked the
		// same way — os.Stdin.Stat for ModeCharDevice, stdlib only, no x/term.
		return fmt.Errorf("ap sync would run commands from these manifests, and there is no terminal to confirm on; pass --yes")
	}
	printCommands(p)
	if !askYes("run these commands?") {
		return errors.New("cancelled — nothing was run and nothing was created")
	}
	return nil
}

// printCommands shows each list labelled by what it can reach: the machine, one
// profile, or — last and loudest — the agent's real configuration. Two blast
// radii on one screen that looked alike would make the prompt worse than no
// prompt, and since the second gate was removed this display is the only thing
// telling them apart.
func printCommands(p syncPlan) {
	fmt.Fprintln(os.Stderr, "\nap sync will run these commands as you:")
	for _, f := range p.files {
		if len(f.bootstrap) > 0 {
			fmt.Fprintf(os.Stderr, "\n  %s — on THIS MACHINE, with no profile in scope:\n", f.path)
			for _, c := range f.bootstrap {
				fmt.Fprintf(os.Stderr, "      %s\n", c)
			}
		}
		for _, t := range f.targets {
			if len(t.install) == 0 {
				continue
			}
			where := fmt.Sprintf("in %s", t.path)
			if t.isDefault {
				// The resolved absolute path, unabbreviated, and named for what
				// it is. This is the directory the developer's own agent reads,
				// and nothing here can be undone by `ap delete`.
				where = fmt.Sprintf("in %s — THE AGENT'S REAL CONFIG, which ap cannot undo", t.path)
			}
			fmt.Fprintf(os.Stderr, "\n  %s — %s:\n", t.ref, where)
			for _, c := range t.install {
				fmt.Fprintf(os.Stderr, "      %s\n", c)
			}
		}
	}
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

// cmdSync materialises profiles from manifests kept in Git.
//
// It parses its own flags, like `sessions` and `resume` and unlike `run`: there
// is no passthrough here, nothing after the path belongs to an agent, and
// parseAroundRef is what lets --dry-run sit on either side of the path.
//
// It is also the one command in this program that operates on MANY profiles
// from a file, where every other command names exactly one explicitly. That is a
// real departure from "there is no active profile", and --dry-run exists
// because of it.
func cmdSync(args []string) error {
	fs := flagSet("sync")
	dry := fs.Bool("dry-run", false, "print the whole plan and change nothing")
	yes := fs.Bool("yes", false, "run the manifests' commands without asking")
	fs.BoolVar(yes, "y", false, "shorthand for --yes")
	stop, path, err := parseAroundRef(fs, args,
		"sync [--dry-run] [--yes] <file-or-directory>")
	if stop {
		return err
	}

	// Everything is parsed, validated and checked for duplicates before
	// anything is created. §9: any error here aborts the whole run with
	// nothing done.
	ms, err := manifest.Load(path)
	if err != nil {
		return err
	}
	plan := buildPlan(ms)

	if *dry {
		fmt.Print("plan (nothing has run)\n")
		plan.print()
		return nil
	}
	if err := gate(plan, *yes); err != nil {
		return err
	}
	return runPlan(plan)
}

// runPlan is §10's step 8, and its orderings are the design rather than an
// implementation detail.
//
// Bootstrap runs before any profile exists, which is what makes it IMPOSSIBLE
// for a bootstrap command to write into one rather than merely discouraged. A
// failed bootstrap fails every platform in its manifest and no other manifest:
// a platform install that needed the tool would fail anyway, and would fail
// with a worse message.
//
// Independent work continues, so the user gets one complete picture instead of
// discovering failures one sync at a time.
func runPlan(p syncPlan) error {
	rep := &syncReport{}
	for _, f := range p.files {
		rep.file(f.path)
		bootFailed := false
		for _, c := range f.bootstrap {
			// StripProfilePaths and NOT InstallEnv: there is no profile in
			// scope, and no agent variable is set at all.
			if err := runCommand(c, run.StripProfilePaths(profile.Root(), os.Environ())); err != nil {
				rep.bad("bootstrap", c, err)
				bootFailed = true
				break
			}
			rep.ok("bootstrap", c)
		}
		for _, t := range f.targets {
			rep.target(t.ref)
			rep.total++
			if bootFailed {
				rep.skipped("profile", "bootstrap failed")
				rep.failed++
				continue
			}
			if err := runTarget(t, rep); err != nil {
				rep.failed++
			}
		}
	}
	rep.print()
	if rep.failed > 0 {
		return fmt.Errorf("%d of %d profiles failed", rep.failed, rep.total)
	}
	return nil
}

// runTarget is step 8b for one identity: ensure the profile, run its install
// commands, then write its variants.
//
// Variants come last so a profile whose install failed gets a legible report
// rather than half a set of variants and no explanation.
func runTarget(t targetPlan, rep *syncReport) error {
	dir := t.path
	if t.isDefault {
		// §6.1: nothing is created. No Create, no Link, no Shim, no
		// seedFirstRun, no wrapper. Link especially must never run here — the
		// shared credential is not linked INTO the real config directory, it
		// IS the file in it, and pointing a symlink at itself destroys it.
		//
		// dir is emptied so run.Env sets no override at all, which is exactly
		// what `ap run claude:default` does.
		rep.ok("profile", "the agent's real config (nothing created)")
		dir = ""
	} else {
		if !t.exists {
			if _, err := profile.Create(t.a, t.name); err != nil {
				rep.bad("profile", "create", err)
				return err
			}
			rep.ok("profile", "created")
		} else {
			rep.ok("profile", "reused")
		}
		// The same four steps `ap create` runs, so a synced profile is
		// indistinguishable from a hand-made one, and idempotent on a reused
		// one. The receipt's rows are not printed — this command has its own
		// report — but its warnings are, because "NOT shared" is exactly the
		// kind of thing silence turns into a mystery weeks later.
		rc := &receipt{}
		err := finishCreate(t.a, t.name, t.path, rc)
		for _, w := range rc.warns {
			fmt.Fprintf(os.Stderr, "ap: %s: %s\n", t.ref, w)
		}
		if err != nil {
			rep.bad("profile", "prepare", err)
			return err
		}
	}

	env := run.InstallEnv(t.a, dir, profile.Root(), os.Environ())
	for _, c := range t.install {
		if err := runCommand(c, env); err != nil {
			rep.bad("install", c, err)
			for _, v := range t.variants {
				rep.skipped("variant", v.name+" (install failed)")
			}
			return err
		}
		rep.ok("install", c)
	}

	var firstErr error
	for _, v := range t.variants {
		// replace = true, so re-syncing converges. This OVERWRITES a hand-made
		// variant of the same name, which is the only place v1 destroys
		// anything a user typed — hence "updated" in the report and in
		// --dry-run rather than a silent "written".
		if err := profile.WriteVariant(t.a, t.name, v.name, v.args, true); err != nil {
			rep.bad("variant", v.name, err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		rep.ok("variant", fmt.Sprintf("%s (%s)", v.name, v.verb))
	}
	return firstErr
}

// syncReport collects what happened and prints it once at the end.
//
// Collected rather than printed as it goes, for the same reason `receipt` is:
// the command output above it is already streaming past, and a summary
// interleaved with ten minutes of npm output is not a summary. What it cannot
// do is what receipt does — roll back — because by the time a report line is
// written the command has already run.
type syncReport struct {
	b             strings.Builder
	total, failed int
}

func (r *syncReport) file(path string)   { fmt.Fprintf(&r.b, "\n%s\n", path) }
func (r *syncReport) target(ref string)  { fmt.Fprintf(&r.b, "\n%s\n", ref) }
func (r *syncReport) ok(label, v string) { fmt.Fprintf(&r.b, "  %s %-10s %s\n", tick, label, v) }

func (r *syncReport) skipped(label, why string) {
	fmt.Fprintf(&r.b, "  – %-10s %s\n", label, why)
}

func (r *syncReport) bad(label, what string, err error) {
	fmt.Fprintf(&r.b, "  ✗ %-10s %s\n                 %v\n", label, what, err)
}

func (r *syncReport) print() {
	fmt.Print(r.b.String())
	fmt.Println()
	if r.failed == 0 {
		fmt.Printf("%d profile(s) synchronised\n", r.total)
		return
	}
	// To stdout with the rest of the report, not stderr: the failure count is
	// the last line of one document, and splitting a report across two streams
	// is how it arrives interleaved wrongly in a log.
	fmt.Printf("%d of %d profiles failed\n", r.failed, r.total)
}
