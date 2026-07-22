package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/daemon"
	"github.com/MTG-Thomas/codex-swarm/internal/protocol"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

func (c cli) codexTaskCollect(args []string) error {
	if len(args) == 0 {
		return errors.New("tasks collect requires <page|finish>")
	}
	switch args[0] {
	case "page":
		return c.codexTaskCollectPage(args[1:])
	case "finish":
		return c.codexTaskCollectFinish(args[1:])
	case "status":
		return c.codexTaskCollectStatus(args[1:])
	default:
		return fmt.Errorf("unknown tasks collect command %q", args[0])
	}
}

type codexTaskCollectionPageInput struct {
	Tasks []protocol.CodexTaskHostObservation `json:"tasks"`
}

func (c cli) codexTaskCollectPage(args []string) error {
	fs := c.flagSet("tasks collect page")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	daemonURL := fs.String("daemon", "", "daemon base URL")
	hostID := fs.String("host", os.Getenv("CODEX_HOST_ID"), "stable Codex host ID (defaults to CODEX_HOST_ID)")
	observationID := fs.String("observation", "", "stable host heartbeat observation ID")
	pageNumber := fs.Int("page", 0, "one-based page number")
	cursor := fs.String("cursor", "", "opaque cursor used to request this page")
	nextCursor := fs.String("next-cursor", "", "opaque cursor returned for the next page")
	file := fs.String("file", "-", "metadata-only page JSON file, or - for stdin")
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
	var input codexTaskCollectionPageInput
	decoder := json.NewDecoder(io.LimitReader(reader, 4<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return fmt.Errorf("decode Codex task collection page: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("Codex task collection page must contain one JSON document")
	}
	request := protocol.CodexTaskCollectionPageRequest{
		HostID: strings.TrimSpace(*hostID), ObservationID: strings.TrimSpace(*observationID),
		ObservedAt: c.now().UTC(), Page: *pageNumber, Cursor: *cursor,
		NextCursor: *nextCursor, Tasks: input.Tasks,
	}
	result, err := c.addCodexTaskCollectionPage(*statePath, *daemonURL, request)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSONLine(c.out, result)
	}
	fmt.Fprintf(c.out, "task collection page host=%s observation=%s page=%d tasks=%d observed_at=%s replayed=%t\n", result.HostID, result.ObservationID, result.Page, result.Tasks, result.ObservedAt.Format(time.RFC3339Nano), result.Replayed)
	return nil
}

func (c cli) addCodexTaskCollectionPage(statePath, daemonURL string, request protocol.CodexTaskCollectionPageRequest) (protocol.CodexTaskCollectionPageResponse, error) {
	if baseURL := configuredDaemonURL(daemonURL); baseURL != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		result, err := (daemon.Client{BaseURL: baseURL}).AddCodexTaskCollectionPage(ctx, request)
		if err != nil {
			return protocol.CodexTaskCollectionPageResponse{}, fmt.Errorf("daemon Codex task collection page: %w", err)
		}
		return result, nil
	}
	return store.NewJSONStore(statePath).AddCodexTaskCollectionPage(request)
}

func (c cli) codexTaskCollectFinish(args []string) error {
	fs := c.flagSet("tasks collect finish")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	daemonURL := fs.String("daemon", "", "daemon base URL")
	hostID := fs.String("host", os.Getenv("CODEX_HOST_ID"), "stable Codex host ID (defaults to CODEX_HOST_ID)")
	observationID := fs.String("observation", "", "stable host heartbeat observation ID")
	coverage := fs.String("coverage", store.CodexTaskCoverageWindow, "snapshot coverage: window or complete")
	jsonOutput := fs.Bool("json", false, "print structured JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	request := protocol.CodexTaskCollectionFinishRequest{
		HostID: strings.TrimSpace(*hostID), ObservationID: strings.TrimSpace(*observationID), Coverage: strings.TrimSpace(*coverage),
	}
	result, err := c.finishCodexTaskCollection(*statePath, *daemonURL, request)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSONLine(c.out, result)
	}
	fmt.Fprintf(c.out, "task collection finished host=%s observation=%s pages=%d coverage=%s observed=%d inserted=%d updated=%d missing_marked=%d replayed=%t\n", result.Ingest.HostID, result.ObservationID, result.Pages, result.Ingest.Coverage, result.Ingest.Observed, result.Ingest.Inserted, result.Ingest.Updated, result.Ingest.MissingMarked, result.Replayed)
	return nil
}

func (c cli) finishCodexTaskCollection(statePath, daemonURL string, request protocol.CodexTaskCollectionFinishRequest) (protocol.CodexTaskCollectionFinishResponse, error) {
	if baseURL := configuredDaemonURL(daemonURL); baseURL != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		result, err := (daemon.Client{BaseURL: baseURL}).FinishCodexTaskCollection(ctx, request)
		if err != nil {
			return protocol.CodexTaskCollectionFinishResponse{}, fmt.Errorf("daemon Codex task collection finish: %w", err)
		}
		return result, nil
	}
	return store.NewJSONStore(statePath).FinishCodexTaskCollection(request)
}

func (c cli) codexTaskCollectStatus(args []string) error {
	fs := c.flagSet("tasks collect status")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	daemonURL := fs.String("daemon", "", "daemon base URL")
	hostID := fs.String("host", os.Getenv("CODEX_HOST_ID"), "stable Codex host ID (defaults to CODEX_HOST_ID)")
	observationID := fs.String("observation", "", "stable host heartbeat observation ID")
	jsonOutput := fs.Bool("json", false, "print structured JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	status, err := c.readCodexTaskCollectionStatus(*statePath, *daemonURL, strings.TrimSpace(*hostID), strings.TrimSpace(*observationID))
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSONLine(c.out, status)
	}
	state := "open"
	if status.FinalizedAt != nil {
		state = "finalized"
	}
	fmt.Fprintf(c.out, "task collection status host=%s observation=%s state=%s pages=%d tasks=%d next_page=%d next_cursor=%q coverage=%s\n", status.HostID, status.ObservationID, state, status.Pages, status.Tasks, status.NextPage, status.NextCursor, emptyDash(status.Coverage))
	return nil
}

func (c cli) readCodexTaskCollectionStatus(statePath, daemonURL, hostID, observationID string) (protocol.CodexTaskCollectionStatusResponse, error) {
	if baseURL := configuredDaemonURL(daemonURL); baseURL != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		status, err := (daemon.Client{BaseURL: baseURL}).CodexTaskCollectionStatus(ctx, hostID, observationID)
		if err != nil {
			return protocol.CodexTaskCollectionStatusResponse{}, fmt.Errorf("daemon Codex task collection status: %w", err)
		}
		return status, nil
	}
	return store.NewJSONStore(statePath).GetCodexTaskCollectionStatus(hostID, observationID)
}

func writeJSONLine(w io.Writer, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(data))
	return err
}
