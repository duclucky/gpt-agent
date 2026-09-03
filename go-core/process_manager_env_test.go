package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSafeEnvironmentProvidesIsolatedWindowsAppDataAndGoCache(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows SAFE environment regression")
	}
	env, err := safeEnvironment(nil)
	if err != nil {
		t.Fatal(err)
	}
	m := envMap(env)
	for _, key := range []string{"HOME", "USERPROFILE", "LOCALAPPDATA", "APPDATA", "GOCACHE", "TEMP", "TMP"} {
		if strings.TrimSpace(m[key]) == "" {
			t.Fatalf("%s missing from SAFE environment: %v", key, m)
		}
	}
	root := filepath.Clean(m["TEMP"])
	for _, key := range []string{"HOME", "USERPROFILE", "LOCALAPPDATA", "APPDATA", "GOCACHE", "TMP"} {
		value := filepath.Clean(m[key])
		rel, err := filepath.Rel(root, value)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
			t.Fatalf("%s escapes SAFE temp root: root=%q value=%q rel=%q err=%v", key, root, value, rel, err)
		}
	}
}

func TestSafeEnvironmentCanRunGoTestOnWindowsWithoutUserAppData(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows Go build-cache regression")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module safe-env-smoke\n\ngo 1.26\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "smoke_test.go"), []byte("package smoke\n\nimport \"testing\"\n\nfunc TestSmoke(t *testing.T) {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	env, err := safeEnvironment(nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := collectCommand(context.Background(), "go", []string{"test", "./..."}, dir, env, 60000, 20000)
	if err != nil {
		t.Fatal(err)
	}
	if code := extractLearningExitCode(result); code != 0 {
		t.Fatalf("go test exit=%d stderr=%v stdout=%v", code, result["stderr"], result["stdout"])
	}
}
