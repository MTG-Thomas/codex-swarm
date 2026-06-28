//go:build !windows

package main

func defaultServiceStatePath() string {
	return defaultStatePath()
}
