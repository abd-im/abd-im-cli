package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/abd-im/abd-im-cli/internal/contracts"
	"github.com/abd-im/abd-im-cli/internal/ipc"
	"github.com/abd-im/abd-im-cli/internal/profile"
)

type daemonProcessStatus struct {
	State string `json:"state"`
	PID   int    `json:"pid"`
}

func runStart(ctx context.Context, args []string, output io.Writer, roots commandRoots, profileName string) int {
	if len(args) != 0 {
		return writeTextError(output, "start accepts no arguments")
	}
	status, started, err := startDaemon(ctx, roots, profileName)
	if err != nil {
		return writeTextError(output, err.Error())
	}
	if started {
		fmt.Fprintf(output, "abdim is running (pid %d).\n", status.PID)
	} else {
		fmt.Fprintf(output, "abdim is already running (pid %d).\n", status.PID)
	}
	return 0
}

func runStop(ctx context.Context, args []string, output io.Writer, roots commandRoots, profileName string) int {
	if len(args) != 0 {
		return writeTextError(output, "stop accepts no arguments")
	}
	stopped, err := stopDaemon(ctx, roots, profileName)
	if err != nil {
		return writeTextError(output, err.Error())
	}
	if stopped {
		fmt.Fprintln(output, "abdim stopped.")
	} else {
		fmt.Fprintln(output, "abdim is not running.")
	}
	return 0
}

func runRestart(ctx context.Context, args []string, output io.Writer, roots commandRoots, profileName string) int {
	if len(args) != 0 {
		return writeTextError(output, "restart accepts no arguments")
	}
	if _, err := stopDaemon(ctx, roots, profileName); err != nil {
		return writeTextError(output, err.Error())
	}
	status, _, err := startDaemon(ctx, roots, profileName)
	if err != nil {
		return writeTextError(output, err.Error())
	}
	fmt.Fprintf(output, "abdim restarted (pid %d).\n", status.PID)
	return 0
}

func runStatus(ctx context.Context, args []string, output io.Writer, roots commandRoots, profileName string) int {
	if len(args) != 0 {
		return writeTextError(output, "status accepts no arguments")
	}
	paths, err := profile.NewPaths(roots.configDir, roots.dataDir, roots.runtimeDir, profileName)
	if err != nil {
		return writeTextError(output, err.Error())
	}
	status, err := readDaemonStatus(ctx, paths)
	if err != nil {
		if _, loadErr := profile.Load(paths.ConfigFile); loadErr != nil {
			fmt.Fprintln(output, "abdim is not configured. Run 'abdim setup'.")
		} else {
			fmt.Fprintln(output, "abdim is stopped.")
		}
		return 0
	}
	fmt.Fprintf(output, "abdim is %s (pid %d).\n", status.State, status.PID)
	return 0
}

func startDaemon(ctx context.Context, roots commandRoots, profileName string) (daemonProcessStatus, bool, error) {
	paths, err := profile.NewPaths(roots.configDir, roots.dataDir, roots.runtimeDir, profileName)
	if err != nil {
		return daemonProcessStatus{}, false, err
	}
	if status, err := readDaemonStatus(ctx, paths); err == nil {
		return status, false, nil
	}
	item, err := profile.Load(paths.ConfigFile)
	if err != nil {
		return daemonProcessStatus{}, false, errors.New("abdim is not configured; run 'abdim setup'")
	}
	if err := item.Deployment.Validate(); err != nil || !item.Pairing.Configured() {
		return daemonProcessStatus{}, false, errors.New("abdim setup is incomplete; run 'abdim setup'")
	}
	if err := paths.EnsurePrivate(); err != nil {
		return daemonProcessStatus{}, false, err
	}
	executable, err := os.Executable()
	if err != nil || !filepath.IsAbs(executable) {
		return daemonProcessStatus{}, false, errors.New("resolve abdim executable")
	}
	logFile, err := os.OpenFile(filepath.Join(paths.LogsDir, "daemon.log"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return daemonProcessStatus{}, false, errors.New("open daemon log")
	}
	if err := logFile.Chmod(0o600); err != nil {
		_ = logFile.Close()
		return daemonProcessStatus{}, false, errors.New("secure daemon log")
	}
	command := exec.Command(executable, "--profile", profileName, "__serve")
	command.Stdin = nil
	command.Stdout = logFile
	command.Stderr = logFile
	command.Env = os.Environ()
	configureDetachedProcess(command)
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		return daemonProcessStatus{}, false, errors.New("start abdim daemon")
	}
	pid := command.Process.Pid
	_ = command.Process.Release()
	_ = logFile.Close()

	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		status, err := readDaemonStatus(waitCtx, paths)
		if err == nil && status.State == "ready" {
			return status, true, nil
		}
		if process, findErr := os.FindProcess(pid); findErr != nil || process.Signal(syscall.Signal(0)) != nil {
			return daemonProcessStatus{}, false, errors.New("abdim daemon exited during startup; check the daemon log")
		}
		select {
		case <-waitCtx.Done():
			if process, findErr := os.FindProcess(pid); findErr == nil {
				_ = process.Signal(syscall.SIGTERM)
			}
			return daemonProcessStatus{}, false, errors.New("abdim daemon did not become ready; check the daemon log")
		case <-ticker.C:
		}
	}
}

func stopDaemon(ctx context.Context, roots commandRoots, profileName string) (bool, error) {
	paths, err := profile.NewPaths(roots.configDir, roots.dataDir, roots.runtimeDir, profileName)
	if err != nil {
		return false, err
	}
	status, err := readDaemonStatus(ctx, paths)
	if err != nil {
		return false, nil
	}
	if status.PID <= 0 {
		return false, errors.New("daemon returned an invalid process ID")
	}
	process, err := os.FindProcess(status.PID)
	if err != nil {
		return false, errors.New("find abdim daemon process")
	}
	if err := process.Signal(syscall.SIGTERM); err != nil {
		return false, errors.New("stop abdim daemon")
	}
	waitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := readDaemonStatus(waitCtx, paths); err != nil {
			return true, nil
		}
		select {
		case <-waitCtx.Done():
			return false, errors.New("abdim daemon did not stop")
		case <-ticker.C:
		}
	}
}

func readDaemonStatus(ctx context.Context, paths profile.Paths) (daemonProcessStatus, error) {
	response, err := ipc.Call(ctx, paths.Socket, contracts.Request{
		APIVersion: contracts.APIVersionV1,
		RequestID:  "lifecycle",
		ProfileID:  paths.ProfileID,
		Method:     "daemon.status",
		Params:     json.RawMessage(`{}`),
	})
	if err != nil || !response.OK {
		return daemonProcessStatus{}, errors.New("daemon is unavailable")
	}
	var status daemonProcessStatus
	if json.Unmarshal(response.Data, &status) != nil || status.PID <= 0 || strings.TrimSpace(status.State) == "" {
		return daemonProcessStatus{}, errors.New("daemon returned invalid status")
	}
	return status, nil
}

func writeTextError(output io.Writer, message string) int {
	fmt.Fprintf(output, "error: %s\n", message)
	return 1
}
