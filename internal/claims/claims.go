package claims

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

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
	if cleanRepo(existing.Repo) != cleanRepo(candidate.Repo) {
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

func cleanScope(value string) string {
	value = filepath.ToSlash(filepath.Clean(strings.TrimSpace(value)))
	value = strings.TrimPrefix(value, "./")
	if value == "." {
		return ""
	}
	return strings.Trim(value, "/")
}
