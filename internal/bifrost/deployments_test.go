package bifrost

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDeploymentClientCreateUsesPR454Contract(t *testing.T) {
	var method, path, authorization string
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path, authorization = r.Method, r.URL.Path, r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"deployment-1","solution_id":"solution-1","state":"ready","bundle_hash":"sha256:b","compiled_manifest_hash":"sha256:m","resolution_map_hash":"sha256:r","source_artifact_key":"source","runtime_storage_prefix":"runtime","created_at":"2026-07-16T12:00:00Z"}`))
	}))
	defer server.Close()

	client := DeploymentHTTPClient{Target: server.URL, Token: "secret", HTTPClient: server.Client()}
	result, err := client.Create(context.Background(), "solution-1", SolutionDeploymentCreate{
		CompiledManifest: json.RawMessage(`{"schema_version":1}`),
		ResolutionMap:    json.RawMessage(`{"schema_version":1}`),
		BaseDeploymentID: pointer("base-1"),
		CodexWorkerID:    "worker-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ID != "deployment-1" || method != http.MethodPost || path != "/api/solutions/solution-1/deployments" {
		t.Fatalf("result=%+v method=%s path=%s", result, method, path)
	}
	if authorization != "Bearer secret" {
		t.Fatalf("authorization=%q", authorization)
	}
	if body["base_deployment_id"] != "base-1" || body["codex_worker_id"] != "worker-1" {
		t.Fatalf("body=%#v", body)
	}
}

func TestDeploymentClientReadsCapabilities(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write([]byte(`{"registration":true,"inspection":true,"artifact_upload":false,"server_side_compilation":false,"activation_configured":false,"safe_for_end_to_end_cs_deploy":false}`))
	}))
	defer server.Close()
	client := DeploymentHTTPClient{Target: server.URL, HTTPClient: server.Client()}

	capabilities, err := client.Capabilities(context.Background(), "solution-1")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/api/solutions/solution-1/deployments/capabilities" || !capabilities.Registration || capabilities.ActivationConfigured {
		t.Fatalf("path=%q capabilities=%+v", path, capabilities)
	}
}

func TestDeploymentRegistrationConflictReconcilesByGET(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"detail":{"code":"deployment_registration_conflict","message":"different closure","reconcile":"GET /api/solutions/solution-1/deployments/deployment-1"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"deployment-1","solution_id":"solution-1","state":"ready","bundle_hash":"sha256:existing","compiled_manifest_hash":"sha256:manifest","resolution_map_hash":"sha256:resolution","source_artifact_key":"source","runtime_storage_prefix":"runtime","created_at":"2026-07-16T12:00:00Z"}`))
	}))
	defer server.Close()
	client := DeploymentHTTPClient{Target: server.URL, HTTPClient: server.Client()}

	_, err := client.CreateWithReconciliation(context.Background(), "solution-1", SolutionDeploymentCreate{
		CompiledManifest: json.RawMessage(`{"deployment_id":"deployment-1"}`),
		ResolutionMap:    json.RawMessage(`{}`),
	})
	var conflict *DeploymentRegistrationConflictError
	if !errors.As(err, &conflict) || conflict.Existing.BundleHash != "sha256:existing" {
		t.Fatalf("error=%v", err)
	}
	want := []string{
		"POST /api/solutions/solution-1/deployments",
		"GET /api/solutions/solution-1/deployments/deployment-1",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls=%q want=%q", calls, want)
	}
}

func TestDeploymentRegistrationSameClosureReplayIsSuccess(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"id":"deployment-1","solution_id":"solution-1","state":"ready","bundle_hash":"sha256:same","compiled_manifest_hash":"sha256:manifest","resolution_map_hash":"sha256:resolution","source_artifact_key":"source","runtime_storage_prefix":"runtime","created_at":"2026-07-16T12:00:00Z"}`))
	}))
	defer server.Close()
	client := DeploymentHTTPClient{Target: server.URL, HTTPClient: server.Client()}

	result, err := client.CreateWithReconciliation(context.Background(), "solution-1", SolutionDeploymentCreate{
		CompiledManifest: json.RawMessage(`{"deployment_id":"deployment-1"}`),
		ResolutionMap:    json.RawMessage(`{}`),
	})
	if err != nil || result.BundleHash != "sha256:same" || calls != 1 {
		t.Fatalf("result=%+v calls=%d error=%v", result, calls, err)
	}
}

func TestDeploymentClientInspectAndPointerPaths(t *testing.T) {
	var calls []string
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.Body != nil && r.Method == http.MethodPost {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			bodies = append(bodies, body)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"id":"draft-1","solution_id":"solution-1","state":"ready","bundle_hash":"sha256:b","compiled_manifest_hash":"sha256:m","resolution_map_hash":"sha256:r","source_artifact_key":"source","runtime_storage_prefix":"runtime","created_at":"2026-07-16T12:00:00Z"}`))
			return
		}
		_, _ = w.Write([]byte(`{"deployment_id":"draft-1","solution_id":"solution-1","state":"active","previous_active_deployment_id":"base-1","active_deployment_id":"draft-1"}`))
	}))
	defer server.Close()
	client := DeploymentHTTPClient{Target: server.URL, HTTPClient: server.Client()}

	if _, err := client.Inspect(context.Background(), "solution-1", "draft-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Activate(context.Background(), "solution-1", "draft-1", pointer("base-1")); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Rollback(context.Background(), "solution-1", "draft-1", pointer("new-1")); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"GET /api/solutions/solution-1/deployments/draft-1",
		"POST /api/solutions/solution-1/deployments/draft-1/activate",
		"POST /api/solutions/solution-1/deployments/draft-1/rollback",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls=%q want=%q", calls, want)
	}
	if bodies[0]["expected_active_deployment_id"] != "base-1" || bodies[1]["expected_active_deployment_id"] != "new-1" {
		t.Fatalf("bodies=%#v", bodies)
	}
}

func TestDeploymentClientPreservesStructuredConflict(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"detail":{"operation":"activate","expected_active_deployment_id":"base","actual_active_deployment_id":"winner"}}`))
	}))
	defer server.Close()
	client := DeploymentHTTPClient{Target: server.URL, HTTPClient: server.Client()}

	_, err := client.Activate(context.Background(), "solution", "loser", pointer("base"))
	var apiErr *DeploymentAPIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusConflict {
		t.Fatalf("error=%v", err)
	}
	var detail map[string]any
	if err := json.Unmarshal(apiErr.Detail, &detail); err != nil || detail["actual_active_deployment_id"] != "winner" {
		t.Fatalf("detail=%s err=%v", apiErr.Detail, err)
	}
}

func TestDeploymentClientRequiresFullTargetURLWithoutMakingCall(t *testing.T) {
	client := DeploymentHTTPClient{Target: "dev"}
	_, err := client.Inspect(context.Background(), "solution", "deployment")
	if err == nil || err.Error() != `--target must be a full http(s) Bifrost instance URL, got "dev"` {
		t.Fatalf("error=%v", err)
	}
}

func TestPrepareDeploymentIsDeterministicAndUsesCanonicalReferences(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "workflows", "run.py"), []byte("def run():\n    return 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{"schema_version":1,"workflows":{},"agents":{},"forms":{},"events":{},"applications":{},"dependencies":{}}`)
	resolution := []byte(`{"schema_version":1,"workflows":{},"agents":{},"forms":{},"events":{},"applications":{},"dependencies":{},"sources":{"workflows/run.py":{"object_key":"placeholder","content_hash":"sha256:source"}}}`)
	input := PrepareDeploymentInput{
		Workspace: workspace, SolutionID: "solution-1", DeploymentID: "deployment-1",
		CompiledManifest: manifest, ResolutionMap: resolution,
	}
	input.OutputDir = filepath.Join(t.TempDir(), "first")
	first, err := PrepareDeployment(input)
	if err != nil {
		t.Fatal(err)
	}
	input.OutputDir = filepath.Join(t.TempDir(), "second")
	second, err := PrepareDeployment(input)
	if err != nil {
		t.Fatal(err)
	}
	if first.BundleHash != second.BundleHash || first.ManifestHash != second.ManifestHash || first.ResolutionMapHash != second.ResolutionMapHash {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	if first.SourceArtifactKey != "_solution_artifacts/solution-1/deployment-1/source.zip" || first.RuntimeStoragePrefix != "_solutions/solution-1/deployment-1/" {
		t.Fatalf("prepared=%+v", first)
	}
	if !first.UploadRequired {
		t.Fatal("prepare must report upload_required")
	}
	wantObjects := []string{
		"_solution_artifacts/solution-1/deployment-1/source.zip",
		"_solutions/solution-1/deployment-1/workflows/run.py",
	}
	if !reflect.DeepEqual(first.RequiredObjectKeys, wantObjects) {
		t.Fatalf("required objects=%q want=%q", first.RequiredObjectKeys, wantObjects)
	}
	var preparedResolution map[string]any
	if err := json.Unmarshal(first.Create.ResolutionMap, &preparedResolution); err != nil {
		t.Fatal(err)
	}
	sources := preparedResolution["sources"].(map[string]any)
	source := sources["workflows/run.py"].(map[string]any)
	if source["object_key"] != wantObjects[1] {
		t.Fatalf("source=%#v", source)
	}
}

func pointer(value string) *string { return &value }
