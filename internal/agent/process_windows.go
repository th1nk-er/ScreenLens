//go:build windows

package agent

import (
	"os/exec"
	"strconv"
	"syscall"
)

const (
	createNewProcessGroup = 0x00000200
	createNoWindow        = 0x08000000
)

func configureCommand(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNewProcessGroup | createNoWindow,
	}
}

func killProcessTree(command *exec.Cmd) {
	if command == nil || command.Process == nil {
		return
	}
	pid := strconv.Itoa(command.Process.Pid)
	if err := exec.Command("taskkill", "/T", "/F", "/PID", pid).Run(); err != nil {
		_ = command.Process.Kill()
	}
}
