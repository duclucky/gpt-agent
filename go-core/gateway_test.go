package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDecodeMCPEnvelopeLargeSSE(t *testing.T) {
	text := strings.Repeat("x", 200000)
	envelope := map[string]any{"jsonrpc": "2.0", "id": 7, "result": map[string]any{"value": text}}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("event: message\ndata: " + string(encoded) + "\n\n")
	parsed, err := decodeMCPEnvelope(body)
	if err != nil {
		t.Fatal(err)
	}
	result := parsed["result"].(map[string]any)
	if result["value"] != text {
		t.Fatalf("large SSE payload changed: got %d chars", len(result["value"].(string)))
	}
}

func TestInitializeInstructionsDescribeDirectGPTAgentRuntime(t *testing.T) {
	g := newGateway(nil, nil, nil, "")
	rec := httptest.NewRecorder()
	g.handleInitialize(rec, rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`1`)})
	parsed, err := decodeMCPEnvelope(rec.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	result, ok := parsed["result"].(map[string]any)
	if !ok {
		t.Fatalf("initialize result type=%T", parsed["result"])
	}
	instructions, _ := result["instructions"].(string)
	for _, required := range []string{"GPT Agent", "gpt_agent_fast_context", "gpt_agent_job_status"} {
		if !strings.Contains(instructions, required) {
			t.Fatalf("initialize instructions missing %q", required)
		}
	}
}

func TestCatalogContainsExactlyFourJobTools(t *testing.T) {
	catalog, err := loadToolCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(catalog.Tools), 83; got != want {
		t.Fatalf("tool count=%d want=%d", got, want)
	}
	seen := map[string]int{}
	jobs := 0
	for _, tool := range catalog.Tools {
		name, _ := tool["name"].(string)
		seen[name]++
		if strings.HasPrefix(name, "gpt_agent_job_") {
			jobs++
		}
	}
	if jobs != 4 {
		t.Fatalf("job tool count=%d want=4", jobs)
	}
	for name, count := range seen {
		if name == "" || count != 1 {
			t.Fatalf("catalog duplicate/invalid tool %q count=%d", name, count)
		}
	}
}

func TestAsyncJobUsesGoInvokerWithoutHoldingCaller(t *testing.T) {
	invoked := make(chan struct{}, 1)
	manager := newJobManager(func(ctx context.Context, tool string, arguments json.RawMessage) (any, error) {
		invoked <- struct{}{}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(120 * time.Millisecond):
		}
		return map[string]any{"ok": true, "tool": tool}, nil
	}, func(tool string) bool { return tool == "gpt_agent_fake" })

	started := time.Now()
	record, err := manager.start("gpt_agent_fake", json.RawMessage(`{"value":1}`), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 50*time.Millisecond {
		t.Fatalf("job start blocked for %s", elapsed)
	}
	select {
	case <-invoked:
	case <-time.After(time.Second):
		t.Fatal("Go invoker was not called")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		current, ok := manager.get(record.ID)
		if !ok {
			t.Fatal("job disappeared")
		}
		if current.State == jobStateSucceeded {
			result, ok := current.Result.(map[string]any)
			if !ok {
				t.Fatalf("completed job result type=%T", current.Result)
			}
			if result["isError"] == true {
				t.Fatalf("completed Go job unexpectedly returned error: %#v", result)
			}
			if _, ok := result["structuredContent"]; !ok {
				t.Fatalf("completed Go job missing structuredContent: %#v", result)
			}
			if jobSummary(current)["engine"] != "go" {
				t.Fatal("job summary did not report Go engine")
			}
			return
		}
		if current.State == jobStateFailed || current.State == jobStateTimedOut {
			t.Fatalf("job ended in %s: %s", current.State, current.Error)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("job did not complete")
}

func TestAsyncJobRejectsUnknownOrRecursiveTools(t *testing.T) {
	manager := newJobManager(func(context.Context, string, json.RawMessage) (any, error) { return nil, nil }, func(tool string) bool { return tool == "gpt_agent_known" })
	if _, err := manager.start("gpt_agent_missing", json.RawMessage(`{}`), time.Second); err == nil {
		t.Fatal("unknown tool was accepted")
	}
	if _, err := manager.start("gpt_agent_job_status", json.RawMessage(`{}`), time.Second); err == nil {
		t.Fatal("recursive job tool was accepted")
	}
}

func TestNativeToolAutoHandoffContinuesSameInvocation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()

	root := t.TempDir()
	native := testNativeTools(t, root)
	manager := newJobManager(native.invoke, native.supports)
	g := newGateway(manager, native, nil, "")
	args, _ := json.Marshal(map[string]any{"host": "127.0.0.1", "port": port, "timeoutMS": 180, "intervalMS": 20})
	params := toolCallParams{Name: "gpt_agent_wait_port", Arguments: args}
	request := rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`1`)}
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8765/mcp", nil)
	rec := httptest.NewRecorder()

	started := time.Now()
	g.handleNativeToolWithHandoff(rec, req, request, params, 25*time.Millisecond)
	if elapsed := time.Since(started); elapsed > 120*time.Millisecond {
		t.Fatalf("auto handoff blocked for %s", elapsed)
	}
	parsed, err := decodeMCPEnvelope(rec.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	result := parsed["result"].(map[string]any)
	payload := result["structuredContent"].(map[string]any)
	if payload["autoAsync"] != true {
		t.Fatalf("missing autoAsync handoff payload: %#v", payload)
	}
	jobID, _ := payload["jobId"].(string)
	if jobID == "" {
		t.Fatalf("handoff missing jobId: %#v", payload)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		current, ok := manager.get(jobID)
		if !ok {
			t.Fatal("auto-handoff job disappeared")
		}
		if current.State == jobStateSucceeded {
			wrapped := current.Result.(map[string]any)
			structured := wrapped["structuredContent"].(map[string]any)
			if structured["open"] != false {
				t.Fatalf("continued invocation result=%#v", structured)
			}
			return
		}
		if current.State == jobStateFailed || current.State == jobStateTimedOut {
			t.Fatalf("auto-handoff job ended in %s: %s", current.State, current.Error)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("auto-handoff invocation did not complete")
}

func TestNativeToolDisconnectAdoptsRunningInvocation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()

	root := t.TempDir()
	native := testNativeTools(t, root)
	manager := newJobManager(native.invoke, native.supports)
	g := newGateway(manager, native, nil, "")
	args, _ := json.Marshal(map[string]any{"host": "127.0.0.1", "port": port, "timeoutMS": 220, "intervalMS": 20})
	params := toolCallParams{Name: "gpt_agent_wait_port", Arguments: args}
	request := rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`2`)}
	ctx, cancelRequest := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8765/mcp", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		g.handleNativeToolWithHandoff(rec, req, request, params, time.Second)
		close(done)
	}()
	time.Sleep(25 * time.Millisecond)
	cancelRequest()
	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("handler did not return promptly after request disconnect")
	}

	jobs := manager.list()
	if len(jobs) != 1 {
		t.Fatalf("disconnect should preserve one adopted job, got %d: %#v", len(jobs), jobs)
	}
	jobID := jobs[0].ID
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		current, ok := manager.get(jobID)
		if !ok {
			t.Fatal("adopted disconnect job disappeared")
		}
		if current.State == jobStateSucceeded {
			wrapped := current.Result.(map[string]any)
			structured := wrapped["structuredContent"].(map[string]any)
			if structured["open"] != false {
				t.Fatalf("continued disconnect invocation result=%#v", structured)
			}
			return
		}
		if current.State == jobStateFailed || current.State == jobStateTimedOut {
			t.Fatalf("disconnect-adopted job ended in %s: %s", current.State, current.Error)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("disconnect-adopted invocation did not complete")
}

func TestAutoHandoffGuardCoversCatalogMaxRun(t *testing.T) {
	catalog, err := loadToolCatalog()
	if err != nil {
		t.Fatal(err)
	}
	guardMS := nativeToolAutoMaxRun.Milliseconds()
	for _, tool := range catalog.Tools {
		name, _ := tool["name"].(string)
		input, _ := tool["inputSchema"].(map[string]any)
		props, _ := input["properties"].(map[string]any)
		maxRun, _ := props["maxRunMs"].(map[string]any)
		if maxRun == nil {
			continue
		}
		maximum := int64(intFromAny(maxRun["maximum"]))
		if maximum > guardMS {
			t.Fatalf("%s maxRunMs.maximum=%d exceeds auto-handoff guard=%d", name, maximum, guardMS)
		}
	}
}

func TestLongCommandCatalogDefaultsMatchAutoAsyncWindow(t *testing.T) {
	catalog, err := loadToolCatalog()
	if err != nil {
		t.Fatal(err)
	}
	wanted := map[string]bool{
		"gpt_agent_run_command":   false,
		"gpt_agent_untrusted_run": false,
		"gpt_agent_full_shell":    false,
	}
	for _, tool := range catalog.Tools {
		name, _ := tool["name"].(string)
		if _, ok := wanted[name]; !ok {
			continue
		}
		input := tool["inputSchema"].(map[string]any)
		props := input["properties"].(map[string]any)
		timeout := props["timeoutMs"].(map[string]any)
		if got := intFromAny(timeout["default"]); got != defaultCommandTimeoutMS {
			t.Fatalf("%s timeout default=%d want=%d", name, got, defaultCommandTimeoutMS)
		}
		wanted[name] = true
	}
	for name, seen := range wanted {
		if !seen {
			t.Fatalf("missing tool %s", name)
		}
	}
}

func TestJobResultIncludesTimedOutBackendResult(t *testing.T) {
	manager := newJobManager(func(ctx context.Context, tool string, arguments json.RawMessage) (any, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}, func(tool string) bool { return tool == "gpt_agent_fake_timeout" })
	record, err := manager.start("gpt_agent_fake_timeout", json.RawMessage(`{}`), 25*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		current, ok := manager.get(record.ID)
		if !ok {
			t.Fatal("timed-out job disappeared")
		}
		if current.State == jobStateTimedOut {
			g := &gateway{jobs: manager}
			args, _ := json.Marshal(map[string]any{"jobId": record.ID})
			params := toolCallParams{Name: "gpt_agent_job_result", Arguments: args}
			req := rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`11`)}
			rec := httptest.NewRecorder()
			g.handleJobTool(rec, req, params)
			parsed, err := decodeMCPEnvelope(rec.Body.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			result := parsed["result"].(map[string]any)
			payload := result["structuredContent"].(map[string]any)
			if payload["state"] != jobStateTimedOut {
				t.Fatalf("job result state=%v want=%s", payload["state"], jobStateTimedOut)
			}
			if payload["backendResult"] == nil || payload["toolResult"] == nil {
				t.Fatalf("timed-out result missing backend/tool result: %#v", payload)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("job did not time out")
}

func TestGatewayHandlesPingAndInitializedWithoutBackend(t *testing.T) {
	g := &gateway{}

	ping := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8765/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping","params":{}}`))
	ping.Host = "127.0.0.1:8765"
	pingRec := httptest.NewRecorder()
	g.handleMCP(pingRec, ping)
	if pingRec.Code != http.StatusOK {
		t.Fatalf("ping status=%d body=%s", pingRec.Code, pingRec.Body.String())
	}
	parsed, err := decodeMCPEnvelope(pingRec.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := parsed["result"]; !ok {
		t.Fatalf("ping missing result: %#v", parsed)
	}

	notification := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8765/mcp", strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`))
	notification.Host = "127.0.0.1:8765"
	notifyRec := httptest.NewRecorder()
	g.handleMCP(notifyRec, notification)
	if notifyRec.Code != http.StatusAccepted {
		t.Fatalf("initialized notification status=%d want=%d", notifyRec.Code, http.StatusAccepted)
	}
}

func TestRequestAllowedLoopbackOnly(t *testing.T) {
	for _, host := range []string{"127.0.0.1:8765", "localhost:8765", "[::1]:8765"} {
		req := httptest.NewRequest(http.MethodPost, "http://"+host+"/mcp", nil)
		req.Host = host
		if !requestAllowed(req) {
			t.Fatalf("loopback host rejected: %s", host)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/mcp", nil)
	req.Host = "example.com"
	if requestAllowed(req) {
		t.Fatal("non-loopback Host accepted")
	}

	originReq := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/mcp", nil)
	originReq.Host = net.JoinHostPort("127.0.0.1", strconv.Itoa(8765))
	originReq.Header.Set("Origin", "https://example.com")
	if requestAllowed(originReq) {
		t.Fatal("non-loopback Origin accepted")
	}
}
