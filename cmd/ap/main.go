// Command ap launches coding agents with per-agent profiles.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
                                       create a profile and a wrapper so it is
                                       a command you can type, optionally
                                       cloning one and seeding it with your
                                       global instructions file
  ap which <agent>:<profile>           print the profile directory
  ap env <agent>:<profile> [cmd...]    print the environment override, or set
                                       it and run cmd — env(1), for tools that
                                       write into the agent's config directory
  ap run <agent>:<profile> [args...]   run the agent with that profile
  ap delete [--yes] <agent>:<profile>  delete a profile and its wrapper, asking
                                       first unless --yes says not to
  ap unlink <agent>:<profile>          remove the wrapper, keep the profile
  ap link <agent>:<profile>            write it back (create already does this;
                                       link is for profiles made before it did,
                                       or after an unlink)
  ap version                           print version, commit and build date

There is no active profile: every command names one explicitly.

"ap run" parses no flags of its own. Everything after the reference goes to the
agent verbatim, which is what lets you write "ap run claude:plan --effort xhigh"
without ap trying to interpret --effort. "ap env" with a command behaves the
same way, for the same reason.

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
  ap env claude:plan npx skills add vercel-labs/agent-skills \
      --skill web-design-guidelines -g -a claude-code
  claude:plan                          # the wrapper ap create wrote, once its
                                       # directory (~/.local/bin by default)
                                       # is on PATH
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
	case "link":
		return cmdLink(args[1:])
	case "unlink":
		return cmdUnlink(args[1:])
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

// --- what a writing command reports -----------------------------------------
//
// `ap which` and `ap env` print one bare line each, because their output is
// consumed: $(ap which ...) and env(1). Everything below is for reading instead,
// and follows two rules. A headline says what happened, and the details go in an
// aligned block underneath, so the one interesting fact is never buried in a
// wall of equal-weight lines. Anything that went wrong leaves that block
// entirely and goes to stderr, because a warning indented into a calm column
// reads as good news.

const tick = "✔"

// receipt collects both as the command works, and prints them once at the end.
// Collected rather than printed as it goes because an error halfway through must
// not leave "created claude:plan" on screen for a profile that was then rolled
// back — which is exactly what the previous printf-as-you-go version did when
// Clone failed and Discard removed the directory.
type receipt struct {
	rows  [][2]string
	warns []string
}

func (r *receipt) add(label, value string) {
	r.rows = append(r.rows, [2]string{label, value})
}

// warn records something worth saying out loud that is nonetheless not an
// error: the profile exists and works, and the command carries on.
func (r *receipt) warn(format string, a ...any) {
	r.warns = append(r.warns, fmt.Sprintf(format, a...))
}

func (r *receipt) print(headline string) {
	fmt.Printf("%s %s\n", tick, headline)
	for _, row := range r.rows {
		fmt.Printf("  %-8s %s\n", row[0], row[1])
	}
	for _, w := range r.warns {
		fmt.Fprintf(os.Stderr, "ap: warning: %s\n", w)
	}
}

// tilde shortens a path under the user's home, for display only. Never for
// `ap which` or `ap env`: in a shell substitution "~" is a literal directory
// name and the path would resolve nowhere.
func tilde(p string) string {
	h, err := os.UserHomeDir()
	if err != nil || h == "" || !strings.HasPrefix(p, h+string(filepath.Separator)) {
		return p
	}
	return "~" + p[len(h):]
}

// onPath reports whether dir is one of the PATH entries. `ap create` used to
// tell everyone that the wrapper needs its directory on PATH; that is noise for
// the people who already have it, and being told what everybody is told is how
// the people who actually need it learn to skip the line.
func onPath(dir string) bool {
	dir = filepath.Clean(dir)
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		if p != "" && filepath.Clean(p) == dir {
			return true
		}
	}
	return false
}

// notThere is the error for a reference that names no profile. Listing what the
// agent does have turns a typo into a one-line fix instead of a second command.
func notThere(a agent.Agent, name string) error {
	have, err := profile.List(a)
	if err != nil || len(have) == 0 {
		return fmt.Errorf("no profile %s:%s", a.Name, name)
	}
	return fmt.Errorf("no profile %s:%s — %s has: %s", a.Name, name, a.Name, strings.Join(have, " "))
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
		// Padded to the longest agent name, so the profile columns line up and a
		// four-agent listing can be read down instead of across.
		fmt.Printf("%-10s %s\n", name+":", strings.Join(profiles, " "))
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
	rc := &receipt{}
	rc.add("dir", tilde(dir))

	if srcDir != "" {
		if err := profile.Clone(a, srcDir, dir); err != nil {
			// Remove the half-populated directory so `ap create` can be retried.
			// Safe here specifically because Link has not run yet, so the directory
			// provably contains no symlinks; the same cleanup after Link would not
			// be safe to reason about so cheaply.
			profile.Discard(a, name)
			return err
		}
		rc.add("cloned", fmt.Sprintf("%s:%s", a.Name, *from))
	}

	if err := linkAndReport(a, dir, rc); err != nil {
		return err
	}
	if err := shim(a, dir); err != nil {
		return err
	}

	seedAndLink(a, name, dir, rc)

	if *copyMD {
		if err := copyInstructions(a, dir); err != nil {
			return err
		}
		rc.add("copied", fmt.Sprintf("%s from %s", a.Instructions.Name, a.Instructions.Source))
	}

	rc.print(fmt.Sprintf("created %q", a.Name+":"+name))

	// A clone arrives configured; a fresh profile arrives empty, and what to do
	// about that is different for every agent. The old line here said
	// "plugin install", which is claude's answer and wrong for the other three.
	if srcDir == "" {
		if hint := setupHint(a, name); hint != "" {
			fmt.Printf("\nnext: %s\n", hint)
		}
	}
	return nil
}

// seedAndLink is the last two things `ap create` does: get the profile past the
// agent's first-run wizard, and make it a command you can type.
//
// Neither returns an error, on purpose. The profile exists and works through
// `ap run` by this point, so a failure here is worth saying out loud and worth
// nothing more — an unusable wrapper name or a first-run file that could not be
// read must not turn a created profile into a failed command.
func seedAndLink(a agent.Agent, name, dir string, rc *receipt) {
	if keys, err := seedFirstRun(a, dir); err != nil {
		rc.warn("first-run flags not seeded: %v", err)
	} else if len(keys) > 0 {
		rc.add("seeded", fmt.Sprintf("%q", keys))
	}
	p, err := writeWrapper(a, name)
	if err != nil {
		rc.warn("not linked: %v", err)
		return
	}
	rc.add("command", a.Name+":"+name)
	// Only the people it applies to. Told to everyone, this line was noise; told
	// to nobody, a wrapper that silently does not run is the confusing case
	// linking was supposed to remove.
	if d := filepath.Dir(p); !onPath(d) {
		rc.warn("%s is not on your PATH, so typing `%s:%s` will not work\n"+
			"    add it to PATH, or use: ap run %s:%s",
			tilde(d), a.Name, name, a.Name, name)
	}
}

// linkAndReport runs profile.Link and prints what it did. Split out of cmdCreate
// purely to keep cmdCreate under the project's cyclomatic-complexity gate.
func linkAndReport(a agent.Agent, dir string, rc *receipt) error {
	linked, skipped, unshared, err := profile.Link(a, dir)
	if err != nil {
		return err
	}
	if len(linked) > 0 {
		rc.add("shares", fmt.Sprintf("%q", linked))
	}
	if len(skipped) > 0 {
		// A warning, not a row. Silence here used to mean the user believed their
		// login was shared when it was not, and only found out much later when a
		// run dead-ended on "refusing to replace real file"; a row in the calm
		// column would be barely louder than silence.
		rc.warn("NOT shared: %q\n"+
			"    run %s once outside a profile first, then re-run `ap create`",
			skipped, a.Bin)
	}
	if len(unshared) > 0 {
		// State the profile used to inherit and now owns.
		rc.add("unshared", fmt.Sprintf("%q — now this profile's own", unshared))
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

// seedFirstRun copies the agent's first-run flags into a new profile, and
// returns the keys it wrote.
//
// Sharing the credential makes a profile logged in, but not started: claude
// still opens on its theme picker, because that is gated on a key in a file the
// profile does not have. Copying the flag, and nothing else in that file, is
// what makes a new profile land where a logged-in agent lands.
//
// Nothing here is an error the caller should stop on — see the call site. A
// missing source file is the ordinary case on a machine where the agent has
// never run outside a profile, and there the wizard is the correct behaviour.
func seedFirstRun(a agent.Agent, dir string) ([]string, error) {
	if a.FirstRun == nil {
		return nil, nil
	}
	b, err := os.ReadFile(a.FirstRun.Source)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(b, &all); err != nil {
		return nil, fmt.Errorf("%s: %w", a.FirstRun.Source, err)
	}
	seed := make(map[string]json.RawMessage, len(a.FirstRun.Keys))
	var wrote []string
	for _, k := range a.FirstRun.Keys {
		if v, ok := all[k]; ok {
			seed[k] = v
			wrote = append(wrote, k)
		}
	}
	if len(wrote) == 0 {
		return nil, nil
	}
	out, err := json.MarshalIndent(seed, "", "  ")
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	// O_EXCL: a clone may already have brought this file, and what the source
	// profile recorded beats a fresh seed. Never a rewrite of a file that exists.
	f, err := root.OpenFile(a.FirstRun.Name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if os.IsExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(append(out, '\n')); err != nil {
		_ = f.Close()
		return nil, err
	}
	return wrote, f.Close()
}

// setupHint is the line `ap create` ends on for a profile that starts empty. A
// profile inherits nothing but the credential, so the next step is real work and
// it is different for each agent — the text lives in the registry, beside the agent
// it describes.
func setupHint(a agent.Agent, name string) string {
	if a.Setup == "" {
		return ""
	}
	// The lead-in belongs to the caller ("next: "), not here, so the two cannot
	// drift into "next: configure it: ...".
	return fmt.Sprintf(a.Setup, a.Name+":"+name)
}

func cmdWhich(args []string) error {
	a, name, err := refAllowDefault(args, "which")
	if err != nil {
		return err
	}
	fmt.Println(profile.Dir(a, name))
	return nil
}

// cmdEnv is env(1): with no command it prints the variable, with one it sets it
// and execs.
//
// The second form is how anything that is not the agent gets to write into a
// profile — `ap env claude:plan npx skills add ...`, because that installer
// finds its target by reading CLAUDE_CONFIG_DIR, the same variable ap sets. A
// separate `ap exec` would have been the same code under a second name; env(1)
// already established that a variable-setter takes an optional command, and one
// fewer command is one fewer thing to document.
//
// Flags are not parsed after the reference, for the same reason as `ap run`:
// everything there belongs to the command being run.
func cmdEnv(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: ap env <agent>:<profile> [command [args...]]")
	}
	a, name, err := profile.ParseRefAllowDefault(args[0])
	if err != nil {
		return err
	}
	if len(args) > 1 {
		// Same preparation as a run, because the command is about to write into
		// the profile: a stale shim would send it at a config root the agent has
		// since stopped using.
		dir, err := prepare(a, name)
		if err != nil {
			return err
		}
		return run.ExecBin(a, dir, args[1], args[2:])
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
	a, name, err := profile.ParseRefAllowDefault(args[0])
	if err != nil {
		return err
	}
	dir, err := prepare(a, name)
	if err != nil {
		return err
	}
	return run.Exec(a, dir, args[1:])
}

// prepare is everything that happens between naming a profile and exec'ing into
// it, shared by `ap run` and by `ap env <ref> <command>`. It returns the
// directory the config variable should point at — empty for Default, which sets
// no override at all.
func prepare(a agent.Agent, name string) (string, error) {
	if !profile.Exists(a, name) {
		if name == profile.Default {
			// "ap create claude:default" is unconditionally refused - that advice
			// would be a dead end. Name the actual path instead: on this machine
			// the agent has never been run outside ap, or its config lives
			// somewhere this registry row does not expect.
			return "", fmt.Errorf("%s's real config directory does not exist: %s", a.Name, profile.Dir(a, name))
		}
		return "", fmt.Errorf("profile %s:%s does not exist; create it with: ap create %s:%s",
			a.Name, name, a.Name, name)
	}

	// Default is the agent's real config, reached exactly as it already is:
	// nothing is created, nothing is linked, no shim is built, and Exec gets no
	// override at all (an empty dir), not even one that happens to equal it.
	if name == profile.Default {
		return "", nil
	}

	dir := profile.Dir(a, name)

	// Re-assert the shared links on every run: agents rewrite their credential
	// files, and a temp-file-plus-rename would leave a real file where our
	// symlink was, silently unsharing auth. See internal/profile.Link.
	if _, _, _, err := profile.Link(a, dir); err != nil {
		return "", err
	}
	// Re-assert the config shim too: ~/.config gains entries over time, and a
	// profile created last month must not hide a tool installed yesterday.
	if err := shim(a, dir); err != nil {
		return "", err
	}
	return dir, nil
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
	fs := flagSet("delete")
	yes := fs.Bool("yes", false, "delete without asking")
	fs.BoolVar(yes, "y", false, "shorthand for --yes")
	// parseAroundRef, like create: delete passes nothing to the agent, so a flag
	// after the reference is unambiguous. `run` must never do this.
	stop, r, err := parseAroundRef(fs, args, "delete [--yes] <agent>:<profile>")
	if stop {
		return err
	}
	a, name, err := profile.ParseRef(r)
	if err != nil {
		return err
	}
	// Before the prompt, not after: a typo must not be answered "y".
	if !profile.Exists(a, name) {
		return notThere(a, name)
	}
	dir := profile.Dir(a, name)
	if !*yes {
		if err := confirm(dir); err != nil {
			return err
		}
	}
	if err := profile.Delete(a, name); err != nil {
		return err
	}
	rc := &receipt{}
	rc.add("removed", tilde(dir))
	// A deleted profile must not leave behind a wrapper that fails confusingly
	// when someone types its name. Not an error if there never was one — and the
	// row is omitted rather than claiming an unlink that did not happen.
	p, err := removeWrapperIfOurs(a.Name + ":" + name)
	if err != nil {
		return err
	}
	if p != "" {
		rc.add("command", fmt.Sprintf("%s:%s unlinked", a.Name, name))
	}
	rc.print(fmt.Sprintf("deleted %q", a.Name+":"+name))
	return nil
}

// wrapperHeader opens every wrapper ap writes, and nothing else ever writes it
// — the same principle as the module's other ownership markers: it lives inside
// the artefact itself, so it cannot drift out of sync with it. `create` and
// `link`, and the removal in `unlink` and `delete`, all use it to tell their own
// file from something else that happens to share its name. The exact bytes are
// load-bearing across versions: change them and every wrapper already on disk
// stops being recognised as ours.
const wrapperHeader = "#!/bin/sh\n# written by ap link. Safe to delete.\n"

// wrapperScript is the wrapper ap writes for ref (an "<agent>:<profile>"
// string). A script, not a symlink with argv[0] dispatch: it names `ap` by
// PATH lookup rather than the currently running binary's own path, so it
// survives ap being upgraded or moved elsewhere on PATH, and unlike a symlink
// it can be read with `cat`.
func wrapperScript(ref string) string {
	return fmt.Sprintf("%sexec ap run %s \"$@\"\n", wrapperHeader, ref)
}

// linkDir is where ap writes wrapper scripts. Always ~/.local/bin
// regardless of where the ap binary itself was installed — install.sh warns
// about that directory unconditionally for exactly this reason — except in
// tests, which need to point it elsewhere.
func linkDir() (string, error) {
	if d := os.Getenv("AP_LINK_DIR"); d != "" {
		return d, nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".local", "bin"), nil
}

func cmdLink(args []string) error {
	a, name, err := ref(args, "link")
	if err != nil {
		return err
	}
	// Belt as well as braces, the same reason Delete re-checks Default
	// independently of ParseRef: ref already routes through ParseRef, which
	// refuses Default for every writing command, so there is no path through
	// dispatch that reaches this branch today - TestLinkRefusesDefaultViaParseRef
	// pins the ParseRef rejection, which is what actually fires. Kept anyway,
	// so a future change to ref or to link's own routing does not silently
	// start writing a wrapper for "nothing" (ap run codex:default is already
	// the real config).
	if name == profile.Default {
		return fmt.Errorf("nothing to link: ap run %s:%s is already your real config", a.Name, profile.Default)
	}
	if !profile.Exists(a, name) {
		return fmt.Errorf("profile %s:%s does not exist; create it with: ap create %s:%s",
			a.Name, name, a.Name, name)
	}
	p, err := writeWrapper(a, name)
	if err != nil {
		return err
	}
	fmt.Printf("linked %s -> %s:%s\n", p, a.Name, name)
	return nil
}

// writeWrapper writes the wrapper for a:name into linkDir and returns its path.
// Shared by `ap link` and by `ap create`, which links every profile it creates —
// the two must agree about the marker and about refusing a foreign file, so
// there is one implementation and not two.
func writeWrapper(a agent.Agent, name string) (string, error) {
	dir, err := linkDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	// Through an os.Root confined to dir, same as everything else that writes
	// into a directory named partly by user input — see internal/profile.Link
	// and copyInstructions above. refStr can never itself contain a path
	// separator (ValidName already guarantees that), but the confinement is
	// what makes that a property of the code rather than of the caller.
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()

	refStr := a.Name + ":" + name
	p := filepath.Join(dir, refStr)
	// Overwriting something ap did not write would be a good way to lose a
	// real binary — refuse unless the file is either absent or already one of
	// ours.
	if b, err := root.ReadFile(refStr); err == nil {
		if !strings.HasPrefix(string(b), wrapperHeader) {
			return "", fmt.Errorf("refusing to overwrite %s: it was not written by ap", p)
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := root.WriteFile(refStr, []byte(wrapperScript(refStr)), 0o755); err != nil {
		return "", err
	}
	return p, nil
}

// confirm asks before the one irreversible thing this program does. The default
// is no: a profile holds its own session transcripts, and an absent-minded Enter
// must not erase them.
//
// EOF is not consent either, which is what makes this safe in a script: with no
// terminal there is nobody to answer, so the answer has to be stated up front
// with --yes. Reading the answer rather than testing for a tty also means
// `echo y | ap delete ...` works, and /dev/null does not.
func confirm(dir string) error {
	fmt.Fprintf(os.Stderr, "? remove %s [y/N] ", tilde(dir))
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(os.Stderr)
		return errors.New("delete needs a terminal to confirm; pass --yes")
	}
	if s := strings.ToLower(strings.TrimSpace(line)); s == "y" || s == "yes" {
		return nil
	}
	return errors.New("cancelled — nothing removed")
}

func cmdUnlink(args []string) error {
	a, name, err := ref(args, "unlink")
	if err != nil {
		return err
	}
	p, err := removeWrapperIfOurs(a.Name + ":" + name)
	if err != nil {
		return err
	}
	if p == "" {
		fmt.Printf("%s:%s was not linked\n", a.Name, name)
		return nil
	}
	fmt.Printf("unlinked %s — the profile is untouched; ap run %s:%s still works\n", tilde(p), a.Name, name)
	return nil
}

// removeWrapperIfOurs removes the wrapper at ref's path in linkDir, if any, and
// returns the path it removed — empty when there was nothing to remove, so a
// caller can report an unlink only when one happened.
//
// Absent is not an error: most profiles are never linked. Present but not
// carrying wrapperHeader is refused, the same as cmdLink refuses to overwrite
// it — either way, something ap did not write is left untouched.
func removeWrapperIfOurs(ref string) (string, error) {
	dir, err := linkDir()
	if err != nil {
		return "", err
	}
	// Same os.Root confinement as cmdLink's write. A missing link dir means
	// nothing was ever linked - there is no wrapper to remove, the same as a
	// missing wrapper file below, not an error. Without this, ap delete and ap
	// unlink failed on any machine that had never run `ap link` at all: the
	// profile was already gone by the time this ran, so the command both broke
	// and reported it as a failure.
	root, err := os.OpenRoot(dir)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()

	p := filepath.Join(dir, ref)
	b, err := root.ReadFile(ref)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(string(b), wrapperHeader) {
		return "", fmt.Errorf("refusing to remove %s: it was not written by ap", p)
	}
	if err := root.Remove(ref); err != nil {
		return "", err
	}
	return p, nil
}
