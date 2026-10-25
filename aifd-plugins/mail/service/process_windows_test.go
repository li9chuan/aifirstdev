//go:build windows

package service

import (
	"os/exec"
	"syscall"
)

func hideTestProcess(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true} }
