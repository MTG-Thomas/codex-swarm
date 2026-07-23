package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/appserver"
	"github.com/MTG-Thomas/codex-swarm/internal/coordination"
	"github.com/MTG-Thomas/codex-swarm/internal/daemon"
	"github.com/MTG-Thomas/codex-swarm/internal/protocol"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

func (c cli) inbox(args []string) error {
	fs := c.flagSet("inbox")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	daemonURL := fs.String("daemon", "", "daemon base URL")
	queuedOnly := fs.Bool("queued", false, "show only queued deliveries")
	jsonOutput := fs.Bool("json", false, "emit machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return errors.New("inbox requires <worker>")
	}
	workerID := rest[0]
	messages, err := loadInbox(*statePath, *daemonURL, workerID)
	if err != nil {
		return err
	}
	visible := messages[:0]
	for _, item := range messages {
		if *queuedOnly && item.Delivery.State != store.DeliveryQueued {
			continue
		}
		visible = append(visible, item)
	}
	if *jsonOutput {
		return json.NewEncoder(c.out).Encode(protocol.InboxResponse{Messages: visible})
	}
	fmt.Fprintf(c.out, "worker=%s messages=%d\n", workerID, len(visible))
	for _, item := range visible {
		fmt.Fprintf(c.out, "%s\t%s\t%s\tfrom=%s\trequest=%s\tdelivery=%s\tcreated=%s\tupdated=%s\t%s\n",
			item.Message.ID, item.Message.Kind, item.Delivery.State, item.Message.From, item.Message.RequestID, item.Delivery.ID,
			item.Delivery.CreatedAt.Format(time.RFC3339Nano), item.Delivery.UpdatedAt.Format(time.RFC3339Nano), short(item.Message.Body, 120))
		for _, event := range item.Delivery.History {
			fmt.Fprintf(c.out, "  transition=%d\tstate=%s\tat=%s\terror=%s\n", event.Sequence, event.State, event.CreatedAt.Format(time.RFC3339Nano), emptyDash(event.LastError))
		}
	}
	return nil
}

func loadInbox(statePath, daemonURL, workerID string) ([]store.DeliveredMessage, error) {
	if baseURL := configuredDaemonURL(daemonURL); baseURL != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		response, err := (daemon.Client{BaseURL: baseURL}).Inbox(ctx, workerID)
		return response.Messages, err
	}
	return store.NewJSONStore(statePath).ListMessages(workerID)
}

func (c cli) touch(args []string) error {
	fs := c.flagSet("touch")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	daemonURL := fs.String("daemon", "", "daemon base URL")
	workerID := fs.String("worker", "", "worker id")
	repo := fs.String("repo", ".", "repository root")
	path := fs.String("path", "", "touched file path")
	operation := fs.String("operation", "write", "touch operation: read or write")
	lineStart := fs.Int("line-start", 0, "first touched line, zero for whole file")
	lineEnd := fs.Int("line-end", 0, "last touched line, zero for whole file")
	intent := fs.String("intent", "", "short edit intent")
	requestIDFlag := fs.String("request-id", "", "idempotency key")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*workerID) == "" || strings.TrimSpace(*path) == "" {
		return errors.New("touch requires --worker and --path")
	}
	repoRoot, err := filepath.Abs(*repo)
	if err != nil {
		return fmt.Errorf("resolve touch repo: %w", err)
	}
	filePath := *path
	if !filepath.IsAbs(filePath) {
		filePath = filepath.Join(repoRoot, filePath)
	}
	filePath = filepath.Clean(filePath)
	requestID, err := c.requestID(*requestIDFlag, c.now().UTC())
	if err != nil {
		return err
	}
	req := protocol.TouchRequest{
		RequestID: requestID, WorkerID: *workerID, Repo: repoRoot, Path: filePath, Operation: *operation,
		LineStart: *lineStart, LineEnd: *lineEnd, Intent: *intent,
	}
	var response protocol.TouchResponse
	if baseURL := configuredDaemonURL(*daemonURL); baseURL != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		response, err = (daemon.Client{BaseURL: baseURL}).Touch(ctx, req)
	} else {
		result, touchErr := (coordination.Service{Store: store.NewJSONStore(*statePath), Now: c.now}).Touch(context.Background(), coordination.TouchRequest{
			RequestID: req.RequestID, WorkerID: req.WorkerID, Repo: req.Repo, Path: req.Path, Operation: req.Operation,
			LineStart: req.LineStart, LineEnd: req.LineEnd, Intent: req.Intent,
		})
		err = touchErr
		response.Touch, response.Conflicts = result.Touch, result.Conflicts
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "touch=%s worker=%s operation=%s path=%s conflicts=%d warning_only=true\n", response.Touch.ID, response.Touch.WorkerID, response.Touch.Operation, response.Touch.Path, len(response.Conflicts))
	for _, conflict := range response.Conflicts {
		fmt.Fprintf(c.out, "WARNING peer=%s path=%s intent=%q\n", conflict.PeerTouch.WorkerID, conflict.Touch.Path, conflict.PeerTouch.Intent)
	}
	return nil
}

func (c cli) forwardCompletion(statePath, daemonURL, requestID, workerID, report string) (protocol.CompletionResponse, error) {
	if baseURL := configuredDaemonURL(daemonURL); baseURL != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		response, err := (daemon.Client{BaseURL: baseURL}).Completion(ctx, protocol.CompletionRequest{RequestID: requestID, WorkerID: workerID, Report: report})
		if err != nil {
			return protocol.CompletionResponse{}, err
		}
		return response, nil
	}
	result, forwarded, err := (coordination.Service{Store: store.NewJSONStore(statePath), Now: c.now}).ForwardCompletion(context.Background(), requestID, workerID, report)
	if err != nil {
		return protocol.CompletionResponse{}, err
	}
	response := protocol.CompletionResponse{Forwarded: forwarded}
	if forwarded {
		message := protocol.MessageResponse{
			Message: result.Message, Deliveries: result.Deliveries, NativeSteering: result.NativeSteering,
			NativeFollowup: result.NativeFollowup, Replayed: result.Replayed,
		}
		for i := range message.NativeSteering {
			message.NativeSteering[i].StatePath = statePath
		}
		for i := range message.NativeFollowup {
			message.NativeFollowup[i].StatePath = statePath
		}
		response.Message = &message
	}
	return response, nil
}

func (c cli) printCompletionResponse(workerID string, response protocol.CompletionResponse) {
	if !response.Forwarded || response.Message == nil {
		return
	}
	fmt.Fprintf(c.out, "completion forwarded worker=%s message=%s deliveries=%d\n", workerID, response.Message.Message.ID, len(response.Message.Deliveries))
	c.printNativeCallbacks(*response.Message)
}

func (c cli) printNativeCallbacks(response protocol.MessageResponse) {
	for _, request := range response.NativeSteering {
		fmt.Fprintf(c.out, "native_steering_required delivery=%s recipient=%s host=%s thread=%s turn=%s\n",
			request.DeliveryID, request.RecipientID, emptyDash(request.HostID), request.ThreadID, request.TurnID)
		prompt, _ := json.Marshal(request.Prompt)
		fmt.Fprintf(c.out, "  prompt=%s\n", prompt)
		fmt.Fprintf(c.out, "  after_success=cs message confirm-steered --state %q --worker %s --thread %s --turn %s %s\n",
			request.StatePath, request.RecipientID, request.ThreadID, request.TurnID, request.DeliveryID)
		fmt.Fprintf(c.out, "  after_failure=cs message steering-failed --state %q --worker %s --thread %s --turn %s --error <error> %s\n",
			request.StatePath, request.RecipientID, request.ThreadID, request.TurnID, request.DeliveryID)
	}
	for _, request := range response.NativeFollowup {
		fmt.Fprintf(c.out, "native_followup_required delivery=%s recipient=%s host=%s thread=%s\n",
			request.DeliveryID, request.RecipientID, emptyDash(request.HostID), request.ThreadID)
		prompt, _ := json.Marshal(request.Prompt)
		fmt.Fprintf(c.out, "  prompt=%s\n", prompt)
		fmt.Fprintf(c.out, "  after_success=cs message confirm-followup --state %q --worker %s --thread %s %s\n",
			request.StatePath, request.RecipientID, request.ThreadID, request.DeliveryID)
		fmt.Fprintf(c.out, "  after_failure=cs message followup-failed --state %q --worker %s --thread %s --error <error> %s\n",
			request.StatePath, request.RecipientID, request.ThreadID, request.DeliveryID)
	}
}

func queuedMessagePrompt(items []store.DeliveredMessage) string {
	if len(items) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("SWARM_QUEUED_MESSAGES\n")
	for _, item := range items {
		fmt.Fprintf(&builder, "[%s from=%s message_id=%s]\n%s\n", strings.ToUpper(string(item.Message.Kind)), item.Message.From, item.Message.ID, item.Message.Body)
	}
	return strings.TrimSpace(builder.String())
}

func markMessagesDelivered(st *store.JSONStore, items []store.DeliveredMessage, at time.Time) error {
	for _, item := range items {
		if err := st.UpdateDelivery(item.Delivery.ID, store.DeliveryDelivered, "", at); err != nil {
			return err
		}
	}
	return nil
}

func (c cli) recordAppserverFileChanges(statePath string, worker store.Worker, changes []appserver.FileChange) error {
	st := store.NewJSONStore(statePath)
	for i, change := range changes {
		path := strings.TrimSpace(change.Path)
		if path == "" {
			continue
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(workerExecutionRoot(worker), path)
		}
		requestID := fmt.Sprintf("appserver-%s-%s-%03d", worker.ID, worker.TurnID, i+1)
		_, err := (coordination.Service{Store: st, Now: c.now}).Touch(context.Background(), coordination.TouchRequest{
			RequestID: requestID, WorkerID: worker.ID, Repo: worker.ProjectRoot, Path: filepath.Clean(path), Operation: "write", Intent: "Codex app-server file change",
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (c cli) workerSteeringPolicy(statePath, workerID string, excludedDeliveryIDs ...string) appserver.SteeringPolicy {
	st := store.NewJSONStore(statePath)
	attempted := map[string]struct{}{}
	for _, id := range excludedDeliveryIDs {
		attempted[id] = struct{}{}
	}
	return appserver.SteeringPolicy{
		PollInterval: 500 * time.Millisecond,
		Source: func(context.Context) ([]appserver.SteerDelivery, error) {
			queued, err := st.ListQueuedMessages(workerID)
			if err != nil {
				return nil, err
			}
			deliveries := make([]appserver.SteerDelivery, 0, len(queued))
			for _, item := range queued {
				if _, ok := attempted[item.Delivery.ID]; ok {
					continue
				}
				deliveries = append(deliveries, appserver.SteerDelivery{ID: item.Delivery.ID, Text: queuedMessagePrompt([]store.DeliveredMessage{item})})
			}
			return deliveries, nil
		},
		Acknowledge: func(id string, deliveryErr error) {
			attempted[id] = struct{}{}
			if deliveryErr != nil {
				_ = st.UpdateDelivery(id, store.DeliveryQueued, deliveryErr.Error(), c.now().UTC())
				return
			}
			_ = st.UpdateDelivery(id, store.DeliverySteered, "", c.now().UTC())
		},
	}
}

func queuedDeliveryIDs(items []store.DeliveredMessage) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.Delivery.ID)
	}
	return ids
}
