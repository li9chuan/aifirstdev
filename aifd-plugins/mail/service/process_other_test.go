//go:build !windows

package service

import "os/exec"

func hideTestProcess(*exec.Cmd) {}
