package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/operation"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

type decisionMutationOutput struct {
	Decision store.Decision `json:"decision"`
	Replayed bool           `json:"replayed"`
}

func (c cli) decision(args []string) error {
	if len(args) == 0 {
		return errors.New("decision requires <record|list|show|supersede>")
	}
	switch args[0] {
	case "record":
		return c.decisionRecord(args[1:])
	case "list":
		return c.decisionList(args[1:])
	case "show":
		return c.decisionShow(args[1:])
	case "supersede":
		return c.decisionSupersede(args[1:])
	default:
		return fmt.Errorf("unknown decision command %q", args[0])
	}
}

func (c cli) decisionRecord(args []string) error {
	fs := c.flagSet("decision record")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	requestIDValue := fs.String("request-id", "", "idempotency key")
	operationValue := fs.String("operation", "", "stable derived operation key")
	repoValue := fs.String("repo", "", "repository path when known")
	issueValue := fs.String("issue", "", "GitHub issue reference when known")
	summary := fs.String("summary", "", "concise decision summary")
	rationale := fs.String("rationale", "", "reason for the decision")
	dissent := fs.String("dissent", "", "dissent or unresolved concern")
	author := fs.String("author", "", "worker that authored the decision")
	authorWorker := fs.String("worker", "", "alias for --author")
	jsonOutput := fs.Bool("json", false, "print structured JSON")
	var evidenceValues stringListFlag
	fs.Var(&evidenceValues, "evidence", "evidence reference; repeat for multiple values")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("decision record accepts flags only")
	}
	now := c.now().UTC()
	requestID, err := c.requestID(*requestIDValue, now)
	if err != nil {
		return err
	}
	id, err := newDecisionID(now)
	if err != nil {
		return err
	}
	st := store.NewJSONStore(*statePath)
	decision, err := buildDecision(st, decisionBuildInput{
		ID: id, RequestID: requestID, Operation: *operationValue, Repo: *repoValue, Issue: *issueValue,
		Summary: *summary, Rationale: *rationale, Evidence: evidenceValues, Dissent: *dissent, Author: firstDecisionNonEmpty(*author, *authorWorker), At: now,
	})
	if err != nil {
		return err
	}
	saved, replayed, err := st.RecordDecision(decision)
	if err != nil {
		return err
	}
	return printDecisionMutation(c.out, saved, replayed, *jsonOutput)
}

func (c cli) decisionSupersede(args []string) error {
	decisionID := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		decisionID = strings.TrimSpace(args[0])
		args = args[1:]
	}
	fs := c.flagSet("decision supersede")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	requestIDValue := fs.String("request-id", "", "idempotency key")
	operationValue := fs.String("operation", "", "replacement operation key")
	repoValue := fs.String("repo", "", "replacement repository path")
	issueValue := fs.String("issue", "", "replacement GitHub issue reference")
	summary := fs.String("summary", "", "replacement decision summary")
	rationale := fs.String("rationale", "", "replacement decision rationale")
	dissent := fs.String("dissent", "", "replacement dissent or unresolved concern")
	author := fs.String("author", "", "worker authoring the replacement")
	authorWorker := fs.String("worker", "", "alias for --author")
	jsonOutput := fs.Bool("json", false, "print structured JSON")
	clearEvidence := fs.Bool("clear-evidence", false, "replace inherited evidence with an empty set")
	clearDissent := fs.Bool("clear-dissent", false, "clear inherited dissent or concern")
	var evidenceValues stringListFlag
	fs.Var(&evidenceValues, "evidence", "replacement evidence reference; repeat for multiple values")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if decisionID == "" && fs.NArg() == 1 {
		decisionID = strings.TrimSpace(fs.Arg(0))
	} else if fs.NArg() != 0 {
		return errors.New("decision supersede requires <decision-id>")
	}
	if decisionID == "" {
		return errors.New("decision supersede requires <decision-id>")
	}
	if strings.TrimSpace(*summary) == "" || strings.TrimSpace(*rationale) == "" {
		return errors.New("decision supersede requires --summary and --rationale")
	}
	if *clearEvidence && len(evidenceValues) > 0 {
		return errors.New("decision supersede cannot combine --clear-evidence with --evidence")
	}
	if *clearDissent && strings.TrimSpace(*dissent) != "" {
		return errors.New("decision supersede cannot combine --clear-dissent with --dissent")
	}
	st := store.NewJSONStore(*statePath)
	prior, err := st.GetDecision(decisionID)
	if err != nil {
		return err
	}
	now := c.now().UTC()
	requestID, err := c.requestID(*requestIDValue, now)
	if err != nil {
		return err
	}
	id, err := newDecisionID(now)
	if err != nil {
		return err
	}
	evidence := evidenceValues
	if len(evidence) == 0 && !*clearEvidence {
		for _, item := range prior.Evidence {
			evidence = append(evidence, item.Ref)
		}
	}
	replacementDissent := firstDecisionNonEmpty(*dissent, prior.Dissent)
	if *clearDissent {
		replacementDissent = ""
	}
	decision, err := buildDecision(st, decisionBuildInput{
		ID: id, RequestID: requestID, Operation: firstDecisionNonEmpty(*operationValue, prior.Operation),
		Repo: firstDecisionNonEmpty(*repoValue, prior.Repo), Issue: firstDecisionNonEmpty(*issueValue, prior.Issue),
		Summary: *summary, Rationale: *rationale, Evidence: evidence,
		Dissent: replacementDissent, Author: firstDecisionNonEmpty(*author, *authorWorker, prior.AuthorWorker),
		SupersedesID: prior.ID, At: now,
	})
	if err != nil {
		return err
	}
	saved, replayed, err := st.RecordDecision(decision)
	if err != nil {
		return err
	}
	return printDecisionMutation(c.out, saved, replayed, *jsonOutput)
}

func (c cli) decisionList(args []string) error {
	fs := c.flagSet("decision list")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	operationValue := fs.String("operation", "", "filter by exact operation key")
	repoValue := fs.String("repo", "", "filter by repository path")
	issueValue := fs.String("issue", "", "filter by GitHub issue reference")
	currentOnly := fs.Bool("current", false, "show only decisions that have not been superseded")
	jsonOutput := fs.Bool("json", false, "print structured JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("decision list accepts flags only")
	}
	repo, err := normalizeDecisionRepo(*repoValue)
	if err != nil {
		return err
	}
	issue, err := normalizeDecisionIssue(*issueValue)
	if err != nil {
		return err
	}
	operationKey := ""
	if strings.TrimSpace(*operationValue) != "" {
		operationKey, err = operation.NormalizeKey(*operationValue)
		if err != nil {
			return err
		}
	}
	decisions, err := store.NewJSONStore(*statePath).ListDecisions(store.DecisionListFilter{
		Operation: operationKey, Repo: repo, Issue: issue, CurrentOnly: *currentOnly,
	})
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeDecisionJSON(c.out, decisions)
	}
	if _, err := fmt.Fprintf(c.out, "decisions=%d\n", len(decisions)); err != nil {
		return err
	}
	for _, decision := range decisions {
		if _, err := fmt.Fprintf(c.out, "%s\tcurrent=%t\toperation=%s\trepo=%s\tissue=%s\tauthor=%s\tsummary=%s\n",
			decision.ID, decision.Current(), strconv.Quote(decision.Operation), strconv.Quote(decision.Repo), strconv.Quote(decision.Issue), strconv.Quote(decision.AuthorWorker), strconv.Quote(decision.Summary)); err != nil {
			return err
		}
	}
	return nil
}

func (c cli) decisionShow(args []string) error {
	fs := c.flagSet("decision show")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	jsonOutput := fs.Bool("json", false, "print structured JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("decision show requires <decision-id>")
	}
	decision, err := store.NewJSONStore(*statePath).GetDecision(fs.Arg(0))
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeDecisionJSON(c.out, decision)
	}
	return printDecision(c.out, decision)
}

type decisionBuildInput struct {
	ID, RequestID, Operation, Repo, Issue, Summary, Rationale, Dissent, Author, SupersedesID string
	Evidence                                                                                 []string
	At                                                                                       time.Time
}

func buildDecision(st *store.JSONStore, input decisionBuildInput) (store.Decision, error) {
	input.Author = strings.TrimSpace(input.Author)
	if input.Author == "" {
		return store.Decision{}, errors.New("decision requires --author")
	}
	repo, err := normalizeDecisionRepo(input.Repo)
	if err != nil {
		return store.Decision{}, err
	}
	issue, err := normalizeDecisionIssue(input.Issue)
	if err != nil {
		return store.Decision{}, err
	}
	requestFingerprint, err := decisionCommandFingerprint(input, repo, issue)
	if err != nil {
		return store.Decision{}, err
	}
	snapshot, err := st.ReadCoordinationSnapshot()
	if err != nil {
		return store.Decision{}, err
	}
	view := operation.Derive(operation.Input{
		Workers: snapshot.Workers, Claims: snapshot.Claims, Messages: snapshot.Messages,
		GateEvidence: snapshot.GateEvidence, CodexTasks: snapshot.CodexTasks,
	})
	resolutions := make(map[string]operation.Resolution, len(view.Resolutions))
	for _, resolution := range view.Resolutions {
		resolutions[resolution.WorkerID] = resolution
	}
	workerByID := make(map[string]store.Worker, len(snapshot.Workers))
	for _, worker := range snapshot.Workers {
		workerByID[worker.ID] = worker
	}
	authorWorker, authorFound := workerByID[input.Author]
	gaps := []string(nil)
	if !authorFound {
		gaps = append(gaps, "author_worker:"+input.Author+" not found")
	} else {
		if repo == "" {
			repo = strings.TrimSpace(authorWorker.ProjectRoot)
		}
		if issue == "" && strings.TrimSpace(authorWorker.Issue) != "" {
			issue, err = normalizeDecisionIssue(authorWorker.Issue)
			if err != nil {
				gaps = append(gaps, "author_worker:"+input.Author+" has invalid issue reference")
				issue = ""
			}
		}
	}

	operationKey := ""
	if strings.TrimSpace(input.Operation) != "" {
		operationKey, err = operation.NormalizeKey(input.Operation)
		if err != nil {
			return store.Decision{}, err
		}
	}
	if issue != "" {
		_, issueKey, err := operation.NormalizeIssueRef(issue)
		if err != nil {
			return store.Decision{}, err
		}
		if operationKey != "" && operationKey != issueKey {
			return store.Decision{}, fmt.Errorf("decision operation %q conflicts with issue-derived operation %q", operationKey, issueKey)
		}
		operationKey = issueKey
	} else if operationKey == "" && authorFound {
		resolution := resolutions[input.Author]
		if resolution.State == operation.StateResolved {
			operationKey = resolution.Key
		} else {
			gaps = append(gaps, fmt.Sprintf("author_worker:%s operation unresolved: %s (%s)", input.Author, resolution.State, resolution.Detail))
		}
	}
	if operationKey != "" && !operationViewHasKey(view, operationKey) {
		gaps = append(gaps, "operation:"+operationKey+" not present in current projection")
	}
	evidence, evidenceGaps, err := classifyDecisionEvidence(st, input.Evidence, snapshot, view)
	if err != nil {
		return store.Decision{}, err
	}
	gaps = append(gaps, evidenceGaps...)
	return store.Decision{
		ID: strings.TrimSpace(input.ID), RequestID: strings.TrimSpace(input.RequestID), Operation: operationKey,
		Repo: repo, Issue: issue, Summary: input.Summary, Rationale: input.Rationale, Evidence: evidence,
		Dissent: input.Dissent, AuthorWorker: input.Author, ProvenanceGaps: gaps,
		SupersedesID: strings.TrimSpace(input.SupersedesID), CreatedAt: input.At, RequestFingerprint: requestFingerprint,
	}, nil
}

func decisionCommandFingerprint(input decisionBuildInput, repo, issue string) (string, error) {
	evidence := make([]string, 0, len(input.Evidence))
	seen := map[string]struct{}{}
	for _, value := range input.Evidence {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		evidence = append(evidence, value)
	}
	payload := struct {
		Operation, Repo, Issue, Summary, Rationale, Dissent, Author, SupersedesID string
		Evidence                                                                  []string
	}{
		strings.TrimSpace(input.Operation), repo, issue, strings.TrimSpace(input.Summary), strings.TrimSpace(input.Rationale),
		strings.TrimSpace(input.Dissent), strings.TrimSpace(input.Author), strings.TrimSpace(input.SupersedesID), evidence,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode decision command fingerprint: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func classifyDecisionEvidence(st *store.JSONStore, values []string, snapshot store.CoordinationSnapshot, view operation.View) ([]store.DecisionEvidence, []string, error) {
	workerIDs := map[string]struct{}{}
	for _, worker := range snapshot.Workers {
		workerIDs[worker.ID] = struct{}{}
	}
	gateIDs := map[string]struct{}{}
	for _, gate := range snapshot.GateEvidence {
		gateIDs[gate.ID] = struct{}{}
	}
	claimIDs := map[string]struct{}{}
	for _, claim := range snapshot.Claims {
		claimIDs[claim.ID] = struct{}{}
	}
	evidence := make([]store.DecisionEvidence, 0, len(values))
	gaps := []string(nil)
	for _, value := range values {
		ref := strings.TrimSpace(value)
		if ref == "" {
			continue
		}
		item := store.DecisionEvidence{Ref: ref, State: store.DecisionEvidenceExternal, Detail: "external reference not fetched"}
		kind, id, recognized := strings.Cut(ref, ":")
		if recognized {
			found := false
			var err error
			switch kind {
			case "worker":
				_, found = workerIDs[id]
			case "gate":
				_, found = gateIDs[id]
			case "claim":
				_, found = claimIDs[id]
			case "decision":
				_, err = st.GetDecision(id)
				found = err == nil
				if err != nil && !errors.Is(err, store.ErrDecisionNotFound) {
					return nil, nil, err
				}
			case "operation":
				id, err = operation.NormalizeKey(id)
				if err != nil {
					return nil, nil, fmt.Errorf("invalid operation evidence %q: %w", ref, err)
				}
				item.Ref = "operation:" + id
				found = operationViewHasKey(view, id)
			default:
				recognized = false
			}
			if recognized {
				if found {
					item.State, item.Detail = store.DecisionEvidenceAvailable, ""
				} else {
					item.State, item.Detail = store.DecisionEvidenceMissing, "local reference not found"
					gaps = append(gaps, "evidence:"+ref+" not found")
				}
			}
		}
		evidence = append(evidence, item)
	}
	return evidence, gaps, nil
}

func operationViewHasKey(view operation.View, key string) bool {
	for _, group := range view.Operations {
		if group.Key == key {
			return true
		}
	}
	return false
}

func normalizeDecisionRepo(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	root, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("resolve decision repo: %w", err)
	}
	return root, nil
}

func normalizeDecisionIssue(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	issue, _, err := operation.NormalizeIssueRef(value)
	return issue, err
}

func newDecisionID(now time.Time) (string, error) {
	suffix, err := randomSuffix(4)
	if err != nil {
		return "", fmt.Errorf("generate decision id: %w", err)
	}
	return fmt.Sprintf("d-%s-%s", now.UTC().Format("20060102-150405"), suffix), nil
}

func printDecisionMutation(out io.Writer, decision store.Decision, replayed, jsonOutput bool) error {
	if jsonOutput {
		return writeDecisionJSON(out, decisionMutationOutput{Decision: decision, Replayed: replayed})
	}
	_, err := fmt.Fprintf(out, "decision %s current=%t replayed=%t operation=%s repo=%s issue=%s author=%s supersedes=%s\n",
		decision.ID, decision.Current(), replayed, strconv.Quote(decision.Operation), strconv.Quote(decision.Repo), strconv.Quote(decision.Issue), strconv.Quote(decision.AuthorWorker), strconv.Quote(decision.SupersedesID))
	return err
}

func printDecision(out io.Writer, decision store.Decision) error {
	if _, err := fmt.Fprintf(out, "id=%s\ncurrent=%t\noperation=%s\nrepo=%s\nissue=%s\nauthor=%s\ncreated_at=%s\nsummary=%s\nrationale=%s\n",
		decision.ID, decision.Current(), strconv.Quote(decision.Operation), strconv.Quote(decision.Repo), strconv.Quote(decision.Issue), strconv.Quote(decision.AuthorWorker),
		decision.CreatedAt.Format(time.RFC3339), strconv.Quote(decision.Summary), strconv.Quote(decision.Rationale)); err != nil {
		return err
	}
	if decision.Dissent != "" {
		if _, err := fmt.Fprintf(out, "dissent=%s\n", strconv.Quote(decision.Dissent)); err != nil {
			return err
		}
	}
	if decision.SupersedesID != "" {
		if _, err := fmt.Fprintf(out, "supersedes=%s\n", strconv.Quote(decision.SupersedesID)); err != nil {
			return err
		}
	}
	if decision.SupersededByID != "" {
		if _, err := fmt.Fprintf(out, "superseded_by=%s\n", strconv.Quote(decision.SupersededByID)); err != nil {
			return err
		}
	}
	if decision.SupersededAt != nil {
		if _, err := fmt.Fprintf(out, "superseded_at=%s\n", decision.SupersededAt.Format(time.RFC3339)); err != nil {
			return err
		}
	}
	for _, item := range decision.Evidence {
		if _, err := fmt.Fprintf(out, "evidence=%s\tstate=%s\tdetail=%s\n", strconv.Quote(item.Ref), strconv.Quote(item.State), strconv.Quote(item.Detail)); err != nil {
			return err
		}
	}
	for _, gap := range decision.ProvenanceGaps {
		if _, err := fmt.Fprintf(out, "provenance_gap=%s\n", strconv.Quote(gap)); err != nil {
			return err
		}
	}
	return nil
}

func writeDecisionJSON(out io.Writer, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode decision JSON: %w", err)
	}
	_, err = fmt.Fprintln(out, string(data))
	return err
}

func firstDecisionNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
