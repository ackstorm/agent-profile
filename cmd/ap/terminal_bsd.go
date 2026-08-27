//go:build unix && !linux

package main

import "syscall"

// The BSDs and macOS spell the same request TIOCGETA.
const ioctlReadTermios = syscall.TIOCGETA
