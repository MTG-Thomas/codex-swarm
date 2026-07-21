package appserver

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
)

// Process starts a Codex app-server process for a workspace. Implementations
// may run Codex locally or transport its stdio over a remote connection.
type Process interface {
	Command(ctx context.Context, cwd string) (*exec.Cmd, error)
}

// LocalProcess starts Codex on the local machine.
type LocalProcess struct {
	Binary string
}

func (p LocalProcess) Command(ctx context.Context, cwd string) (*exec.Cmd, error) {
	binary := p.Binary
	if binary == "" {
		binary = "codex"
	}
	cmd := exec.CommandContext(ctx, binary, "app-server")
	cmd.Dir = cwd
	return cmd, nil
}

var sshEndpointPattern = regexp.MustCompile(`^[A-Za-z0-9._:@-]+$`)
var remoteBinaryPattern = regexp.MustCompile(`^[A-Za-z0-9._/+~-]+$`)

// SSHProcess transports app-server JSON-RPC over OpenSSH stdio. Authentication
// and host-key policy remain owned by the operator's SSH configuration.
type SSHProcess struct {
	Binary      string
	Target      string
	Jump        string
	CodexBinary string
}

func (p SSHProcess) Command(ctx context.Context, _ string) (*exec.Cmd, error) {
	if !sshEndpointPattern.MatchString(p.Target) {
		return nil, fmt.Errorf("invalid SSH target %q", p.Target)
	}
	if p.Jump != "" && !sshEndpointPattern.MatchString(p.Jump) {
		return nil, fmt.Errorf("invalid SSH jump host %q", p.Jump)
	}
	remoteBinary := p.CodexBinary
	if remoteBinary == "" {
		remoteBinary = "codex"
	}
	if !remoteBinaryPattern.MatchString(remoteBinary) {
		return nil, fmt.Errorf("invalid remote Codex binary %q", remoteBinary)
	}
	binary := p.Binary
	if binary == "" {
		binary = "ssh"
	}
	args := []string{"-o", "BatchMode=yes"}
	if p.Jump != "" {
		args = append(args, "-J", p.Jump)
	}
	args = append(args, p.Target, "--", remoteBinary, "app-server")
	return exec.CommandContext(ctx, binary, args...), nil
}
