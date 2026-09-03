package main

import "testing"

func TestSelfCheckResultProtectsReservedFields(t *testing.T) {
	item := selfCheckResult("source-git", "warn", map[string]any{
		"name":      "overwritten-name",
		"status":    "overwritten-status",
		"gitStatus": "## main",
	})
	if item["name"] != "source-git" {
		t.Fatalf("name=%v want source-git", item["name"])
	}
	if item["status"] != "warn" {
		t.Fatalf("status=%v want warn", item["status"])
	}
	if item["gitStatus"] != "## main" {
		t.Fatalf("gitStatus=%v", item["gitStatus"])
	}
}

func TestMCPToolsListHealthRequiresDirectDeveloperCore(t *testing.T) {
	required := []string{
		"gpt_agent_status", "gpt_agent_self_check", "gpt_agent_read_file", "gpt_agent_git_diff",
		"gpt_agent_run_command", "gpt_agent_lsp_status", "gpt_agent_learning_stats", "gpt_agent_project_inspect",
	}
	rawTools := make([]any, 83)
	for i := range rawTools {
		rawTools[i] = map[string]any{"name": "filler"}
	}
	for i, name := range required {
		rawTools[i] = map[string]any{"name": name}
	}
	ok, requiredPresent := mcpToolsListHealth(200, rawTools)
	if !ok || !requiredPresent {
		t.Fatalf("current 83-tool catalog should pass: ok=%v required=%v", ok, requiredPresent)
	}
	rawTools[3] = map[string]any{"name": "filler"}
	ok, requiredPresent = mcpToolsListHealth(200, rawTools)
	if ok || requiredPresent {
		t.Fatalf("missing required direct developer tool should fail: ok=%v required=%v", ok, requiredPresent)
	}
}
