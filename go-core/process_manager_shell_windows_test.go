package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func enableTestFullShell(t *testing.T, native *nativeTools) {
	t.Helper()
	grant := []byte(`{"confirmedByUser":true,"expiresAt":"2099-12-31T23:59:59Z"}`)
	if err := os.WriteFile(filepath.Join(native.dataRoot, "full-shell.grant.json"), grant, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestFullShellExitIsIsolatedFromPersistentBroker(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows FULL SHELL worker regression")
	}

	root := t.TempDir()
	native := testNativeTools(t, root)
	enableTestFullShell(t, native)
	t.Cleanup(native.processes.close)

	first, err := native.processes.runFullShell(context.Background(), "test", ".", "Write-Output 'before-exit'; exit 7", 10000)
	if err != nil {
		t.Fatalf("explicit exit terminated broker: %v", err)
	}
	if got := intFromAny(first["exitCode"]); got != 7 {
		t.Fatalf("explicit exit code=%d want=7; result=%v", got, first)
	}
	if got := strings.TrimSpace(first["stdout"].(string)); got != "before-exit" {
		t.Fatalf("stdout=%q want=%q", got, "before-exit")
	}
	if native.processes.shell == nil || native.processes.shell.cmd == nil || native.processes.shell.cmd.Process == nil {
		t.Fatal("persistent broker missing after explicit exit")
	}
	brokerPID := native.processes.shell.cmd.Process.Pid

	second, err := native.processes.runFullShell(context.Background(), "test", ".", "Write-Output 'after-exit'", 10000)
	if err != nil {
		t.Fatalf("follow-up command failed: %v", err)
	}
	if got := intFromAny(second["exitCode"]); got != 0 {
		t.Fatalf("follow-up exit code=%d want=0; result=%v", got, second)
	}
	if got := strings.TrimSpace(second["stdout"].(string)); got != "after-exit" {
		t.Fatalf("follow-up stdout=%q want=%q", got, "after-exit")
	}
	if native.processes.shell == nil || native.processes.shell.cmd.Process.Pid != brokerPID {
		t.Fatalf("persistent broker restarted after isolated exit: before=%d after=%v", brokerPID, native.processes.shell)
	}
}

func TestFullShellPreservesNativeExitCodeInsideIsolation(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows FULL SHELL worker regression")
	}

	root := t.TempDir()
	native := testNativeTools(t, root)
	enableTestFullShell(t, native)
	t.Cleanup(native.processes.close)

	result, err := native.processes.runFullShell(context.Background(), "test", ".", "cmd.exe /d /c exit 9", 10000)
	if err != nil {
		t.Fatal(err)
	}
	if got := intFromAny(result["exitCode"]); got != 9 {
		t.Fatalf("native exit code=%d want=9; result=%v", got, result)
	}
}
