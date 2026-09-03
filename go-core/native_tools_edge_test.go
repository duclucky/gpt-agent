package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
