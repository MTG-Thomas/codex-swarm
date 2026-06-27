//go:build !windows

package main

func maybeRunService() (bool, error) {
	return false, nil
}
