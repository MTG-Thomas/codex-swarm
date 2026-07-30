//go:build !windows

package userpath

import (
	"fmt"
	"runtime"
)

func UpdateExecutableDir(Action) (Result, error) {
	return "", fmt.Errorf("user PATH registry updates are unsupported on %s", runtime.GOOS)
}
