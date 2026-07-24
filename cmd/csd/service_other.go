//go:build !windows && !darwin && !linux

package main

import "fmt"

func installService(args []string) error {
	if _, err := parseServiceScope("install", args); err != nil {
		return err
	}
	return fmt.Errorf("service install is not implemented for this platform; run `csd serve` or install it with your OS service manager")
}

func uninstallService(args []string) error {
	if _, err := parseServiceScope("uninstall", args); err != nil {
		return err
	}
	return fmt.Errorf("service uninstall is not implemented for this platform")
}
