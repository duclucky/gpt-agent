//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

type processJob struct {
	handle windows.Handle
}

func attachProcessJob(pid int) (*processJob, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create backend job object: %w", err)
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = windows.CloseHandle(job)
		return nil, fmt.Errorf("configure backend job object: %w", err)
	}
	process, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(pid),
	)
	if err != nil {
		_ = windows.CloseHandle(job)
		return nil, fmt.Errorf("open backend process: %w", err)
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		_ = windows.CloseHandle(job)
		return nil, fmt.Errorf("assign backend to job object: %w", err)
	}
	return &processJob{handle: job}, nil
}

func (j *processJob) close() error {
	if j == nil || j.handle == 0 {
		return nil
	}
	h := j.handle
	j.handle = 0
	return windows.CloseHandle(h)
}

func killProcessTree(pid int) error {
	if pid <= 0 {
		return nil
	}
	cmd := exec.Command("taskkill.exe", "/PID", strconv.Itoa(pid), "/T", "/F")
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	message := strings.ToLower(string(out))
	if strings.Contains(message, "not found") || strings.Contains(message, "not running") || strings.Contains(message, "no running instance") {
		return nil
	}
	return fmt.Errorf("taskkill backend tree: %w: %s", err, strings.TrimSpace(string(out)))
}
