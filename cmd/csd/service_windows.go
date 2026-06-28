//go:build windows

package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

func installService() error {
	cfg, err := defaultServiceConfig()
	if err != nil {
		return err
	}
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect service manager: %w", err)
	}
	defer m.Disconnect()

	existing, err := m.OpenService(cfg.Name)
	if err == nil {
		existing.Close()
		return fmt.Errorf("service %s already exists", cfg.Name)
	}
	service, err := m.CreateService(cfg.Name, cfg.Executable, mgr.Config{
		DisplayName: cfg.DisplayName,
		Description: cfg.Description,
		StartType:   mgr.StartAutomatic,
	}, cfg.Args...)
	if err != nil {
		return fmt.Errorf("create service %s: %w", cfg.Name, err)
	}
	defer service.Close()
	fmt.Printf("installed service=%s executable=%s\n", cfg.Name, cfg.Executable)
	return nil
}

func uninstallService() error {
	cfg, err := defaultServiceConfig()
	if err != nil {
		return err
	}
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect service manager: %w", err)
	}
	defer m.Disconnect()

	service, err := m.OpenService(cfg.Name)
	if err != nil {
		return fmt.Errorf("open service %s: %w", cfg.Name, err)
	}
	defer service.Close()
	_ = stopWindowsService(service, 10*time.Second)
	if err := service.Delete(); err != nil {
		return fmt.Errorf("delete service %s: %w", cfg.Name, err)
	}
	fmt.Printf("uninstalled service=%s\n", cfg.Name)
	return nil
}

func stopWindowsService(service *mgr.Service, timeout time.Duration) error {
	status, err := service.Query()
	if err != nil {
		return err
	}
	if status.State == svc.Stopped {
		return nil
	}
	if _, err := service.Control(svc.Stop); err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		status, err := service.Query()
		if err != nil {
			return err
		}
		if status.State == svc.Stopped {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("service did not stop within %s", timeout)
}

func maybeRunService() (bool, error) {
	isService, err := svc.IsWindowsService()
	if err != nil {
		return false, fmt.Errorf("detect windows service context: %w", err)
	}
	if !isService {
		return false, nil
	}
	return true, svc.Run(serviceName, windowsService{})
}

type windowsService struct{}

func (windowsService) Execute(args []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	if len(args) > 0 && args[0] == "serve" {
		args = args[1:]
	}
	addr, statePath, err := serveOptionsWithDefaultState(args, defaultServiceStatePath())
	if err != nil {
		return false, 1
	}
	go func() {
		done <- runServer(ctx, addr, statePath, io.Discard)
	}()

	changes <- svc.Status{State: svc.StartPending}
	changes <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				changes <- request.CurrentStatus
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				cancel()
				select {
				case <-done:
				case <-time.After(10 * time.Second):
				}
				return false, 0
			default:
			}
		case <-done:
			cancel()
			return false, 0
		}
	}
}

func defaultServiceStatePath() string {
	programData := envDefault("ProgramData", `C:\ProgramData`)
	return filepath.Join(programData, "codex-swarm", "state.json")
}
