package bifrost

import (
	"context"
	"encoding/json"
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
	calls     []runnerCall
}

func (f *fakeRunner) Run(_ context.Context, name string, args []string, _ []byte, env []string) ([]byte, error) {
	f.calls = append(f.calls, runnerCall{name, args, env})
	out := f.responses[0]
	f.responses = f.responses[1:]
	return out, nil
}

type fakeStore struct{ records map[string]Record }

func (f *fakeStore) SaveBifrostChangeset(r Record) error {
	if f.records == nil {
		f.records = map[string]Record{}
	}
	f.records[r.ID] = r
	return nil
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
	r := &fakeRunner{responses: [][]byte{[]byte(`{"id":"c1","state":"open","base_revision":"rev1"}`), []byte(`{"id":"c1","state":"committed","commit_sha":"abc"}`)}}
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
	if !reflect.DeepEqual(r.calls[0].env, []string{"BIFROST_TARGET=dev"}) {
		t.Fatalf("env=%q", r.calls[0].env)
	}
}

func TestServiceRecordsLifecycleEvidence(t *testing.T) {
	r := &fakeRunner{responses: [][]byte{[]byte(`{"id":"c1","state":"open","base_revision":"rev1"}`), []byte(`{"id":"c1","state":"validated","validation":{"ok":true}}`), []byte(`{"id":"c1","state":"committed","commit_sha":"abc"}`)}}
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
	var validation map[string]bool
	if err := json.Unmarshal(rec.Validation, &validation); err != nil || !validation["ok"] {
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
	if got := r.calls[0].args[2]; got != DefaultBasePath+"/inspect?scope=features%2Fa+b" {
		t.Fatalf("path=%q", got)
	}
}
