//go:build !windows

package main

import (
	"fmt"
	"os"
	"syscall"
)

type processJob struct{}

func attachProcessJob(pid int) (*processJob, error) {
	if pid <= 0 {
		return nil, fmt.Errorf("invalid backend pid %d", pid)
	}
	return &processJob{}, nil
}

func (j *processJob) close() error { return nil }

func killProcessTree(pid int) error {
	if pid <= 0 {
		return nil
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err == nil || err == syscall.ESRCH {
		return nil
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Kill()
}
