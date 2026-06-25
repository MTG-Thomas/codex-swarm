package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

func (c cli) agent(args []string) error {
	if len(args) == 0 {
		return errors.New("agent requires <register|current|list>")
	}
	switch args[0] {
	case "register":
		return c.agentRegister(args[1:])
	case "current":
		return c.agentCurrent(args[1:])
	case "list":
		return c.agentList(args[1:])
	default:
		return fmt.Errorf("unknown agent command %q", args[0])
	}
}

func (c cli) agentRegister(args []string) error {
	fs := c.flagSet("agent register")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	name := fs.String("name", "", "agent name")
	role := fs.String("role", "", "agent role")
	current := fs.Bool("current", true, "make this the current agent")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*name) == "" {
		return errors.New("agent register requires --name")
	}
	now := c.now().UTC()
	agent := store.Agent{
		ID:        fmt.Sprintf("a-%s", now.Format("20060102-150405")),
		Name:      *name,
		Role:      *role,
		Current:   *current,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.NewJSONStore(*statePath).SaveAgent(agent); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "agent %s name=%q role=%s current=%t\n", agent.ID, agent.Name, emptyDash(agent.Role), agent.Current)
	return nil
}

func (c cli) agentCurrent(args []string) error {
	fs := c.flagSet("agent current")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	agent, err := store.NewJSONStore(*statePath).CurrentAgent()
	if err != nil {
		if errors.Is(err, store.ErrAgentNotFound) {
			return errors.New("no current agent registered")
		}
		return err
	}
	printAgent(c.out, agent)
	return nil
}

func (c cli) agentList(args []string) error {
	fs := c.flagSet("agent list")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	agents, err := store.NewJSONStore(*statePath).ListAgents()
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "agents=%d state=%s\n", len(agents), *statePath)
	for _, agent := range agents {
		printAgent(c.out, agent)
	}
	return nil
}

func printAgent(out interface{ Write([]byte) (int, error) }, agent store.Agent) {
	fmt.Fprintf(out, "%s\t%s\t%s\tcurrent=%t\n", agent.ID, agent.Name, emptyDash(agent.Role), agent.Current)
}
