//go:build unix

package codex

import (
	"os/exec"
	"syscall"
)

func configureProcessGroup(command *exec.Cmd) {
	attributes := command.SysProcAttr
	if attributes == nil {
		attributes = &syscall.SysProcAttr{}
	}
	attributes.Setpgid = true
	command.SysProcAttr = attributes
}

func terminateProcessGroup(command *exec.Cmd) {
	if command != nil && command.Process != nil {
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
}
