package main

import (
	"context"
	"fmt"
	"io"

	"github.com/abd-im/abd-im-cli/internal/profile"
)

type inboundToolsDependencies struct {
	status func(context.Context, profile.Paths) (daemonProcessStatus, error)
	stop   func(context.Context, commandRoots, string) (bool, error)
	start  func(context.Context, commandRoots, string) (daemonProcessStatus, bool, error)
}

func defaultInboundToolsDependencies() inboundToolsDependencies {
	return inboundToolsDependencies{status: readDaemonStatus, stop: stopDaemon, start: startDaemon}
}

func runInboundTools(ctx context.Context, args []string, output io.Writer, roots commandRoots, profileName string) int {
	return runInboundToolsWith(ctx, args, output, roots, profileName, defaultInboundToolsDependencies())
}

func runInboundToolsWith(ctx context.Context, args []string, output io.Writer, roots commandRoots, profileName string, dependencies inboundToolsDependencies) int {
	if len(args) != 1 || (args[0] != "enable" && args[0] != "disable" && args[0] != "status") {
		return writeTextError(output, "inbound tools requires enable, disable, or status")
	}
	paths, err := profile.NewPaths(roots.configDir, roots.dataDir, roots.runtimeDir, profileName)
	if err != nil {
		return writeTextError(output, err.Error())
	}
	item, err := profile.Load(paths.ConfigFile)
	if err != nil {
		return writeTextError(output, "abdim is not configured; run 'abdim setup'")
	}

	if args[0] == "status" {
		writeInboundToolsStatus(output, item.InboundToolsEnabled)
		return 0
	}

	enabled := args[0] == "enable"
	if item.InboundToolsEnabled == enabled {
		writeInboundToolsStatus(output, enabled)
		return 0
	}
	_, runningErr := dependencies.status(ctx, paths)
	item.InboundToolsEnabled = enabled
	if err := profile.Save(paths.ConfigFile, item); err != nil {
		return writeTextError(output, err.Error())
	}
	if runningErr != nil {
		writeInboundToolsChanged(output, enabled, 0)
		return 0
	}
	if _, err := dependencies.stop(ctx, roots, profileName); err != nil {
		item.InboundToolsEnabled = !enabled
		if restoreErr := profile.Save(paths.ConfigFile, item); restoreErr != nil {
			return writeTextError(output, fmt.Sprintf("%v; restore inbound tools setting: %v", err, restoreErr))
		}
		return writeTextError(output, err.Error())
	}
	status, _, err := dependencies.start(ctx, roots, profileName)
	if err != nil {
		return writeTextError(output, err.Error())
	}
	writeInboundToolsChanged(output, enabled, status.PID)
	return 0
}

func writeInboundToolsStatus(output io.Writer, enabled bool) {
	if enabled {
		fmt.Fprintln(output, "Inbound MCP tools are enabled for all direct-message senders.")
		return
	}
	fmt.Fprintln(output, "Inbound MCP tools are disabled; direct messages are reply-only.")
}

func writeInboundToolsChanged(output io.Writer, enabled bool, pid int) {
	state := "disabled; direct messages are reply-only"
	if enabled {
		state = "enabled for all direct-message senders"
	}
	if pid > 0 {
		fmt.Fprintf(output, "Inbound MCP tools %s. abdim restarted (pid %d).\n", state, pid)
		return
	}
	fmt.Fprintf(output, "Inbound MCP tools %s. The setting applies on the next start.\n", state)
}
