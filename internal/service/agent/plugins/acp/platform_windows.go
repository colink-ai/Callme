//go:build windows

package acp

import (
	"os"
	"os/exec"
	"strconv"

	"go.uber.org/zap"
)

// killProcessTree Windows 下用 taskkill /T /F 终止整个进程树
func killProcessTree(process *os.Process) error {
	if process == nil {
		return nil
	}
	LogInfo("ACP: terminating process tree (taskkill)", zap.Int("pid", process.Pid))
	kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(process.Pid))
	return kill.Run()
}
