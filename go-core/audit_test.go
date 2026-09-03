package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestAuditWriterCompatibleWithNodeConcurrentWriters(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node executable is required for Node/Go audit compatibility test")
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	auditModule, err := filepath.Abs(filepath.Join(cwd, "..", "src", "audit.js"))
	if err != nil {
		t.Fatal(err)
	}
	dataRoot := t.TempDir()
	writer := newAuditWriter(dataRoot)

	if _, err := writer.append(map[string]any{"type": "go.test.start", "value": 1}); err != nil {
		t.Fatalf("initial Go audit append: %v", err)
	}

	script := `
import { pathToFileURL } from 'node:url';
const mod = await import(pathToFileURL(process.env.GPT_AGENT_AUDIT_MODULE).href);
for (let i = 0; i < 20; i++) await mod.audit({type:'node.test.concurrent', value:i});
const verify = await mod.verifyAudit();
console.log(JSON.stringify(verify));
if (!verify.ok) process.exit(3);
`
	cmd := exec.Command(node, "--input-type=module", "-e", script)
	cmd.Env = append(os.Environ(),
		"GPT_AGENT_HOME="+dataRoot,
		"GPT_AGENT_AUDIT_MODULE="+auditModule,
	)
	var nodeOut strings.Builder
	cmd.Stdout = &nodeOut
	cmd.Stderr = &nodeOut
	if err := cmd.Start(); err != nil {
		t.Fatalf("start Node audit writer: %v", err)
	}

	for i := 0; i < 20; i++ {
		if _, err := writer.append(map[string]any{"type": "go.test.concurrent", "value": i}); err != nil {
			t.Fatalf("Go concurrent audit append %d: %v", i, err)
		}
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("Node audit writer/verify failed: %v\n%s", err, nodeOut.String())
	}

	lines, err := os.ReadFile(writer.path)
	if err != nil {
		t.Fatal(err)
	}
	recordLines := strings.Split(strings.TrimSpace(string(lines)), "\n")
	if len(recordLines) != 41 {
		t.Fatalf("mixed audit count=%d, want 41", len(recordLines))
	}
	for _, line := range recordLines {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		if _, ok := record["eventHash"].(string); !ok {
			t.Fatalf("missing eventHash in record: %s", line)
		}
	}
}

func TestHashRawJSONNormalizesObjects(t *testing.T) {
	a := hashRawJSON(json.RawMessage(`{"b":2,"a":1}`))
	b := hashRawJSON(json.RawMessage(`{ "a": 1, "b": 2 }`))
	if a != b {
		t.Fatalf("object digest should ignore whitespace/key order: %s != %s", a, b)
	}
	if len(a) != 64 {
		t.Fatalf("unexpected digest length %s", strconv.Itoa(len(a)))
	}
}

func TestAuditWriterRecoversCorruptTailWithoutLosingEvidence(t *testing.T) {
	dataRoot := t.TempDir()
	writer := newAuditWriter(dataRoot)
	if _, err := writer.append(map[string]any{"type": "test.before"}); err != nil {
		t.Fatal(err)
	}

	f, err := os.OpenFile(writer.path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte{0, 'b', 'a', 'd', '\n'}); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	corrupt, err := os.ReadFile(writer.path)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := writer.append(map[string]any{"type": "tool.success", "tool": "gpt_agent_status"}); err != nil {
		t.Fatalf("append after corrupt tail: %v", err)
	}
	active, err := os.ReadFile(writer.path)
	if err != nil {
		t.Fatal(err)
	}
	verified := verifyAuditRaw(active)
	if !verified.OK || verified.Count != 3 {
		t.Fatalf("recovered audit verify=%#v\n%s", verified, active)
	}

	backups, err := filepath.Glob(filepath.Join(dataRoot, "audit-corruption-backups", "*.audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 {
		t.Fatalf("corrupt audit backups=%d want=1: %#v", len(backups), backups)
	}
	backup, err := os.ReadFile(backups[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != string(corrupt) {
		t.Fatal("corrupt audit backup did not preserve the original bytes")
	}

	lines := strings.Split(strings.TrimSpace(string(active)), "\n")
	var recovery map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &recovery); err != nil {
		t.Fatal(err)
	}
	if recovery["type"] != "audit.tail.recovery" {
		t.Fatalf("recovery event type=%v", recovery["type"])
	}
}
