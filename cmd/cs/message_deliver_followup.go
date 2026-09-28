package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/codexqueue"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

// deliverFollowup sends one queued native_followup envelope through
// `codex queue` instead of the owning Codex host's native task-message tool.
// It is an explicit single-attempt adapter: success performs the existing
// confirm-followup transition, definite failure records the existing
// followup-failed transition, and an ambiguous outcome leaves the delivery
// queued without retry so a duplicate prompt cannot be queued blindly.
func (c cli) deliverFollowup(args []string) error {
	fs := c.flagSet("message deliver-followup")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	workerID := fs.String("worker", "", "recipient worker id")
	threadID := fs.String("thread", "", "thread id recorded on the worker")
	via := fs.String("via", "", "delivery transport: codex-queue")
	codexBinary := fs.String("codex-binary", "", "local codex binary (default codex)")
	sshBinary := fs.String("ssh-binary", "", "ssh binary for remote delivery (default ssh)")
	timeout := fs.Duration("timeout", time.Minute, "maximum time for version check and queue submission")
	jsonOutput := fs.Bool("json", false, "emit machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*via) != "codex-queue" {
		return fmt.Errorf("message deliver-followup requires --via codex-queue (got %q)", *via)
	}
	if fs.NArg() != 1 || strings.TrimSpace(*workerID) == "" || strings.TrimSpace(*threadID) == "" {
		return errors.New("message deliver-followup requires --worker, --thread, --via codex-queue, and <delivery-id>")
	}
	if *timeout <= 0 {
		return errors.New("message deliver-followup --timeout must be positive")
	}
	deliveryID := fs.Arg(0)
	st := store.NewJSONStore(*statePath)
	worker, err := st.GetWorker(*workerID)
	if err != nil {
		return fmt.Errorf("deliver follow-up worker %s: %w", *workerID, err)
	}
	item, err := findWorkerDelivery(st, worker.ID, deliveryID)
	if err != nil {
		return err
	}
	if item.Delivery.State != store.DeliveryQueued {
		return fmt.Errorf("refuse codex queue delivery for %s: delivery state is %s; deliver-followup only sends queued native follow-ups",
			deliveryID, item.Delivery.State)
	}
	capabilities := store.CapabilitiesForWorker(worker)
	if capabilities.Has(store.CapabilityNativeSteeringBridge) &&
		worker.Status == store.WorkerRunning &&
		strings.TrimSpace(worker.ThreadID) != "" && strings.TrimSpace(worker.TurnID) != "" {
		return fmt.Errorf("refuse codex queue delivery for %s: worker %s has an active turn %s; codex queue cannot enforce the expected turn, use the native task-message tool with confirm-steered or steering-failed",
			deliveryID, worker.ID, worker.TurnID)
	}
	if !capabilities.Has(store.CapabilityNativeFollowupBridge) || strings.TrimSpace(worker.ThreadID) == "" {
		return fmt.Errorf("refuse codex queue delivery for %s: worker %s does not hold a queued native_followup envelope", deliveryID, worker.ID)
	}
	if worker.ThreadID != *threadID {
		return fmt.Errorf("refuse codex queue delivery for %s: worker runtime is thread=%s, delivery expected thread=%s",
			deliveryID, emptyDash(worker.ThreadID), *threadID)
	}
	if !codexqueue.ValidThread(worker.ThreadID) {
		return fmt.Errorf("refuse codex queue delivery for %s: thread %q is not an exact thread UUID; codex queue never resolves session names",
			deliveryID, worker.ThreadID)
	}
	prompt := followupQueuePrompt(item.Message)
	cfg := codexqueue.Config{CodexBinary: *codexBinary, SSHBinary: *sshBinary}
	if worker.Remote != nil {
		cfg.Remote = &codexqueue.Remote{Target: worker.Remote.Host, Jump: worker.Remote.JumpHost, CodexBinary: worker.Remote.CodexBinary}
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	submission, submitErr := codexqueue.Submit(ctx, codexqueue.ExecRunner, cfg, worker.ThreadID, prompt)
	if submitErr != nil {
		var unsupported *codexqueue.UnsupportedError
		var ambiguous *codexqueue.AmbiguousError
		switch {
		case errors.As(submitErr, &unsupported), errors.As(submitErr, &ambiguous):
			return submitErr
		}
		if recordErr := st.UpdateDelivery(deliveryID, store.DeliveryQueued, submitErr.Error(), c.now().UTC()); recordErr != nil {
			return errors.Join(submitErr, recordErr)
		}
		item, err = findWorkerDelivery(st, worker.ID, deliveryID)
		if err != nil {
			return errors.Join(submitErr, err)
		}
		if *jsonOutput {
			_ = json.NewEncoder(c.out).Encode(item)
		} else {
			fmt.Fprintf(c.out, "recorded failure for native follow-up delivery=%s recipient=%s thread=%s state=%s error=%s\n",
				item.Delivery.ID, worker.ID, worker.ThreadID, item.Delivery.State, emptyDash(item.Delivery.LastError))
		}
		return fmt.Errorf("codex queue delivery failed and followup-failed was recorded: %w", submitErr)
	}
	if err := st.UpdateDelivery(deliveryID, store.DeliverySteered, "", c.now().UTC()); err != nil {
		return err
	}
	item, err = findWorkerDelivery(st, worker.ID, deliveryID)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return json.NewEncoder(c.out).Encode(item)
	}
	fmt.Fprintf(c.out, "confirmed native follow-up delivery=%s recipient=%s thread=%s state=%s submission=%s\n",
		item.Delivery.ID, worker.ID, worker.ThreadID, item.Delivery.State, emptyDash(submission))
	return nil
}

// followupQueuePrompt rebuilds the exact prompt a native follow-up carries:
// the same envelope the owning Codex host would inject with its native
// task-message tool. It mirrors the coordination send path so queue delivery
// and native delivery stay byte-identical.
func followupQueuePrompt(message store.Message) string {
	return fmt.Sprintf("SWARM_%s from=%s message_id=%s\n%s", strings.ToUpper(string(message.Kind)), message.From, message.ID, message.Body)
}
