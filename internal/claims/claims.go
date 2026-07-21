package claims

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

const externalWorkerMarker = "[external]"

func IsOpen(claim store.Claim, now time.Time) bool {
	if claim.Status != store.ClaimActive && claim.Status != store.ClaimBlocked {
		return false
	}
	if claim.ExpiresAt.IsZero() {
		return true
	}
	return claim.ExpiresAt.After(now)
}

func Conflicts(existing, candidate store.Claim, now time.Time) bool {
	if existing.ID == candidate.ID || !IsOpen(existing, now) {
		return false
	}
	if sameIssue(existing.Issue, candidate.Issue) {
		return scopesOverlap(existing, candidate)
	}
	if !sameRepoPath(cleanRepo(existing.Repo), cleanRepo(candidate.Repo)) {
		return false
	}
	return scopesOverlap(existing, candidate)
}

func FindConflicts(all []store.Claim, candidate store.Claim, now time.Time) []store.Claim {
	var conflicts []store.Claim
	for _, claim := range all {
		if Conflicts(claim, candidate, now) {
			conflicts = append(conflicts, claim)
		}
	}
	return conflicts
}

func ValidateWorkerID(workerID string, workers []store.Worker) error {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return errors.New("claim create requires --worker")
	}
	for _, worker := range workers {
		if worker.ID == workerID {
			return nil
		}
	}
	return fmt.Errorf("worker %q not found", workerID)
}

func ValidateWorkerForRepo(workerID, repo string, workers []store.Worker) error {
	worker, err := findWorker(workerID, workers)
	if err != nil {
		return err
	}
	workerRepo := cleanRepo(worker.ProjectRoot)
	claimRepo := cleanRepo(repo)
	if workerRepo == "" || claimRepo == "" || !sameRepoPath(workerRepo, claimRepo) {
		return fmt.Errorf("worker %q is for repo %q, not %q", worker.ID, worker.ProjectRoot, repo)
	}
	return nil
}

func WorkerMatchesRepo(worker store.Worker, repo string) bool {
	workerRepo := cleanRepo(worker.ProjectRoot)
	claimRepo := cleanRepo(repo)
	return workerRepo != "" && claimRepo != "" && sameRepoPath(workerRepo, claimRepo)
}

func MarkExternalWorker(claim store.Claim) store.Claim {
	return MarkExternalWorkerWithSource(claim, "external")
}

func MarkExternalWorkerWithSource(claim store.Claim, source string) store.Claim {
	if strings.TrimSpace(claim.WorkerID) == "" {
		return claim
	}
	claim.ExternalWorker = true
	if strings.TrimSpace(claim.WorkerSource) == "" {
		claim.WorkerSource = strings.TrimSpace(source)
	}
	return claim
}

func IsExternalWorker(claim store.Claim) bool {
	return claim.ExternalWorker || strings.Contains(claim.Note, externalWorkerMarker)
}

func findWorker(workerID string, workers []store.Worker) (store.Worker, error) {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return store.Worker{}, errors.New("claim create requires --worker")
	}
	for _, worker := range workers {
		if worker.ID == workerID {
			return worker, nil
		}
	}
	return store.Worker{}, fmt.Errorf("worker %q not found", workerID)
}

func scopesOverlap(a, b store.Claim) bool {
	aKind, aValue := splitScope(a.ScopeKind, a.Scope)
	bKind, bValue := splitScope(b.ScopeKind, b.Scope)
	if aKind != bKind || aValue == "" || bValue == "" {
		return false
	}
	if aKind == store.ClaimScopeTask {
		return strings.EqualFold(aValue, bValue)
	}
	return equalScopeValue(aValue, bValue) || scopeHasPrefix(aValue, bValue) || scopeHasPrefix(bValue, aValue)
}

// NormalizeScope validates and canonicalizes a new typed claim scope.
func NormalizeScope(kind store.ClaimScopeKind, value string) (store.ClaimScopeKind, string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", errors.New("claim scope is required")
	}
	if strings.Contains(value, ",") {
		return "", "", errors.New("claim scope must contain one value; repeat --scope instead of using commas")
	}
	if inferred, remainder, ok := strings.Cut(value, ":"); ok {
		inferredKind := store.ClaimScopeKind(strings.ToLower(strings.TrimSpace(inferred)))
		if validScopeKind(inferredKind) {
			if kind != "" && kind != store.ClaimScopePath && kind != inferredKind {
				return "", "", fmt.Errorf("claim scope prefix %q conflicts with --kind %q", inferredKind, kind)
			}
			kind = inferredKind
			value = remainder
		}
	}
	if kind == "" {
		kind = store.ClaimScopePath
	}
	if !validScopeKind(kind) {
		return "", "", fmt.Errorf("unsupported claim scope kind %q", kind)
	}
	value = cleanScopeValue(kind, value)
	if value == "" {
		return "", "", errors.New("claim scope value is required")
	}
	if kind == store.ClaimScopePath && (filepath.IsAbs(value) || value == ".." || strings.HasPrefix(value, "../")) {
		return "", "", fmt.Errorf("path claim scope must be repository-relative: %q", value)
	}
	return kind, value, nil
}

// ScopeLabel renders a stable typed scope for operator output.
func ScopeLabel(claim store.Claim) string {
	kind, value := splitScope(claim.ScopeKind, claim.Scope)
	return string(kind) + ":" + value
}

func splitScope(kind store.ClaimScopeKind, value string) (store.ClaimScopeKind, string) {
	value = strings.TrimSpace(value)
	if kind == "" {
		if prefix, remainder, ok := strings.Cut(value, ":"); ok {
			candidate := store.ClaimScopeKind(strings.ToLower(strings.TrimSpace(prefix)))
			if validScopeKind(candidate) {
				kind = candidate
				value = remainder
			}
		}
	}
	if kind == "" {
		kind = store.ClaimScopePath
	}
	return kind, cleanScopeValue(kind, value)
}

func validScopeKind(kind store.ClaimScopeKind) bool {
	return kind == store.ClaimScopePath || kind == store.ClaimScopeTask || kind == store.ClaimScopeLive
}

func cleanScopeValue(kind store.ClaimScopeKind, value string) string {
	value = strings.TrimSpace(value)
	if kind == store.ClaimScopePath {
		return cleanScope(value)
	}
	value = filepath.ToSlash(value)
	value = strings.Trim(value, "/")
	return value
}

func equalScopeValue(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func scopeHasPrefix(value, prefix string) bool {
	if runtime.GOOS == "windows" {
		value = strings.ToLower(value)
		prefix = strings.ToLower(prefix)
	}
	return strings.HasPrefix(value, prefix+"/")
}

func cleanRepo(value string) string {
	if value == "" {
		return ""
	}
	cleaned, err := filepath.Abs(value)
	if err != nil {
		return filepath.Clean(value)
	}
	return filepath.Clean(cleaned)
}

func sameRepoPath(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func sameIssue(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	return a != "" && b != "" && strings.EqualFold(a, b)
}

func cleanScope(value string) string {
	value = filepath.ToSlash(filepath.Clean(strings.TrimSpace(value)))
	value = strings.TrimPrefix(value, "./")
	if value == "." {
		return ""
	}
	return strings.Trim(value, "/")
}
