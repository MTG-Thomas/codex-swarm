// Package operation derives stable logical operation scope from the
// authoritative coordination records. It does not persist another ledger.
package operation

import (
	"fmt"
	"sort"
	"strings"
	"time"

	gh "github.com/MTG-Thomas/codex-swarm/internal/github"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

const (
	KindIssue  = "issue"
	KindWorker = "worker"

	StateResolved      = "resolved"
	StateMissingParent = "missing_parent"
	StateParentCycle   = "parent_cycle"
	StateInvalidIssue  = "invalid_issue"
	StateUnlinked      = "unlinked"
)

// Resolution describes how one worker maps to an operation. Key is empty
// unless State is resolved; callers must not invent a fallback for degraded
// lineage.
type Resolution struct {
	WorkerID     string `json:"worker_id"`
	Key          string `json:"key,omitempty"`
	Kind         string `json:"kind,omitempty"`
	Issue        string `json:"issue,omitempty"`
	RootWorkerID string `json:"root_worker_id,omitempty"`
	State        string `json:"state"`
	Detail       string `json:"detail,omitempty"`
}

// PullRequest retains the worker relationship for embedded PR state.
type PullRequest struct {
	WorkerID string                 `json:"worker_id"`
	State    store.PullRequestState `json:"state"`
}

// Worker is the compact, non-transcript identity exposed by the operation
// projection. Prompts, reports, events, and remote connection details stay in
// their dedicated worker views.
type Worker struct {
	ID        string             `json:"id"`
	ParentID  string             `json:"parent_id,omitempty"`
	Role      string             `json:"role,omitempty"`
	Issue     string             `json:"issue,omitempty"`
	Repo      string             `json:"repo,omitempty"`
	Worktree  string             `json:"worktree,omitempty"`
	ThreadID  string             `json:"thread_id,omitempty"`
	HostID    string             `json:"host_id,omitempty"`
	Status    store.WorkerStatus `json:"status"`
	UpdatedAt time.Time          `json:"updated_at"`
}

// Claim is the compact ownership relationship needed for operation grouping.
type Claim struct {
	ID        string               `json:"id"`
	WorkerID  string               `json:"worker_id,omitempty"`
	Issue     string               `json:"issue,omitempty"`
	Repo      string               `json:"repo,omitempty"`
	ScopeKind store.ClaimScopeKind `json:"scope_kind,omitempty"`
	Scope     string               `json:"scope"`
	Status    store.ClaimStatus    `json:"status"`
	UpdatedAt time.Time            `json:"updated_at"`
}

// Message identifies a delivery without copying its body into broad operation
// listings. The inbox remains the authority for message content and history.
type Message struct {
	ID          string              `json:"id"`
	DeliveryID  string              `json:"delivery_id"`
	Kind        store.MessageKind   `json:"kind"`
	From        string              `json:"from"`
	RecipientID string              `json:"recipient_id"`
	State       store.DeliveryState `json:"state"`
	CreatedAt   time.Time           `json:"created_at"`
	UpdatedAt   time.Time           `json:"updated_at"`
}

// GateEvidence omits captured command output while retaining the proof link.
type GateEvidence struct {
	ID        string    `json:"id"`
	GateID    string    `json:"gate_id"`
	WorkerID  string    `json:"worker_id,omitempty"`
	Repo      string    `json:"repo,omitempty"`
	Scope     string    `json:"scope,omitempty"`
	ExitCode  int       `json:"exit_code"`
	Commit    string    `json:"commit,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// CodexTask is the stable discovery identity and classification used for
// grouping. Prompt, response, and transcript content is never part of it.
type CodexTask struct {
	HostID     string    `json:"host_id"`
	ThreadID   string    `json:"thread_id"`
	Project    string    `json:"project,omitempty"`
	Status     string    `json:"status,omitempty"`
	Tier       string    `json:"tier,omitempty"`
	Unread     bool      `json:"unread"`
	LastSeenAt time.Time `json:"last_seen_at"`
}

// Group is one derived operation and the existing records linked to it.
type Group struct {
	Key          string         `json:"key"`
	Kind         string         `json:"kind"`
	Issue        string         `json:"issue,omitempty"`
	RootWorkerID string         `json:"root_worker_id,omitempty"`
	Workers      []Worker       `json:"workers,omitempty"`
	Claims       []Claim        `json:"claims,omitempty"`
	Messages     []Message      `json:"messages,omitempty"`
	GateEvidence []GateEvidence `json:"gate_evidence,omitempty"`
	PullRequests []PullRequest  `json:"pull_requests,omitempty"`
	CodexTasks   []CodexTask    `json:"codex_tasks,omitempty"`
}

// UnscopedRecord makes unsupported or broken relationships visible without
// fabricating operation membership.
type UnscopedRecord struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	WorkerID string `json:"worker_id,omitempty"`
	State    string `json:"state"`
	Detail   string `json:"detail,omitempty"`
}

// Input is the current authoritative state used for a projection.
type Input struct {
	Workers      []store.Worker
	Claims       []store.Claim
	Messages     []store.DeliveredMessage
	GateEvidence []store.GateEvidence
	CodexTasks   []store.CodexTask
}

// View is a deterministic operation projection. Resolutions includes every
// worker, including keyless degraded workers.
type View struct {
	Operations  []Group          `json:"operations"`
	Resolutions []Resolution     `json:"resolutions"`
	Unscoped    []UnscopedRecord `json:"unscoped,omitempty"`
}

// NormalizeIssueRef returns the case-normalized issue reference and operation
// key used by all issue-backed records.
func NormalizeIssueRef(value string) (issue, key string, err error) {
	ref, err := gh.ParseIssueRef(strings.TrimSpace(value))
	if err != nil {
		return "", "", err
	}
	issue = fmt.Sprintf("%s/%s#%d", strings.ToLower(ref.Owner), strings.ToLower(ref.Repo), ref.Number)
	return issue, "issue:" + issue, nil
}

// NormalizeKey validates and canonicalizes an explicit operation key.
func NormalizeKey(value string) (string, error) {
	value = strings.TrimSpace(value)
	switch {
	case strings.HasPrefix(strings.ToLower(value), "issue:"):
		_, key, err := NormalizeIssueRef(value[len("issue:"):])
		return key, err
	case strings.HasPrefix(strings.ToLower(value), "worker:"):
		workerID := strings.TrimSpace(value[len("worker:"):])
		if workerID == "" {
			return "", fmt.Errorf("worker operation key requires a worker ID")
		}
		return "worker:" + workerID, nil
	default:
		return "", fmt.Errorf("operation key must begin with issue: or worker:")
	}
}

// ResolveWorkers applies the operation resolution order to every worker:
// explicit issue, inherited ancestor issue, then root worker. Missing parents,
// cycles, and invalid issue references remain keyless.
func ResolveWorkers(workers []store.Worker) map[string]Resolution {
	byID := make(map[string]store.Worker, len(workers))
	for _, worker := range workers {
		byID[worker.ID] = worker
	}
	memo := make(map[string]Resolution, len(workers))
	visiting := make(map[string]bool, len(workers))
	var resolve func(string) Resolution
	resolve = func(id string) Resolution {
		if result, ok := memo[id]; ok {
			return result
		}
		worker, ok := byID[id]
		if !ok {
			return Resolution{WorkerID: id, State: StateMissingParent, Detail: "worker record not found"}
		}
		if issueValue := strings.TrimSpace(worker.Issue); issueValue != "" {
			issue, key, err := NormalizeIssueRef(issueValue)
			if err != nil {
				result := Resolution{WorkerID: id, State: StateInvalidIssue, Detail: err.Error()}
				memo[id] = result
				return result
			}
			result := Resolution{WorkerID: id, Key: key, Kind: KindIssue, Issue: issue, State: StateResolved}
			memo[id] = result
			return result
		}
		if visiting[id] {
			return Resolution{WorkerID: id, State: StateParentCycle, Detail: "parent lineage contains a cycle"}
		}
		if strings.TrimSpace(worker.ParentID) == "" {
			result := Resolution{WorkerID: id, Key: "worker:" + id, Kind: KindWorker, RootWorkerID: id, State: StateResolved}
			memo[id] = result
			return result
		}
		if _, ok := byID[worker.ParentID]; !ok {
			result := Resolution{WorkerID: id, State: StateMissingParent, Detail: "parent worker " + worker.ParentID + " not found"}
			memo[id] = result
			return result
		}
		visiting[id] = true
		parent := resolve(worker.ParentID)
		delete(visiting, id)
		result := parent
		result.WorkerID = id
		if parent.State != StateResolved && result.Detail == "" {
			result.Detail = "unresolved parent " + worker.ParentID
		}
		memo[id] = result
		return result
	}
	for id := range byID {
		resolve(id)
	}
	return memo
}

// Derive groups records only through explicit issue, worker, parent, or
// host/thread relationships already present in the store.
func Derive(input Input) View {
	resolutions := ResolveWorkers(input.Workers)
	groups := make(map[string]*Group)
	ensureGroup := func(res Resolution) *Group {
		group := groups[res.Key]
		if group == nil {
			group = &Group{Key: res.Key, Kind: res.Kind, Issue: res.Issue, RootWorkerID: res.RootWorkerID}
			groups[res.Key] = group
		}
		return group
	}
	ensureKeyGroup := func(key string) *Group {
		if group := groups[key]; group != nil {
			return group
		}
		group := &Group{Key: key}
		switch {
		case strings.HasPrefix(key, "issue:"):
			group.Kind = KindIssue
			group.Issue = strings.TrimPrefix(key, "issue:")
		case strings.HasPrefix(key, "worker:"):
			group.Kind = KindWorker
			group.RootWorkerID = strings.TrimPrefix(key, "worker:")
		}
		groups[key] = group
		return group
	}

	view := View{}
	workersByThread := make(map[string][]store.Worker)
	for _, worker := range input.Workers {
		res := resolutions[worker.ID]
		view.Resolutions = append(view.Resolutions, res)
		if res.State == StateResolved {
			group := ensureGroup(res)
			group.Workers = append(group.Workers, compactWorker(worker))
		} else {
			view.Unscoped = append(view.Unscoped, UnscopedRecord{Kind: "worker", ID: worker.ID, WorkerID: worker.ID, State: res.State, Detail: res.Detail})
		}
		for i, pr := range worker.PullRequests {
			if res.State == StateResolved {
				group := ensureGroup(res)
				group.PullRequests = append(group.PullRequests, PullRequest{WorkerID: worker.ID, State: pr})
				continue
			}
			id := strings.TrimSpace(pr.URL)
			if id == "" {
				id = fmt.Sprintf("%s/pr/%d", worker.ID, i+1)
			}
			view.Unscoped = append(view.Unscoped, UnscopedRecord{Kind: "pull_request", ID: id, WorkerID: worker.ID, State: res.State, Detail: res.Detail})
		}
		if worker.ThreadID != "" {
			workersByThread[worker.ThreadID] = append(workersByThread[worker.ThreadID], worker)
		}
	}

	for _, claim := range input.Claims {
		keys, state, detail := recordKeys(claim.Issue, []string{claim.WorkerID}, resolutions)
		if len(keys) == 0 {
			view.Unscoped = append(view.Unscoped, UnscopedRecord{Kind: "claim", ID: claim.ID, WorkerID: claim.WorkerID, State: state, Detail: detail})
			continue
		}
		for _, key := range keys {
			group := ensureKeyGroup(key)
			group.Claims = append(group.Claims, compactClaim(claim))
		}
	}

	for _, message := range input.Messages {
		keys, state, detail := recordKeys("", []string{message.Message.From, message.Delivery.RecipientID}, resolutions)
		if len(keys) == 0 {
			view.Unscoped = append(view.Unscoped, UnscopedRecord{Kind: "message", ID: message.Delivery.ID, State: state, Detail: detail})
			continue
		}
		for _, key := range keys {
			group := ensureKeyGroup(key)
			group.Messages = append(group.Messages, compactMessage(message))
		}
	}

	for _, evidence := range input.GateEvidence {
		keys, state, detail := recordKeys("", []string{evidence.WorkerID}, resolutions)
		if len(keys) == 0 {
			view.Unscoped = append(view.Unscoped, UnscopedRecord{Kind: "gate", ID: evidence.ID, WorkerID: evidence.WorkerID, State: state, Detail: detail})
			continue
		}
		for _, key := range keys {
			group := ensureKeyGroup(key)
			group.GateEvidence = append(group.GateEvidence, compactGateEvidence(evidence))
		}
	}

	for _, task := range input.CodexTasks {
		candidates := matchingTaskWorkers(task, workersByThread[task.ThreadID])
		workerIDs := make([]string, 0, len(candidates))
		for _, worker := range candidates {
			workerIDs = append(workerIDs, worker.ID)
		}
		keys, state, detail := recordKeys("", workerIDs, resolutions)
		if len(keys) == 0 {
			if len(candidates) == 0 {
				state, detail = StateUnlinked, "no worker has this host/thread identity"
			}
			view.Unscoped = append(view.Unscoped, UnscopedRecord{Kind: "codex_task", ID: task.HostID + "/" + task.ThreadID, State: state, Detail: detail})
			continue
		}
		for _, key := range keys {
			group := ensureKeyGroup(key)
			group.CodexTasks = append(group.CodexTasks, compactCodexTask(task))
		}
	}

	for _, group := range groups {
		sortGroup(group)
		view.Operations = append(view.Operations, *group)
	}
	sort.Slice(view.Operations, func(i, j int) bool { return view.Operations[i].Key < view.Operations[j].Key })
	sort.Slice(view.Resolutions, func(i, j int) bool { return view.Resolutions[i].WorkerID < view.Resolutions[j].WorkerID })
	sort.Slice(view.Unscoped, func(i, j int) bool {
		if view.Unscoped[i].Kind != view.Unscoped[j].Kind {
			return view.Unscoped[i].Kind < view.Unscoped[j].Kind
		}
		return view.Unscoped[i].ID < view.Unscoped[j].ID
	})
	return view
}

func recordKeys(issueValue string, workerIDs []string, resolutions map[string]Resolution) ([]string, string, string) {
	if strings.TrimSpace(issueValue) != "" {
		_, key, err := NormalizeIssueRef(issueValue)
		if err != nil {
			return nil, StateInvalidIssue, err.Error()
		}
		return []string{key}, StateResolved, ""
	}
	seen := map[string]struct{}{}
	state, detail := StateUnlinked, "no linked worker"
	for _, workerID := range workerIDs {
		if strings.TrimSpace(workerID) == "" {
			continue
		}
		res, ok := resolutions[workerID]
		if !ok {
			state, detail = StateUnlinked, "worker "+workerID+" not found"
			continue
		}
		if res.State != StateResolved {
			state, detail = res.State, res.Detail
			continue
		}
		seen[res.Key] = struct{}{}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys, state, detail
}

func matchingTaskWorkers(task store.CodexTask, candidates []store.Worker) []store.Worker {
	var exact []store.Worker
	for _, worker := range candidates {
		if strings.TrimSpace(worker.HostID) != "" && strings.EqualFold(worker.HostID, task.HostID) {
			exact = append(exact, worker)
		}
	}
	return exact
}

func compactWorker(worker store.Worker) Worker {
	status := worker.Status
	if worker.Lifecycle != nil {
		status = store.WorkerStatus(worker.Lifecycle.DeriveStatus())
	}
	return Worker{
		ID: worker.ID, ParentID: worker.ParentID, Role: worker.Role, Issue: worker.Issue,
		Repo: worker.ProjectRoot, Worktree: worker.Worktree, ThreadID: worker.ThreadID,
		HostID: worker.HostID, Status: status, UpdatedAt: worker.UpdatedAt,
	}
}

func compactClaim(claim store.Claim) Claim {
	return Claim{
		ID: claim.ID, WorkerID: claim.WorkerID, Issue: claim.Issue, Repo: claim.Repo,
		ScopeKind: claim.ScopeKind, Scope: claim.Scope, Status: claim.Status, UpdatedAt: claim.UpdatedAt,
	}
}

func compactMessage(message store.DeliveredMessage) Message {
	return Message{
		ID: message.Message.ID, DeliveryID: message.Delivery.ID, Kind: message.Message.Kind,
		From: message.Message.From, RecipientID: message.Delivery.RecipientID, State: message.Delivery.State,
		CreatedAt: message.Message.CreatedAt, UpdatedAt: message.Delivery.UpdatedAt,
	}
}

func compactGateEvidence(evidence store.GateEvidence) GateEvidence {
	return GateEvidence{
		ID: evidence.ID, GateID: evidence.GateID, WorkerID: evidence.WorkerID,
		Repo: evidence.Repo, Scope: evidence.Scope, ExitCode: evidence.ExitCode,
		Commit: evidence.Commit, CreatedAt: evidence.CreatedAt,
	}
}

func compactCodexTask(task store.CodexTask) CodexTask {
	return CodexTask{
		HostID: task.HostID, ThreadID: task.ThreadID, Project: task.Project,
		Status: task.Status, Tier: task.Tier, Unread: task.Unread, LastSeenAt: task.LastSeenAt,
	}
}

func sortGroup(group *Group) {
	sort.Slice(group.Workers, func(i, j int) bool { return group.Workers[i].ID < group.Workers[j].ID })
	sort.Slice(group.Claims, func(i, j int) bool { return group.Claims[i].ID < group.Claims[j].ID })
	sort.Slice(group.Messages, func(i, j int) bool { return group.Messages[i].DeliveryID < group.Messages[j].DeliveryID })
	sort.Slice(group.GateEvidence, func(i, j int) bool { return group.GateEvidence[i].ID < group.GateEvidence[j].ID })
	sort.Slice(group.PullRequests, func(i, j int) bool {
		if group.PullRequests[i].State.URL != group.PullRequests[j].State.URL {
			return group.PullRequests[i].State.URL < group.PullRequests[j].State.URL
		}
		return group.PullRequests[i].WorkerID < group.PullRequests[j].WorkerID
	})
	sort.Slice(group.CodexTasks, func(i, j int) bool {
		if group.CodexTasks[i].HostID != group.CodexTasks[j].HostID {
			return group.CodexTasks[i].HostID < group.CodexTasks[j].HostID
		}
		return group.CodexTasks[i].ThreadID < group.CodexTasks[j].ThreadID
	})
}
