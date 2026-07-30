// Command ap launches coding agents with per-agent profiles.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/ackstorm/agent-profile/internal/agent"
	"github.com/ackstorm/agent-profile/internal/profile"
	"github.com/ackstorm/agent-profile/internal/run"
)

const usage = `ap - per-agent profile launcher

Usage:
  ap agents                            list supported agents
  ap list [agent]                      list profiles
  ap create [--from <profile>] [--copy-instructions] <agent>:<profile>
                                       create a profile, optionally cloning one
                                       and seeding it with your global
                                       instructions file
  ap which <agent>:<profile>           print the profile directory
  ap env <agent>:<profile>             print the environment override
  ap run <agent>:<profile> [args...]   run the agent with that profile
  ap delete <agent>:<profile>          delete a profile
  ap version                           print version, commit and build date

There is no active profile: every command names one explicitly.

"ap run" parses no flags of its own. Everything after the reference goes to the
agent verbatim, which is what lets you write "ap run claude:plan --effort xhigh"
without ap trying to interpret --effort.

"ap create" has nothing to pass through, so --from and --copy-instructions work
on either side of the reference, and after it reads better because the agent is
already stated: "ap create claude:review --from plan" clones claude:plan.

Examples:
  ap create claude:plan
  ap create claude:review --from plan
  ap create claude:work --copy-instructions
  ap run claude:plan plugin install caveman@caveman
  ap run claude:plan
  ap run claude:plan --effort xhigh
  ap run opencode:review --model anthropic/claude-sonnet-4-5
`

func main() {
	if err := dispatch(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "ap:", err)
		os.Exit(1)
	}
}

func dispatch(args []string) error {
	if len(args) == 0 {
		fmt.Print(usage)
		return nil
	}
	switch args[0] {
	case "agents":
		return cmdAgents(args[1:])
	case "list", "ls":
		return cmdList(args[1:])
	case "create":
		return cmdCreate(args[1:])
	case "which":
		return cmdWhich(args[1:])
	case "env":
		return cmdEnv(args[1:])
	case "run":
		return cmdRun(args[1:])
	case "delete", "rm":
		return cmdDelete(args[1:])
	case "version", "--version", "-v":
		return cmdVersion(args[1:])
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	default:
		return fmt.Errorf("unknown command %q (try `ap help`)", args[0])
	}
}

// flagSet builds a FlagSet that stays quiet: ContinueOnError otherwise writes the
// error and usage to stderr itself, and main would then print the same error a
// second time. -h is not a failure, so it is reported separately.
func flagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// parse reports whether the caller should stop. -h prints usage and exits clean.
func parse(fs *flag.FlagSet, args []string) (stop bool, err error) {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Print(usage)
			return true, nil
		}
		return true, err
	}
	return false, nil
}

// parseAroundRef parses flags that appear on either side of the reference, and
// returns the reference.
//
// Only for commands with no passthrough. `run` must keep the strict "flags
// first" rule, because everything after the reference belongs to the agent and
// `ap run claude:plan --effort xhigh` has to reach claude untouched. `create`
// has nothing to pass through, so `ap create claude:exec --from review` is
// unambiguous — and it reads better, because the agent is already stated to the
// left of the bare source name.
//
// flag.Parse stops at the first non-flag argument, so this parses twice: once up
// to the reference, then again over whatever followed it.
func parseAroundRef(fs *flag.FlagSet, args []string, cmd string) (stop bool, ref string, err error) {
	if stop, err := parse(fs, args); stop {
		return true, "", err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return true, "", fmt.Errorf("usage: ap %s", cmd)
	}
	ref = rest[0]
	if stop, err := parse(fs, rest[1:]); stop {
		return true, "", err
	}
	if extra := fs.Args(); len(extra) > 0 {
		return true, "", fmt.Errorf("unexpected argument %q\nusage: ap %s", extra[0], cmd)
	}
	return false, ref, nil
}

// Filled in at link time by goreleaser (see .goreleaser.yml) and by
// `make build`. A source build reports "dev", which is the honest answer: a
// binary someone compiled themselves has no release version.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func cmdVersion(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: ap version")
	}
	fmt.Printf("ap %s (commit %s, built %s, %s/%s, %s)\n",
		version, commit, date, runtime.GOOS, runtime.GOARCH, runtime.Version())
	return nil
}

func cmdAgents(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: ap agents")
	}
	for _, name := range agent.Names() {
		a, _ := agent.Lookup(name)
		fmt.Printf("%-9s %-22s %-8s", a.Name, a.ConfigEnv, a.Mode)
		if a.Note != "" {
			fmt.Printf("  %s", a.Note)
		}
		fmt.Println()
	}
	return nil
}

func cmdList(args []string) error {
	names := agent.Names()
	switch len(args) {
	case 0:
	case 1:
		if _, ok := agent.Lookup(args[0]); !ok {
			return fmt.Errorf("unknown agent %q: supported are %s", args[0], strings.Join(agent.Names(), ", "))
		}
		names = args[:1]
	default:
		return fmt.Errorf("usage: ap list [agent]")
	}
	// List always includes Default, so there is no "no profiles yet" case left
	// to report: every agent has at least its real config to show.
	for _, name := range names {
		a, _ := agent.Lookup(name)
		profiles, err := profile.List(a)
		if err != nil {
			return err
		}
		fmt.Printf("%s: %s\n", name, strings.Join(profiles, " "))
	}
	return nil
}

// ref parses the single positional argument shared by the writing commands
// (create, delete). ParseRef rejects Default.
func ref(args []string, cmd string) (agent.Agent, string, error) {
	if len(args) != 1 {
		return agent.Agent{}, "", fmt.Errorf("usage: ap %s <agent>:<profile>", cmd)
	}
	return profile.ParseRef(args[0])
}

// refAllowDefault is ref but also accepts Default, for the read-only commands
// that may resolve to the agent's real config directory: which, env, run.
func refAllowDefault(args []string, cmd string) (agent.Agent, string, error) {
	if len(args) != 1 {
		return agent.Agent{}, "", fmt.Errorf("usage: ap %s <agent>:<profile>", cmd)
	}
	return profile.ParseRefAllowDefault(args[0])
}

func cmdCreate(args []string) error {
	fs := flagSet("create")
	from := fs.String("from", "", "clone an existing profile of the same agent")
	copyMD := fs.Bool("copy-instructions", false, "copy your global instructions file (CLAUDE.md, AGENTS.md) into the profile")
	stop, r, err := parseAroundRef(fs, args, "create [--from <profile>] [--copy-instructions] <agent>:<profile>")
	if stop {
		return err
	}
	a, name, err := profile.ParseRef(r)
	if err != nil {
		return err
	}

	// Validate the source before creating anything, so a typo does not leave an
	// empty profile behind.
	var srcDir string
	if *from != "" {
		// Validate before building a path from it. Without this, --from is a
		// traversal: profile.Dir joins and cleans, so "--from ../../../.claude"
		// resolves outside the profile root and Clone copies the real home.
		// ParseRefAllowDefault, not ValidName directly, because --from is one of
		// the four read-only paths that may name Default: `--from default`
		// clones the agent's real config.
		if _, _, err := profile.ParseRefAllowDefault(a.Name + ":" + *from); err != nil {
			return fmt.Errorf("--from: %w", err)
		}
		if *from == name {
			return fmt.Errorf("--from %s is the profile being created", *from)
		}
		if !profile.Exists(a, *from) {
			return fmt.Errorf("source profile %s:%s does not exist", a.Name, *from)
		}
		srcDir = profile.Dir(a, *from)
	}
	// Same rule as --from: an unusable --copy-instructions must fail before the
	// profile exists, not after.
	if *copyMD {
		if err := checkCopyInstructions(a); err != nil {
			return err
		}
	}

	dir, err := profile.Create(a, name)
	if err != nil {
		return err
	}
	fmt.Printf("created %s:%s at %s\n", a.Name, name, dir)

	if srcDir != "" {
		if err := profile.Clone(a, srcDir, dir); err != nil {
			// Remove the half-populated directory so `ap create` can be retried.
			// Safe here specifically because Link has not run yet, so the directory
			// provably contains no symlinks; the same cleanup after Link would not
			// be safe to reason about so cheaply.
			profile.Discard(a, name)
			return err
		}
		fmt.Printf("cloned from %s:%s\n", a.Name, *from)
	}

	if err := linkAndReport(a, dir); err != nil {
		return err
	}
	if err := shim(a, dir); err != nil {
		return err
	}

	if *copyMD {
		if err := copyInstructions(a, dir); err != nil {
			return err
		}
		fmt.Printf("copied %s from %s\n", a.Instructions.Name, a.Instructions.Source)
	}

	// A clone arrives configured; a fresh profile arrives empty, and what to do
	// about that is different for every agent. The old line here said
	// "plugin install", which is claude's answer and wrong for the other three.
	if srcDir == "" {
		if hint := setupHint(a, name); hint != "" {
			fmt.Println(hint)
		}
	}
	return nil
}

// linkAndReport runs profile.Link and prints what it did. Split out of cmdCreate
// purely to keep cmdCreate under the project's cyclomatic-complexity gate.
func linkAndReport(a agent.Agent, dir string) error {
	linked, skipped, unshared, err := profile.Link(a, dir)
	if err != nil {
		return err
	}
	if len(linked) > 0 {
		fmt.Printf("shared with your real config: %s\n", strings.Join(linked, " "))
	}
	if len(skipped) > 0 {
		// Say it out loud. Silence here used to mean the user believed their login
		// was shared when it was not, and only found out much later when a run
		// dead-ended on "refusing to replace real file".
		fmt.Printf("NOT shared yet: %s - run %s once outside a profile first, then re-run `ap create`\n",
			strings.Join(skipped, " "), a.Bin)
	}
	if len(unshared) > 0 {
		// Say it out loud: this is state the profile used to inherit and now owns.
		fmt.Printf("no longer shared (now this profile's own): %s\n", strings.Join(unshared, " "))
	}
	return nil
}

// checkCopyInstructions validates that --copy-instructions is usable for a,
// without copying anything yet — the same "fail before creating" rule as --from.
func checkCopyInstructions(a agent.Agent) error {
	if a.Instructions == nil {
		return fmt.Errorf("--copy-instructions: no global instructions file is known for %s "+
			"(only claude is verified; see internal/agent)", a.Name)
	}
	if _, err := os.Stat(a.Instructions.Source); err != nil {
		return fmt.Errorf("--copy-instructions: %w", err)
	}
	return nil
}

// copyInstructions seeds the profile with your global instructions file.
//
// A copy, not a link: the profile owns it, and the reason to want it in a profile at
// all is usually to then change it there. Through an os.Root confined to the profile,
// same as everything else that writes into one.
func copyInstructions(a agent.Agent, dir string) error {
	b, err := os.ReadFile(a.Instructions.Source)
	if err != nil {
		return fmt.Errorf("--copy-instructions: %w", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	f, err := root.OpenFile(a.Instructions.Name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// setupHint is the line `ap create` ends on for a profile that starts empty. A
// profile inherits nothing but the credential, so the next step is real work and
// it is different for each agent — the text lives in the registry, beside the agent
// it describes.
func setupHint(a agent.Agent, name string) string {
	if a.Setup == "" {
		return ""
	}
	return "configure it: " + fmt.Sprintf(a.Setup, a.Name+":"+name)
}

func cmdWhich(args []string) error {
	a, name, err := refAllowDefault(args, "which")
	if err != nil {
		return err
	}
	fmt.Println(profile.Dir(a, name))
	return nil
}

func cmdEnv(args []string) error {
	a, name, err := refAllowDefault(args, "env")
	if err != nil {
		return err
	}
	// Default sets no override: Env treats an empty dir as "none", which is
	// what makes `ap env <agent>:default` print nothing rather than the real
	// config directory it would otherwise be pointless to assign to itself.
	dir := profile.Dir(a, name)
	if name == profile.Default {
		dir = ""
	}
	for _, e := range run.Env(a, dir, nil) {
		fmt.Println(e)
	}
	return nil
}

func cmdRun(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: ap run <agent>:<profile> [args...]")
	}
	// No flag parsing at all: everything after the reference belongs to the agent,
	// verbatim. See the flag-order note in usage.
	rest := args
	a, name, err := profile.ParseRefAllowDefault(rest[0])
	if err != nil {
		return err
	}
	if !profile.Exists(a, name) {
		return fmt.Errorf("profile %s:%s does not exist; create it with: ap create %s:%s",
			a.Name, name, a.Name, name)
	}

	// Default is the agent's real config, reached exactly as it already is:
	// nothing is created, nothing is linked, no shim is built, and Exec gets no
	// override at all (an empty dir), not even one that happens to equal it.
	if name == profile.Default {
		return run.Exec(a, "", rest[1:])
	}

	dir := profile.Dir(a, name)

	// Re-assert the shared links on every run: agents rewrite their credential
	// files, and a temp-file-plus-rename would leave a real file where our
	// symlink was, silently unsharing auth. See internal/profile.Link.
	if _, _, _, err := profile.Link(a, dir); err != nil {
		return err
	}
	// Re-assert the config shim too: ~/.config gains entries over time, and a
	// profile created last month must not hide a tool installed yesterday.
	if err := shim(a, dir); err != nil {
		return err
	}
	return run.Exec(a, dir, rest[1:])
}

// shim builds or refreshes the config shim and reports anything a program wrote
// into it for real, which would otherwise be invisible from outside the profile.
func shim(a agent.Agent, dir string) error {
	_, foundReal, err := profile.Shim(a, dir)
	if err != nil {
		return err
	}
	if len(foundReal) > 0 {
		fmt.Fprintf(os.Stderr,
			"ap: warning: real config inside the profile shim: %s\n"+
				"    a program wrote there instead of following a passthrough link, so it is\n"+
				"    invisible outside this profile. Move it to %s/ to share it.\n",
			strings.Join(foundReal, " "), profile.ConfigBase())
	}
	return nil
}

func cmdDelete(args []string) error {
	a, name, err := ref(args, "delete")
	if err != nil {
		return err
	}
	if err := profile.Delete(a, name); err != nil {
		return err
	}
	fmt.Printf("deleted %s:%s\n", a.Name, name)
	return nil
}
