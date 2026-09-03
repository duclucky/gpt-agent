package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testNativeTools(t *testing.T, root string) *nativeTools {
	t.Helper()
	enabled := true
	var cfg runtimeConfig
	cfg.Server.MaxToolOutputChars = 160000
	cfg.Security.SecretFilePatterns = []string{".env", ".env.*", "*.pem", "*.key"}
	cfg.Workspaces = []workspaceConfig{{ID: "test", Root: root, Enabled: &enabled}}
	if err := normalizeNativeConfig(&cfg); err != nil {
		t.Fatal(err)
	}
	return newNativeTools(cfg, newAuditWriter(t.TempDir()))
}

func TestNativeReadFileRangeAndSecretPolicy(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.txt"), []byte("one\r\ntwo\r\nthree"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("SECRET=value"), 0o644); err != nil {
		t.Fatal(err)
	}
	native := testNativeTools(t, root)

	start, end := 2, 3
	result, err := native.readFile("test", "sample.txt", 120000, &start, &end)
	if err != nil {
		t.Fatal(err)
	}
	if got := result["content"]; got != "2: two\n3: three" {
		t.Fatalf("range content=%q", got)
	}
	if result["bytes"] != 15 {
		t.Fatalf("bytes=%v want=15", result["bytes"])
	}
	if _, _, _, err := native.resolveWorkspacePath("test", ".env"); err == nil || !strings.Contains(err.Error(), "Secret-like path blocked") {
		t.Fatalf("secret policy was not enforced: %v", err)
	}
	outside := filepath.Join(filepath.Dir(root), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := native.resolveWorkspacePath("test", filepath.Join("..", filepath.Base(outside))); err == nil || !strings.Contains(err.Error(), "Path escapes workspace") {
		t.Fatalf("workspace escape was not blocked: %v", err)
	}
}

func TestNativeListFilesMatchesIgnoreAndDepthPolicy(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"root.txt", "src/a.go", "src/deep/b.go", "node_modules/pkg/index.js", ".git/config"} {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(rel), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	native := testNativeTools(t, root)
	result, err := native.listFiles("test", ".", 100, 1)
	if err != nil {
		t.Fatal(err)
	}
	files := result["files"].([]string)
	joined := strings.Join(files, "|")
	if !strings.Contains(joined, "root.txt") || !strings.Contains(joined, "src/a.go") {
		t.Fatalf("expected files missing: %v", files)
	}
	if strings.Contains(joined, "node_modules") || strings.Contains(joined, ".git") || strings.Contains(joined, "src/deep/b.go") {
		t.Fatalf("ignore/depth policy mismatch: %v", files)
	}
}

func TestNativeSearchTextStopsAtGlobalLimit(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep not available")
	}
	root := t.TempDir()
	for i := 0; i < 150; i++ {
		name := filepath.Join(root, "files", strings.Repeat("x", i%5)+time.Unix(int64(i), 0).Format("150405")+".txt")
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte("needle needle needle\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	native := testNativeTools(t, root)
	started := time.Now()
	result, err := native.searchText(context.Background(), "test", ".", "needle", "", 5, false, false)
	if err != nil {
		t.Fatal(err)
	}
	matches := result["matches"].([]map[string]any)
	if len(matches) != 5 {
		t.Fatalf("matches=%d want=5", len(matches))
	}
	if time.Since(started) > 3*time.Second {
		t.Fatalf("bounded search took too long: %s", time.Since(started))
	}
}

func TestNativeSearchTextFallsBackWhenResolvedRipgrepCannotStart(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "visible.txt"), []byte("needle here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	native := testNativeTools(t, root)
	native.rgPath = filepath.Join(root, "missing-rg.exe")
	result, err := native.searchText(context.Background(), "test", ".", "needle", "*.txt", 10, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if result["engine"] != "go-fallback" {
		t.Fatalf("engine=%v want go-fallback", result["engine"])
	}
	matches := result["matches"].([]map[string]any)
	if len(matches) != 1 {
		t.Fatalf("matches=%d want=1", len(matches))
	}
}
func TestNativeSearchTextGoFallbackDoesNotNeedRipgrepAndSkipsSecrets(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "visible.txt"), []byte("Needle here\nsecond needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("needle=secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	native := testNativeTools(t, root)
	native.rgPath = ""
	result, err := native.searchText(context.Background(), "test", ".", "needle", "*.txt", 10, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if result["engine"] != "go-fallback" {
		t.Fatalf("engine=%v want go-fallback", result["engine"])
	}
	matches := result["matches"].([]map[string]any)
	if len(matches) != 2 {
		t.Fatalf("matches=%d want=2: %#v", len(matches), matches)
	}
	for _, match := range matches {
		if match["path"] != "visible.txt" {
			t.Fatalf("secret or unexpected path leaked into fallback search: %#v", match)
		}
	}
}

func TestNativeGitStatusAndDiff(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "a.txt")
	run("-c", "user.name=GPT Agent Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "base")
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	native := testNativeTools(t, root)

	status, err := native.call(context.Background(), "gpt_agent_git_status", json.RawMessage(`{"workspaceId":"test"}`))
	if err != nil {
		t.Fatal(err)
	}
	statusMap, ok := status.(map[string]any)
	if !ok {
		t.Fatalf("status payload type = %T, want map[string]any", status)
	}
	if !strings.Contains(statusMap["output"].(string), "a.txt") {
		t.Fatalf("status missing changed file: %q", statusMap["output"])
	}
	diff, err := native.call(context.Background(), "gpt_agent_git_diff", json.RawMessage(`{"workspaceId":"test"}`))
	if err != nil {
		t.Fatal(err)
	}
	diffMap, ok := diff.(map[string]any)
	if !ok {
		t.Fatalf("diff payload type = %T, want map[string]any", diff)
	}
	if !strings.Contains(diffMap["diff"].(string), "+two") {
		t.Fatalf("diff missing change: %q", diffMap["diff"])
	}
}
