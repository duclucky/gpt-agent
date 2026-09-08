//go:build !windows

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUntrustedReturnsExplicitCapabilityErrorOutsideWindows(t *testing.T) {
	native := testNativeTools(t, t.TempDir())
	_, err := native.processes.runUntrusted(context.Background(), "test", ".", "git", []string{"status"}, nil, 10000)
	if err == nil || !strings.Contains(err.Error(), "UNTRUSTED is unavailable") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPOSIXFullShellPreservesExitCodeAndCwd(t *testing.T) {
	root := t.TempDir()
	native := testNativeTools(t, root)
	grant := []byte(`{"confirmedByUser":true,"expiresAt":"2099-12-31T23:59:59Z"}`)
	if err := os.WriteFile(filepath.Join(native.dataRoot, "full-shell.grant.json"), grant, 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := native.processes.runFullShell(context.Background(), "test", ".", `printf 'mac-posix-shell\n'; exit 9`, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if got := intFromAny(result["exitCode"]); got != 9 {
		t.Fatalf("exit code=%d want=9; result=%v", got, result)
	}
	if got := strings.TrimSpace(result["stdout"].(string)); got != "mac-posix-shell" {
		t.Fatalf("stdout=%q", got)
	}
	if got := result["cwdAbsolute"].(string); filepath.Clean(got) != filepath.Clean(root) {
		t.Fatalf("cwdAbsolute=%q want=%q", got, root)
	}
}
