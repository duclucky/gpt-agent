package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLearningEvolutionCapturesVerifiedRepairAndAdaptsSkillScore(t *testing.T) {
	e := newLearningEvolution(t.TempDir())
	scope := "project:d:/repo"
	session := e.beginTask("repo", ".", scope, "Fix flaky build", []string{"gpt-agent-bug-hunter"})
	if session["active"] != true {
		t.Fatalf("session=%v", session)
	}

	e.observeTool("gpt_agent_replace_text", json.RawMessage(`{}`), map[string]any{"ok": true}, nil)
	e.observeTool("gpt_agent_run_command", json.RawMessage(`{"executable":"go","args":["test","./..."]}`), map[string]any{"exitCode": 1}, nil)
	e.observeTool("gpt_agent_run_command", json.RawMessage(`{"executable":"go","args":["test","./..."]}`), nil, errors.New("build failed after test"))
	e.observeTool("gpt_agent_project_verify", json.RawMessage(`{}`), map[string]any{"passed": true}, nil)

	e.beginTask("repo", ".", scope, "Implement next feature", []string{"gpt-agent-repo-surgeon"})
	stats := e.stats()
	if stats["historyCount"] != 1 {
		t.Fatalf("historyCount=%v", stats["historyCount"])
	}
	outcomes := stats["outcomes"].(map[string]int)
	if outcomes["verified"] != 1 {
		t.Fatalf("outcomes=%v", outcomes)
	}
	if stats["pendingCandidates"].(int) < 1 {
		t.Fatalf("pendingCandidates=%v", stats["pendingCandidates"])
	}

	adjusted, feedback := e.skillAdjustedScore("gpt-agent-bug-hunter", .5)
	if adjusted <= .5 {
		t.Fatalf("adjusted=%v feedback=%v", adjusted, feedback)
	}
	if feedback["successes"] != 1 || feedback["failures"] != 0 {
		t.Fatalf("feedback=%v", feedback)
	}

	ctx := e.context(scope, "Fix flaky build", 6)
	candidates := ctx["candidates"].([]map[string]any)
	foundRepair := false
	for _, candidate := range candidates {
		if candidate["type"] == "repair-pattern" {
			foundRepair = true
		}
	}
	if !foundRepair {
		t.Fatalf("candidates=%v", candidates)
	}
}

func TestLearningEvolutionTreatsStrongFollowUpCorrectionAsNegativeFeedback(t *testing.T) {
	e := newLearningEvolution(t.TempDir())
	scope := "project:d:/repo"
	e.beginTask("repo", ".", scope, "Implement parser", []string{"gpt-agent-repo-surgeon"})
	e.observeTool("gpt_agent_project_verify", json.RawMessage(`{}`), map[string]any{"passed": true}, nil)

	e.beginTask("repo", ".", scope, "Vẫn lỗi, sửa lại parser cho đúng", []string{"gpt-agent-bug-hunter"})
	stats := e.db.Skills["gpt-agent-repo-surgeon"]
	if stats.Corrections != 1 || stats.Failures != 1 || stats.Successes != 0 {
		t.Fatalf("skill stats=%+v", stats)
	}
	if len(e.db.History) != 1 || e.db.History[0].Outcome != "corrected" {
		t.Fatalf("history=%+v", e.db.History)
	}
	ctx := e.context(scope, "parser", 6)
	candidates := ctx["candidates"].([]map[string]any)
	found := false
	for _, c := range candidates {
		if c["type"] == "correction-review" {
			found = true
		}
	}
	if !found {
		t.Fatalf("candidates=%v", candidates)
	}
}

func TestLearningEvolutionPersistsAcrossRuntimeRestart(t *testing.T) {
	root := t.TempDir()
	e := newLearningEvolution(root)
	scope := "project:d:/repo"
	e.beginTask("repo", ".", scope, "Run migration", []string{"gpt-agent-verification-gate"})
	e.observeTool("gpt_agent_project_verify", json.RawMessage(`{}`), map[string]any{"passed": true}, nil)
	e.beginTask("repo", ".", scope, "Next task", []string{"gpt-agent-repo-surgeon"})

	reloaded := newLearningEvolution(root)
	stats := reloaded.stats()
	if stats["historyCount"] != 1 {
		t.Fatalf("reloaded stats=%v", stats)
	}
	if stats["activeSession"] != true {
		t.Fatalf("expected active session after reload: %v", stats)
	}
	adjusted, feedback := reloaded.skillAdjustedScore("gpt-agent-verification-gate", .5)
	if adjusted <= .5 || feedback["successes"] != 1 {
		t.Fatalf("adjusted=%v feedback=%v", adjusted, feedback)
	}
}

func TestSanitizeLearningTaskRedactsCredentialAssignments(t *testing.T) {
	got := sanitizeLearningTask("debug API_KEY=super-secret-value and password=hunter2 without leaking it")
	if got == "" {
		t.Fatal("sanitized task is empty")
	}
	if containsAny(got, []string{"super-secret-value", "hunter2"}) {
		t.Fatalf("secret-like value survived sanitization: %q", got)
	}
	if !containsAny(got, []string{"[REDACTED]"}) {
		t.Fatalf("expected redaction marker: %q", got)
	}
}

func TestSanitizeLearningTaskRedactsBearerAndKnownTokenPrefixes(t *testing.T) {
	got := sanitizeLearningTask("retry Authorization: Bearer bearer-secret-123 and sk-exampletoken123456 and ghp_exampletoken123456")
	for _, secret := range []string{"bearer-secret-123", "sk-exampletoken123456", "ghp_exampletoken123456"} {
		if strings.Contains(got, secret) {
			t.Fatalf("token survived sanitization: %q", got)
		}
	}
	if !strings.Contains(got, "[REDACTED]") || !strings.Contains(got, "[REDACTED_TOKEN]") {
		t.Fatalf("expected bearer/token redaction markers: %q", got)
	}
}

func TestLearningEvolutionDoesNotPersistRawToolArgumentsOrPayloads(t *testing.T) {
	root := t.TempDir()
	e := newLearningEvolution(root)
	e.beginTask("repo", ".", "project:d:/repo", "Fix formatter", []string{"gpt-agent-repo-surgeon"})
	e.observeTool(
		"gpt_agent_full_shell",
		json.RawMessage(`{"command":"Set-Content note.txt RAW_TOOL_SECRET_123"}`),
		map[string]any{"stdout": "RAW_PAYLOAD_SECRET_456"},
		nil,
	)
	buf, err := os.ReadFile(filepath.Join(root, "learning", "evolution.json"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(buf)
	for _, secret := range []string{"RAW_TOOL_SECRET_123", "RAW_PAYLOAD_SECRET_456"} {
		if strings.Contains(text, secret) {
			t.Fatalf("raw tool data persisted in evolution state: %s", secret)
		}
	}
}

func TestLearningEvolutionResolvesCandidateLifecycle(t *testing.T) {
	e := newLearningEvolution(t.TempDir())
	scope := "project:d:/repo"
	e.beginTask("repo", ".", scope, "Fix parser regression", []string{"gpt-agent-bug-hunter"})
	e.observeTool("gpt_agent_run_command", json.RawMessage(`{"executable":"go","args":["test","./..."]}`), map[string]any{"exitCode": 1}, nil)
	e.observeTool("gpt_agent_project_verify", json.RawMessage(`{}`), map[string]any{"passed": true}, nil)
	e.beginTask("repo", ".", scope, "Next task", []string{"gpt-agent-repo-surgeon"})

	ctx := e.context(scope, "parser regression", 6)
	candidates := ctx["candidates"].([]map[string]any)
	if len(candidates) == 0 {
		t.Fatal("expected a learning candidate")
	}
	candidateID := candidates[0]["id"].(string)
	resolved, err := e.resolveCandidates(scope, []string{candidateID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := resolved["promoted"].([]string); len(got) != 1 || got[0] != candidateID {
		t.Fatalf("resolution=%v", resolved)
	}
	ctx = e.context(scope, "parser regression", 6)
	for _, candidate := range ctx["candidates"].([]map[string]any) {
		if candidate["id"] == candidateID {
			t.Fatalf("promoted candidate remained pending: %v", candidate)
		}
	}
	statuses := e.stats()["candidateStatuses"].(map[string]int)
	if statuses["promoted"] != 1 {
		t.Fatalf("candidateStatuses=%v", statuses)
	}
	if _, err := e.resolveCandidates(scope, []string{candidateID}, nil); err == nil {
		t.Fatal("already-resolved candidate was accepted again")
	}
}

func TestLearningEvolutionRejectsCrossScopeCandidateResolution(t *testing.T) {
	e := newLearningEvolution(t.TempDir())
	e.db.Candidates = append(e.db.Candidates, learningCandidate{ID: "candidate-1", Scope: "project:a", Status: "pending"})
	if err := e.validateCandidateResolution("project:b", []string{"candidate-1"}, nil); err == nil {
		t.Fatal("cross-scope candidate resolution was accepted")
	}
	if err := e.validateCandidateResolution("project:a", []string{"candidate-1"}, []string{"candidate-1"}); err == nil {
		t.Fatal("candidate was accepted as both promoted and dismissed")
	}
}

func containsAny(text string, items []string) bool {
	for _, item := range items {
		if len(item) > 0 && len(text) >= len(item) {
			for i := 0; i+len(item) <= len(text); i++ {
				if text[i:i+len(item)] == item {
					return true
				}
			}
		}
	}
	return false
}

func TestMutationSignalIgnoresReadOnlyShellAndVerificationCommands(t *testing.T) {
	if mutationSignal("gpt_agent_full_shell", json.RawMessage(`{"command":"Get-Content README.md; git status --short"}`)) {
		t.Fatal("read-only full shell command must not invalidate verification")
	}
	if !mutationSignal("gpt_agent_full_shell", json.RawMessage(`{"command":"Set-Content README.md 'changed'"}`)) {
		t.Fatal("Set-Content must be recognized as mutation")
	}
	if mutationSignal("gpt_agent_full_shell", json.RawMessage(`{"command":"gofmt -d main.go"}`)) {
		t.Fatal("gofmt -d must not be recognized as mutation")
	}
	if !mutationSignal("gpt_agent_full_shell", json.RawMessage(`{"command":"gofmt -w main.go"}`)) {
		t.Fatal("gofmt -w in full shell must be recognized as mutation")
	}
	if mutationSignal("gpt_agent_run_command", json.RawMessage(`{"executable":"go","args":["test","./..."]}`)) {
		t.Fatal("go test must be verification, not mutation")
	}
	if !mutationSignal("gpt_agent_run_command", json.RawMessage(`{"executable":"gofmt","args":["-w","main.go"]}`)) {
		t.Fatal("gofmt -w must be recognized as mutation")
	}
}

func TestCodingPackPreservesIndividualSkillVersions(t *testing.T) {
	store := newLearningStore(t.TempDir())
	result, err := store.installCodingPack()
	if err != nil {
		t.Fatal(err)
	}
	if result["version"] != codingSkillPackVersion {
		t.Fatalf("pack version=%v want=%s", result["version"], codingSkillPackVersion)
	}
	var pack []embeddedSkill
	if err := json.Unmarshal(embeddedCodingSkillPack, &pack); err != nil {
		t.Fatal(err)
	}
	for _, expected := range pack {
		got, err := store.getSkill(expected.Slug, false)
		if err != nil {
			t.Fatal(err)
		}
		meta := got["metadata"].(skillMetadata)
		if meta.Version != expected.Metadata.Version {
			t.Fatalf("%s metadata version=%s want=%s", expected.Slug, meta.Version, expected.Metadata.Version)
		}
		content := got["content"].(string)
		if !strings.Contains(content, "version: "+expected.Metadata.Version) {
			t.Fatalf("%s SKILL.md frontmatter does not match metadata version %s", expected.Slug, expected.Metadata.Version)
		}
	}
}
