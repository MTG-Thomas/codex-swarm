package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

// attach binds a real external or app-server thread to a durable worker record.
func (c cli) attach(args []string) error {
	fs := c.flagSet("attach")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	repo := fs.String("repo", ".", "repository root")
	workerID := fs.String("worker", "", "existing worker id; omit to create one")
	threadID := fs.String("thread", os.Getenv("CODEX_THREAD_ID"), "Codex or app-server thread id")
	turnID := fs.String("turn", os.Getenv("CODEX_TURN_ID"), "active app-server turn id")
	engine := fs.String("engine", "tracker", "attachment engine: tracker or appserver")
	role := fs.String("role", "", "worker role")
	prompt := fs.String("prompt", "", "short task summary")
	parentID := fs.String("parent", "", "parent worker id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*threadID) == "" {
		return errors.New("attach requires --thread or CODEX_THREAD_ID")
	}
	if *engine != "tracker" && *engine != "appserver" {
		return fmt.Errorf("unknown attachment engine %q", *engine)
	}
	repoRoot, err := filepath.Abs(*repo)
	if err != nil {
		return fmt.Errorf("resolve attach repo: %w", err)
	}
	now := c.now().UTC()
	id := strings.TrimSpace(*workerID)
	st := store.NewJSONStore(*statePath)
	if id == "" {
		id, err = newWorkerID(now)
		if err != nil {
			return fmt.Errorf("generate attached worker id: %w", err)
		}
		worker := store.Worker{
			ID: id, ParentID: strings.TrimSpace(*parentID), Role: strings.TrimSpace(*role), ProjectRoot: repoRoot,
			ThreadID: strings.TrimSpace(*threadID), TurnID: strings.TrimSpace(*turnID), Engine: *engine,
			Prompt: strings.TrimSpace(*prompt), CreatedAt: now, UpdatedAt: now,
		}
		applyAttachedStatus(&worker, now)
		worker.LastMessage = attachmentMessage(worker)
		worker.Events = append(worker.Events, store.Event{At: now, Type: "worker.attached", Message: worker.LastMessage, WorkerID: worker.ID})
		if err := st.SaveWorker(worker); err != nil {
			return err
		}
		printAttachment(c.out, worker)
		return nil
	}
	worker, err := st.UpdateWorker(id, func(worker *store.Worker) error {
		if !sameStatusRepo(worker.ProjectRoot, repoRoot) {
			return fmt.Errorf("worker %s belongs to repo %s, not %s", worker.ID, worker.ProjectRoot, repoRoot)
		}
		worker.Engine = *engine
		worker.ThreadID = strings.TrimSpace(*threadID)
		worker.TurnID = strings.TrimSpace(*turnID)
		if strings.TrimSpace(*role) != "" {
			worker.Role = strings.TrimSpace(*role)
		}
		if strings.TrimSpace(*prompt) != "" {
			worker.Prompt = strings.TrimSpace(*prompt)
		}
		if strings.TrimSpace(*parentID) != "" {
			worker.ParentID = strings.TrimSpace(*parentID)
		}
		applyAttachedStatus(worker, now)
		worker.LastMessage = attachmentMessage(*worker)
		worker.Events = append(worker.Events, store.Event{At: now, Type: "worker.attached", Message: worker.LastMessage, WorkerID: worker.ID})
		worker.UpdatedAt = now
		return nil
	})
	if err != nil {
		if errors.Is(err, store.ErrWorkerNotFound) {
			return fmt.Errorf("worker %q not found", id)
		}
		return err
	}
	printAttachment(c.out, worker)
	return nil
}

func applyAttachedStatus(worker *store.Worker, now time.Time) {
	if store.CapabilitiesForWorker(*worker).Has(store.CapabilityLiveMessage) && strings.TrimSpace(worker.TurnID) != "" {
		worker.ApplyStatusAt(store.WorkerRunning, now)
		return
	}
	worker.ApplyStatusAt(store.WorkerIdle, now)
}

func attachmentMessage(worker store.Worker) string {
	return fmt.Sprintf("thread attached: engine=%s thread=%s turn=%s", worker.Engine, worker.ThreadID, emptyDash(worker.TurnID))
}

func printAttachment(out interface{ Write([]byte) (int, error) }, worker store.Worker) {
	liveMessages := "queued"
	capabilities := store.CapabilitiesForWorker(worker)
	if capabilities.Has(store.CapabilityLiveMessage) && worker.Status == store.WorkerRunning && worker.TurnID != "" {
		liveMessages = "steerable"
	}
	fmt.Fprintf(out, "attached worker=%s engine=%s capabilities=%s thread=%s turn=%s status=%s live_messages=%s\n",
		worker.ID, worker.Engine, strings.Join(capabilities.Strings(), ","), worker.ThreadID, emptyDash(worker.TurnID), displayWorkerStatus(worker), liveMessages)
}
