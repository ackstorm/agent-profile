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
	"unicode/utf8"

	"github.com/ackstorm/agent-profile/internal/agent"
	"github.com/ackstorm/agent-profile/internal/profile"
	"github.com/ackstorm/agent-profile/internal/run"
)

const usage = `ap - per-agent profile launcher

Run claude, codex, opencode or pi under a named profile, so each one has its
own settings, skills, agents and MCP servers. Your login and your session
history stay shared with the agent you already had: a profile separates
configuration, nothing else.

Usage:
  ap <command> <agent>:<profile>[:<variant>] [args...]

Commands:
  list      List profiles and variants, or just one agent's
  create    Create a profile and a wrapper you can type as a command
  variant   Name a set of launch arguments over an existing profile
            (over one that exists it asks first; --yes answers)
  which     Print the profile directory
  env       Print the environment override, or run a command under it
  run       Run the agent with that profile
  delete    Delete a profile and its wrapper, asking first
  unlink    Remove the wrapper, keep the profile
  link      Write the wrapper back
  version   Print version, commit and build date

There is no active profile: every command names one explicitly.

"ap list" prints a tree: an agent per line, its profiles under it, and each
profile's variants under that, with a variant's arguments after its name so a
name that disables every permission prompt is never invisible. Every reference
is qualified, so any line is pasteable after "ap run". "ap list --raw" is the
same listing for scripts: one tab-separated line per reference, the reference
in field 1 and one argument per field after it, no tree and no padding.

"ap create" takes --from <profile> to clone an existing profile,
--only-settings <key> (repeatable) to narrow that clone to a few keys of the
agent's settings file instead of everything --from would normally carry, and
--copy-instructions to seed it with your global instructions file. --from
copies configuration only, never sessions or credentials, and "default" names
the agent you already had, outside any profile. It has nothing to pass
through, so every flag works on either side of the reference, and after it
reads better because the agent is already stated: "ap create claude:review
--from plan" clones claude:plan.

"ap run" parses no flags of its own. Everything after the reference goes to the
agent verbatim, which lets you write "ap run claude:plan --effort xhigh"
without ap trying to interpret --effort. "ap env" with a command behaves the
same way, for the same reason.

A variant is a named set of launch arguments over a profile, so "ap variant
claude:review:opus -- --effort xhigh" then "ap run claude:review:opus" runs
those arguments first and yours after. It has no directory of its own: "ap
which" and "ap env" answer for the parent, and "ap env" never passes a
variant's arguments to the command it runs.

A variant may leave "{}" where your arguments should go, spelled the way
"xargs -I{}" spells it. They are joined with a space and substituted there,
reaching the agent as one argument rather than appended as a new one, which is
how a variant becomes a prompt prefix: "ap variant claude:plan:exec --
'/superpowers:executing-plans {}'" then "ap run claude:plan:exec plan.md". A
variant that does not mention "{}" composes exactly as before.

"ap delete" asks before it removes a profile, and --yes is how a script
answers. Deleting a variant goes without asking, since the profile it varies
is untouched — but writing OVER one asks, and shows both argument lists while
it does, because a variant's arguments are the part you wrote once and have
not read since. --yes answers that too. "ap link" writes a wrapper back;
create already does this, so link is for profiles made before it did, or after
an unlink.

Examples:
  ap create claude:plan
  ap create claude:review --from plan
  ap create claude:review --from default    # from the agent you already had
  ap create claude:work --copy-instructions
  ap create claude:new --from default \
      --only-settings statusLine --only-settings theme
  ap variant claude:review:opus -- --model='claude-opus-5[1m]' --effort=xhigh
  ap run claude:review:opus         # those arguments, then yours
  ap variant claude:review:on -- '/code-review {}'
  ap run claude:review:on src/auth.go   # runs "/code-review src/auth.go"
  ap variant claude:review:on --yes -- '/code-review {} --fix'   # overwrite it
  ap run claude:plan plugin install caveman@caveman
  ap run claude:plan
  ap run claude:plan --effort xhigh
  ap run opencode:review --model anthropic/claude-sonnet-4-5
  ap env claude:plan npx skills add vercel-labs/agent-skills \
      --skill web-design-guidelines -g -a claude-code
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
	case "list", "ls":
		return cmdList(args[1:])
	case "create":
		return cmdCreate(args[1:])
	case "variant":
		return cmdVariant(args[1:])
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

// repeatedFlag collects a flag given more than once. --only-settings takes many
// values where --from takes one, and repeating is what the rest of this CLI
// does; splitting on commas would make a key holding a comma untypeable and
// would be a second syntax to remember.
type repeatedFlag []string

func (r *repeatedFlag) String() string { return strings.Join(*r, " ") }

func (r *repeatedFlag) Set(v string) error {
	if v == "" {
		return errors.New("empty key")
	}
	*r = append(*r, v)
	return nil
}

// checkOnlySettings validates --only-settings before anything is created — the
// same "fail before the profile exists" rule as --from and --copy-instructions.
func checkOnlySettings(a agent.Agent, keys []string, from string) error {
	if len(keys) == 0 {
		return nil
	}
	if from == "" {
		return errors.New("--only-settings needs --from: there is no source to take settings out of\n" +
			"    `--from default` reads the agent's real config")
	}
	if a.Settings == "" {
		return fmt.Errorf("--only-settings: no settings file is known for %s (see internal/agent)", a.Name)
	}
	return nil
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

func cmdList(args []string) error {
	fs := flagSet("list")
	raw := fs.Bool("raw", false, "one tab-separated line per reference, no tree and no padding")
	stop, err := parse(fs, args)
	if stop {
		return err
	}
	names := agent.Names()
	// The agent is optional, so this cannot use parseAroundRef, which requires
	// one. The second parse is that helper's trick all the same: it is what lets
	// `ap list claude --raw` work as well as `ap list --raw claude`. list has no
	// passthrough, so there is nothing for either order to be ambiguous about.
	if rest := fs.Args(); len(rest) > 0 {
		if _, ok := agent.Lookup(rest[0]); !ok {
			return fmt.Errorf("unknown agent %q: supported are %s", rest[0], strings.Join(agent.Names(), ", "))
		}
		names = []string{rest[0]}
		if stop, err := parse(fs, rest[1:]); stop {
			return err
		}
		if extra := fs.Args(); len(extra) > 0 {
			return fmt.Errorf("unexpected argument %q\nusage: ap list [--raw] [agent]", extra[0])
		}
	}
	rows, err := listRows(names)
	if err != nil {
		return err
	}
	if *raw {
		printRaw(rows)
		return nil
	}
	printTree(rows)
	return nil
}

// defaultNote is what Default carries where a variant carries its arguments.
//
// Default is the one row in the listing ap did not create and cannot remove —
// Dir resolves it to the agent's real config directory — so printing it exactly
// like a profile invites `ap delete claude:default`, which is the command
// profile.ValidName exists to refuse. It replaced a bracketed name plus a
// footnote explaining the brackets: the note says the same thing in the place
// you are already looking, and a reference nothing decorates stays pasteable.
const defaultNote = "(the agent's own config: read-only)"

// listRow is one line of the listing.
//
// ref is qualified on every row, so any line is pasteable after `ap run`. That
// is the whole reason the tree carries full references rather than the leaf
// names the glyphs would let it get away with: the format is read to answer
// "which one was it?", and the answer has to be copyable where it is read.
type listRow struct {
	head string   // the agent's own line; every other field is empty on it
	tree string   // box-drawing prefix, human output only
	ref  string   // the qualified reference
	args []string // a variant's arguments; nil for a profile
	note string   // what Default carries instead of arguments
	bad  error    // set when a variant's arguments could not be read
}

// listRows walks the agents in one pass and returns every line to print, in
// order. Both renderers consume the same slice, so `--raw` cannot drift out of
// agreement with what the tree shows.
func listRows(names []string) ([]listRow, error) {
	var rows []listRow
	for _, name := range names {
		a, _ := agent.Lookup(name)
		// List always includes Default, so there is no "no profiles yet" case to
		// report: every agent has at least its real config to show.
		profiles, err := profile.List(a)
		if err != nil {
			return nil, err
		}
		rows = append(rows, listRow{head: a.Name})
		for i, p := range profiles {
			lastProfile := i == len(profiles)-1
			row := listRow{tree: branch(lastProfile), ref: a.Name + ":" + p}
			if p == profile.Default {
				row.note = defaultNote
			}
			rows = append(rows, row)

			variants, err := profile.Variants(a, p)
			if err != nil {
				return nil, err
			}
			for j, v := range variants {
				r := listRow{
					tree: continuation(lastProfile) + branch(j == len(variants)-1),
					ref:  a.Name + ":" + p + ":" + v,
				}
				// An entry that cannot be read is reported on its own row, not
				// returned. Returning aborted the whole listing after it had
				// already printed part of it — one unreadable file under claude
				// and codex, and pi and opencode never appeared at all. That also
				// silently defeated scripts/smoke.sh's agents(), which reads this
				// output: it yielded one agent instead of four, and two blocks
				// then tested nothing while still reporting nothing wrong.
				r.args, r.bad = profile.VariantArgs(a, p, v)
				rows = append(rows, r)
			}
		}
	}
	return rows, nil
}

func branch(last bool) string {
	if last {
		return "└─ "
	}
	return "├─ "
}

func continuation(lastProfile bool) string {
	if lastProfile {
		return "   "
	}
	return "│  "
}

// printTree is the human listing: an agent per column-0 line, its profiles
// under it, and each profile's variants under that.
//
// The tree is what attaches a variant to the profile it belongs to. The format
// this replaced collected every variant into one block at the bottom, so the
// name and the thing it varies were nowhere near each other.
//
// The payload column is last and allowed to overflow, the deal `ps aux` makes
// with CMD. Arguments are printed unquoted: `--model=claude-opus-5[1m]` in zsh
// is `no matches found`, but quoting them for display would reintroduce a
// shell-quoting function, and its hostile-argument test, for a line nothing
// execs.
//
// They are printed at all because they are the point, not decoration. A command
// whose name silently disables every permission prompt is a real hazard, and ap
// exists precisely so that you have enough profiles not to remember what each
// one does. Printing them here and in the `ap variant` receipt is what keeps
// --dangerously-skip-permissions visible, with no special handling for that flag
// in particular — and it costs nothing, because the store is already a list of
// strings.
func printTree(rows []listRow) {
	// Width over the rows that actually have a payload. Measuring the others too
	// would pad short rows out to a column nothing occupies, which is trailing
	// whitespace with extra steps.
	w := 0
	for _, r := range rows {
		if r.ref == "" || r.payload() == "" {
			continue
		}
		if n := utf8.RuneCountInString(r.tree + r.ref); n > w {
			w = n
		}
	}
	for _, r := range rows {
		if r.head != "" {
			fmt.Println(r.head)
			continue
		}
		name := r.tree + r.ref
		pay := r.payload()
		if pay == "" {
			fmt.Println(name)
			continue
		}
		fmt.Printf("%s%s%s\n", name, strings.Repeat(" ", w-utf8.RuneCountInString(name)+3), pay)
	}
}

// payload is what follows the reference: a variant's arguments, the note that
// marks Default, or nothing.
func (r listRow) payload() string {
	switch {
	case r.bad != nil:
		return fmt.Sprintf("(unreadable: %v)", r.bad)
	case len(r.args) > 0:
		return strings.Join(r.args, " ")
	default:
		return r.note
	}
}

// printRaw is the machine listing: the reference in field 1, one argument per
// field after it, tab-separated, no tree, no padding, no notes, no header.
//
// It exists so that nothing has to parse the human format. scripts/smoke.sh
// parsed it for years — `agents()` cut every column-0 line at its first colon —
// and that coupling has already broken twice in ways that reddened blocks
// testing something else entirely. `ap list --raw | cut -f1` is also the exact
// list of references `ap run` accepts, which is what shell completion needs.
//
// One argument per field rather than one joined string, because that is the
// same shape the store already has (one argument per line) — so there is no
// quoting to invent and none to get wrong. WriteVariant refuses an argument
// containing a tab for the same reason it refuses a newline, which is what
// makes this lossless by construction rather than by luck.
//
// An unreadable entry goes to stderr and its reference is still printed, with
// no arguments. `cut -f1` — the reason raw exists — stays correct, and the
// failure is not silent.
func printRaw(rows []listRow) {
	for _, r := range rows {
		if r.ref == "" {
			continue
		}
		if r.bad != nil {
			fmt.Fprintf(os.Stderr, "ap list: %v\n", r.bad)
		}
		fmt.Println(strings.Join(append([]string{r.ref}, r.args...), "\t"))
	}
}

// vref parses the single positional argument taken by link and unlink.
// ParseVariantRef rejects Default. The variant is "" for a two-segment
// reference.
//
// Not delete: that one takes a flag as well, so it parses with parseAroundRef
// and calls ParseVariantRef itself.
func vref(args []string, cmd string) (agent.Agent, string, string, error) {
	if len(args) != 1 {
		return agent.Agent{}, "", "", fmt.Errorf("usage: ap %s <agent>:<profile>[:<variant>]", cmd)
	}
	a, name, v, err := profile.ParseVariantRef(args[0])
	return a, name, v, err
}

// vrefAllowDefault is vref but also accepts Default, for `which` — the one
// read-only command that takes nothing but a reference and may resolve to the
// agent's real config directory. `env` has the same rule but takes a trailing
// command, so it calls ParseVariantRefAllowDefault itself.
//
// The variant is parsed and then ignored, on purpose: a variant has no
// configuration of its own, and answering with anything but the parent's
// directory would invent a second one that nothing writes to.
func vrefAllowDefault(args []string, cmd string) (agent.Agent, string, string, error) {
	if len(args) != 1 {
		return agent.Agent{}, "", "", fmt.Errorf("usage: ap %s <agent>:<profile>[:<variant>]", cmd)
	}
	a, name, v, err := profile.ParseVariantRefAllowDefault(args[0])
	return a, name, v, err
}

func cmdCreate(args []string) error {
	fs := flagSet("create")
	from := fs.String("from", "", "clone an existing profile of the same agent")
	copyMD := fs.Bool("copy-instructions", false, "copy your global instructions file (CLAUDE.md, AGENTS.md) into the profile")
	var only repeatedFlag
	fs.Var(&only, "only-settings", "clone only this key of the source's settings file, and nothing else (repeatable; needs --from)")
	stop, r, err := parseAroundRef(fs, args,
		"create [--from <profile>] [--only-settings <key>]... [--copy-instructions] <agent>:<profile>")
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
		// Reject a qualified reference before it reaches ParseRefAllowDefault:
		// that call builds "<agent>:"+*from internally, so "--from codex:new"
		// becomes the 3-part "codex:codex:new" and fails with a "want
		// <agent>:<profile>" message that reads as if --from wanted that
		// format, when the real fix is to drop the agent prefix.
		if strings.Contains(*from, ":") {
			return fmt.Errorf("--from %q: profile name only, not agent:profile — agent is already %q", *from, a.Name)
		}
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
	if err := checkOnlySettings(a, only, *from); err != nil {
		return err
	}

	dir, err := profile.Create(a, name)
	if err != nil {
		return err
	}
	rc := &receipt{}
	rc.add("dir", tilde(dir))

	if err := cloneAndReport(a, srcDir, *from, only, name, dir, rc); err != nil {
		return err
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
	// A --only-settings profile arrives nearly empty too, so it gets the same
	// next step a fresh one does.
	if srcDir == "" || len(only) > 0 {
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
	linkWrapper(a.Name+":"+name, rc)
}

// linkWrapper writes ref's wrapper and reports it, or says why not.
//
// Never an error, for the reason above, and shared by `ap create` and `ap
// variant` so the PATH warning cannot come with one and not the other — the
// warning is not a question the variant design left open, it is a consequence
// of reusing this helper.
func linkWrapper(ref string, rc *receipt) {
	p, err := writeWrapper(ref)
	if err != nil {
		rc.warn("not linked: %v", err)
		return
	}
	rc.add("command", ref)
	// Only the people it applies to. Told to everyone, this line was noise; told
	// to nobody, a wrapper that silently does not run is the confusing case
	// linking was supposed to remove.
	if d := filepath.Dir(p); !onPath(d) {
		rc.warn("%s is not on your PATH, so typing `%s` will not work\n"+
			"    add it to PATH, or use: ap run %s", tilde(d), ref, ref)
	}
}

// callerShown stands for the caller's arguments in the `runs:` receipt: what a
// run will put wherever this appears.
const callerShown = "[your args...]"

// cmdVariant records a set of launch arguments over an existing profile.
//
// A noun where every other writing verb is a verb, deliberately: `ap create`
// makes profiles, and overloading it so the same word sometimes builds forty
// megabytes and sometimes writes two lines is worse than one noun.
//
// `--` is required and the payload is taken from the raw arguments, not from a
// FlagSet: everything after the separator belongs to the agent, and a flag
// package would eat `-p`. Requiring the separator is also what makes an empty
// payload impossible to type by accident — `ap variant <ref> --` with nothing
// after it would create a name that behaves identically to its parent.
//
// The head, before the separator, has no passthrough at all, so it parses with
// parseAroundRef exactly like `create` and `delete` do — which is what lets
// --yes sit on either side of the reference.
func cmdVariant(args []string) error {
	const use = "variant <agent>:<profile>:<variant> [--yes] -- <args...>"
	sep := -1
	for i, s := range args {
		if s == "--" {
			sep = i
			break
		}
	}
	if sep < 0 {
		return errors.New("usage: ap " + use)
	}
	payload := args[sep+1:]

	fs := flagSet("variant")
	yes := fs.Bool("yes", false, "overwrite an existing variant without asking")
	fs.BoolVar(yes, "y", false, "shorthand for --yes")
	stop, r, err := parseAroundRef(fs, args[:sep], use)
	if stop {
		return err
	}
	a, name, v, err := profile.ParseVariantRef(r)
	if err != nil {
		return err
	}
	if v == "" {
		return fmt.Errorf("%s:%s names no variant\nusage: ap %s", a.Name, name, use)
	}
	// Never creates the parent implicitly: a profile is the expensive half, and
	// this command writes two lines.
	if !profile.Exists(a, name) {
		return fmt.Errorf("profile %s:%s does not exist; create it with: ap create %s:%s",
			a.Name, name, a.Name, name)
	}

	// replace is decided here rather than passed straight from --yes, and the
	// difference matters: for a variant that does not exist, false is what makes
	// WriteVariant publish with Link, so one created in the gap between this read
	// and that write is refused rather than silently clobbered. --yes answers a
	// question; it does not turn every write into an overwrite.
	was, err := profile.VariantArgs(a, name, v)
	replace := err == nil
	ref := a.Name + ":" + name + ":" + v
	if replace && !*yes {
		if err := confirmOverwrite(ref, was, payload); err != nil {
			return err
		}
	}
	if err := profile.WriteVariant(a, name, v, payload, replace); err != nil {
		return err
	}

	rc := &receipt{}
	rc.add("profile", tilde(profile.Dir(a, name)))
	rc.add("args", strings.Join(payload, " "))
	// The composed line, unconditionally, with the caller's arguments shown where
	// they will actually land — substituted into the placeholder if the variant
	// left one, appended at the end if it did not.
	//
	// A variant whose last argument is a positional prompt and leaves no
	// placeholder is terminal: claude's grammar is `claude [options] [command]
	// [prompt]`, one trailing positional, and a second one is DROPPED IN SILENCE
	// — measured, not assumed: `claude -p "say FIRST" "say SECOND"` answers FIRST
	// and exits 0. There is no error to read, which is exactly why printing what
	// a run will look like matters: it is visible at the moment the variant is
	// created, and never afterwards.
	//
	// Detecting that case instead would be a guess about someone else's argv
	// grammar — `--model opus` also ends in a non-flag word — and this repository
	// distrusts those by policy. The placeholder is the other answer: the author
	// states the position, so nothing has to be inferred.
	//
	// This line is also where a placeholder that collided with a literal `{}`
	// becomes visible, since it renders the substitution rather than the store.
	shown, filled := fill(payload, callerShown)
	runs := strings.Join(append([]string{a.Bin}, shown...), " ")
	if !filled {
		runs += " " + callerShown
	}
	rc.add("runs", runs)
	// Also printing --dangerously-skip-permissions where anyone creating a
	// variant will read it. A command whose name silently disables every
	// permission prompt is a real hazard, and this costs nothing: the store is
	// already a list of strings.
	linkWrapper(ref, rc)
	// "replaced", not "created", when it was. The receipt is the record of what
	// just happened, and a variant whose arguments were silently swapped under a
	// line that says "created" is the kind of report that teaches you to stop
	// reading receipts.
	verb := "created"
	if replace {
		verb = "replaced"
	}
	rc.print(fmt.Sprintf("%s %q", verb, ref))
	return nil
}

// cloneAndReport runs whichever clone --from asked for and reports it. Split out
// of cmdCreate for the same reason linkAndReport was: the complexity gate.
//
// A failed clone removes the half-populated directory so `ap create` can be
// retried. Safe here specifically because Link has not run yet, so the directory
// provably contains no symlinks.
func cloneAndReport(a agent.Agent, srcDir, from string, only []string, name, dir string, rc *receipt) error {
	if srcDir == "" {
		return nil
	}
	if len(only) == 0 {
		if err := profile.Clone(a, srcDir, dir); err != nil {
			profile.Discard(a, name)
			return err
		}
		rc.add("cloned", fmt.Sprintf("%s:%s", a.Name, from))
		return nil
	}
	found, missing, err := profile.CloneSettings(a, srcDir, dir, only)
	if err != nil {
		profile.Discard(a, name)
		return err
	}
	for _, k := range missing {
		// Named, not counted. A typo must not seed silence.
		if strings.Contains(k, ".") {
			// A key holding a literal dot is unreachable because paths split
			// on ".". Only said for a key that actually has one — a plain
			// typo is not a dotted-path problem, and saying so would send
			// someone looking in the wrong place.
			rc.warn("--only-settings %s: no such key in %s\n"+
				"    (paths split on \".\", so a key holding a literal dot cannot be named)", k, a.Settings)
			continue
		}
		rc.warn("--only-settings %s: no such key in %s", k, a.Settings)
	}
	if len(found) > 0 {
		// "nothing else" is not decoration: --from default normally carries six
		// entries, and a reader has to be able to tell at a glance that this run
		// did not.
		rc.add("cloned", fmt.Sprintf("%s: %s   (from %s; nothing else)",
			a.Settings, strings.Join(found, " "), from))
	}
	return nil
}

// linkAndReport runs profile.Link and prints what it did. Split out of cmdCreate
// purely to keep cmdCreate under the project's cyclomatic-complexity gate.
func linkAndReport(a agent.Agent, dir string, rc *receipt) error {
	linked, skipped, unshared, orphaned, err := profile.Link(a, dir, nil)
	if err != nil {
		return err
	}
	if len(linked) > 0 {
		rc.add("shares", fmt.Sprintf("%q", linked))
	}
	if len(orphaned) > 0 {
		rc.warn("%s", orphanWarning(orphaned))
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

// orphanWarning phrases what Link did when an agent had overwritten a shared
// link with a real file of its own. One wording, used by both `ap create`'s
// receipt and `ap run`'s stderr, so the two cannot drift apart.
func orphanWarning(orphaned []string) string {
	return fmt.Sprintf("sharing restored; the profile's own copy is kept as %s\n"+
		"    (delete it once you are logged in again)", strings.Join(orphaned, " "))
}

// linkForRun re-asserts the shared links before a run and reports what it took to
// do it. Agents rewrite their credential files, and a temp-file-plus-rename leaves
// a real file where our symlink was, silently unsharing auth.
//
// Unlike the create path, this one can ask. A file that differs from the shared
// one may hold the only token that still works, and only the person at the
// terminal can say whether it should become the machine-wide login — see
// profile.Promote for why nothing can decide that on its own.
func linkForRun(a agent.Agent, name, dir string) error {
	var promoted []string
	_, _, _, orphaned, err := profile.Link(a, dir, func(c profile.Conflict) profile.Resolution {
		if askToPromote(a, name, c) != profile.Promote {
			return profile.Orphan
		}
		promoted = append(promoted, fmt.Sprintf("%s updated, previous kept as %s",
			c.SharedPath, filepath.Base(c.PreviousPath)))
		return profile.Promote
	})
	if err != nil {
		return err
	}
	// Say what happened. Link healed the sharing, but a credential was moved aside
	// to do it, and a token the agent wrote inside this profile is no longer the
	// one it will use. Silence here would make a re-login look like it came out of
	// nowhere. Checked after err, so neither line can claim a write that failed.
	switch {
	case len(promoted) > 0:
		fmt.Fprintf(os.Stderr, "ap: promoted — %s\n", strings.Join(promoted, "; "))
	case len(orphaned) > 0:
		fmt.Fprintf(os.Stderr, "ap: warning: %s\n", orphanWarning(orphaned))
	}
	return nil
}

// askToPromote explains a credential conflict and asks what to do about it.
//
// Only "1" promotes. Anything else — an empty line, a typo, a terminal that is not
// one — keeps the shared credential, because that is the answer that changes
// nothing outside the profile, and the question comes back on the next run.
// Non-interactive runs are not prompted at all: `ap run` in a script or a CI job
// has nobody to ask, and guessing "promote" there would write into the user's real
// config directory unattended.
func askToPromote(a agent.Agent, name string, c profile.Conflict) profile.Resolution {
	if !stdinIsTerminal() {
		return profile.Orphan
	}
	const stamp = "2006-01-02 15:04"
	// Say which way round it actually is. Usually the profile's copy is the newer
	// one, but a bare agent run after the profile diverged makes the shared one
	// newer, and promoting is then the wrong answer. Asserting "newer" unchecked
	// is the same assumption this whole path exists to stop Link making.
	age := "newer"
	if c.ProfileTime.Before(c.SharedTime) {
		age = "older"
	}
	fmt.Fprintf(os.Stderr, "\n"+
		"ap: %s:%s has its own %s, %s than the shared one.\n"+
		"    %s leaves one here when it refreshes a token or you run /login,\n"+
		"    and only a promotion can carry it back to the shared credential.\n"+
		"\n"+
		"      here %s   ·   shared %s\n"+
		"\n"+
		"      1) Promote it — every profile and a bare %s use it from now on\n"+
		"      2) Ignore it  — this profile goes back to the shared credential\n"+
		"\n"+
		"    [2] ",
		a.Name, name, c.Rel, age,
		a.Bin,
		c.ProfileTime.Format(stamp), c.SharedTime.Format(stamp),
		a.Bin)

	if strings.TrimSpace(readLine(os.Stdin)) == "1" {
		return profile.Promote
	}
	return profile.Orphan
}

// stdinIsTerminal reports whether there is anyone there to answer a prompt.
// os.Stdin.Stat rather than x/term because this project is standard library only.
func stdinIsTerminal() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// readLine reads one line from r, one byte at a time.
//
// Unbuffered on purpose: bufio reads ahead past the newline, and everything it
// swallowed would be missing from the stdin the agent inherits moments later at
// exec.
func readLine(r io.Reader) string {
	var line []byte
	var b [1]byte
	for {
		n, err := r.Read(b[:])
		if n == 0 || err != nil || b[0] == '\n' {
			return string(line)
		}
		line = append(line, b[0])
	}
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
	a, name, _, err := vrefAllowDefault(args, "which")
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
	// The variant is parsed and then dropped, both for the printing form and for
	// the exec'ing one. `ap env <ref> <command...>` runs something that is NOT
	// the agent — an installer, `npx skills add` — and a variant's arguments are
	// the agent's flags. runArgs is deliberately not called here.
	a, name, _, err := profile.ParseVariantRefAllowDefault(args[0])
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
		return fmt.Errorf("usage: ap run <agent>:<profile>[:<variant>] [args...]")
	}
	// No flag parsing at all: everything after the reference belongs to the agent,
	// verbatim. See the flag-order note in usage.
	a, name, v, err := profile.ParseVariantRefAllowDefault(args[0])
	if err != nil {
		return err
	}
	// prepare first, so a variant of a profile that is not there reports the
	// missing profile and how to create it, rather than a missing variant of
	// nothing. A dangling variant needs no guard of its own beyond that.
	dir, err := prepare(a, name)
	if err != nil {
		return err
	}
	argv, err := runArgs(a, name, v, args[1:])
	if err != nil {
		return err
	}
	return run.Exec(a, dir, argv)
}

// placeholder is the hole a variant may leave for the caller's arguments,
// spelled the way `xargs -I{}` and `find -exec … {} \;` spell it.
//
// Deliberately NOT a whole-argument token. The case it exists for is a prompt
// prefix — `"/superpowers:executing-plans {}"` — and a prompt has to reach the
// agent as ONE element of argv. A token that only matched a bare argument would
// substitute into a new element and change nothing.
//
// There is no escape, the same stated limit as the newline and the tab. The
// collision it buys is real and worth naming: claude takes `--agents <json>`,
// and a nested empty object (`{"reviewer":{}}`) baked into a variant would be
// substituted. `ap variant`'s receipt prints the composed line at create time,
// so it is visible where it is written rather than the first time it runs.
const placeholder = "{}"

// runArgs composes what the agent receives.
//
// Without a placeholder: the variant's arguments, then the caller's. One rule,
// no special cases — later wins in every CLI here, so a caller can override a
// baked default for one invocation without editing anything, and ap still
// parses none of it.
//
// With one: the caller's arguments are joined by a space and substituted where
// the variant asked for them, and are NOT also appended. That exists because a
// baked positional prompt is otherwise terminal — claude's grammar has exactly
// one trailing positional, so a second is dropped in silence (measured: `claude
// -p "say FIRST" "say SECOND"` answers FIRST and exits 0).
//
// This is not the agent-specific insertion that was rejected before, and the
// distinction is the whole reason it is allowed now. Inserting the caller's
// arguments before a trailing positional on ap's own initiative would mean
// deciding which baked argument IS the positional — `--model opus` also ends in
// a bare word — which is a guess about four external CLIs, re-verified every
// release. A placeholder guesses nothing: the author states the position, and
// ap substitutes text. Every variant without one behaves exactly as before.
func runArgs(a agent.Agent, name, v string, caller []string) ([]string, error) {
	if v == "" {
		return caller, nil
	}
	baked, err := profile.VariantArgs(a, name, v)
	if err != nil {
		return nil, err
	}
	if filled, ok := fill(baked, strings.Join(caller, " ")); ok {
		return filled, nil
	}
	return append(baked, caller...), nil
}

// fill substitutes with into every placeholder in args, and reports whether it
// found one. Every occurrence, like `xargs -I`, because a variant that names the
// hole twice meant it twice.
//
// A caller with no arguments substitutes the empty string rather than being an
// error: running only the prefix — the slash command with no argument, so the
// agent asks — is a legitimate thing to want from the same name.
func fill(args []string, with string) ([]string, bool) {
	found := false
	out := make([]string, len(args))
	for i, s := range args {
		if strings.Contains(s, placeholder) {
			found = true
			s = strings.ReplaceAll(s, placeholder, with)
		}
		out[i] = s
	}
	return out, found
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

	if err := linkForRun(a, name, dir); err != nil {
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
	foundReal, err := profile.Shim(a, dir)
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
	stop, r, err := parseAroundRef(fs, args, "delete [--yes] <agent>:<profile>[:<variant>]")
	if stop {
		return err
	}
	a, name, v, err := profile.ParseVariantRef(r)
	if err != nil {
		return err
	}
	if v != "" {
		return deleteVariant(a, name, v)
	}
	// Before the prompt, not after: a typo must not be answered "y".
	if !profile.Exists(a, name) {
		return notThere(a, name)
	}
	// Read before the prompt, because the prompt names them: the answer is only
	// meaningful with the whole cost in view.
	variants, err := profile.Variants(a, name)
	if err != nil {
		return err
	}
	dir := profile.Dir(a, name)
	if !*yes {
		if err := confirm(dir, variants); err != nil {
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
	// row is omitted rather than claiming an unlink that did not happen. The
	// same argument applies to a variant of a profile that no longer exists,
	// which is why the cascade below is not optional.
	p, err := removeWrapperIfOurs(a.Name + ":" + name)
	if err != nil {
		return err
	}
	if p != "" {
		rc.add("command", fmt.Sprintf("%s:%s unlinked", a.Name, name))
	}
	deleteTheVariantsToo(a, name, variants, rc)
	rc.print(fmt.Sprintf("deleted %q", a.Name+":"+name))
	return nil
}

// deleteTheVariantsToo removes the store entries and the wrappers that named
// the profile just deleted. Split out of cmdDelete purely to keep it under the
// project's cyclomatic-complexity gate.
//
// Never an error, and it never stops early, for the same reason seedAndLink is
// not an error: the irreversible thing has already happened by the time this
// runs. It used to return on the first wrapper it was refused — a foreign file
// sitting at one variant's wrapper path — which abandoned every later variant
// AND swallowed the receipt, so `ap delete` reported a refusal and never
// mentioned the profile it had just erased.
func deleteTheVariantsToo(a agent.Agent, name string, variants []string, rc *receipt) {
	if err := profile.DeleteVariants(a, name); err != nil {
		rc.warn("variant arguments not removed: %v", err)
	}
	for _, v := range variants {
		if _, err := removeWrapperIfOurs(a.Name + ":" + name + ":" + v); err != nil {
			rc.warn("%s", err)
		}
	}
	if len(variants) > 0 {
		rc.add("variants", strings.Join(variants, " "))
	}
}

// deleteVariant removes one variant and its wrapper, and asks nothing first.
//
// Confirmation is proportional to what is lost: `ap delete` asks about a
// profile because a profile holds its own session transcripts, and a variant
// holds two lines of text. Asking about both equally is how a prompt stops
// being read.
func deleteVariant(a agent.Agent, name, v string) error {
	ref := a.Name + ":" + name + ":" + v
	if err := profile.DeleteVariant(a, name, v); err != nil {
		return err
	}
	rc := &receipt{}
	rc.add("removed", "variant args (the profile is untouched)")
	// Warned, not returned: the store entry is already gone, so reporting only
	// the wrapper refusal would leave the user believing nothing happened.
	p, err := removeWrapperIfOurs(ref)
	if err != nil {
		rc.warn("%s", err)
	}
	if p != "" {
		rc.add("command", ref+" unlinked")
	}
	rc.print(fmt.Sprintf("deleted %q", ref))
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
	a, name, v, err := vref(args, "link")
	if err != nil {
		return err
	}
	// Belt as well as braces, the same reason Delete re-checks Default
	// independently of ParseRef: vref already routes through ParseVariantRef,
	// which refuses Default for every writing command, so there is no path
	// through dispatch that reaches this branch today -
	// TestLinkRefusesDefaultViaParseRef pins the ParseRef rejection, which is
	// what actually fires. Kept anyway, so a future change to vref or to link's
	// own routing does not silently start writing a wrapper for "nothing" (ap
	// run codex:default is already the real config).
	if name == profile.Default {
		return fmt.Errorf("nothing to link: ap run %s:%s is already your real config", a.Name, profile.Default)
	}
	if !profile.Exists(a, name) {
		return fmt.Errorf("profile %s:%s does not exist; create it with: ap create %s:%s",
			a.Name, name, a.Name, name)
	}
	ref := a.Name + ":" + name
	if v != "" {
		// A name is invocable only if `ap run` can resolve it. link links names
		// that already exist; it never invents one.
		if _, err := profile.VariantArgs(a, name, v); err != nil {
			return err
		}
		ref += ":" + v
	}
	p, err := writeWrapper(ref)
	if err != nil {
		return err
	}
	fmt.Printf("linked %s -> %s\n", p, ref)
	return nil
}

// writeWrapper writes the wrapper for ref into linkDir and returns its path.
// ref is the whole reference — "claude:plan" or "claude:review:opus" — because
// a variant's wrapper is named for all three segments and this function never
// needed anything else from the agent.
//
// Shared by `ap link`, by `ap create` and by `ap variant`: the three must agree
// about the marker and about refusing a foreign file, so there is one
// implementation and not three.
func writeWrapper(ref string) (string, error) {
	dir, err := linkDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	// Through an os.Root confined to dir, same as everything else that writes
	// into a directory named partly by user input — see internal/profile.Link
	// and copyInstructions above. ref can never itself contain a path
	// separator (ValidName already guarantees that for every segment), but the
	// confinement is what makes that a property of the code rather than of the
	// caller.
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()

	p := filepath.Join(dir, ref)
	// Overwriting something ap did not write would be a good way to lose a
	// real binary — refuse unless the file is either absent or already one of
	// ours.
	if b, err := root.ReadFile(ref); err == nil {
		if !strings.HasPrefix(string(b), wrapperHeader) {
			return "", fmt.Errorf("refusing to overwrite %s: it was not written by ap", p)
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := root.WriteFile(ref, []byte(wrapperScript(ref)), 0o755); err != nil {
		return "", err
	}
	return p, nil
}

// confirm asks before the one irreversible thing this program does. The default
// is no: a profile holds its own session transcripts, and an absent-minded Enter
// must not erase them.
//
// variants are named because they go with it. A variant without its parent is a
// command that fails confusingly, so the cascade is not optional — and a
// question that hides half of what it is about is not a question.
//
// EOF is not consent either, which is what makes this safe in a script: with no
// terminal there is nobody to answer, so the answer has to be stated up front
// with --yes. Reading the answer rather than testing for a tty also means
// `echo y | ap delete ...` works, and /dev/null does not.
func confirm(dir string, variants []string) error {
	fmt.Fprintf(os.Stderr, "? remove %s", tilde(dir))
	if n := len(variants); n > 0 {
		noun := "variants"
		if n == 1 {
			noun = "variant"
		}
		fmt.Fprintf(os.Stderr, "\n  and its %d %s: %s", n, noun, strings.Join(variants, ", "))
	}
	yes, noTerminal := answered()
	if noTerminal {
		return errors.New("delete needs a terminal to confirm; pass --yes")
	}
	if yes {
		return nil
	}
	return errors.New("cancelled — nothing removed")
}

// answered prints the prompt's tail and reads the reply. Shared by the two
// questions ap asks, so the rule that makes them safe in a script is written
// once: EOF is not consent. With no terminal there is nobody to answer, so the
// answer has to be stated up front with --yes.
//
// Reading the answer rather than testing for a tty also means `echo y | ap
// delete ...` works and /dev/null does not, which is the behaviour a script
// wants from both.
func answered() (yes, noTerminal bool) {
	fmt.Fprint(os.Stderr, " [y/N] ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(os.Stderr)
		return false, true
	}
	s := strings.ToLower(strings.TrimSpace(line))
	return s == "y" || s == "yes", false
}

// confirmOverwrite asks before replacing a variant's arguments, and shows both
// sets while asking.
//
// Showing the old ones is the whole value of the question. What you lose is a
// line of flags you wrote once and have not read since — which is the same
// reason `ap list` prints them — and a prompt that only said "overwrite? [y/N]"
// would be asking you to consent to something it declined to show you. `ap
// delete` names the variants it would take with the profile for the same reason.
func confirmOverwrite(ref string, was, now []string) error {
	// The trailing newline gives the question its own line. Both argument lists
	// run past 80 columns routinely — that is why they are worth showing — and
	// `[y/N]` tacked onto the end of the second one is a question you scroll to
	// find.
	fmt.Fprintf(os.Stderr, "? overwrite %s\n    was  %s\n    now  %s\n",
		ref, strings.Join(was, " "), strings.Join(now, " "))
	yes, noTerminal := answered()
	if noTerminal {
		return fmt.Errorf("%s already exists, and overwriting needs a terminal to confirm; pass --yes", ref)
	}
	if yes {
		return nil
	}
	return errors.New("cancelled — the variant is unchanged")
}

func cmdUnlink(args []string) error {
	a, name, v, err := vref(args, "unlink")
	if err != nil {
		return err
	}
	ref := a.Name + ":" + name
	if v != "" {
		ref += ":" + v
	}
	p, err := removeWrapperIfOurs(ref)
	if err != nil {
		return err
	}
	if p == "" {
		fmt.Printf("%s was not linked\n", ref)
		return nil
	}
	fmt.Printf("unlinked %s — nothing else is touched; ap run %s still works\n", tilde(p), ref)
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
