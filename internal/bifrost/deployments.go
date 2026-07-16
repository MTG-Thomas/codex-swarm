package bifrost

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
)

type SolutionDeploymentCreate struct {
	CompiledManifest   json.RawMessage `json:"compiled_manifest"`
	ResolutionMap      json.RawMessage `json:"resolution_map"`
	BaseDeploymentID   *string         `json:"base_deployment_id,omitempty"`
	ParentDeploymentID *string         `json:"parent_deployment_id,omitempty"`
	DeclaredVersion    string          `json:"declared_version,omitempty"`
	GitRepository      string          `json:"git_repository,omitempty"`
	GitRef             string          `json:"git_ref,omitempty"`
	GitCommitSHA       string          `json:"git_commit_sha,omitempty"`
	CodexWorkerID      string          `json:"codex_worker_id,omitempty"`
}

type SolutionDeployment struct {
	ID                   string          `json:"id"`
	OrganizationID       *string         `json:"organization_id"`
	SolutionID           string          `json:"solution_id"`
	ParentDeploymentID   *string         `json:"parent_deployment_id"`
	BaseDeploymentID     *string         `json:"base_deployment_id"`
	State                string          `json:"state"`
	BundleHash           string          `json:"bundle_hash"`
	CompiledManifestHash string          `json:"compiled_manifest_hash"`
	ResolutionMapHash    string          `json:"resolution_map_hash"`
	SourceArtifactKey    string          `json:"source_artifact_key"`
	RuntimeStoragePrefix string          `json:"runtime_storage_prefix"`
	CreatedAt            time.Time       `json:"created_at"`
	FailureDetail        json.RawMessage `json:"failure_detail,omitempty"`
}

type SolutionDeploymentCapabilities struct {
	Registration            bool `json:"registration"`
	Inspection              bool `json:"inspection"`
	ArtifactUpload          bool `json:"artifact_upload"`
	ServerSideCompilation   bool `json:"server_side_compilation"`
	ActivationConfigured    bool `json:"activation_configured"`
	SafeForEndToEndCSDeploy bool `json:"safe_for_end_to_end_cs_deploy"`
}

type DeploymentActivation struct {
	DeploymentID               string          `json:"deployment_id"`
	SolutionID                 string          `json:"solution_id"`
	State                      string          `json:"state"`
	PreviousActiveDeploymentID *string         `json:"previous_active_deployment_id"`
	ActiveDeploymentID         *string         `json:"active_deployment_id"`
	Conflict                   json.RawMessage `json:"conflict,omitempty"`
	Recovery                   json.RawMessage `json:"recovery,omitempty"`
}

type DeploymentAPIError struct {
	Status int
	Detail json.RawMessage
}

type DeploymentRegistrationConflictError struct {
	API           *DeploymentAPIError
	Existing      SolutionDeployment
	ReconcilePath string
}

func (e *DeploymentRegistrationConflictError) Error() string {
	return fmt.Sprintf(
		"deployment registration conflicts with existing deployment %s (bundle=%s manifest=%s resolution=%s); reconcile with %s",
		e.Existing.ID, e.Existing.BundleHash, e.Existing.CompiledManifestHash,
		e.Existing.ResolutionMapHash, e.ReconcilePath,
	)
}

func (e *DeploymentRegistrationConflictError) Unwrap() error { return e.API }

func (e *DeploymentAPIError) Error() string {
	return fmt.Sprintf("Bifrost deployment API returned HTTP %d: %s", e.Status, strings.TrimSpace(string(e.Detail)))
}

type DeploymentHTTPClient struct {
	Target     string
	Token      string
	HTTPClient *http.Client
}

func (c DeploymentHTTPClient) Create(ctx context.Context, solutionID string, body SolutionDeploymentCreate) (SolutionDeployment, error) {
	var result SolutionDeployment
	err := c.call(ctx, http.MethodPost, "/api/solutions/"+url.PathEscape(solutionID)+"/deployments", body, &result)
	return result, err
}

func (c DeploymentHTTPClient) Capabilities(ctx context.Context, solutionID string) (SolutionDeploymentCapabilities, error) {
	var result SolutionDeploymentCapabilities
	err := c.call(ctx, http.MethodGet, "/api/solutions/"+url.PathEscape(solutionID)+"/deployments/capabilities", nil, &result)
	return result, err
}

func (c DeploymentHTTPClient) CreateWithReconciliation(ctx context.Context, solutionID string, body SolutionDeploymentCreate) (SolutionDeployment, error) {
	created, err := c.Create(ctx, solutionID, body)
	if err == nil {
		return created, nil
	}
	var apiErr *DeploymentAPIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusConflict {
		return SolutionDeployment{}, err
	}
	var detail struct {
		Code      string `json:"code"`
		Reconcile string `json:"reconcile"`
	}
	if json.Unmarshal(apiErr.Detail, &detail) != nil || detail.Code != "deployment_registration_conflict" {
		return SolutionDeployment{}, err
	}
	deploymentID, extractErr := deploymentIDFromManifest(body.CompiledManifest)
	if extractErr != nil {
		return SolutionDeployment{}, fmt.Errorf("reconcile deployment registration: %w", extractErr)
	}
	existing, inspectErr := c.Inspect(ctx, solutionID, deploymentID)
	if inspectErr != nil {
		return SolutionDeployment{}, fmt.Errorf("registration conflict; reconciliation GET failed: %w", inspectErr)
	}
	return SolutionDeployment{}, &DeploymentRegistrationConflictError{
		API: apiErr, Existing: existing, ReconcilePath: detail.Reconcile,
	}
}

func deploymentIDFromManifest(raw json.RawMessage) (string, error) {
	var manifest struct {
		DeploymentID string `json:"deployment_id"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return "", err
	}
	if manifest.DeploymentID == "" {
		return "", errors.New("compiled manifest has no deployment_id")
	}
	return manifest.DeploymentID, nil
}

func (c DeploymentHTTPClient) Inspect(ctx context.Context, solutionID, deploymentID string) (SolutionDeployment, error) {
	var result SolutionDeployment
	err := c.call(ctx, http.MethodGet, "/api/solutions/"+url.PathEscape(solutionID)+"/deployments/"+url.PathEscape(deploymentID), nil, &result)
	return result, err
}

func (c DeploymentHTTPClient) Activate(ctx context.Context, solutionID, deploymentID string, expected *string) (DeploymentActivation, error) {
	return c.movePointer(ctx, solutionID, deploymentID, "activate", expected)
}

func (c DeploymentHTTPClient) Rollback(ctx context.Context, solutionID, deploymentID string, expected *string) (DeploymentActivation, error) {
	if expected == nil || strings.TrimSpace(*expected) == "" {
		return DeploymentActivation{}, errors.New("rollback requires expected active deployment ID")
	}
	return c.movePointer(ctx, solutionID, deploymentID, "rollback", expected)
}

func (c DeploymentHTTPClient) movePointer(ctx context.Context, solutionID, deploymentID, action string, expected *string) (DeploymentActivation, error) {
	var result DeploymentActivation
	body := struct {
		Expected *string `json:"expected_active_deployment_id"`
	}{Expected: expected}
	path := "/api/solutions/" + url.PathEscape(solutionID) + "/deployments/" + url.PathEscape(deploymentID) + "/" + action
	err := c.call(ctx, http.MethodPost, path, body, &result)
	return result, err
}

func (c DeploymentHTTPClient) call(ctx context.Context, method, path string, body, out any) error {
	base, err := validateTargetURL(c.Target)
	if err != nil {
		return err
	}
	var payload io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode deployment request: %w", err)
		}
		payload = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(base.String(), "/")+path, payload)
	if err != nil {
		return fmt.Errorf("create deployment request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("call Bifrost deployment API at %s: %w", base.Redacted(), err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("read Bifrost deployment response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var envelope struct {
			Detail json.RawMessage `json:"detail"`
		}
		if json.Unmarshal(data, &envelope) != nil || len(envelope.Detail) == 0 {
			envelope.Detail = json.RawMessage(strconvQuote(strings.TrimSpace(string(data))))
		}
		return &DeploymentAPIError{Status: resp.StatusCode, Detail: envelope.Detail}
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode Bifrost deployment response: %w", err)
	}
	return nil
}

func validateTargetURL(value string) (*url.URL, error) {
	if strings.TrimSpace(value) == "" {
		return nil, errors.New("--target requires a full Bifrost instance URL")
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("--target must be a full http(s) Bifrost instance URL, got %q", value)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("--target must not contain credentials, query parameters, or a fragment")
	}
	return parsed, nil
}

func strconvQuote(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

type PreparedDeployment struct {
	SchemaVersion        int                      `json:"schema_version"`
	SolutionID           string                   `json:"solution_id"`
	DeploymentID         string                   `json:"deployment_id"`
	SourceArchive        string                   `json:"source_archive"`
	SourceArtifactKey    string                   `json:"source_artifact_key"`
	RuntimeStoragePrefix string                   `json:"runtime_storage_prefix"`
	BundleHash           string                   `json:"bundle_hash"`
	ManifestHash         string                   `json:"compiled_manifest_hash"`
	ResolutionMapHash    string                   `json:"resolution_map_hash"`
	UploadRequired       bool                     `json:"upload_required"`
	RequiredObjectKeys   []string                 `json:"required_object_keys"`
	Create               SolutionDeploymentCreate `json:"create"`
}

type PrepareDeploymentInput struct {
	Workspace, OutputDir, SolutionID, DeploymentID string
	CompiledManifest, ResolutionMap                []byte
	BaseDeploymentID, ParentDeploymentID           *string
	DeclaredVersion, GitRepository, GitRef         string
	GitCommitSHA, CodexWorkerID                    string
}

func PrepareDeployment(input PrepareDeploymentInput) (PreparedDeployment, error) {
	if strings.TrimSpace(input.SolutionID) == "" {
		return PreparedDeployment{}, errors.New("solution ID is required")
	}
	if strings.TrimSpace(input.Workspace) == "" || strings.TrimSpace(input.OutputDir) == "" {
		return PreparedDeployment{}, errors.New("workspace and output directory are required")
	}
	deploymentID := strings.TrimSpace(input.DeploymentID)
	if deploymentID == "" {
		var err error
		deploymentID, err = randomUUID()
		if err != nil {
			return PreparedDeployment{}, err
		}
	}
	if len(input.CompiledManifest) == 0 || len(input.ResolutionMap) == 0 {
		return PreparedDeployment{}, errors.New("compiled manifest and resolution map are required; cs does not invent runtime entity definitions")
	}
	manifest, err := decodeObject(input.CompiledManifest)
	if err != nil {
		return PreparedDeployment{}, fmt.Errorf("compiled manifest: %w", err)
	}
	resolution, err := decodeObject(input.ResolutionMap)
	if err != nil {
		return PreparedDeployment{}, fmt.Errorf("resolution map: %w", err)
	}
	artifactKey := fmt.Sprintf("_solution_artifacts/%s/%s/source.zip", input.SolutionID, deploymentID)
	runtimePrefix := fmt.Sprintf("_solutions/%s/%s/", input.SolutionID, deploymentID)
	requiredKeys, err := anchorRuntimeSources(resolution, runtimePrefix)
	if err != nil {
		return PreparedDeployment{}, err
	}
	resolutionCanonical, err := canonicalJSON(resolution)
	if err != nil {
		return PreparedDeployment{}, err
	}
	resolutionHash := digest(resolutionCanonical)
	if err := validateManifestResolution(manifest, resolution); err != nil {
		return PreparedDeployment{}, err
	}
	outputDir, err := filepath.Abs(input.OutputDir)
	if err != nil {
		return PreparedDeployment{}, fmt.Errorf("resolve output directory: %w", err)
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return PreparedDeployment{}, fmt.Errorf("create output directory: %w", err)
	}
	archivePath := filepath.Join(outputDir, "source.zip")
	archive, err := deterministicZip(input.Workspace, archivePath)
	if err != nil {
		return PreparedDeployment{}, err
	}
	bundleHash := digest(archive)
	manifest["schema_version"] = 1
	manifest["solution_id"] = input.SolutionID
	manifest["deployment_id"] = deploymentID
	manifest["bundle_hash"] = bundleHash
	manifest["resolution_map_hash"] = resolutionHash
	manifest["source"] = map[string]any{"artifact_key": artifactKey, "runtime_prefix": runtimePrefix}
	manifestCanonical, err := canonicalJSON(manifest)
	if err != nil {
		return PreparedDeployment{}, err
	}
	return PreparedDeployment{
		SchemaVersion: 1, SolutionID: input.SolutionID, DeploymentID: deploymentID,
		SourceArchive: archivePath, SourceArtifactKey: artifactKey, RuntimeStoragePrefix: runtimePrefix,
		BundleHash: bundleHash, ManifestHash: digest(manifestCanonical), ResolutionMapHash: resolutionHash,
		UploadRequired: true, RequiredObjectKeys: append([]string{artifactKey}, requiredKeys...),
		Create: SolutionDeploymentCreate{
			CompiledManifest: manifestCanonical, ResolutionMap: resolutionCanonical,
			BaseDeploymentID: input.BaseDeploymentID, ParentDeploymentID: input.ParentDeploymentID,
			DeclaredVersion: input.DeclaredVersion, GitRepository: input.GitRepository, GitRef: input.GitRef,
			GitCommitSHA: input.GitCommitSHA, CodexWorkerID: input.CodexWorkerID,
		},
	}, nil
}

func anchorRuntimeSources(resolution map[string]any, runtimePrefix string) ([]string, error) {
	raw, ok := resolution["sources"]
	if !ok {
		resolution["sources"] = map[string]any{}
		return nil, nil
	}
	sources, ok := raw.(map[string]any)
	if !ok {
		return nil, errors.New("resolution map sources must be an object")
	}
	keys := make([]string, 0, len(sources))
	for sourceRef, rawSource := range sources {
		clean := path.Clean(strings.ReplaceAll(sourceRef, "\\", "/"))
		if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) {
			return nil, fmt.Errorf("resolution source reference must be relative: %q", sourceRef)
		}
		source, ok := rawSource.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("resolution source %q must be an object", sourceRef)
		}
		key := runtimePrefix + clean
		source["object_key"] = key
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys, nil
}

func validateManifestResolution(manifest, resolution map[string]any) error {
	for _, field := range []string{"workflows", "agents", "forms", "events", "applications", "dependencies"} {
		manifestValue, manifestOK := manifest[field]
		resolutionValue, resolutionOK := resolution[field]
		if !manifestOK {
			manifestValue = map[string]any{}
		}
		if !resolutionOK {
			resolutionValue = map[string]any{}
		}
		if !reflect.DeepEqual(manifestValue, resolutionValue) {
			return fmt.Errorf("compiled manifest and resolution map disagree for %s", field)
		}
	}
	return nil
}

func decodeObject(data []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := ensureEOF(decoder); err != nil {
		return nil, err
	}
	return value, nil
}

func ensureEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func canonicalJSON(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, fmt.Errorf("encode canonical JSON: %w", err)
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func randomUUID() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("generate deployment ID: %w", err)
	}
	data[6] = (data[6] & 0x0f) | 0x40
	data[8] = (data[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", data[0:4], data[4:6], data[6:8], data[8:10], data[10:16]), nil
}

func deterministicZip(root, excludedOutput string) ([]byte, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("workspace must be a directory: %s", root)
	}
	excludedOutput, _ = filepath.Abs(excludedOutput)
	excludedDir := filepath.Dir(excludedOutput)
	preparedOutput := filepath.Join(excludedDir, "prepared-deployment.json")
	var files []string
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == excludedOutput || path == preparedOutput {
			return nil
		}
		if path == excludedDir && path != root && entry.IsDir() {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() && (rel == ".git" || strings.HasPrefix(filepath.ToSlash(rel), ".git/")) {
			return filepath.SkipDir
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("deployment source contains symlink %s", rel)
		}
		if !entry.IsDir() {
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan deployment workspace: %w", err)
	}
	sort.Strings(files)
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, rel := range files {
		content, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			return nil, fmt.Errorf("read deployment source %s: %w", rel, err)
		}
		header := &zip.FileHeader{Name: filepath.ToSlash(rel), Method: zip.Deflate}
		header.SetMode(0o644)
		header.SetModTime(time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC))
		entry, err := writer.CreateHeader(header)
		if err != nil {
			return nil, err
		}
		if _, err := entry.Write(content); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	if err := os.WriteFile(excludedOutput, buffer.Bytes(), 0o644); err != nil {
		return nil, fmt.Errorf("write source archive: %w", err)
	}
	return buffer.Bytes(), nil
}
