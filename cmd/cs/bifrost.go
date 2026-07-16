package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	bf "github.com/MTG-Thomas/codex-swarm/internal/bifrost"
)

func (c cli) bifrost(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: cs bifrost <inspect|begin|show|validate|commit|abort>")
	}
	fs := c.flagSet("bifrost " + args[0])
	target := fs.String("target", "", "Bifrost target/profile recorded with the changeset")
	binary := fs.String("cli", "bifrost", "Bifrost CLI executable")
	basePath := fs.String("api-base", bf.DefaultBasePath, "workspace changeset API base path")
	scope := fs.String("scope", "", "remote workspace scope")
	worker := fs.String("worker", "", "codex-swarm worker ID")
	base := fs.String("base-revision", "", "expected workspace base revision")
	title := fs.String("title", "", "changeset title")
	message := fs.String("message", "", "Git commit message")
	push := fs.Bool("push", false, "ask Bifrost to push the activated commit")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	runner := c.bifrostRunner
	if runner == nil {
		runner = bf.ExecRunner{}
	}
	client := bf.NewClient(runner)
	client.Binary, client.BasePath, client.Target = *binary, *basePath, *target
	ctx := context.Background()
	if args[0] == "inspect" {
		if *scope == "" {
			return errors.New("--scope is required")
		}
		result, err := client.Inspect(ctx, *scope)
		if err != nil {
			return err
		}
		return writeBifrostJSON(c.out, result)
	}
	if c.bifrostRecords == nil {
		return errors.New("Bifrost changeset record store is unavailable")
	}
	svc := bf.Service{Client: client, Store: c.bifrostRecords, Now: c.now}
	var result bf.Record
	var err error
	switch args[0] {
	case "begin":
		if *scope == "" || *worker == "" {
			return errors.New("--scope and --worker are required")
		}
		result, err = svc.Begin(ctx, *scope, *base, *title, *worker)
	case "show", "validate", "abort":
		if fs.NArg() != 1 {
			return fmt.Errorf("usage: cs bifrost %s [flags] <changeset-id>", args[0])
		}
		if args[0] == "show" {
			result, err = svc.Show(ctx, fs.Arg(0))
		}
		if args[0] == "validate" {
			result, err = svc.Validate(ctx, fs.Arg(0))
		}
		if args[0] == "abort" {
			result, err = svc.Abort(ctx, fs.Arg(0))
		}
	case "commit":
		if fs.NArg() != 1 || *message == "" {
			return errors.New("usage: cs bifrost commit --message <message> [--push] <changeset-id>")
		}
		result, err = svc.Commit(ctx, fs.Arg(0), *message, *push)
	default:
		return fmt.Errorf("unknown bifrost command %q", args[0])
	}
	if err != nil {
		return err
	}
	return writeBifrostJSON(c.out, result)
}

func writeBifrostJSON(out interface{ Write([]byte) (int, error) }, value any) error {
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	return enc.Encode(value)
}
