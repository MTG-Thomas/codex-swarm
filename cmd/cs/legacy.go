package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/claims"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

type legacyClaim struct {
	ID          string `json:"id"`
	Owner       string `json:"owner"`
	Repo        string `json:"repo"`
	Scope       string `json:"scope"`
	Status      string `json:"status"`
	Note        string `json:"note"`
	ReleaseNote string `json:"release_note"`
	Blocker     string `json:"blocker"`
	Next        string `json:"next_action"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
	ExpiresAt   string `json:"expires_at"`
}

func (c cli) legacy(args []string) error {
	if len(args) == 0 {
		return errors.New("legacy requires <import-coordinator>")
	}
	switch args[0] {
	case "import-coordinator":
		return c.legacyImportCoordinator(args[1:])
	default:
		return fmt.Errorf("unknown legacy command %q", args[0])
	}
}

func (c cli) legacyImportCoordinator(args []string) error {
	fs := c.flagSet("legacy import-coordinator")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	sourcePath := fs.String("source", defaultLegacyCoordinatorClaimsPath(), "legacy coordinator claims.json path")
	includeExpired := fs.Bool("include-expired", false, "import expired active/blocked claims")
	all := fs.Bool("all", false, "import released and historical claims too")
	if err := fs.Parse(args); err != nil {
		return err
	}
	data, err := os.ReadFile(*sourcePath)
	if err != nil {
		return fmt.Errorf("read legacy claims %s: %w", *sourcePath, err)
	}
	var legacy []legacyClaim
	if err := json.Unmarshal(data, &legacy); err != nil {
		return fmt.Errorf("parse legacy claims %s: %w", *sourcePath, err)
	}
	now := c.now().UTC()
	st := store.NewJSONStore(*statePath)
	imported := 0
	skipped := 0
	for _, item := range legacy {
		claim, err := mapLegacyClaim(item)
		if err != nil {
			skipped++
			continue
		}
		if !*all {
			if claim.Status != store.ClaimActive && claim.Status != store.ClaimBlocked {
				skipped++
				continue
			}
			if !*includeExpired && !claims.IsOpen(claim, now) {
				skipped++
				continue
			}
		}
		if err := st.SaveClaim(claim); err != nil {
			return err
		}
		imported++
	}
	fmt.Fprintf(c.out, "imported=%d skipped=%d source=%s state=%s\n", imported, skipped, *sourcePath, *statePath)
	return nil
}

func mapLegacyClaim(item legacyClaim) (store.Claim, error) {
	if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.Repo) == "" || strings.TrimSpace(item.Scope) == "" {
		return store.Claim{}, errors.New("legacy claim missing id, repo, or scope")
	}
	createdAt, err := parseLegacyTime(item.CreatedAt)
	if err != nil {
		return store.Claim{}, err
	}
	updatedAt := createdAt
	if strings.TrimSpace(item.UpdatedAt) != "" {
		if parsed, err := parseLegacyTime(item.UpdatedAt); err == nil {
			updatedAt = parsed
		}
	}
	expiresAt := createdAt.Add(24 * time.Hour)
	if strings.TrimSpace(item.ExpiresAt) != "" {
		if parsed, err := parseLegacyTime(item.ExpiresAt); err == nil {
			expiresAt = parsed
		}
	}
	status := store.ClaimActive
	switch strings.ToLower(strings.TrimSpace(item.Status)) {
	case "released":
		status = store.ClaimReleased
	case "blocked":
		status = store.ClaimBlocked
	}
	note := item.Note
	if item.ReleaseNote != "" {
		if note != "" {
			note += " | "
		}
		note += "release: " + item.ReleaseNote
	}
	return store.Claim{
		ID:        "legacy-" + item.ID,
		WorkerID:  item.Owner,
		Repo:      item.Repo,
		Scope:     item.Scope,
		Status:    status,
		Note:      note,
		Blocker:   item.Blocker,
		Next:      item.Next,
		ExpiresAt: expiresAt,
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}, nil
}

func parseLegacyTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, errors.New("empty legacy time")
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}

func defaultLegacyCoordinatorClaimsPath() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".codex", "agent-coordinator", "claims.json")
	}
	return filepath.Join(".codex", "agent-coordinator", "claims.json")
}
