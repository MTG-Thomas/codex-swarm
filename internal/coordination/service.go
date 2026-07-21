// Package coordination owns durable, warning-only communication between workers.
package coordination

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

const maxMessageBytes = 256 << 10

type messageStore interface {
	GetWorker(string) (store.Worker, error)
	ListWorkers() ([]store.Worker, error)
	CreateMessage(store.Message, []string) (store.Message, []store.Delivery, bool, error)
	UpdateDelivery(string, store.DeliveryState, string, time.Time) error
	RecordFileTouch(store.FileTouch) ([]store.TouchConflict, error)
}

type timelineStore interface {
	UpdateWorkersWithRequest(string, string, string, []string, func(map[string]*store.Worker) (store.WorkerMutationResult, error)) (store.WorkerMutationResult, bool, error)
}

// TurnSteerer injects a message into one known active turn.
type TurnSteerer interface {
	SteerTurn(context.Context, string, string, string, string) error
}

// Service routes the deliberately small DM/subtree/system-message vocabulary.
type Service struct {
	Store   messageStore
	Steerer TurnSteerer
	Now     func() time.Time
}

type SendRequest struct {
	RequestID string
	Kind      store.MessageKind
	From      string
	To        string
	Body      string
}

type SendResult struct {
	Message    store.Message
	Deliveries []store.Delivery
	Replayed   bool
}

type TouchRequest struct {
	RequestID string
	WorkerID  string
	Repo      string
	Path      string
	Operation string
	LineStart int
	LineEnd   int
	Intent    string
}

type TouchResult struct {
	Touch     store.FileTouch
	Conflicts []store.TouchConflict
	Warnings  []SendResult
}

func (s Service) Send(ctx context.Context, req SendRequest) (SendResult, error) {
	if s.Store == nil {
		return SendResult{}, errors.New("coordination store is required")
	}
	if req.Kind != store.MessageDirect && req.Kind != store.MessageSubtree {
		return SendResult{}, fmt.Errorf("unsupported user message kind %q", req.Kind)
	}
	if _, err := s.Store.GetWorker(req.From); err != nil {
		return SendResult{}, fmt.Errorf("message sender %s: %w", req.From, err)
	}
	recipients, err := s.recipients(req.Kind, req.To, req.From)
	if err != nil {
		return SendResult{}, err
	}
	return s.createAndDeliver(ctx, req.RequestID, req.Kind, req.From, req.Body, recipients)
}

// ForwardCompletion automatically reports a child terminal result to its parent.
func (s Service) ForwardCompletion(ctx context.Context, requestID, workerID, report string) (SendResult, bool, error) {
	worker, err := s.Store.GetWorker(workerID)
	if err != nil {
		return SendResult{}, false, err
	}
	if strings.TrimSpace(worker.ParentID) == "" {
		return SendResult{}, false, nil
	}
	body := fmt.Sprintf("child=%s status=%s report=%s", worker.ID, worker.Status, strings.TrimSpace(report))
	result, err := s.createAndDeliver(ctx, requestID, store.MessageCompletion, worker.ID, body, []string{worker.ParentID})
	return result, true, err
}

// Touch records file intent and emits bilateral warnings for overlapping writes.
// It never rejects the touch or blocks either worker.
func (s Service) Touch(ctx context.Context, req TouchRequest) (TouchResult, error) {
	if _, err := s.Store.GetWorker(req.WorkerID); err != nil {
		return TouchResult{}, fmt.Errorf("touch worker %s: %w", req.WorkerID, err)
	}
	now := s.now()
	touch := store.FileTouch{
		ID:        "t-" + req.RequestID,
		WorkerID:  req.WorkerID,
		Repo:      req.Repo,
		Path:      req.Path,
		Operation: req.Operation,
		LineStart: req.LineStart,
		LineEnd:   req.LineEnd,
		Intent:    req.Intent,
		CreatedAt: now,
	}
	conflicts, err := s.Store.RecordFileTouch(touch)
	if err != nil {
		return TouchResult{}, err
	}
	result := TouchResult{Touch: touch, Conflicts: conflicts}
	for i, conflict := range conflicts {
		body := conflictWarning(conflict)
		warning, err := s.createAndDeliver(ctx, fmt.Sprintf("%s-conflict-%03d", req.RequestID, i+1), store.MessageConflict, "system", body,
			[]string{conflict.Touch.WorkerID, conflict.PeerTouch.WorkerID})
		if err != nil {
			return TouchResult{}, fmt.Errorf("create conflict warning: %w", err)
		}
		result.Warnings = append(result.Warnings, warning)
	}
	return result, nil
}

func (s Service) recipients(kind store.MessageKind, target, sender string) ([]string, error) {
	if strings.TrimSpace(target) == "" {
		return nil, errors.New("message recipient is required")
	}
	if kind == store.MessageDirect {
		if _, err := s.Store.GetWorker(target); err != nil {
			return nil, fmt.Errorf("message recipient %s: %w", target, err)
		}
		return []string{target}, nil
	}
	workers, err := s.Store.ListWorkers()
	if err != nil {
		return nil, err
	}
	known := map[string]store.Worker{}
	for _, worker := range workers {
		known[worker.ID] = worker
	}
	if _, ok := known[target]; !ok {
		return nil, fmt.Errorf("message subtree root %s: %w", target, store.ErrWorkerNotFound)
	}
	selected := map[string]struct{}{target: {}}
	changed := true
	for changed {
		changed = false
		for _, worker := range workers {
			if _, ok := selected[worker.ID]; ok {
				continue
			}
			if _, ok := selected[worker.ParentID]; ok {
				selected[worker.ID] = struct{}{}
				changed = true
			}
		}
	}
	recipients := make([]string, 0, len(selected))
	for id := range selected {
		if id != sender {
			recipients = append(recipients, id)
		}
	}
	if len(recipients) == 0 {
		return nil, errors.New("message subtree has no recipients after excluding sender")
	}
	return recipients, nil
}

func (s Service) createAndDeliver(ctx context.Context, requestID string, kind store.MessageKind, from, body string, recipients []string) (SendResult, error) {
	if strings.TrimSpace(requestID) == "" {
		return SendResult{}, errors.New("message request id is required")
	}
	if len(body) > maxMessageBytes {
		return SendResult{}, fmt.Errorf("message exceeds %d-byte limit", maxMessageBytes)
	}
	id, err := newID("m", s.now())
	if err != nil {
		return SendResult{}, err
	}
	message := store.Message{ID: id, RequestID: requestID, Kind: kind, From: from, Body: strings.TrimSpace(body), CreatedAt: s.now()}
	saved, deliveries, replayed, err := s.Store.CreateMessage(message, recipients)
	if err != nil {
		return SendResult{}, err
	}
	result := SendResult{Message: saved, Deliveries: deliveries, Replayed: replayed}
	if !replayed {
		if err := s.recordTimeline(saved, recipients); err != nil {
			return SendResult{}, err
		}
	}
	if replayed || s.Steerer == nil {
		return result, nil
	}
	for i := range result.Deliveries {
		delivery := &result.Deliveries[i]
		worker, err := s.Store.GetWorker(delivery.RecipientID)
		if err != nil {
			return SendResult{}, err
		}
		if worker.Engine != "appserver" || worker.Status != store.WorkerRunning || worker.ThreadID == "" || worker.TurnID == "" {
			continue
		}
		root := worker.Worktree
		if info, statErr := os.Stat(root); strings.TrimSpace(root) == "" || statErr != nil || !info.IsDir() {
			root = worker.ProjectRoot
		}
		err = s.Steerer.SteerTurn(ctx, root, worker.ThreadID, worker.TurnID, formatForWorker(saved))
		if err != nil {
			delivery.LastError = err.Error()
			if updateErr := s.Store.UpdateDelivery(delivery.ID, store.DeliveryQueued, delivery.LastError, s.now()); updateErr != nil {
				return SendResult{}, errors.Join(err, updateErr)
			}
			continue
		}
		delivery.State = store.DeliverySteered
		delivery.LastError = ""
		if err := s.Store.UpdateDelivery(delivery.ID, store.DeliverySteered, "", s.now()); err != nil {
			return SendResult{}, err
		}
	}
	return result, nil
}

func (s Service) recordTimeline(message store.Message, recipients []string) error {
	st, ok := s.Store.(timelineStore)
	if !ok {
		return nil
	}
	ids := append([]string(nil), recipients...)
	if message.From != "system" {
		ids = append(ids, message.From)
	}
	ids = unique(ids)
	sum := sha256.Sum256([]byte(string(message.Kind) + "\x00" + message.From + "\x00" + message.Body + "\x00" + strings.Join(ids, "\x00")))
	fingerprint := hex.EncodeToString(sum[:])
	_, _, err := st.UpdateWorkersWithRequest(message.RequestID, "coordination.message", fingerprint, ids, func(workers map[string]*store.Worker) (store.WorkerMutationResult, error) {
		events := make([]store.Event, 0, len(recipients))
		issue := ""
		if from := workers[message.From]; from != nil {
			issue = from.Issue
			from.Events = append(from.Events, store.Event{At: message.CreatedAt, Type: "message.sent", Message: message.Body, From: message.From, RequestID: message.RequestID, WorkerID: message.From, Issue: issue})
			from.UpdatedAt = message.CreatedAt
		}
		for _, recipient := range recipients {
			worker := workers[recipient]
			if worker == nil {
				continue
			}
			if issue == "" {
				issue = worker.Issue
			}
			worker.Events = append(worker.Events, store.Event{At: message.CreatedAt, Type: "message.received", Message: message.Body, From: message.From, To: recipient, RequestID: message.RequestID, WorkerID: recipient, Issue: issue})
			worker.UpdatedAt = message.CreatedAt
			events = append(events, store.Event{At: message.CreatedAt, Type: "message", Message: message.Body, From: message.From, To: recipient, RequestID: message.RequestID, WorkerID: message.From, Issue: issue})
		}
		return store.WorkerMutationResult{Fingerprint: fingerprint, Output: message.ID, Events: events}, nil
	})
	return err
}

func unique(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func newID(prefix string, at time.Time) (string, error) {
	var random [5]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate %s id: %w", prefix, err)
	}
	return fmt.Sprintf("%s-%s-%s", prefix, at.UTC().Format("20060102-150405"), hex.EncodeToString(random[:])), nil
}

func formatForWorker(message store.Message) string {
	return fmt.Sprintf("SWARM_%s from=%s message_id=%s\n%s", strings.ToUpper(string(message.Kind)), message.From, message.ID, message.Body)
}

func conflictWarning(conflict store.TouchConflict) string {
	rangeText := "whole file"
	if conflict.Touch.LineStart > 0 && conflict.Touch.LineEnd > 0 {
		rangeText = fmt.Sprintf("lines %d-%d", conflict.Touch.LineStart, conflict.Touch.LineEnd)
	}
	return fmt.Sprintf("warning-only overlapping write: workers=%s,%s path=%s range=%s intents=%q,%q",
		conflict.Touch.WorkerID, conflict.PeerTouch.WorkerID, conflict.Touch.Path, rangeText, conflict.Touch.Intent, conflict.PeerTouch.Intent)
}
