package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/MTG-Thomas/codex-swarm/internal/readiness"
)

type issueMetadata struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

type CLIssueMetadataProvider struct{}

func NewIssueMetadataProviderFromEnv() (readiness.IssueMetadataProvider, error) {
	return newIssueMetadataProviderFromEnv(os.ReadFile)
}

func newIssueMetadataProviderFromEnv(readFile func(string) ([]byte, error)) (readiness.IssueMetadataProvider, error) {
	appID := strings.TrimSpace(os.Getenv("CODEX_SWARM_GITHUB_APP_ID"))
	clientID := strings.TrimSpace(os.Getenv("CODEX_SWARM_GITHUB_APP_CLIENT_ID"))
	keyFile := strings.TrimSpace(os.Getenv("CODEX_SWARM_GITHUB_APP_PRIVATE_KEY_FILE"))
	keyValue := strings.TrimSpace(os.Getenv("CODEX_SWARM_GITHUB_APP_PRIVATE_KEY"))
	if appID == "" && clientID == "" && keyFile == "" && keyValue == "" {
		return CLIssueMetadataProvider{}, nil
	}
	if appID == "" && clientID == "" {
		return nil, fmt.Errorf("CODEX_SWARM_GITHUB_APP_ID or CODEX_SWARM_GITHUB_APP_CLIENT_ID is required when GitHub App auth is configured")
	}
	var id int64
	var err error
	if appID != "" {
		id, err = strconv.ParseInt(appID, 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("CODEX_SWARM_GITHUB_APP_ID must be a positive integer")
		}
	}
	var key []byte
	switch {
	case keyValue != "":
		key = []byte(keyValue)
	case keyFile != "":
		key, err = readFile(keyFile)
		if err != nil {
			if errors.Is(err, os.ErrPermission) {
				return CLIssueMetadataProvider{}, nil
			}
			return nil, fmt.Errorf("read CODEX_SWARM_GITHUB_APP_PRIVATE_KEY_FILE: %w", err)
		}
	default:
		return nil, fmt.Errorf("CODEX_SWARM_GITHUB_APP_PRIVATE_KEY_FILE or CODEX_SWARM_GITHUB_APP_PRIVATE_KEY is required when GitHub App auth is configured")
	}
	var installationID int64
	if value := strings.TrimSpace(os.Getenv("CODEX_SWARM_GITHUB_APP_INSTALLATION_ID")); value != "" {
		installationID, err = strconv.ParseInt(value, 10, 64)
		if err != nil || installationID <= 0 {
			return nil, fmt.Errorf("CODEX_SWARM_GITHUB_APP_INSTALLATION_ID must be a positive integer")
		}
	}
	return NewAppIssueMetadataProvider(AppConfig{
		AppID:          id,
		Issuer:         clientID,
		InstallationID: installationID,
		PrivateKeyPEM:  key,
		APIURL:         strings.TrimSpace(os.Getenv("CODEX_SWARM_GITHUB_API_URL")),
	})
}

func (CLIssueMetadataProvider) IssueMetadata(ctx context.Context, issue string) (readiness.Issue, error) {
	ref, err := ParseIssueRef(issue)
	if err != nil {
		return readiness.Issue{}, err
	}
	cmd := exec.CommandContext(ctx, "gh", "issue", "view", fmt.Sprintf("%d", ref.Number), "--repo", ref.Owner+"/"+ref.Repo, "--json", "title,body")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return readiness.Issue{}, fmt.Errorf("read GitHub issue metadata: %s", message)
	}
	var metadata issueMetadata
	if err := json.Unmarshal(stdout.Bytes(), &metadata); err != nil {
		return readiness.Issue{}, fmt.Errorf("parse GitHub issue metadata: %w", err)
	}
	return readiness.Issue{
		Ref:   ref.String(),
		Title: metadata.Title,
		Body:  metadata.Body,
	}, nil
}
