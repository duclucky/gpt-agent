package main

import (
	"context"
	"encoding/json"
	"testing"
)

func TestLearnToolPromotesReviewedCandidateWithDurableMemory(t *testing.T) {
	root := t.TempDir()
	native := testNativeTools(t, root)
	project, err := native.projectInspect(context.Background(), "test", ".", false, 10000)
	if err != nil {
		t.Fatal(err)
	}
	scope := projectLearningScopeNative(project)
	native.evolution.db.Candidates = append(native.evolution.db.Candidates, learningCandidate{
		ID:              "candidate-promote",
		Type:            "repair-pattern",
		Scope:           scope,
		Task:            "Fix parser regression",
		TaskFingerprint: "fp",
		Confidence:      .9,
		Occurrences:     1,
		Status:          "pending",
	})

	raw, _ := json.Marshal(map[string]any{
		"workspaceId":  "test",
		"path":         ".",
		"candidateIds": []string{"candidate-promote"},
		"memories": []map[string]any{{
			"content":    "Parser fixtures require the generated table to be refreshed before verification.",
			"kind":       "lesson",
			"importance": .8,
		}},
	})
	result, err := native.learnTool(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	resolution := result["candidateResolution"].(map[string]any)
	promoted := resolution["promoted"].([]string)
	if len(promoted) != 1 || promoted[0] != "candidate-promote" {
		t.Fatalf("candidateResolution=%v", resolution)
	}
	if native.evolution.db.Candidates[0].Status != "promoted" {
		t.Fatalf("candidate status=%q", native.evolution.db.Candidates[0].Status)
	}
	memories, err := native.learning.search("generated table", 10, nil, []string{scope})
	if err != nil {
		t.Fatal(err)
	}
	if len(memories) != 1 {
		t.Fatalf("memories=%v", memories)
	}
}

func TestLearnToolValidatesCandidateBeforePersistingMemory(t *testing.T) {
	root := t.TempDir()
	native := testNativeTools(t, root)
	raw, _ := json.Marshal(map[string]any{
		"workspaceId":  "test",
		"path":         ".",
		"candidateIds": []string{"missing-candidate"},
		"memories": []map[string]any{{
			"content": "THIS_MUST_NOT_PERSIST",
			"kind":    "lesson",
		}},
	})
	if _, err := native.learnTool(context.Background(), raw); err == nil {
		t.Fatal("unknown candidate was accepted")
	}
	memories, err := native.learning.search("THIS_MUST_NOT_PERSIST", 10, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(memories) != 0 {
		t.Fatalf("memory was persisted before candidate validation: %v", memories)
	}
}
