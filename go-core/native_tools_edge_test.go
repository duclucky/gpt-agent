package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeExplicitZeroDoesNotBecomeDefault(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	native := testNativeTools(t, root)
	cases := []struct {
		name string
		raw  string
	}{
		{"gpt_agent_read_file", `{"workspaceId":"test","path":"a.txt","maxChars":0}`},
		{"gpt_agent_list_files", `{"workspaceId":"test","path":".","maxFiles":0,"maxDepth":8}`},
		{"gpt_agent_search_text", `{"workspaceId":"test","path":".","query":"hello","maxResults":0}`},
	}
	for _, tc := range cases {
		if _, err := native.call(context.Background(), tc.name, json.RawMessage(tc.raw)); err == nil {
			t.Fatalf("%s accepted explicit zero", tc.name)
		}
	}
}

func TestNativeTruncationUsesJavaScriptUTF16Length(t *testing.T) {
	text := "a😀b"
	out, truncated, original := truncateNativeText(text, 1000)
	if truncated {
		t.Fatal("short text unexpectedly truncated")
	}
	if out != text {
		t.Fatalf("text changed: %q", out)
	}
	if original != 4 {
		t.Fatalf("UTF-16 length=%d want=4", original)
	}
}

func TestHTTPToolBlocksRedirectOutsideAllowlist(t *testing.T) {
	root := t.TempDir()
	native := testNativeTools(t, root)
	native.cfg.Security.HTTPAllowedHosts = []string{"127.0.0.1"}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.com/blocked", http.StatusFound)
	}))
	defer server.Close()

	raw, _ := json.Marshal(map[string]any{"url": server.URL, "method": "GET"})
	if _, err := native.httpRequestTool(context.Background(), raw); err == nil || !strings.Contains(err.Error(), "redirect host blocked") {
		t.Fatalf("redirect outside allowlist was not blocked: %v", err)
	}
}

func TestLoopbackHTTPURLValidation(t *testing.T) {
	for _, raw := range []string{"http://127.0.0.1:8765/healthz", "https://localhost:9443/readyz", "http://[::1]:8765/mcp"} {
		if !isLoopbackHTTPURL(raw) {
			t.Fatalf("loopback URL rejected: %s", raw)
		}
	}
	for _, raw := range []string{"https://example.com/", "file:///tmp/x", "http://192.168.1.2/"} {
		if isLoopbackHTTPURL(raw) {
			t.Fatalf("non-loopback URL accepted: %s", raw)
		}
	}
}
