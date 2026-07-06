package main

import (
	"errors"
	"fmt"

	"github.com/MTG-Thomas/codex-swarm/internal/version"
)

func (c cli) version(args []string) error {
	if len(args) != 0 {
		return errors.New("version does not accept arguments")
	}
	fmt.Fprintln(c.out, version.String())
	return nil
}
