package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/operation"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

func TestOperationListAndShowAreScriptable(t *testing.T) {
	var out bytes.Buffer
	c := cli{out: &out, err: &out, now: time.Now}
	state := filepath.Join(t.TempDir(), "state.db")
	st := store.NewJSONStore(state)
	for _, worker := range []store.Worker{
		{ID: "root", Issue: "Owner/Repo#7", Status: store.WorkerRunning},
		{ID: "child", ParentID: "root", Status: store.WorkerIdle},
		{ID: "broken", ParentID: "missing", Status: store.WorkerIdle},
	} {
		if err := st.SaveWorker(worker); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SaveClaim(store.Claim{ID: "claim", WorkerID: "child", Status: store.ClaimActive}); err != nil {
		t.Fatal(err)
	}

	if err := c.run([]string{"operation", "list", "--state", state, "--issue", "OWNER/REPO#007", "--json"}); err != nil {
		t.Fatal(err)
	}
	var view operation.View
	if err := json.Unmarshal(out.Bytes(), &view); err != nil {
		t.Fatalf("decode %q: %v", out.String(), err)
	}
	if len(view.Operations) != 1 || view.Operations[0].Key != "issue:owner/repo#7" || len(view.Operations[0].Workers) != 2 || len(view.Operations[0].Claims) != 1 {
		t.Fatalf("filtered view = %#v", view)
	}

	out.Reset()
	if err := c.run([]string{"operation", "show", "issue:OWNER/REPO#007", "--state", state}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "issue:owner/repo#7") || !strings.Contains(out.String(), "workers=2") {
		t.Fatalf("human output = %q", out.String())
	}

	out.Reset()
	if err := c.run([]string{"operation", "list", "--state", state, "--worker", "broken", "--json"}); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(out.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Operations) != 0 || len(view.Resolutions) != 1 || view.Resolutions[0].State != operation.StateMissingParent || len(view.Unscoped) != 1 {
		t.Fatalf("broken worker view = %#v", view)
	}
}

func TestOperationCommandRejectsAmbiguousFiltersAndUnknownKeys(t *testing.T) {
	var out bytes.Buffer
	c := cli{out: &out, err: &out, now: time.Now}
	state := filepath.Join(t.TempDir(), "state.db")
	if err := c.run([]string{"operation", "list", "--state", state, "--issue", "owner/repo#1", "--worker", "w-1"}); err == nil {
		t.Fatal("accepted ambiguous filters")
	}
	if err := c.run([]string{"operation", "show", "issue:owner/repo#1", "--state", state}); err == nil || !strings.Contains(err.Error(), "operation not found") {
		t.Fatalf("show missing operation error = %v", err)
	}
}
