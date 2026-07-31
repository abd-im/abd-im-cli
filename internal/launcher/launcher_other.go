//go:build !unix

package launcher

import (
	"errors"
	"os/exec"
)

func validateControlFile(string) error {
	return errors.New("isolated provider launcher requires a Unix deployment")
}

func validateRuntime(Config) error {
	return errors.New("isolated provider launcher requires a Unix deployment")
}

func prepareWorkingDir(string) error {
	return errors.New("isolated provider launcher requires a Unix deployment")
}

func (*Launcher) CopyCodexAuth(string) error {
	return errors.New("isolated provider launcher requires a Unix deployment")
}

func (*Launcher) PrepareRun(string, string, string) error {
	return errors.New("isolated provider launcher requires a Unix deployment")
}

func (*Launcher) PrepareSocket(string) error {
	return errors.New("isolated provider launcher requires a Unix deployment")
}

func (*Launcher) Configure(*exec.Cmd) error {
	return errors.New("isolated provider launcher requires a Unix deployment")
}
