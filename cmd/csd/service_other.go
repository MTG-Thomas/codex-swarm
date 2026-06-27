//go:build !windows && !darwin

package main

import "fmt"

func installService() error {
	return fmt.Errorf("service install is not implemented for this platform; run `csd serve` or install it with your OS service manager")
}

func uninstallService() error {
	return fmt.Errorf("service uninstall is not implemented for this platform")
}
