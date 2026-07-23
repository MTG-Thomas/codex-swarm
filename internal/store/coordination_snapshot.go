package store

import (
	"errors"
	"fmt"
	"sort"
)

// ReadCoordinationSnapshot returns the operation-projection record set from a
// single SQLite transaction so concurrent mutations cannot create a torn view.
func (s *JSONStore) ReadCoordinationSnapshot() (CoordinationSnapshot, error) {
	var snapshot CoordinationSnapshot
	err := s.withStateLock(func() error {
		var err error
		snapshot.Workers, err = listRecords[Worker](s.tx, "worker")
		if err != nil {
			return fmt.Errorf("list snapshot workers: %w", err)
		}
		for i := range snapshot.Workers {
			normalizeWorkerLifecycleForRead(&snapshot.Workers[i])
		}
		sort.Slice(snapshot.Workers, func(i, j int) bool { return snapshot.Workers[i].UpdatedAt.After(snapshot.Workers[j].UpdatedAt) })

		snapshot.Claims, err = listRecords[Claim](s.tx, "claim")
		if err != nil {
			return fmt.Errorf("list snapshot claims: %w", err)
		}
		sort.Slice(snapshot.Claims, func(i, j int) bool { return snapshot.Claims[i].UpdatedAt.After(snapshot.Claims[j].UpdatedAt) })

		snapshot.Messages, err = listAllMessages(s.tx)
		if err != nil {
			return err
		}

		snapshot.GateEvidence, err = listRecords[GateEvidence](s.tx, "gate_evidence")
		if err != nil {
			return fmt.Errorf("list snapshot gate evidence: %w", err)
		}
		sort.Slice(snapshot.GateEvidence, func(i, j int) bool {
			return snapshot.GateEvidence[i].CreatedAt.After(snapshot.GateEvidence[j].CreatedAt)
		})

		snapshot.CodexTasks, err = listAllCodexTasks(s.tx)
		return err
	})
	return snapshot, err
}

func listAllCodexTasks(q sqlExecutor) (tasks []CodexTask, err error) {
	rows, err := q.Query(`SELECT host_id,thread_id,title,description,cwd,project,status,unread,coordinator,tier,last_meaningful_outcome,unresolved_loop,smallest_next_action,operator_decision,last_classified_at,last_classification_snapshot_id,created_at,updated_at,first_seen_at,last_seen_at,state_observed_at,state_snapshot_id,discovery_source,wait_cursor,last_snapshot_id,missing_since,tombstoned_at FROM codex_tasks ORDER BY last_seen_at DESC,host_id,thread_id`)
	if err != nil {
		return nil, fmt.Errorf("list snapshot Codex tasks: %w", err)
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		task, err := scanCodexTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}
