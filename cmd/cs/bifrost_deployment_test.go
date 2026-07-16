package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDeploymentRegisterStopsBeforeNetworkWithoutArtifactAssertion(t *testing.T) {
	c := cli{out: &bytes.Buffer{}, err: &bytes.Buffer{}}
	err := c.bifrost([]string{
		"deployment", "register",
		"--target", "https://dev.example",
		"--solution", "solution-1",
		"--prepared", "does-not-need-to-exist.json",
	})
	if err == nil || !strings.Contains(err.Error(), "no source/runtime upload transport") {
		t.Fatalf("error=%v", err)
	}
}

func TestDeploymentActivationRequiresExperimentalCapabilityGate(t *testing.T) {
	c := cli{out: &bytes.Buffer{}, err: &bytes.Buffer{}}
	err := c.bifrost([]string{
		"deployment", "activate",
		"--target", "https://dev.example",
		"--solution", "solution-1",
		"--deployment", "draft-1",
		"--expected-active", "base-1",
	})
	if err == nil || !strings.Contains(err.Error(), "activation hooks may return 503") {
		t.Fatalf("error=%v", err)
	}
}

func TestDeploymentInspectRequiresEphemeralAccessTokenBeforeNetwork(t *testing.T) {
	t.Setenv("BIFROST_ACCESS_TOKEN", "")
	c := cli{out: &bytes.Buffer{}, err: &bytes.Buffer{}}
	err := c.bifrost([]string{
		"deployment", "inspect",
		"--target", "https://dev.example",
		"--solution", "solution-1",
		"--deployment", "draft-1",
	})
	if err == nil || !strings.Contains(err.Error(), "BIFROST_ACCESS_TOKEN is required") {
		t.Fatalf("error=%v", err)
	}
}

func TestDeploymentActivationCapabilityRefusalMakesOnlyPreflightCall(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		_, _ = w.Write([]byte(`{"registration":true,"inspection":true,"artifact_upload":false,"server_side_compilation":false,"activation_configured":false,"safe_for_end_to_end_cs_deploy":false}`))
	}))
	defer server.Close()
	t.Setenv("BIFROST_ACCESS_TOKEN", "ephemeral")
	c := cli{out: &bytes.Buffer{}, err: &bytes.Buffer{}}

	err := c.bifrost([]string{
		"deployment", "activate",
		"--target", server.URL,
		"--solution", "solution-1",
		"--deployment", "draft-1",
		"--expected-active", "base-1",
		"--experimental-solution-deployments",
	})
	if err == nil || !strings.Contains(err.Error(), "activation_configured=false") {
		t.Fatalf("error=%v", err)
	}
	if len(calls) != 1 || calls[0] != "GET /api/solutions/solution-1/deployments/capabilities" {
		t.Fatalf("calls=%q", calls)
	}
}
