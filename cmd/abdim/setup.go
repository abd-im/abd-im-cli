package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/abd-im/abd-im-cli/internal/connector"
	"github.com/abd-im/abd-im-cli/internal/profile"
	"golang.org/x/term"
)

type setupDependencies struct {
	login func(context.Context, string, string, string) (string, string, error)
	stop  func(context.Context, commandRoots, string) (bool, error)
	start func(context.Context, commandRoots, string) (daemonProcessStatus, bool, error)
}

func defaultSetupDependencies() setupDependencies {
	return setupDependencies{
		login: func(ctx context.Context, account, areaCode, password string) (string, string, error) {
			return connector.AccountLogin(ctx, &http.Client{Timeout: 15 * time.Second}, connector.ABDLoginURL, account, areaCode, password)
		},
		stop:  stopDaemon,
		start: startDaemon,
	}
}

func runSetup(ctx context.Context, args []string, input io.Reader, output, prompt io.Writer, roots commandRoots, profileName string) int {
	return runSetupWith(ctx, args, input, output, prompt, roots, profileName, defaultSetupDependencies())
}

func runSetupWith(ctx context.Context, args []string, input io.Reader, output, prompt io.Writer, roots commandRoots, profileName string, dependencies setupDependencies) int {
	agent, specified, err := setupAgent(args)
	if err != nil {
		return writeTextError(output, err.Error())
	}
	paths, err := profile.NewPaths(roots.configDir, roots.dataDir, roots.runtimeDir, profileName)
	if err != nil {
		return writeTextError(output, err.Error())
	}
	inboundToolsEnabled := false
	if existing, loadErr := profile.Load(paths.ConfigFile); loadErr == nil {
		inboundToolsEnabled = existing.InboundToolsEnabled
		if !specified {
			agent = existing.Agent
		}
	}
	launch, err := agentLaunch(agent)
	if err != nil {
		return writeTextError(output, err.Error())
	}
	if _, err := exec.LookPath(launch.command); err != nil {
		return writeTextError(output, launch.command+" is not installed or is unavailable on PATH")
	}
	account, areaCode, password, err := promptAccount(input, prompt)
	if err != nil {
		return writeTextError(output, err.Error())
	}
	userID, token, err := dependencies.login(ctx, account, areaCode, password)
	password = ""
	if err != nil {
		return writeTextError(output, err.Error())
	}
	if _, err := dependencies.stop(ctx, roots, profileName); err != nil {
		return writeTextError(output, err.Error())
	}

	if err := paths.EnsurePrivate(); err != nil {
		return writeTextError(output, err.Error())
	}
	store, err := profile.NewFileStore(roots.dataDir)
	if err != nil {
		return writeTextError(output, err.Error())
	}
	credentialRef, err := store.Put(ctx, profileName, []byte(token))
	token = ""
	if err != nil {
		return writeTextError(output, err.Error())
	}
	item := profile.Profile{
		Name:                profileName,
		CredentialRef:       credentialRef,
		InboundToolsEnabled: inboundToolsEnabled,
		Agent:               agent,
		Deployment: profile.Deployment{
			UserID:     userID,
			APIAddr:    connector.ABDAPIAddr,
			WSAddr:     connector.ABDWSAddr,
			PlatformID: connector.ABDPlatformID,
		},
	}
	if err := profile.Save(paths.ConfigFile, item); err != nil {
		return writeTextError(output, err.Error())
	}
	status, _, err := dependencies.start(ctx, roots, profileName)
	if err != nil {
		return writeTextError(output, err.Error())
	}

	fmt.Fprintf(output, "Setup complete. abdim is running (pid %d).\n", status.PID)
	return 0
}

func setupAgent(args []string) (string, bool, error) {
	if len(args) == 0 {
		return profile.DefaultAgent, false, nil
	}
	if len(args) != 2 || args[0] != "--agent" {
		return "", false, errors.New("setup accepts only --agent codex|hermes|openclaw")
	}
	agent, err := profile.NormalizeAgent(args[1])
	return agent, true, err
}

func promptAccount(input io.Reader, prompt io.Writer) (account, areaCode, password string, err error) {
	if input == nil || prompt == nil {
		return "", "", "", errors.New("interactive input is unavailable")
	}
	reader := bufio.NewReader(input)
	fmt.Fprint(prompt, "ABD bot account (phone or email): ")
	account, err = readSetupLine(reader)
	if err != nil || account == "" {
		return "", "", "", errors.New("ABD bot account is required")
	}
	if !strings.Contains(account, "@") {
		fmt.Fprint(prompt, "Area code [+86]: ")
		areaCode, err = readSetupLine(reader)
		if err != nil {
			return "", "", "", errors.New("read area code")
		}
		if areaCode == "" {
			areaCode = "+86"
		}
	}
	fmt.Fprint(prompt, "Password: ")
	if file, ok := input.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		secret, readErr := term.ReadPassword(int(file.Fd()))
		fmt.Fprintln(prompt)
		if readErr != nil {
			return "", "", "", errors.New("read password")
		}
		password = string(secret)
	} else {
		password, err = readSetupLine(reader)
		if err != nil {
			return "", "", "", errors.New("read password")
		}
	}
	if password == "" {
		return "", "", "", errors.New("password is required")
	}
	return account, areaCode, password, nil
}

func readSetupLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil && len(line) == 0 {
		return "", err
	}
	return strings.TrimSpace(line), nil
}
