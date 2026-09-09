package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"

	"github.com/MTG-Thomas/codex-swarm/internal/config"
	"github.com/MTG-Thomas/codex-swarm/internal/relay"
)

func (c cli) relayCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cs relay register|session-start|send|get|tasks|inbox|receipt|claim|claims|release (see docs/cross-host-relay.md)")
	}
	if args[0] == "session-start" {
		return c.relaySessionStartStdin(args[1:])
	}
	fs := flag.NewFlagSet("relay "+args[0], flag.ContinueOnError)
	fs.SetOutput(c.err)
	resource := fs.String("resource", "", "stable exact shared resource key")
	note := fs.String("note", "", "claim intent")
	thread := fs.String("thread", "", "exact destination task UUID")
	host := fs.String("host", "", "destination host ID")
	request := fs.String("request-id", "", "stable UUID for idempotent mutation retries")
	title := fs.String("title", "", "enrolled task title")
	disabled := fs.Bool("disabled", false, "disable this host's task enrollment")
	file := fs.String("message-file", "", "UTF-8 handoff file, at most 16 KiB")
	id := fs.String("id", "", "message ID returned by send")
	state := fs.String("status", "", "observed acknowledged or completed state")
	evidence := fs.String("evidence", "", "destination turn/result evidence reference")
	journalPath := fs.String("journal", filepath.Join(filepath.Dir(config.DefaultStatePath()), "relay.db"), "local relay journal")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected relay arguments")
	}
	client, err := relay.FromEnv()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var result any
	switch args[0] {
	case "register":
		// Enrollment is local authorization to deliver to this task, in addition to
		// the coordinator's discovery record. It never discovers tasks by scraping.
		if err = relay.CheckUser(); err != nil {
			return err
		}
		j, e := relay.OpenJournal(*journalPath, client.URL, client.Host)
		if e != nil {
			return e
		}
		defer j.Close()
		// Disable locally first so interrupted revocation cannot permit execution.
		if *disabled {
			if err = j.Enroll(ctx, *thread, false); err != nil {
				return err
			}
		}
		b := map[string]any{"thread_id": *thread, "title": *title, "enabled": !*disabled}
		if err = client.Call(ctx, "POST", "/v1/tasks", b, &result); err != nil {
			return err
		}
		if err = j.Enroll(ctx, *thread, !*disabled); err != nil {
			return err
		}
	case "send":
		if *request == "" || *file == "" {
			return fmt.Errorf("send requires --request-id and --message-file; reuse the same request ID after an uncertain response")
		}
		f, e := os.Open(*file)
		if e != nil {
			return e
		}
		defer f.Close()
		b, e := io.ReadAll(io.LimitReader(f, 16385))
		if e != nil {
			return e
		}
		if len(b) == 0 || len(b) > 16384 || !utf8.Valid(b) {
			return fmt.Errorf("message file must contain 1..16384 UTF-8 bytes")
		}
		err = client.Call(ctx, "POST", "/v1/messages", map[string]string{"request_id": *request, "target_host": *host, "thread_id": *thread, "prompt": string(b)}, &result)
	case "claim":
		if *request == "" {
			return fmt.Errorf("claim requires --request-id")
		}
		err = client.Call(ctx, "POST", "/v1/claims", map[string]string{"request_id": *request, "resource": *resource, "note": *note}, &result)
	case "release":
		if *id == "" {
			return fmt.Errorf("release requires --id")
		}
		err = client.Call(ctx, "POST", "/v1/claims/release", map[string]string{"id": *id}, &result)
	case "claims":
		claims := []json.RawMessage{}
		cursor := ""
		seen := map[string]bool{}
		for {
			var page struct {
				Claims []json.RawMessage `json:"claims"`
				Next   string            `json:"next_cursor"`
			}
			if err = client.Call(ctx, "GET", "/v1/claims?resource="+url.QueryEscape(*resource)+"&after="+url.QueryEscape(cursor), nil, &page); err != nil {
				return err
			}
			claims = append(claims, page.Claims...)
			if page.Next == "" {
				break
			}
			if seen[page.Next] {
				return fmt.Errorf("relay repeated claim pagination cursor")
			}
			seen[page.Next] = true
			cursor = page.Next
		}
		result = map[string]any{"claims": claims}
	case "get":
		if *id == "" {
			return fmt.Errorf("get requires --id")
		}
		err = client.Call(ctx, "GET", "/v1/messages/"+url.PathEscape(*id), nil, &result)
	case "tasks":
		// Walk all pages automatically; never silently cap discovery at one page.
		tasks := []json.RawMessage{}
		cursor := ""
		seen := map[string]bool{}
		for {
			var page struct {
				Tasks []json.RawMessage `json:"tasks"`
				Next  string            `json:"next_cursor"`
			}
			if err = client.Call(ctx, "GET", "/v1/tasks?host="+url.QueryEscape(*host)+"&after="+url.QueryEscape(cursor), nil, &page); err != nil {
				return err
			}
			tasks = append(tasks, page.Tasks...)
			if page.Next == "" {
				break
			}
			if seen[page.Next] {
				return fmt.Errorf("relay repeated task pagination cursor")
			}
			seen[page.Next] = true
			cursor = page.Next
		}
		result = map[string]any{"tasks": tasks}
	case "inbox":
		messages := []json.RawMessage{}
		cursor := ""
		seen := map[string]bool{}
		for {
			var page struct {
				Messages []json.RawMessage `json:"messages"`
				Next     string            `json:"next_cursor"`
			}
			if err = client.Call(ctx, "GET", "/v1/inbox?after="+url.QueryEscape(cursor), nil, &page); err != nil {
				return err
			}
			messages = append(messages, page.Messages...)
			if page.Next == "" {
				break
			}
			if seen[page.Next] {
				return fmt.Errorf("relay repeated inbox pagination cursor")
			}
			seen[page.Next] = true
			cursor = page.Next
		}
		result = map[string]any{"messages": messages}
	case "receipt":
		if *request == "" || *id == "" || *evidence == "" || (*state != "acknowledged" && *state != "completed") {
			return fmt.Errorf("receipt requires --request-id --id --status acknowledged|completed --evidence from destination readback")
		}
		r := relay.Receipt{RequestID: *request, MessageID: *id, State: *state, Evidence: *evidence}
		err = client.Call(ctx, "POST", "/v1/receipts", r, &result)
	default:
		return fmt.Errorf("unknown relay command %q", args[0])
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(c.out).Encode(result)
}
