package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const (
	maxDecisionEvidence = 100
	maxDecisionGaps     = 100
)

// RecordDecision atomically creates a decision or replays the result of an
// identical request. When SupersedesID is set, the prior decision is marked as
// superseded in the same transaction and its history remains readable.
func (s *JSONStore) RecordDecision(decision Decision) (Decision, bool, error) {
	decision = normalizeDecision(decision)
	if err := validateDecision(decision); err != nil {
		return Decision{}, false, err
	}
	fingerprint, err := decisionFingerprint(decision)
	if err != nil {
		return Decision{}, false, err
	}
	if decision.RequestFingerprint != "" {
		fingerprint = decision.RequestFingerprint
	}
	var saved Decision
	replayed := false
	err = s.withStateLock(func() error {
		var existingFingerprint string
		err := s.tx.QueryRow(`SELECT fingerprint FROM decisions WHERE request_id=?`, decision.RequestID).Scan(&existingFingerprint)
		switch {
		case err == nil:
			if existingFingerprint != fingerprint {
				return fmt.Errorf("request %q does not match original decision mutation: %w", decision.RequestID, ErrDecisionReplayMismatch)
			}
			saved, err = getDecision(s.tx, "request_id", decision.RequestID)
			replayed = true
			return err
		case !errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("read decision replay %q: %w", decision.RequestID, err)
		}

		if decision.SupersedesID != "" {
			prior, err := getDecision(s.tx, "id", decision.SupersedesID)
			if err != nil {
				return err
			}
			if !prior.Current() {
				return fmt.Errorf("decision %q was superseded by %q: %w", prior.ID, prior.SupersededByID, ErrDecisionSuperseded)
			}
			if decision.Operation == "" {
				decision.Operation = prior.Operation
			}
			if decision.Repo == "" {
				decision.Repo = prior.Repo
			}
			if decision.Issue == "" {
				decision.Issue = prior.Issue
			}
		}

		evidence, err := json.Marshal(decision.Evidence)
		if err != nil {
			return fmt.Errorf("encode decision evidence: %w", err)
		}
		gaps, err := json.Marshal(decision.ProvenanceGaps)
		if err != nil {
			return fmt.Errorf("encode decision provenance gaps: %w", err)
		}
		_, err = s.tx.Exec(`INSERT INTO decisions(id,request_id,fingerprint,operation_key,repo,repo_key,issue,summary,rationale,evidence,dissent,author_worker,provenance_gaps,supersedes_id,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			decision.ID, decision.RequestID, fingerprint, decision.Operation, decision.Repo, pathKey(decision.Repo), decision.Issue,
			decision.Summary, decision.Rationale, evidence, decision.Dissent, decision.AuthorWorker, gaps, nullIfEmpty(decision.SupersedesID), formatDBTime(decision.CreatedAt))
		if err != nil {
			return fmt.Errorf("insert decision %s: %w", decision.ID, err)
		}
		if decision.SupersedesID != "" {
			result, err := s.tx.Exec(`UPDATE decisions SET superseded_by_id=?,superseded_at=? WHERE id=? AND superseded_by_id IS NULL`, decision.ID, formatDBTime(decision.CreatedAt), decision.SupersedesID)
			if err != nil {
				return fmt.Errorf("supersede decision %s: %w", decision.SupersedesID, err)
			}
			rows, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if rows != 1 {
				return fmt.Errorf("decision %q changed during supersession: %w", decision.SupersedesID, ErrDecisionSuperseded)
			}
		}
		saved, err = getDecision(s.tx, "id", decision.ID)
		return err
	})
	return saved, replayed, err
}

// GetDecision returns one decision including preserved supersession metadata.
func (s *JSONStore) GetDecision(id string) (Decision, error) {
	var decision Decision
	err := s.withStateLock(func() error {
		var err error
		decision, err = getDecision(s.tx, "id", strings.TrimSpace(id))
		return err
	})
	return decision, err
}

// ListDecisions returns newest-first decision history for exact filters.
func (s *JSONStore) ListDecisions(filter DecisionListFilter) ([]Decision, error) {
	filter.Operation = strings.TrimSpace(filter.Operation)
	filter.Repo = strings.TrimSpace(filter.Repo)
	filter.Issue = strings.TrimSpace(filter.Issue)
	var decisions []Decision
	err := s.withStateLock(func() (err error) {
		clauses := []string{"1=1"}
		args := []any{}
		if filter.Operation != "" {
			clauses = append(clauses, "operation_key=?")
			args = append(args, filter.Operation)
		}
		if filter.Repo != "" {
			clauses = append(clauses, "repo_key=?")
			args = append(args, pathKey(filter.Repo))
		}
		if filter.Issue != "" {
			clauses = append(clauses, "issue=?")
			args = append(args, filter.Issue)
		}
		if filter.CurrentOnly {
			clauses = append(clauses, "superseded_by_id IS NULL")
		}
		rows, err := s.tx.Query(decisionSelect+` WHERE `+strings.Join(clauses, " AND ")+` ORDER BY created_at DESC,id DESC`, args...)
		if err != nil {
			return fmt.Errorf("list decisions: %w", err)
		}
		defer func() { err = errors.Join(err, rows.Close()) }()
		for rows.Next() {
			decision, err := scanDecision(rows)
			if err != nil {
				return err
			}
			decisions = append(decisions, decision)
		}
		return rows.Err()
	})
	return decisions, err
}

const decisionSelect = `SELECT id,request_id,operation_key,repo,issue,summary,rationale,evidence,dissent,author_worker,provenance_gaps,supersedes_id,superseded_by_id,created_at,superseded_at FROM decisions`

func getDecision(q *sql.Tx, column, value string) (Decision, error) {
	if column != "id" && column != "request_id" {
		return Decision{}, errors.New("unsupported decision lookup")
	}
	decision, err := scanDecision(q.QueryRow(decisionSelect+` WHERE `+column+`=?`, value))
	if errors.Is(err, sql.ErrNoRows) {
		return Decision{}, fmt.Errorf("%w: %s", ErrDecisionNotFound, value)
	}
	if err != nil {
		return Decision{}, fmt.Errorf("read decision %s: %w", value, err)
	}
	return decision, nil
}

func scanDecision(row scanner) (Decision, error) {
	var decision Decision
	var evidence, gaps []byte
	var supersedes, supersededBy, supersededAt sql.NullString
	var created string
	if err := row.Scan(&decision.ID, &decision.RequestID, &decision.Operation, &decision.Repo, &decision.Issue, &decision.Summary, &decision.Rationale,
		&evidence, &decision.Dissent, &decision.AuthorWorker, &gaps, &supersedes, &supersededBy, &created, &supersededAt); err != nil {
		return Decision{}, err
	}
	if err := json.Unmarshal(evidence, &decision.Evidence); err != nil {
		return Decision{}, fmt.Errorf("decode decision %s evidence: %w", decision.ID, err)
	}
	if err := json.Unmarshal(gaps, &decision.ProvenanceGaps); err != nil {
		return Decision{}, fmt.Errorf("decode decision %s provenance gaps: %w", decision.ID, err)
	}
	var err error
	decision.CreatedAt, err = parseDBTime(created)
	if err != nil {
		return Decision{}, fmt.Errorf("parse decision %s created_at: %w", decision.ID, err)
	}
	decision.SupersedesID = supersedes.String
	decision.SupersededByID = supersededBy.String
	if supersededAt.Valid {
		at, err := parseDBTime(supersededAt.String)
		if err != nil {
			return Decision{}, fmt.Errorf("parse decision %s superseded_at: %w", decision.ID, err)
		}
		decision.SupersededAt = &at
	}
	return decision, nil
}

func normalizeDecision(decision Decision) Decision {
	decision.ID = strings.TrimSpace(decision.ID)
	decision.RequestID = strings.TrimSpace(decision.RequestID)
	decision.Operation = strings.TrimSpace(decision.Operation)
	decision.Repo = strings.TrimSpace(decision.Repo)
	decision.Issue = strings.TrimSpace(decision.Issue)
	decision.Summary = strings.TrimSpace(decision.Summary)
	decision.Rationale = strings.TrimSpace(decision.Rationale)
	decision.Dissent = strings.TrimSpace(decision.Dissent)
	decision.AuthorWorker = strings.TrimSpace(decision.AuthorWorker)
	decision.SupersedesID = strings.TrimSpace(decision.SupersedesID)
	decision.CreatedAt = decision.CreatedAt.UTC()
	seenEvidence := map[string]struct{}{}
	evidence := make([]DecisionEvidence, 0, len(decision.Evidence))
	for _, item := range decision.Evidence {
		item.Ref = strings.TrimSpace(item.Ref)
		item.State = strings.TrimSpace(item.State)
		item.Detail = strings.TrimSpace(item.Detail)
		if item.Ref == "" {
			continue
		}
		if _, ok := seenEvidence[item.Ref]; ok {
			continue
		}
		seenEvidence[item.Ref] = struct{}{}
		evidence = append(evidence, item)
	}
	decision.Evidence = evidence
	decision.ProvenanceGaps = uniqueSortedStrings(decision.ProvenanceGaps)
	return decision
}

func validateDecision(decision Decision) error {
	if decision.ID == "" || decision.RequestID == "" {
		return errors.New("decision id and request id are required")
	}
	if decision.Summary == "" || decision.Rationale == "" || decision.AuthorWorker == "" {
		return errors.New("decision summary, rationale, and author worker are required")
	}
	if decision.CreatedAt.IsZero() {
		return errors.New("decision created_at is required")
	}
	if len(decision.Evidence) > maxDecisionEvidence || len(decision.ProvenanceGaps) > maxDecisionGaps {
		return fmt.Errorf("decision accepts at most %d evidence references and %d provenance gaps", maxDecisionEvidence, maxDecisionGaps)
	}
	if len(decision.ID) > 256 || len(decision.RequestID) > 256 || len(decision.Operation) > 1024 || len(decision.Repo) > 4096 || len(decision.Issue) > 512 || len(decision.Summary) > 2000 || len(decision.Rationale) > 8000 || len(decision.Dissent) > 4000 || len(decision.AuthorWorker) > 256 || len(decision.SupersedesID) > 256 {
		return errors.New("decision contains an oversized field")
	}
	if len(decision.RequestFingerprint) > 256 {
		return errors.New("decision request fingerprint is oversized")
	}
	for _, item := range decision.Evidence {
		if len(item.Ref) > 4096 || len(item.Detail) > 2000 {
			return errors.New("decision contains an oversized evidence reference")
		}
		if item.State != DecisionEvidenceAvailable && item.State != DecisionEvidenceMissing && item.State != DecisionEvidenceExternal {
			return fmt.Errorf("unsupported decision evidence state %q", item.State)
		}
	}
	for _, gap := range decision.ProvenanceGaps {
		if len(gap) > 2000 {
			return errors.New("decision contains an oversized provenance gap")
		}
	}
	return nil
}

func decisionFingerprint(decision Decision) (string, error) {
	refs := make([]string, 0, len(decision.Evidence))
	for _, item := range decision.Evidence {
		refs = append(refs, item.Ref)
	}
	payload := struct {
		Operation, Repo, Issue, Summary, Rationale, Dissent, AuthorWorker, SupersedesID string
		Evidence                                                                        []string
	}{decision.Operation, decision.Repo, decision.Issue, decision.Summary, decision.Rationale, decision.Dissent, decision.AuthorWorker, decision.SupersedesID, refs}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode decision fingerprint: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func uniqueSortedStrings(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
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

func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}
