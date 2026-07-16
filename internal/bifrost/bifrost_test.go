package bifrost

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

type runnerCall struct {
	name      string
	args, env []string
}
type fakeRunner struct {
	responses [][]byte
	errors    []error
	calls     []runnerCall
}

func (f *fakeRunner) Run(_ context.Context, name string, args []string, _ []byte, env []string) ([]byte, error) {
	f.calls = append(f.calls, runnerCall{name, args, env})
	out := f.responses[0]
	f.responses = f.responses[1:]
	if len(f.errors) == 0 {
		return out, nil
	}
	err := f.errors[0]
	f.errors = f.errors[1:]
	return out, err
}

func TestClientPreservesStructuredConflict(t *testing.T) {
	r := &fakeRunner{
		responses: [][]byte{[]byte(`{"detail":{"reason":"revision_mismatch","current_revision":"new","base_revision":"old","conflicting_paths":["features/a.py"]}}`)},
		errors:    []error{errors.New("exit status 1")},
	}
	_, err := NewClient(r).Commit(context.Background(), "c1", "message", false)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("Commit() error = %v, want APIError", err)
	}
	if apiErr.Conflict.CurrentRevision != "new" || len(apiErr.Conflict.ConflictingPaths) != 1 {
		t.Fatalf("conflict = %#v", apiErr.Conflict)
	}
}

type fakeStore struct {
	records map[string]Record
	saveErr error
}

func (f *fakeStore) SaveBifrostChangeset(r Record) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	if f.records == nil {
		f.records = map[string]Record{}
	}
	f.records[r.ID] = r
	return nil
}

func TestClientStagesFileThroughCentralContract(t *testing.T) {
	r := &fakeRunner{responses: [][]byte{[]byte(`{"id":"c1","status":"staged"}`)}}
	c := NewClient(r)
	_, err := c.Stage(context.Background(), "c1", FileMutation{Path: "features/a.py", Operation: "write", ContentBase64: "eA==", ExpectedHash: "hash"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"api", "POST", DefaultBasePath + "/c1/files", `{"path":"features/a.py","operation":"write","content_base64":"eA==","expected_hash":"hash"}`}
	if !reflect.DeepEqual(r.calls[0].args, want) {
		t.Fatalf("stage args=%q, want %q", r.calls[0].args, want)
	}
}

func TestServiceUsesRecordedTargetAndRejectsMismatch(t *testing.T) {
	record := Record{ID: "c1", RemoteChangesetID: "remote-1", Target: "https://recorded.example", State: "open"}
	store := &fakeStore{records: map[string]Record{"c1": record}}
	r := &fakeRunner{responses: [][]byte{[]byte(`{"id":"remote-1","status":"open"}`)}}
	client := NewClient(r)
	if _, err := (Service{Client: client, Store: store}).Show(context.Background(), "c1"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.calls[0].env, []string{"BIFROST_API_URL=https://recorded.example"}) {
		t.Fatalf("env=%q", r.calls[0].env)
	}

	client.Target = "https://other.example"
	if _, err := (Service{Client: client, Store: store}).Show(context.Background(), "c1"); err == nil {
		t.Fatal("target mismatch error = nil")
	}
}

func TestBeginAbortsRemoteChangesetWhenLocalPersistenceFails(t *testing.T) {
	r := &fakeRunner{responses: [][]byte{[]byte(`{"id":"c1","status":"open","base_revision":"rev1"}`), []byte(`{"id":"c1","status":"aborted"}`)}}
	svc := Service{Client: NewClient(r), Store: &fakeStore{saveErr: errors.New("disk full")}}
	if _, err := svc.Begin(context.Background(), "features", "rev1", "fix", "w1"); err == nil {
		t.Fatal("Begin() error = nil")
	}
	if len(r.calls) != 2 || r.calls[1].args[2] != DefaultBasePath+"/c1/abort" {
		t.Fatalf("calls=%#v, want compensating abort", r.calls)
	}
}
func (f *fakeStore) GetBifrostChangeset(id string) (Record, error) { return f.records[id], nil }
func (f *fakeStore) ListBifrostChangesets() ([]Record, error) {
	var v []Record
	for _, r := range f.records {
		v = append(v, r)
	}
	return v, nil
}

func TestClientUsesCentralChangesetContract(t *testing.T) {
	r := &fakeRunner{responses: [][]byte{[]byte(`{"id":"c1","status":"open","base_revision":"rev1"}`), []byte(`{"id":"c1","status":"committed","commit_sha":"abc"}`)}}
	c := NewClient(r)
	c.Target = "dev"
	got, err := c.Begin(context.Background(), "features/ninja", "rev1", "fix", "w1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "c1" {
		t.Fatalf("id=%q", got.ID)
	}
	_, err = c.Commit(context.Background(), "c1", "fix ninja", true)
	if err != nil {
		t.Fatal(err)
	}
	wantBegin := []string{"api", "POST", DefaultBasePath, `{"base_revision":"rev1","scope":"features/ninja","title":"fix","worker_id":"w1"}`}
	if !reflect.DeepEqual(r.calls[0].args, wantBegin) {
		t.Fatalf("begin args=%q", r.calls[0].args)
	}
	wantCommit := []string{"api", "POST", DefaultBasePath + "/c1/activate", `{"commit_message":"fix ninja","push":true}`}
	if !reflect.DeepEqual(r.calls[1].args, wantCommit) {
		t.Fatalf("commit args=%q", r.calls[1].args)
	}
	if !reflect.DeepEqual(r.calls[0].env, []string{"BIFROST_API_URL=dev"}) {
		t.Fatalf("env=%q", r.calls[0].env)
	}
}

func TestServiceRecordsLifecycleEvidence(t *testing.T) {
	r := &fakeRunner{responses: [][]byte{[]byte(`{"id":"c1","status":"open","base_revision":"rev1"}`), []byte(`{"valid":true,"diagnostics":[],"pending_deactivations":[],"validated_revision":"rev1"}`), []byte(`{"id":"c1","status":"committed","commit_sha":"abc"}`)}}
	c := NewClient(r)
	c.Target = "dev"
	st := &fakeStore{}
	now := time.Date(2026, 7, 16, 14, 0, 0, 0, time.UTC)
	svc := Service{Client: c, Store: st, Now: func() time.Time { return now }}
	rec, err := svc.Begin(context.Background(), "features/ninja", "rev1", "fix", "w1")
	if err != nil {
		t.Fatal(err)
	}
	if rec.WorkerID != "w1" || rec.Target != "dev" || rec.Scope != "features/ninja" {
		t.Fatalf("record=%+v", rec)
	}
	rec, err = svc.Validate(context.Background(), "c1")
	if err != nil {
		t.Fatal(err)
	}
	var validation map[string]any
	if err := json.Unmarshal(rec.Validation, &validation); err != nil || validation["valid"] != true {
		t.Fatalf("validation=%s err=%v", rec.Validation, err)
	}
	rec, err = svc.Commit(context.Background(), "c1", "fix", false)
	if err != nil {
		t.Fatal(err)
	}
	if rec.State != "committed" || rec.CommitSHA != "abc" {
		t.Fatalf("record=%+v", rec)
	}
}

func TestInspectEscapesScope(t *testing.T) {
	r := &fakeRunner{responses: [][]byte{[]byte(`{"scope":"features/a b","revision":"r","ready":true}`)}}
	c := NewClient(r)
	_, err := c.Inspect(context.Background(), "features/a b")
	if err != nil {
		t.Fatal(err)
	}
	if got := r.calls[0].args[2]; got != DefaultBasePath+"/state?scope=features%2Fa+b" {
		t.Fatalf("path=%q", got)
	}
}
