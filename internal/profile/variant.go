package profile

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ackstorm/agent-profile/internal/agent"
)

// A launch variant is a set of arguments over an existing profile's
// configuration. It has no directory, no shim, no links and no first-run
// seeding: anything that resolves a configuration directory resolves the
// parent's. All a variant is, is this file.
//
// One argument per line. No JSON, no escaping, no format to design — the cost
// is the stated limit that an argument may not contain a newline, and
// WriteVariant refuses one rather than inventing an escape syntax for a case
// nobody has.
//
// Nothing here ever goes near a shell: ap run reads the file into a []string
// and hands it to syscall.Exec. That is why `--model=claude-opus-5[1m]` cannot
// glob and `$(…)` cannot execute, and why there is no quoting anywhere in this
// feature.

// VariantsRoot is where launch variants live: a sibling of Root(), never inside
// a profile.
//
// The profile directory IS the agent's configuration directory — the agent
// lists it, validates it and rewrites parts of it. Putting ap's own metadata
// there is putting a foreign file in someone else's house, and CLAUDE.md
// already documents what happens when the contents of that directory surprise
// us.
func VariantsRoot() string {
	return filepath.Join(filepath.Dir(Root()), "variants")
}

// WriteVariant records the launch arguments for a:name:v.
//
// replace is false for the default, which refuses an existing variant exactly as
// Create refuses an existing profile. `ap variant` asks before passing true, and
// --yes is how a script answers — the same shape as `ap delete`.
//
// The asking is the point, not the refusing. This used to have no override at
// all, on the grounds that `ap delete` then `ap variant` is one line of shell;
// what that argument missed is that the pair is one line only when you already
// know the variant exists, and you find that out from an error after typing the
// whole payload.
func WriteVariant(a agent.Agent, name, v string, args []string, replace bool) error {
	if len(args) == 0 {
		return fmt.Errorf("variant %s:%s:%s would carry no arguments, so it would behave identically to %s:%s",
			a.Name, name, v, a.Name, name)
	}
	for _, s := range args {
		switch {
		case strings.Contains(s, "\n"):
			return fmt.Errorf("argument %q contains a newline: the store is one argument per line, and has no escape", s)
		case strings.Contains(s, "\t"):
			// The same stated limit as the newline above, for the same reason and
			// one layer out: `ap list --raw` separates arguments with a tab so that
			// nothing has to parse the human listing, and it has no escape either.
			// Refusing at write time is what makes that format lossless by
			// construction rather than by luck.
			return fmt.Errorf("argument %q contains a tab: `ap list --raw` separates arguments with one, and has no escape", s)
		}
	}
	root, err := openVariantsRoot(true)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()

	// Mkdir rather than MkdirAll, one level at a time and through the Root, so
	// every component is created inside the confinement — the same shape as
	// Link's parent-directory handling.
	for _, d := range []string{a.Name, filepath.Join(a.Name, name)} {
		if err := root.Mkdir(d, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
	}
	// Written to a temporary name and linked into place, never written at the
	// final name directly.
	//
	// A create-then-write would publish the entry before its contents exist, so
	// an interrupted `ap variant` (a kill, ENOSPC) leaves a TRUNCATED argument
	// list that reads back without error — and a variant whose first line is
	// --dangerously-skip-permissions and whose remaining lines were lost still
	// execs. Publishing the finished temporary file is what makes the entry
	// appear complete or not at all.
	//
	// Which call publishes it is exactly the refuse-or-replace decision, and that
	// is why it is not a check followed by a write. Link fails with EEXIST rather
	// than clobbering, so the refusal is atomic — a Stat-then-Link would refuse a
	// variant written between the two, and worse, permit one. Rename replaces
	// atomically, so a reader either sees all of the old arguments or all of the
	// new ones, never a mix.
	//
	// The temporary name is dot-prefixed so Variants skips it: a leftover from a
	// crash between write and publish is invisible rather than a variant nobody
	// can explain.
	rel := filepath.Join(a.Name, name, v)
	tmp := filepath.Join(a.Name, name, "."+v+".tmp")
	_ = root.Remove(tmp) // a leftover from an earlier crash must not block this one
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(strings.Join(args, "\n") + "\n"); err != nil {
		_ = f.Close()
		_ = root.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = root.Remove(tmp)
		return err
	}
	if replace {
		// Rename consumes the temporary name, so there is nothing left to clean
		// up and nothing to remove on the way out.
		return root.Rename(tmp, rel)
	}
	err = root.Link(tmp, rel)
	_ = root.Remove(tmp)
	if errors.Is(err, fs.ErrExist) {
		// `ap variant` looks the variant up and offers to overwrite before it gets
		// here, so reaching this means the entry appeared between the two — the
		// race Link exists to lose safely. The advice is the same either way, and
		// it is the advice a caller of this function directly needs too.
		return fmt.Errorf("variant %s:%s:%s already exists; overwrite it with: ap variant %s:%s:%s --yes -- <args...>",
			a.Name, name, v, a.Name, name, v)
	}
	return err
}

// VariantArgs returns the arguments recorded for a:name:v.
func VariantArgs(a agent.Agent, name, v string) ([]string, error) {
	root, err := openVariantsRoot(false)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, noVariant(a, name, v)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()

	f, err := root.Open(filepath.Join(a.Name, name, v))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, noVariant(a, name, v)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	// bufio.Scanner, and not a Split of the whole file, because its line
	// semantics are exactly the three rules this format needs: the final newline
	// TERMINATES rather than separates, so "-p\n" is one argument and "-p\n\n"
	// is two with the second empty; an empty line is an empty argument, which is
	// legal argv; and a line is verbatim, so "--model=opus " keeps its trailing
	// space. A TrimSpace anywhere here would silently corrupt an argument that
	// round-trips everywhere else.
	var args []string
	sc := bufio.NewScanner(f)
	// The default 64 KiB line cap is small for a baked prompt. Raised, and the
	// error checked, so an argument too long to read back fails loudly rather
	// than truncating into something that still execs.
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		args = append(args, sc.Text())
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("variant %s:%s:%s: %w", a.Name, name, v, err)
	}
	if len(args) == 0 {
		return nil, fmt.Errorf("variant %s:%s:%s is empty; delete it and write it again", a.Name, name, v)
	}
	return args, nil
}

// Variants lists the variant names recorded for a profile, sorted. A missing
// directory means none, not an error — most profiles have none.
func Variants(a agent.Agent, name string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(VariantsRoot(), a.Name, name))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// DeleteVariant removes one variant. The profile is untouched — a variant holds
// two lines of text, and that is the whole reason it is cheap to try five.
func DeleteVariant(a agent.Agent, name, v string) error {
	root, err := openVariantsRoot(false)
	if errors.Is(err, fs.ErrNotExist) {
		return noVariant(a, name, v)
	}
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()

	err = root.Remove(filepath.Join(a.Name, name, v))
	if errors.Is(err, fs.ErrNotExist) {
		return noVariant(a, name, v)
	}
	return err
}

// DeleteVariants removes every variant of a profile, for `ap delete
// <agent>:<profile>`. A variant without its parent is a command that fails
// confusingly — the same reason delete already removes wrappers.
func DeleteVariants(a agent.Agent, name string) error {
	root, err := openVariantsRoot(false)
	if errors.Is(err, fs.ErrNotExist) {
		return nil // nothing was ever recorded
	}
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	return root.RemoveAll(filepath.Join(a.Name, name))
}

// openVariantsRoot confines every write and removal to the variants root, the
// same as everything else here that builds a path partly from user input.
// create is false for the readers, so a machine that has never written a
// variant reports "no variant" rather than creating a directory to prove it.
func openVariantsRoot(create bool) (*os.Root, error) {
	if create {
		if err := os.MkdirAll(VariantsRoot(), 0o700); err != nil {
			return nil, err
		}
	}
	return os.OpenRoot(VariantsRoot())
}

// noVariant is the error for a reference that names no variant. Listing what
// the profile does have turns a typo into a one-line fix, the same as notThere
// does for a profile.
func noVariant(a agent.Agent, name, v string) error {
	have, err := Variants(a, name)
	if err != nil || len(have) == 0 {
		return fmt.Errorf("no variant %s:%s:%s", a.Name, name, v)
	}
	return fmt.Errorf("no variant %s:%s:%s — %s:%s has: %s",
		a.Name, name, v, a.Name, name, strings.Join(have, " "))
}
