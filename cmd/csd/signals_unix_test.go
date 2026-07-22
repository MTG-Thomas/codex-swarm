//go:build !windows

package main

import (
	"syscall"
	"testing"
)

func TestTerminationSignalsIncludeSIGTERM(t *testing.T) {
	for _, signal := range terminationSignals() {
		if signal == syscall.SIGTERM {
			return
		}
	}
	t.Fatal("termination signals do not include SIGTERM")
}
