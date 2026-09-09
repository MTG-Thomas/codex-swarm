package main

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestUserTaskKeepsIdentityAndArguments(t *testing.T) {
	executable := `C:\User & Test\csd.exe`
	arguments := `serve --relay-config "C:\User & Test\private.json"`
	doc := userTaskXML("S-1-5-21-test", executable, arguments)
	var task struct {
		Principals struct {
			Principal struct {
				UserID    string `xml:"UserId"`
				LogonType string
				RunLevel  string
			}
		}
		Actions struct {
			Exec struct{ Command, Arguments string }
		}
	}
	if err := xml.Unmarshal([]byte(doc), &task); err != nil {
		t.Fatal(err)
	}
	p := task.Principals.Principal
	if p.UserID != "S-1-5-21-test" || p.LogonType != "InteractiveToken" || p.RunLevel != "LeastPrivilege" {
		t.Fatalf("unsafe principal: %+v", p)
	}
	if task.Actions.Exec.Command != executable || task.Actions.Exec.Arguments != arguments {
		t.Fatalf("arguments changed: %+v", task.Actions)
	}
	if strings.Contains(doc, "<Password>") {
		t.Fatal("task includes password")
	}
}

func TestServeRelayConfig(t *testing.T) {
	t.Setenv("CODEX_SWARM_RELAY_CONFIG", "/config/private.json")
	cfg, err := parseServeConfig([]string{"--addr", "127.0.0.1:9876"}, "/state/state.db")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RelayConfig != "/config/private.json" || cfg.Addr != "127.0.0.1:9876" {
		t.Fatalf("wrong config: %+v", cfg)
	}
	cfg, err = parseServeConfig([]string{"--relay-config="}, "/state/state.db")
	if err != nil || cfg.RelayConfig != "" {
		t.Fatal("explicit empty option must disable environment config")
	}
}
