//go:build linux

package main

import "syscall"

// ioctlReadTermios is the "read this fd's terminal settings" request. It
// succeeds only on a real terminal, which is the whole question — see
// stdinIsTerminal.
const ioctlReadTermios = syscall.TCGETS
