package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

func (c cli) trace(args []string) error {
	if len(args) == 0 {
		return errors.New("trace requires <start|into|done|back|log|status|merge>")
	}
	switch args[0] {
	case "start":
		return c.traceStart(args[1:])
	case "into":
		return c.traceInto(args[1:])
	case "done":
		return c.tracePop(args[1:], "done")
	case "back":
		return c.tracePop(args[1:], "back")
	case "log":
		return c.traceLog(args[1:])
	case "status":
		return c.traceStatus(args[1:])
	case "merge":
		return c.traceMerge(args[1:])
	default:
		return fmt.Errorf("unknown trace command %q", args[0])
	}
}

type traceOutput struct {
	Status     string            `json:"status"`
	Action     string            `json:"action"`
	Agent      string            `json:"agent"`
	Created    bool              `json:"created"`
	StackDepth int               `json:"stack_depth"`
	Current    string            `json:"current,omitempty"`
	Stack      []store.TraceItem `json:"stack,omitempty"`
}

func (c cli) traceStart(args []string) error {
	fs := c.flagSet("trace start")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	agent := fs.String("agent", defaultTraceAgent(), "trace agent lane")
	key := fs.String("key", "", "idempotency key")
	force := fs.Bool("force", false, "replace active stack")
	jsonOutput := fs.Bool("json", false, "print structured JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	title := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if title == "" {
		return errors.New("trace start requires <title>")
	}
	now := c.now().UTC()
	created := true
	lane, err := store.NewJSONStore(*statePath).UpdateTraceLane(*agent, func(lane *store.TraceLane) error {
		if len(lane.Stack) > 0 && strings.TrimSpace(*key) != "" && len(lane.Stack) == 1 && lane.Stack[0].Key == strings.TrimSpace(*key) {
			created = false
			return nil
		}
		if len(lane.Stack) > 0 && !*force {
			return errors.New("trace stack is already active; use --force to replace it")
		}
		if lane.CreatedAt.IsZero() {
			lane.CreatedAt = now
		}
		lane.Stack = []store.TraceItem{{Title: title, Key: strings.TrimSpace(*key), StartedAt: now}}
		lane.Events = append(lane.Events, store.TraceEvent{At: now, Type: "start", Title: title, Key: strings.TrimSpace(*key), Depth: 0})
		lane.UpdatedAt = now
		return nil
	})
	if err != nil {
		return err
	}
	return c.printTraceResult("start", lane, created, *jsonOutput)
}

func (c cli) traceInto(args []string) error {
	fs := c.flagSet("trace into")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	agent := fs.String("agent", defaultTraceAgent(), "trace agent lane")
	key := fs.String("key", "", "idempotency key")
	jsonOutput := fs.Bool("json", false, "print structured JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	title := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if title == "" {
		return errors.New("trace into requires <title>")
	}
	now := c.now().UTC()
	created := true
	lane, err := store.NewJSONStore(*statePath).UpdateTraceLane(*agent, func(lane *store.TraceLane) error {
		if len(lane.Stack) == 0 {
			return errors.New("no active trace; start one first")
		}
		if strings.TrimSpace(*key) != "" && lane.Stack[len(lane.Stack)-1].Key == strings.TrimSpace(*key) {
			created = false
			return nil
		}
		depth := len(lane.Stack)
		lane.Stack = append(lane.Stack, store.TraceItem{Title: title, Key: strings.TrimSpace(*key), StartedAt: now})
		lane.Events = append(lane.Events, store.TraceEvent{At: now, Type: "into", Title: title, Key: strings.TrimSpace(*key), Depth: depth})
		lane.UpdatedAt = now
		return nil
	})
	if err != nil {
		return err
	}
	return c.printTraceResult("into", lane, created, *jsonOutput)
}

func (c cli) tracePop(args []string, action string) error {
	fs := c.flagSet("trace " + action)
	statePath := fs.String("state", defaultStatePath(), "state file path")
	agent := fs.String("agent", defaultTraceAgent(), "trace agent lane")
	jsonOutput := fs.Bool("json", false, "print structured JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	note := strings.TrimSpace(strings.Join(fs.Args(), " "))
	now := c.now().UTC()
	lane, err := store.NewJSONStore(*statePath).UpdateTraceLane(*agent, func(lane *store.TraceLane) error {
		if len(lane.Stack) == 0 {
			return errors.New("no active trace")
		}
		depth := len(lane.Stack) - 1
		item := lane.Stack[depth]
		lane.Stack = lane.Stack[:depth]
		lane.Events = append(lane.Events, store.TraceEvent{At: now, Type: action, Title: item.Title, Message: note, Key: item.Key, Depth: depth})
		lane.UpdatedAt = now
		return nil
	})
	if err != nil {
		return err
	}
	return c.printTraceResult(action, lane, true, *jsonOutput)
}

func (c cli) traceLog(args []string) error {
	fs := c.flagSet("trace log")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	agent := fs.String("agent", defaultTraceAgent(), "trace agent lane")
	jsonOutput := fs.Bool("json", false, "print structured JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	message := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if message == "" {
		return errors.New("trace log requires <message>")
	}
	now := c.now().UTC()
	lane, err := store.NewJSONStore(*statePath).UpdateTraceLane(*agent, func(lane *store.TraceLane) error {
		if len(lane.Stack) == 0 {
			return errors.New("no active trace")
		}
		lane.Events = append(lane.Events, store.TraceEvent{At: now, Type: "log", Message: message, Depth: len(lane.Stack) - 1})
		lane.UpdatedAt = now
		return nil
	})
	if err != nil {
		return err
	}
	return c.printTraceResult("log", lane, true, *jsonOutput)
}

func (c cli) traceStatus(args []string) error {
	fs := c.flagSet("trace status")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	agent := fs.String("agent", defaultTraceAgent(), "trace agent lane")
	all := fs.Bool("all", false, "show all lanes")
	jsonOutput := fs.Bool("json", false, "print structured JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *all {
		lanes, err := store.NewJSONStore(*statePath).ListTraceLanes()
		if err != nil {
			return err
		}
		if *jsonOutput {
			data, err := json.Marshal(map[string]any{"status": "ok", "action": "status", "lanes": lanes})
			if err != nil {
				return err
			}
			fmt.Fprintln(c.out, string(data))
			return nil
		}
		fmt.Fprintf(c.out, "trace_lanes=%d state=%s\n", len(lanes), *statePath)
		for _, lane := range lanes {
			fmt.Fprintf(c.out, "agent=%s depth=%d current=%s\n", lane.Agent, len(lane.Stack), traceCurrent(lane))
		}
		return nil
	}
	lane, err := store.NewJSONStore(*statePath).GetTraceLane(strings.TrimSpace(*agent))
	if errors.Is(err, store.ErrTraceNotFound) {
		lane = store.TraceLane{Agent: strings.TrimSpace(*agent)}
	} else if err != nil {
		return err
	}
	return c.printTraceResult("status", lane, false, *jsonOutput)
}

func (c cli) traceMerge(args []string) error {
	fs := c.flagSet("trace merge")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	jsonOutput := fs.Bool("json", false, "print structured JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	lanes, err := store.NewJSONStore(*statePath).ListTraceLanes()
	if err != nil {
		return err
	}
	active := make([]store.TraceLane, 0, len(lanes))
	for _, lane := range lanes {
		if len(lane.Stack) > 0 {
			active = append(active, lane)
		}
	}
	if len(active) == 0 {
		return errors.New("no active trace lanes to merge")
	}
	if *jsonOutput {
		data, err := json.Marshal(map[string]any{"status": "ok", "action": "merge", "lanes": active})
		if err != nil {
			return err
		}
		fmt.Fprintln(c.out, string(data))
		return nil
	}
	fmt.Fprintf(c.out, "trace_merge lanes=%d\n", len(active))
	for _, lane := range active {
		titles := make([]string, 0, len(lane.Stack))
		for _, item := range lane.Stack {
			titles = append(titles, item.Title)
		}
		fmt.Fprintf(c.out, "agent=%s path=%s\n", lane.Agent, strings.Join(titles, " -> "))
	}
	return nil
}

func (c cli) printTraceResult(action string, lane store.TraceLane, created bool, jsonOutput bool) error {
	payload := traceOutput{
		Status:     "ok",
		Action:     action,
		Agent:      lane.Agent,
		Created:    created,
		StackDepth: len(lane.Stack),
		Current:    traceCurrent(lane),
		Stack:      lane.Stack,
	}
	if jsonOutput {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		fmt.Fprintln(c.out, string(data))
		return nil
	}
	fmt.Fprintf(c.out, "trace action=%s agent=%s depth=%d current=%s created=%t\n", action, lane.Agent, len(lane.Stack), emptyDash(payload.Current), created)
	for i, item := range lane.Stack {
		fmt.Fprintf(c.out, "%d\t%s\t%s\n", i, emptyDash(item.Key), item.Title)
	}
	return nil
}

func defaultTraceAgent() string {
	if value := strings.TrimSpace(os.Getenv("CODEX_SWARM_TRACE_AGENT")); value != "" {
		return value
	}
	if value := strings.TrimSpace(os.Getenv("DETOUR_AGENT")); value != "" {
		return value
	}
	return "default"
}

func traceCurrent(lane store.TraceLane) string {
	if len(lane.Stack) == 0 {
		return ""
	}
	return lane.Stack[len(lane.Stack)-1].Title
}
