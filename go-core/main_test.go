package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestConfigureRuntimeLogRotatesLegacyUTF16(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "runtime.log")
	legacy := []byte{0xFF, 0xFE, 'o', 0x00, 'l', 0x00, 'd', 0x00, '\n', 0x00}
	if err := os.WriteFile(logPath, legacy, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GPT_AGENT_LOG_PATH", logPath)

	writer, closeLog, err := configureRuntimeLog()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(writer, "new-log\n"); err != nil {
		closeLog()
		t.Fatal(err)
	}
	closeLog()

	preserved, err := os.ReadFile(logPath + ".legacy-utf16")
	if err != nil {
		t.Fatalf("legacy log was not preserved: %v", err)
	}
	if string(preserved) != string(legacy) {
		t.Fatalf("legacy bytes changed: got %v want %v", preserved, legacy)
	}
	current, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != "new-log\n" {
		t.Fatalf("new runtime log mismatch: %q", current)
	}
}

func TestConfigureRuntimeLogKeepsUTF8File(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "runtime.log")
	if err := os.WriteFile(logPath, []byte("old-utf8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GPT_AGENT_LOG_PATH", logPath)

	writer, closeLog, err := configureRuntimeLog()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(writer, "next\n"); err != nil {
		closeLog()
		t.Fatal(err)
	}
	closeLog()

	current, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != "old-utf8\nnext\n" {
		t.Fatalf("UTF-8 runtime log should append in place: %q", current)
	}
	if _, err := os.Stat(logPath + ".legacy-utf16"); !os.IsNotExist(err) {
		t.Fatalf("UTF-8 log should not be rotated, stat err=%v", err)
	}
}
