package devservice

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// Status 描述一个受管理的开发态服务进程。
type Status struct {
	Name    string
	PID     int
	Running bool
	PIDFile string
	LogFile string
	Error   string
}

// RunDir 返回保存 PID 和日志文件的项目本地目录。
func RunDir(projectDir string) string {
	if projectDir == "" {
		projectDir = "."
	}
	return filepath.Join(projectDir, ".aifd", "run")
}

// Start 启动开发态服务；如果 PID 文件指向存活进程，则直接返回当前状态。
func Start(name string, exe string, args []string, projectDir string) (Status, error) {
	status := GetStatus(name, projectDir)
	if status.Running {
		return status, nil
	}
	if exe == "" {
		return status, fmt.Errorf("%s executable is empty", name)
	}
	runDir := RunDir(projectDir)
	if err := os.MkdirAll(runDir, 0755); err != nil {
		return status, err
	}
	logFile := filepath.Join(runDir, name+".log")
	pidFile := filepath.Join(runDir, name+".pid")

	log, err := os.OpenFile(logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return status, err
	}
	defer log.Close()

	cmd := exec.Command(exe, args...)
	cmd.Dir = projectDir
	cmd.Stdout = log
	cmd.Stderr = log
	if err := cmd.Start(); err != nil {
		return status, err
	}
	pid := cmd.Process.Pid
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(pid)+"\n"), 0644); err != nil {
		_ = cmd.Process.Kill()
		return status, err
	}
	return Status{Name: name, PID: pid, Running: true, PIDFile: pidFile, LogFile: logFile}, nil
}

// Stop 结束受管理的开发态服务，并删除 PID 文件。
func Stop(name string, projectDir string) (Status, error) {
	status := GetStatus(name, projectDir)
	if status.PID <= 0 {
		_ = os.Remove(status.PIDFile)
		return status, nil
	}
	if status.Running {
		process, err := os.FindProcess(status.PID)
		if err != nil {
			status.Error = err.Error()
		} else if err := process.Kill(); err != nil {
			status.Error = err.Error()
			return status, err
		}
	}
	status.Running = false
	_ = os.Remove(status.PIDFile)
	return status, nil
}

// GetStatus 读取 PID 文件，并报告对应进程是否存活。
func GetStatus(name string, projectDir string) Status {
	runDir := RunDir(projectDir)
	status := Status{
		Name:    name,
		PIDFile: filepath.Join(runDir, name+".pid"),
		LogFile: filepath.Join(runDir, name+".log"),
	}
	raw, err := os.ReadFile(status.PIDFile)
	if err != nil {
		return status
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		status.Error = err.Error()
		return status
	}
	status.PID = pid
	status.Running = ProcessAlive(pid)
	return status
}

// ProcessAlive 判断指定 PID 在当前操作系统上是否仍在运行。
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	if runtime.GOOS == "windows" {
		out, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/NH").CombinedOutput()
		if err != nil {
			return false
		}
		text := string(out)
		return strings.Contains(text, strconv.Itoa(pid)) && !strings.Contains(strings.ToLower(text), "no tasks")
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}
