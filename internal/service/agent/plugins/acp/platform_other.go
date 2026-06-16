//go:build !windows

package acp

import (
	"os"
	"syscall"
	"time"

	"go.uber.org/zap"
)

const killGracePeriod = 3 * time.Second

// killProcessTree Unix 下用 SIGTERM→SIGKILL 终止进程（信号会传播到进程组）
func killProcessTree(process *os.Process) error {
	if process == nil {
		return nil
	}
	LogInfo("ACP: terminating process tree", zap.Int("pid", process.Pid))
	process.Signal(syscall.SIGTERM)
	time.Sleep(killGracePeriod)
	return process.Kill()
}
