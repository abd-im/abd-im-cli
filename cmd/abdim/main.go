package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/abd-im-cli/abdim-cli/internal/cli"
	"github.com/abd-im-cli/abdim-cli/internal/contracts"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return writeLocalError(os.Stdout, "cli", fmt.Errorf("resolve config directory: %w", err))
	}
	dataDir, err := os.UserCacheDir()
	if err != nil {
		return writeLocalError(os.Stdout, "cli", fmt.Errorf("resolve data directory: %w", err))
	}
	return runWithIO(args, os.Stdin, os.Stdout, commandRoots{configDir: configDir, dataDir: dataDir, runtimeDir: os.TempDir()})
}

type commandRoots struct {
	configDir  string
	dataDir    string
	runtimeDir string
}

func runWithIO(args []string, input io.Reader, output io.Writer, roots commandRoots) int {
	profileName := "default"
	requestID := "cli"
	format := cli.OutputJSON
	for len(args) > 0 && strings.HasPrefix(args[0], "--") {
		switch args[0] {
		case "--profile":
			if len(args) < 2 {
				return writeInvalidArgument(output, requestID, "--profile requires a value")
			}
			profileName, args = args[1], args[2:]
		case "--request-id":
			if len(args) < 2 {
				return writeInvalidArgument(output, requestID, "--request-id requires a value")
			}
			requestID, args = args[1], args[2:]
		case "--output":
			if len(args) < 2 {
				return writeInvalidArgument(output, requestID, "--output requires a value")
			}
			format, args = cli.Output(args[1]), args[2:]
			if format != cli.OutputJSON && format != cli.OutputJSONL {
				return writeInvalidArgument(output, requestID, "--output must be json or jsonl")
			}
		default:
			return writeInvalidArgument(output, requestID, "unsupported global flag")
		}
	}

	if len(args) < 2 || args[0] != "auth" || args[1] != "import" {
		return writeInvalidArgument(output, requestID, "supported command is auth import --token-stdin")
	}
	args = args[2:]
	tokenFromStdin := false
	allowPlaintext := false
	for _, argument := range args {
		switch argument {
		case "--token-stdin":
			tokenFromStdin = true
		case "--allow-plaintext-credentials":
			allowPlaintext = true
		default:
			return writeInvalidArgument(output, requestID, "unsupported auth import flag")
		}
	}
	if !tokenFromStdin {
		return writeInvalidArgument(output, requestID, "auth import requires --token-stdin")
	}

	response, err := cli.ImportToken(context.Background(), input, cli.AuthImportOptions{
		ProfileName:    profileName,
		ConfigDir:      roots.configDir,
		DataDir:        roots.dataDir,
		RuntimeDir:     roots.runtimeDir,
		AllowPlaintext: allowPlaintext,
		RequestID:      requestID,
	})
	if err != nil {
		return writeLocalErrorForFormat(output, format, requestID, err)
	}
	if err := cli.WriteResponse(output, format, response); err != nil {
		return 1
	}
	return cli.ExitCode(response)
}

func writeInvalidArgument(output io.Writer, requestID, message string) int {
	response := cli.ErrorResponse(requestID, contracts.CodeInvalidArgument, errorsNew(message))
	_ = cli.WriteResponse(output, cli.OutputJSON, response)
	return cli.ExitCode(response)
}

func writeLocalError(output io.Writer, requestID string, err error) int {
	return writeLocalErrorForFormat(output, cli.OutputJSON, requestID, err)
}

func writeLocalErrorForFormat(output io.Writer, format cli.Output, requestID string, err error) int {
	code := contracts.CodeInternal
	if cli.IsInvalidArgument(err) {
		code = contracts.CodeInvalidArgument
	}
	response := cli.ErrorResponse(requestID, code, err)
	_ = cli.WriteResponse(output, format, response)
	return cli.ExitCode(response)
}

func errorsNew(message string) error { return fmt.Errorf("%s", message) }
