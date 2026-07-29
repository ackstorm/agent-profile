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
  ap create [--from <profile>] <agent>:<profile>
                                       create a profile, optionally cloning one
  ap which <agent>:<profile>           print the profile directory
  ap env <agent>:<profile>             print the environment override
  ap run <agent>:<profile> [args...]   run the agent with that profile
  ap delete <agent>:<profile>          delete a profile
  ap version                           print version, commit and build date

There is no active profile: every command names one explicitly.

"ap run" parses no flags of its own. Everything after the reference goes to the
agent verbatim, which is what lets you write "ap run claude:plan --effort xhigh"
without ap trying to interpret --effort.

"ap create" has nothing to pass through, so --from works on either side of the
reference, and after it reads better because the agent is already stated:
"ap create claude:review --from plan" clones claude:plan.

Examples:
  ap create claude:plan
  ap create claude:review --from plan
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
	var any bool
	for _, name := range names {
		a, _ := agent.Lookup(name)
		profiles, err := profile.List(a)
		if err != nil {
			return err
		}
		if len(profiles) == 0 {
			continue
		}
		any = true
		fmt.Printf("%s: %s\n", name, strings.Join(profiles, " "))
	}
	if !any {
		fmt.Println("no profiles yet - create one with: ap create claude:plan")
	}
	return nil
}

// ref parses the single positional argument shared by most commands.
func ref(args []string, cmd string) (agent.Agent, string, error) {
	if len(args) != 1 {
		return agent.Agent{}, "", fmt.Errorf("usage: ap %s <agent>:<profile>", cmd)
	}
	return profile.ParseRef(args[0])
}

func cmdCreate(args []string) error {
	fs := flagSet("create")
	from := fs.String("from", "", "clone an existing profile of the same agent")
	stop, r, err := parseAroundRef(fs, args, "create [--from <profile>] <agent>:<profile>")
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
		if err := profile.ValidName(*from); err != nil {
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

	linked, skipped, err := profile.Link(a, dir)
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
	if err := shim(a, dir); err != nil {
		return err
	}
	if srcDir == "" {
		fmt.Printf("populate it: ap run %s:%s plugin install <plugin>\n", a.Name, name)
	}
	return nil
}

func cmdWhich(args []string) error {
	a, name, err := ref(args, "which")
	if err != nil {
		return err
	}
	fmt.Println(profile.Dir(a, name))
	return nil
}

func cmdEnv(args []string) error {
	a, name, err := ref(args, "env")
	if err != nil {
		return err
	}
	// Empty base, so only the overrides print.
	for _, e := range run.Env(a, profile.Dir(a, name), nil) {
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
	a, name, err := profile.ParseRef(rest[0])
	if err != nil {
		return err
	}
	if !profile.Exists(a, name) {
		return fmt.Errorf("profile %s:%s does not exist; create it with: ap create %s:%s",
			a.Name, name, a.Name, name)
	}
	dir := profile.Dir(a, name)

	// Re-assert the shared links on every run: agents rewrite their credential
	// files, and a temp-file-plus-rename would leave a real file where our
	// symlink was, silently unsharing auth. See internal/profile.Link.
	if _, _, err := profile.Link(a, dir); err != nil {
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
