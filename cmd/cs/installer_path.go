package main

import (
	"errors"
	"fmt"

	"github.com/MTG-Thomas/codex-swarm/internal/userpath"
)

func (c cli) installerPath(args []string) error {
	if len(args) != 1 {
		return errors.New("__installer-path requires add or remove")
	}

	action := userpath.Action(args[0])
	if action != userpath.Add && action != userpath.Remove {
		return fmt.Errorf("__installer-path action must be add or remove, got %q", args[0])
	}

	result, err := userpath.UpdateExecutableDir(action)
	if err != nil {
		return fmt.Errorf("%s executable directory in user PATH: %w", action, err)
	}
	fmt.Fprint(c.out, result)
	return nil
}
