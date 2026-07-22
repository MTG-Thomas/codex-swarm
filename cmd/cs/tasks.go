package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/daemon"
	"github.com/MTG-Thomas/codex-swarm/internal/protocol"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

func (c cli) codexTasks(args []string) error {
	if len(args) == 0 {
		return errors.New("tasks requires <list|ingest|status>")
	}
	switch args[0] {
	case "list":
		return c.codexTaskList(args[1:])
	case "ingest", "sync":
		return c.codexTaskIngest(args[1:])
	case "collect":
		return c.codexTaskCollect(args[1:])
	case "status":
		return c.codexTaskStatus(args[1:])
	default:
		return fmt.Errorf("unknown tasks command %q", args[0])
	}
}

func (c cli) codexTaskList(args []string) error {
	fs := c.flagSet("tasks list")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	daemonURL := fs.String("daemon", "", "daemon base URL")
	host := fs.String("host", "", "filter by Codex host ID")
	project := fs.String("project", "", "filter by project")
	status := fs.String("status", "", "filter by observed Codex status")
	source := fs.String("source", "", "filter by discovery source")
	tier := fs.String("tier", "", "filter by coordinator tier P0-P3")
	unreadValue := fs.String("unread", "any", "filter unread state: any, true, or false")
	includeTombstoned := fs.Bool("include-tombstoned", false, "include explicitly tombstoned tasks")
	staleFor := fs.Duration("stale-for", 0, "only tasks not observed for this duration")
	limit := fs.Int("limit", 50, "maximum rows per page (1-500)")
	cursor := fs.String("cursor", "", "opaque next-page cursor")
	jsonOutput := fs.Bool("json", false, "print structured JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *staleFor < 0 {
		return errors.New("tasks list --stale-for must not be negative")
	}
	filter := store.CodexTaskListFilter{
		HostID: strings.TrimSpace(*host), Project: strings.TrimSpace(*project), Status: strings.TrimSpace(*status),
		Source: strings.TrimSpace(*source), Tier: strings.ToUpper(strings.TrimSpace(*tier)), IncludeTombstoned: *includeTombstoned,
		Limit: *limit, Cursor: strings.TrimSpace(*cursor),
	}
	unread, err := parseOptionalBool(*unreadValue)
	if err != nil {
		return err
	}
	filter.Unread = unread
	if *staleFor > 0 {
		at := c.now().UTC().Add(-*staleFor)
		filter.StaleBefore = &at
	}
	page, err := c.readCodexTaskPage(*statePath, *daemonURL, filter)
	if err != nil {
		return err
	}
	if *jsonOutput {
		data, err := json.MarshalIndent(page, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(c.out, string(data))
		return nil
	}
	fmt.Fprintf(c.out, "tasks shown=%d total=%d next_cursor=%s\n", len(page.Tasks), page.Total, emptyDash(page.NextCursor))
	for _, task := range page.Tasks {
		var taskFlags []string
		if task.Unread {
			taskFlags = append(taskFlags, "unread")
		}
		if task.MissingSince != nil {
			taskFlags = append(taskFlags, "missing")
		}
		if task.TombstonedAt != nil {
			taskFlags = append(taskFlags, "tombstoned")
		}
		if task.Coordinator {
			taskFlags = append(taskFlags, "coordinator")
		}
		flags := emptyDash(strings.Join(taskFlags, ","))
		fmt.Fprintf(c.out, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", task.HostID, task.ThreadID, emptyDash(task.Status), emptyDash(task.Tier), flags, task.LastSeenAt.Format(time.RFC3339), short(task.Title, 72))
	}
	return nil
}

func (c cli) readCodexTaskPage(statePath, daemonURL string, filter store.CodexTaskListFilter) (protocol.CodexTaskListResponse, error) {
	if baseURL := configuredDaemonURL(daemonURL); baseURL != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		page, err := (daemon.Client{BaseURL: baseURL}).CodexTasks(ctx, filter)
		if err != nil {
			return protocol.CodexTaskListResponse{}, fmt.Errorf("daemon Codex tasks: %w", err)
		}
		return page, nil
	}
	return store.NewJSONStore(statePath).ListCodexTasks(filter)
}

func (c cli) codexTaskIngest(args []string) error {
	fs := c.flagSet("tasks ingest")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	daemonURL := fs.String("daemon", "", "daemon base URL")
	file := fs.String("file", "-", "snapshot JSON file, or - for stdin")
	requestIDValue := fs.String("request-id", "", "idempotency key override")
	jsonOutput := fs.Bool("json", false, "print structured JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	reader, closeReader, err := codexTaskInput(*file)
	if err != nil {
		return err
	}
	if closeReader != nil {
		defer closeReader()
	}
	var request protocol.CodexTaskIngestRequest
	decoder := json.NewDecoder(io.LimitReader(reader, 4<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return fmt.Errorf("decode Codex task snapshot: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("Codex task snapshot must contain one JSON document")
	}
	requestID, err := c.requestID(taskFirstNonEmpty(strings.TrimSpace(*requestIDValue), request.RequestID), c.now().UTC())
	if err != nil {
		return err
	}
	request.RequestID = requestID
	if request.ObservedAt.IsZero() {
		request.ObservedAt = c.now().UTC()
	}
	result, err := c.ingestCodexTasks(*statePath, *daemonURL, request)
	if err != nil {
		return err
	}
	return c.printCodexTaskIngestResult(result, *jsonOutput)
}

func (c cli) ingestCodexTasks(statePath, daemonURL string, request protocol.CodexTaskIngestRequest) (protocol.CodexTaskIngestResponse, error) {
	if baseURL := configuredDaemonURL(daemonURL); baseURL != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		result, err := (daemon.Client{BaseURL: baseURL}).IngestCodexTasks(ctx, request)
		if err != nil {
			return protocol.CodexTaskIngestResponse{}, fmt.Errorf("daemon Codex task ingest: %w", err)
		}
		return result, nil
	}
	return store.NewJSONStore(statePath).IngestCodexTasks(request)
}

func (c cli) printCodexTaskIngestResult(result protocol.CodexTaskIngestResponse, jsonOutput bool) error {
	if jsonOutput {
		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(c.out, string(data))
		return nil
	}
	fmt.Fprintf(c.out, "tasks ingested request=%s host=%s source=%s coverage=%s observed=%d inserted=%d updated=%d stale_skipped=%d missing_marked=%d replayed=%t\n", result.RequestID, result.HostID, result.Source, result.Coverage, result.Observed, result.Inserted, result.Updated, result.StaleSkipped, result.MissingMarked, result.Replayed)
	return nil
}

func (c cli) codexTaskStatus(args []string) error {
	fs := c.flagSet("tasks status")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	daemonURL := fs.String("daemon", "", "daemon base URL")
	staleFor := fs.Duration("stale-for", 24*time.Hour, "count tasks not observed for this duration")
	jsonOutput := fs.Bool("json", false, "print structured JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *staleFor < 0 {
		return errors.New("tasks status --stale-for must not be negative")
	}
	var staleBefore *time.Time
	if *staleFor > 0 {
		at := c.now().UTC().Add(-*staleFor)
		staleBefore = &at
	}
	var stats protocol.CodexTaskStatusResponse
	var err error
	if baseURL := configuredDaemonURL(*daemonURL); baseURL != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		stats, err = (daemon.Client{BaseURL: baseURL}).CodexTaskStatus(ctx, staleBefore)
	} else {
		stats, err = store.NewJSONStore(*statePath).CodexTaskStats(staleBefore)
	}
	if err != nil {
		return err
	}
	if *jsonOutput {
		data, err := json.MarshalIndent(stats, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(c.out, string(data))
		return nil
	}
	fmt.Fprintf(c.out, "tasks total=%d unread=%d stale=%d missing=%d tombstoned=%d\n", stats.Total, stats.Unread, stats.Stale, stats.Missing, stats.Tombstoned)
	printCountMap(c.out, "host", stats.ByHost)
	printCountMap(c.out, "status", stats.ByStatus)
	printCountMap(c.out, "tier", stats.ByTier)
	return nil
}

func codexTaskInput(path string) (io.Reader, func(), error) {
	if strings.TrimSpace(path) == "" || path == "-" {
		return os.Stdin, nil, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open Codex task snapshot %s: %w", path, err)
	}
	return file, func() { _ = file.Close() }, nil
}

func parseOptionalBool(value string) (*bool, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || value == "any" {
		return nil, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return nil, errors.New("tasks --unread must be any, true, or false")
	}
	return &parsed, nil
}

func printCountMap(out io.Writer, label string, values map[string]int) {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(out, "%s=%s count=%d\n", label, emptyDash(key), values[key])
	}
}

func taskFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
