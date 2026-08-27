// Package hydrate writes a resolved profile into a root and records what it
// wrote.
//
// A ROOT is always a parameter. §33.2 gives it two values — the agent's real
// configuration directory, or a named profile's namespace — and they are one
// mechanism: point the agent's config-directory variable at a directory. That
// is why there are two and not three. Nothing here infers a root from the
// environment, and nothing here reads $HOME.
//
// The LEDGER is the only state (§33.1). A manifest is an input: it is applied
// and may be thrown away, and nothing ever writes back to it. What is installed
// lives in <root>/.ap-ledger.json and nowhere else.
//
// Apply is ADDITIVE (§33). A manifest that stops declaring a resource does not
// remove it, and a file the user added by hand survives an apply that
// overwrites its siblings. Every overwrite is logged, because a silent
// overwrite is the one thing an additive policy cannot afford.
//
// The ledger is written LAST (§37.2). A ledger claiming files that were never
// written is worse than no ledger: every later verdict — remove, skip, report
// as modified — would rest on a record that was never true. A crash between
// materialization and the ledger write leaves files unclaimed, and re-running
// the apply repairs it.
//
// There is exactly ONE lock (§37.3): the root. The source cache is deliberately
// unlocked, because pkg/source publishes entries atomically, so two processes
// resolving one source waste a fetch and cannot corrupt anything. Do not add a
// second lock: it would buy that fetch back for the price of a lock-ordering
// rule and the deadlock the rule exists to exclude.
package hydrate
