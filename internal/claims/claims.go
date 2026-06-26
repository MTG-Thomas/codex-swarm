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
	if !sameRepoPath(cleanRepo(existing.Repo), cleanRepo(candidate.Repo)) {
		return false
	}
	return scopesOverlap(existing.Scope, candidate.Scope)
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

func scopesOverlap(a, b string) bool {
	a = cleanScope(a)
	b = cleanScope(b)
	if a == "" || b == "" {
		return false
	}
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
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

func cleanScope(value string) string {
	value = filepath.ToSlash(filepath.Clean(strings.TrimSpace(value)))
	value = strings.TrimPrefix(value, "./")
	if value == "." {
		return ""
	}
	return strings.Trim(value, "/")
}
