package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/user"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/config"
	"github.com/MTG-Thomas/codex-swarm/internal/relay"
)

func (c cli) relaySessionStart(args []string, input io.Reader) error {
	fs := flag.NewFlagSet("relay session-start", flag.ContinueOnError)
	fs.SetOutput(c.err)
	cfg := fs.String("config", "", "absolute private relay config file")
	policy := fs.String("policy", "", "absolute host/user enrollment policy file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected session-start arguments")
	}
	// Hook runtime failures warn but never block the user's task or leak input.
	state, err := enrollSession(*cfg, *policy, input)
	if err != nil {
		return json.NewEncoder(c.out).Encode(map[string]string{"systemMessage": "Swarm enrollment unavailable; task continues. Check the enrollment policy, local relay configuration and coordinator connectivity."})
	}
	if state != "enrolled" {
		return json.NewEncoder(c.out).Encode(map[string]any{})
	}
	return json.NewEncoder(c.out).Encode(map[string]any{"hookSpecificOutput": map[string]string{"hookEventName": "SessionStart", "additionalContext": "This task is enrolled in the configured fleet relay. Use the codex-task-handoff skill for authorized messaging. Local work and conversation history remain local; enrollment does not authorize extra operations."}})
}

func enrollSession(cfg, policy string, input io.Reader) (string, error) {
	if err := relay.CheckUser(); err != nil {
		return "", err
	}
	p, err := relay.LoadEnrollmentPolicy(policy)
	if err != nil {
		return "", err
	}
	if !p.Enabled {
		return "disabled", nil
	}
	hc, err := relay.LoadHostConfig(cfg, config.DefaultStatePath())
	if err != nil {
		return "", err
	}
	u, err := user.Current()
	if err != nil {
		return "", err
	}
	if p.Host != hc.Host || p.OSUser != u.Uid {
		return "", fmt.Errorf("enrollment policy identity mismatch")
	}
	event, err := relay.DecodeSessionStart(input)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// A receiver may own the journal briefly. Retry only lock contention; never
	// retry network failures or queue submissions from this hook.
	var j *relay.Journal
	for {
		j, err = relay.OpenJournal(hc.Journal, hc.URL, hc.Host)
		if err == nil {
			break
		}
		if !relay.IsJournalBusy(err) {
			return "", err
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	defer j.Close()
	return relay.AutoEnroll(ctx, &relay.Client{URL: hc.URL, Host: hc.Host, Token: hc.Token}, j, p, u.Uid, event)
}

// Keep stdin injectable in tests without a second process or live session.
func (c cli) relaySessionStartStdin(args []string) error { return c.relaySessionStart(args, os.Stdin) }
