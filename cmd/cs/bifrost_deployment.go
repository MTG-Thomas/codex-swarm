package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	bf "github.com/MTG-Thomas/codex-swarm/internal/bifrost"
)

func (c cli) bifrostDeployment(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: cs bifrost deployment <prepare|capabilities|register|inspect|activate|rollback>")
	}
	fs := c.flagSet("bifrost deployment " + args[0])
	target := fs.String("target", "", "full Bifrost instance URL")
	solutionID := fs.String("solution", "", "Solution install UUID")
	deploymentID := fs.String("deployment", "", "SolutionDeployment UUID")
	expectedActive := fs.String("expected-active", "", "active deployment UUID expected by compare-and-swap")
	workspace := fs.String("workspace", ".", "local workspace directory to package")
	manifestPath := fs.String("compiled-manifest", "", "Bifrost-compiled manifest JSON")
	resolutionPath := fs.String("resolution-map", "", "Bifrost-compiled resolution map JSON")
	outputDir := fs.String("out", "", "prepare output directory")
	preparedPath := fs.String("prepared", "", "prepared-deployment.json from prepare")
	artifactsUploaded := fs.Bool("artifacts-uploaded", false, "confirm source/runtime objects already exist at prepared canonical keys")
	experimental := fs.Bool("experimental-solution-deployments", false, "acknowledge PR #454 transport/activation capabilities are incomplete")
	baseID := fs.String("base", "", "base deployment UUID shared by concurrent drafts")
	parentID := fs.String("parent", "", "parent deployment UUID")
	version := fs.String("version", "", "declared Solution version")
	worker := fs.String("worker", "", "codex-swarm worker ID recorded as provenance")
	gitRepository := fs.String("git-repository", "", "source Git repository")
	gitRef := fs.String("git-ref", "", "resolved Git ref")
	gitCommit := fs.String("git-commit", "", "resolved Git commit SHA")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	switch args[0] {
	case "prepare":
		if *solutionID == "" || *manifestPath == "" || *resolutionPath == "" || *outputDir == "" {
			return errors.New("prepare requires --solution, --compiled-manifest, --resolution-map, and --out")
		}
		manifest, err := os.ReadFile(*manifestPath)
		if err != nil {
			return fmt.Errorf("read compiled manifest: %w", err)
		}
		resolution, err := os.ReadFile(*resolutionPath)
		if err != nil {
			return fmt.Errorf("read resolution map: %w", err)
		}
		prepared, err := bf.PrepareDeployment(bf.PrepareDeploymentInput{
			Workspace: *workspace, OutputDir: *outputDir, SolutionID: *solutionID, DeploymentID: *deploymentID,
			CompiledManifest: manifest, ResolutionMap: resolution, BaseDeploymentID: optionalString(*baseID),
			ParentDeploymentID: optionalString(*parentID), DeclaredVersion: *version, CodexWorkerID: *worker,
			GitRepository: *gitRepository, GitRef: *gitRef, GitCommitSHA: *gitCommit,
		})
		if err != nil {
			return err
		}
		preparedFile := filepath.Join(*outputDir, "prepared-deployment.json")
		data, err := json.MarshalIndent(prepared, "", "  ")
		if err != nil {
			return fmt.Errorf("encode prepared deployment: %w", err)
		}
		if err := os.WriteFile(preparedFile, append(data, '\n'), 0o644); err != nil {
			return fmt.Errorf("write prepared deployment: %w", err)
		}
		return writeBifrostJSON(c.out, prepared)
	case "register":
		if *target == "" || *solutionID == "" || *preparedPath == "" {
			return errors.New("register requires --target, --solution, and --prepared")
		}
		if !*artifactsUploaded {
			return errors.New("registration stopped: PR #454 has no source/runtime upload transport; upload prepared artifacts to their canonical keys, then rerun with --artifacts-uploaded")
		}
		if !*experimental {
			return errors.New("register is experimental and requires --experimental-solution-deployments; PR #454 does not provide artifact upload")
		}
		prepared, err := readPreparedDeployment(*preparedPath)
		if err != nil {
			return err
		}
		if prepared.SolutionID != *solutionID {
			return fmt.Errorf("prepared deployment belongs to solution %s, not %s", prepared.SolutionID, *solutionID)
		}
		client, err := deploymentClient(*target)
		if err != nil {
			return err
		}
		capabilities, err := client.Capabilities(context.Background(), *solutionID)
		if err != nil {
			return fmt.Errorf("check SolutionDeployment capabilities: %w", err)
		}
		if !capabilities.Registration {
			return errors.New("server capabilities report SolutionDeployment registration is disabled")
		}
		result, err := client.CreateWithReconciliation(context.Background(), *solutionID, prepared.Create)
		if err != nil {
			return err
		}
		return writeBifrostJSON(c.out, result)
	case "capabilities":
		if *target == "" || *solutionID == "" {
			return errors.New("capabilities requires --target and --solution")
		}
		client, err := deploymentClient(*target)
		if err != nil {
			return err
		}
		result, err := client.Capabilities(context.Background(), *solutionID)
		if err != nil {
			return err
		}
		return writeBifrostJSON(c.out, result)
	case "inspect":
		if *target == "" || *solutionID == "" || *deploymentID == "" {
			return errors.New("inspect requires --target, --solution, and --deployment")
		}
		client, err := deploymentClient(*target)
		if err != nil {
			return err
		}
		result, err := client.Inspect(context.Background(), *solutionID, *deploymentID)
		if err != nil {
			return err
		}
		return writeBifrostJSON(c.out, result)
	case "activate", "rollback":
		if *target == "" || *solutionID == "" || *deploymentID == "" {
			return fmt.Errorf("%s requires --target, --solution, and --deployment", args[0])
		}
		if !*experimental {
			return fmt.Errorf("%s is experimental and requires --experimental-solution-deployments; PR #454 activation hooks may return 503", args[0])
		}
		expected := optionalString(*expectedActive)
		if args[0] == "rollback" && expected == nil {
			return errors.New("rollback requires --expected-active")
		}
		client, err := deploymentClient(*target)
		if err != nil {
			return err
		}
		capabilities, err := client.Capabilities(context.Background(), *solutionID)
		if err != nil {
			return fmt.Errorf("check SolutionDeployment capabilities: %w", err)
		}
		if !capabilities.ActivationConfigured {
			return fmt.Errorf("server capabilities report SolutionDeployment %s is unavailable (activation_configured=false)", args[0])
		}
		var result bf.DeploymentActivation
		if args[0] == "activate" {
			result, err = client.Activate(context.Background(), *solutionID, *deploymentID, expected)
		} else {
			result, err = client.Rollback(context.Background(), *solutionID, *deploymentID, expected)
		}
		if err != nil {
			return err
		}
		return writeBifrostJSON(c.out, result)
	default:
		return fmt.Errorf("unknown bifrost deployment command %q", args[0])
	}
}

func deploymentClient(target string) (bf.DeploymentHTTPClient, error) {
	token := strings.TrimSpace(os.Getenv("BIFROST_ACCESS_TOKEN"))
	if token == "" {
		return bf.DeploymentHTTPClient{}, errors.New("BIFROST_ACCESS_TOKEN is required for SolutionDeployment API access; obtain a short-lived token without storing it in codex-swarm")
	}
	return bf.DeploymentHTTPClient{
		Target:     target,
		Token:      token,
		HTTPClient: &http.Client{Timeout: 2 * time.Minute},
	}, nil
}

func optionalString(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	value = strings.TrimSpace(value)
	return &value
}

func readPreparedDeployment(path string) (bf.PreparedDeployment, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return bf.PreparedDeployment{}, fmt.Errorf("read prepared deployment: %w", err)
	}
	var prepared bf.PreparedDeployment
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&prepared); err != nil {
		return bf.PreparedDeployment{}, fmt.Errorf("decode prepared deployment: %w", err)
	}
	return prepared, nil
}
