package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/MTG-Thomas/codex-swarm/internal/operation"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

func (c cli) operation(args []string) error {
	if len(args) == 0 {
		return errors.New("operation requires <list|show>")
	}
	switch args[0] {
	case "list":
		return c.operationList(args[1:])
	case "show":
		return c.operationShow(args[1:])
	default:
		return fmt.Errorf("unknown operation command %q", args[0])
	}
}

func (c cli) operationList(args []string) error {
	fs := c.flagSet("operation list")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	key := fs.String("key", "", "filter by canonical operation key")
	issue := fs.String("issue", "", "filter by GitHub issue reference")
	workerID := fs.String("worker", "", "filter by a worker's derived operation")
	jsonOutput := fs.Bool("json", false, "print structured JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("operation list does not accept positional arguments")
	}
	view, err := readOperationView(*statePath)
	if err != nil {
		return err
	}
	filterKey, workerFilter, err := operationFilterKey(view, *key, *issue, *workerID)
	if err != nil {
		return err
	}
	view = filterOperationView(view, filterKey, workerFilter)
	return c.printOperationView(view, *jsonOutput)
}

func (c cli) operationShow(args []string) error {
	if len(args) == 0 {
		return errors.New("operation show requires exactly one operation key")
	}
	keyValue := args[0]
	fs := c.flagSet("operation show")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	jsonOutput := fs.Bool("json", false, "print structured JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("operation show requires exactly one operation key")
	}
	key, err := operation.NormalizeKey(keyValue)
	if err != nil {
		return err
	}
	view, err := readOperationView(*statePath)
	if err != nil {
		return err
	}
	view = filterOperationView(view, key, "")
	if len(view.Operations) == 0 {
		return fmt.Errorf("operation not found: %s", key)
	}
	return c.printOperationView(view, *jsonOutput)
}

func readOperationView(statePath string) (operation.View, error) {
	st := store.NewJSONStore(statePath)
	snapshot, err := st.ReadCoordinationSnapshot()
	if err != nil {
		return operation.View{}, fmt.Errorf("read operation snapshot: %w", err)
	}
	return operation.Derive(operation.Input{
		Workers: snapshot.Workers, Claims: snapshot.Claims, Messages: snapshot.Messages,
		GateEvidence: snapshot.GateEvidence, CodexTasks: snapshot.CodexTasks,
	}), nil
}

func operationFilterKey(view operation.View, keyValue, issueValue, workerID string) (string, string, error) {
	values := 0
	for _, value := range []string{keyValue, issueValue, workerID} {
		if strings.TrimSpace(value) != "" {
			values++
		}
	}
	if values > 1 {
		return "", "", errors.New("operation list accepts only one of --key, --issue, or --worker")
	}
	if strings.TrimSpace(keyValue) != "" {
		key, err := operation.NormalizeKey(keyValue)
		return key, "", err
	}
	if strings.TrimSpace(issueValue) != "" {
		_, key, err := operation.NormalizeIssueRef(issueValue)
		return key, "", err
	}
	if strings.TrimSpace(workerID) == "" {
		return "", "", nil
	}
	workerID = strings.TrimSpace(workerID)
	for _, resolution := range view.Resolutions {
		if resolution.WorkerID == workerID {
			return resolution.Key, workerID, nil
		}
	}
	return "", "", fmt.Errorf("worker not found: %s", workerID)
}

func filterOperationView(view operation.View, key, workerID string) operation.View {
	if key == "" && workerID == "" {
		return view
	}
	filtered := operation.View{}
	for _, group := range view.Operations {
		if group.Key == key {
			filtered.Operations = append(filtered.Operations, group)
		}
	}
	for _, resolution := range view.Resolutions {
		if (workerID != "" && resolution.WorkerID == workerID) || (workerID == "" && resolution.Key == key) {
			filtered.Resolutions = append(filtered.Resolutions, resolution)
		}
	}
	for _, record := range view.Unscoped {
		if workerID != "" && record.WorkerID == workerID {
			filtered.Unscoped = append(filtered.Unscoped, record)
		}
	}
	return filtered
}

func (c cli) printOperationView(view operation.View, jsonOutput bool) error {
	if jsonOutput {
		data, err := json.MarshalIndent(view, "", "  ")
		if err != nil {
			return fmt.Errorf("encode operation view: %w", err)
		}
		fmt.Fprintln(c.out, string(data))
		return nil
	}
	fmt.Fprintf(c.out, "operations=%d resolutions=%d unscoped=%d\n", len(view.Operations), len(view.Resolutions), len(view.Unscoped))
	for _, group := range view.Operations {
		fmt.Fprintf(c.out, "%s\tkind=%s\tworkers=%d\tclaims=%d\tmessages=%d\tgates=%d\tprs=%d\ttasks=%d\n",
			group.Key, group.Kind, len(group.Workers), len(group.Claims), len(group.Messages), len(group.GateEvidence), len(group.PullRequests), len(group.CodexTasks))
	}
	states := map[string]int{}
	for _, record := range view.Unscoped {
		states[record.State]++
	}
	keys := make([]string, 0, len(states))
	for state := range states {
		keys = append(keys, state)
	}
	sort.Strings(keys)
	for _, state := range keys {
		fmt.Fprintf(c.out, "unscoped_state=%s count=%d\n", state, states[state])
	}
	return nil
}
