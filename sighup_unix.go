//go:build !windows

package main

import (
	"os"
	"syscall"
)

// sinaisRecarga: SIGHUP recarrega agentes/*.yaml e as OpenAPIs (specs/03 F6).
func sinaisRecarga() []os.Signal { return []os.Signal{syscall.SIGHUP} }
