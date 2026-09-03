package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"
)

type gateway struct {
	jobs       *jobManager
	native     *nativeTools
	catalog    *toolCatalog
	configPath string
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   any             `json:"error,omitempty"`
}

const (
	nativeToolAutoHandoffAfter = 30 * time.Second
	nativeToolAutoMaxRun       = 2 * time.Hour
)

func newGateway(jobs *jobManager, native *nativeTools, catalog *toolCatalog, configPath string) *gateway {
	return &gateway{jobs: jobs, native: native, catalog: catalog, configPath: configPath}
}

func (g *gateway) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", g.handleHealth)
	mux.HandleFunc("/mcp", g.handleMCP)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found", "endpoints": []string{"/mcp", "/healthz"}})
	})
	return mux
}

func (g *gateway) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"name":     "GPT Agent",
		"version":  runtimeVersion,
		"platform": runtime.GOOS,
		"core": map[string]any{
			"engine":            "go",
			"goVersion":         runtime.Version(),
			"pid":               os.Getpid(),
			"configPath":        g.configPath,
			"nativeTools":       g.native.names(),
			"nativeToolCount":   len(g.native.names()),
			"catalogToolCount":  len(g.catalog.Tools),
			"schemaOwner":       "go",
			"automaticLearning": g.native.evolution != nil,
		},
		"backend": map[string]any{
			"engine":   "none",
			"ready":    true,
			"required": false,
		},
	})
}

func (g *gateway) handleMCP(w http.ResponseWriter, r *http.Request) {
	if !requestAllowed(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "Host/Origin rejected. Runtime is loopback-only."})
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method_not_allowed"})
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		writeRPCError(w, nil, -32700, "Parse error", http.StatusBadRequest)
		return
	}
	var request rpcRequest
	if err := json.Unmarshal(body, &request); err != nil {
		writeRPCError(w, nil, -32700, "Parse error", http.StatusBadRequest)
		return
	}
	if request.JSONRPC != "" && request.JSONRPC != "2.0" {
		writeRPCError(w, request.ID, -32600, "Invalid Request", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(request.Method) == "" {
		writeRPCError(w, request.ID, -32600, "Invalid Request", http.StatusBadRequest)
		return
	}

	switch request.Method {
	case "initialize":
		g.handleInitialize(w, request)
	case "notifications/initialized", "notifications/cancelled":
		w.WriteHeader(http.StatusAccepted)
	case "ping":
		writeMCPEnvelope(w, http.StatusOK, "text/event-stream", rpcResponse{JSONRPC: "2.0", ID: request.ID, Result: map[string]any{}})
	case "tools/list":
		g.handleToolsList(w, request)
	case "tools/call":
		g.handleToolCall(w, r, request)
	default:
		writeRPCError(w, request.ID, -32601, "Method not found", http.StatusOK)
	}
}

func (g *gateway) handleInitialize(w http.ResponseWriter, request rpcRequest) {
	const instructions = "You are connected to GPT Agent, a direct local developer runtime for ChatGPT. Use it to inspect and modify code only inside configured workspaces. Inspect and search before editing; prefer project-scoped gpt_agent_fast_context, gpt_agent_project_inspect, LSP, and structural search when they reduce guessing. For non-trivial coding work, use gpt_agent_coding_brief to load project context, relevant durable memory, and reusable coding skills. Prefer minimal targeted edits, SHA-256 optimistic checks, and checkpoints before broad or risky Git-backed changes. Run targeted verification during iteration and the relevant project verification gates before completion. Review the final diff and risk signals. Persist only durable verified project facts or reusable procedures; never persist secrets, credentials, transient logs, guesses, or unverified hypotheses. SAFE command execution is preferred. FULL SHELL requires a local time-bounded grant and should be used only when specialized or SAFE tools are insufficient. Do not commit, push, publish, deploy, or touch production unless the user explicitly authorizes that action. Native tool calls that exceed the synchronous window may continue as recoverable jobs; use gpt_agent_job_list, gpt_agent_job_status, and gpt_agent_job_result to reconnect to them. GPT Agent contains no embedded model and no secondary coding agent; ChatGPT remains the sole reasoning and agent loop."
	writeMCPEnvelope(w, http.StatusOK, "text/event-stream", rpcResponse{
		JSONRPC: "2.0",
		ID:      request.ID,
		Result: map[string]any{
			"protocolVersion": "2025-11-25",
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "GPT Agent", "version": runtimeVersion},
			"instructions":    instructions,
		},
	})
}

func (g *gateway) handleToolsList(w http.ResponseWriter, request rpcRequest) {
	tools := make([]map[string]any, len(g.catalog.Tools))
	copy(tools, g.catalog.Tools)
	writeMCPEnvelope(w, http.StatusOK, "text/event-stream", rpcResponse{
		JSONRPC: "2.0",
		ID:      request.ID,
		Result:  map[string]any{"tools": tools},
	})
}

func (g *gateway) handleToolCall(w http.ResponseWriter, r *http.Request, request rpcRequest) {
	var params toolCallParams
	if err := json.Unmarshal(request.Params, &params); err != nil || strings.TrimSpace(params.Name) == "" {
		writeRPCError(w, request.ID, -32602, "Invalid params", http.StatusOK)
		return
	}
	if strings.HasPrefix(params.Name, "gpt_agent_job_") {
		g.handleJobTool(w, request, params)
		return
	}
	if g.native == nil || !g.native.supports(params.Name) {
		writeNativeToolEnvelope(w, request.ID, nil, fmt.Errorf("unknown tool: %s", params.Name))
		return
	}
	g.handleNativeTool(w, r, request, params)
}

func (g *gateway) handleNativeTool(w http.ResponseWriter, r *http.Request, request rpcRequest, params toolCallParams) {
	g.handleNativeToolWithHandoff(w, r, request, params, nativeToolAutoHandoffAfter)
}

func (g *gateway) handleNativeToolWithHandoff(w http.ResponseWriter, r *http.Request, request rpcRequest, params toolCallParams, handoffAfter time.Duration) {
	if g.jobs == nil || handoffAfter <= 0 {
		payload, toolErr := g.native.invoke(r.Context(), params.Name, params.Arguments)
		writeNativeToolEnvelope(w, request.ID, payload, toolErr)
		return
	}

	startedAt := time.Now().UTC()
	runCtx, cancel := context.WithTimeout(context.Background(), nativeToolAutoMaxRun)
	done := make(chan toolExecutionResult, 1)
	go func() {
		payload, err := g.native.invoke(runCtx, params.Name, params.Arguments)
		done <- toolExecutionResult{Payload: payload, Err: err}
	}()

	timer := time.NewTimer(handoffAfter)
	defer timer.Stop()
	finishSync := func(result toolExecutionResult) {
		cancel()
		writeNativeToolEnvelope(w, request.ID, result.Payload, result.Err)
	}

	select {
	case result := <-done:
		finishSync(result)
	case <-r.Context().Done():
		select {
		case result := <-done:
			finishSync(result)
		default:
			record, err := g.jobs.adoptRunning(params.Name, startedAt, done, runCtx, cancel)
			if err != nil {
				cancel()
				return
			}
			// The transport is already gone, so there is no reliable response path here.
			// Preserve the exact invocation as a recoverable job instead; callers can
			// rediscover it with gpt_agent_job_list after reconnecting.
			_ = record
			return
		}
	case <-timer.C:
		select {
		case result := <-done:
			finishSync(result)
		default:
			record, err := g.jobs.adoptRunning(params.Name, startedAt, done, runCtx, cancel)
			if err != nil {
				cancel()
				writeNativeToolEnvelope(w, request.ID, nil, fmt.Errorf("automatic async handoff failed: %w", err))
				return
			}
			payload := jobSummary(record)
			payload["autoAsync"] = true
			payload["handoffAfterMs"] = handoffAfter.Milliseconds()
			payload["hint"] = "The original tool invocation is still running exactly once. Use gpt_agent_job_status and gpt_agent_job_result with this jobId; do not retry the original operation."
			writeNativeToolEnvelope(w, request.ID, payload, nil)
		}
	}
}

func (g *gateway) handleJobTool(w http.ResponseWriter, request rpcRequest, params toolCallParams) {
	var payload map[string]any
	var err error
	switch params.Name {
	case "gpt_agent_job_start":
		var args struct {
			Tool      string          `json:"tool"`
			Arguments json.RawMessage `json:"arguments"`
			MaxRunMS  int             `json:"maxRunMs"`
		}
		if decodeErr := decodeObject(params.Arguments, &args); decodeErr != nil {
			err = decodeErr
			break
		}
		maxRun := timeDurationMS(args.MaxRunMS)
		record, startErr := g.jobs.start(args.Tool, args.Arguments, maxRun)
		if startErr != nil {
			err = startErr
			break
		}
		payload = jobSummary(record)
		payload["hint"] = "Use gpt_agent_job_status for progress and gpt_agent_job_result after completion."
	case "gpt_agent_job_status":
		var args struct {
			JobID string `json:"jobId"`
		}
		if decodeErr := decodeObject(params.Arguments, &args); decodeErr != nil {
			err = decodeErr
			break
		}
		record, ok := g.jobs.get(args.JobID)
		if !ok {
			err = fmt.Errorf("unknown job id: %s", args.JobID)
			break
		}
		payload = jobSummary(record)
	case "gpt_agent_job_result":
		var args struct {
			JobID string `json:"jobId"`
		}
		if decodeErr := decodeObject(params.Arguments, &args); decodeErr != nil {
			err = decodeErr
			break
		}
		record, ok := g.jobs.get(args.JobID)
		if !ok {
			err = fmt.Errorf("unknown job id: %s", args.JobID)
			break
		}
		payload = jobSummary(record)
		if record.State == jobStateSucceeded || record.State == jobStateFailed || record.State == jobStateTimedOut {
			payload["backendResult"] = record.Result
			payload["toolResult"] = record.Result
		}
	case "gpt_agent_job_list":
		items := g.jobs.list()
		jobs := make([]map[string]any, 0, len(items))
		for _, record := range items {
			jobs = append(jobs, jobSummary(record))
		}
		payload = map[string]any{"jobs": jobs, "count": len(jobs), "capacity": maxJobs, "engine": "go"}
	default:
		err = fmt.Errorf("unknown Go core job tool: %s", params.Name)
	}

	writeNativeToolEnvelope(w, request.ID, payload, err)
}

func timeDurationMS(ms int) time.Duration {
	if ms <= 0 {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}

func decodeObject(raw json.RawMessage, target any) error {
	if len(raw) == 0 || string(raw) == "null" {
		raw = json.RawMessage(`{}`)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("invalid tool arguments: %w", err)
	}
	return nil
}

func toolResultObject(payload any, toolErr error) map[string]any {
	if toolErr != nil {
		return map[string]any{
			"content": []any{map[string]any{"type": "text", "text": toolErr.Error()}},
			"isError": true,
		}
	}
	encoded, _ := json.MarshalIndent(payload, "", "  ")
	return map[string]any{
		"content":           []any{map[string]any{"type": "text", "text": string(encoded)}},
		"structuredContent": payload,
	}
}

func writeNativeToolEnvelope(w http.ResponseWriter, id json.RawMessage, payload any, toolErr error) {
	envelope := rpcResponse{JSONRPC: "2.0", ID: id, Result: toolResultObject(payload, toolErr)}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	writeMCPEnvelope(w, http.StatusOK, "text/event-stream", envelope)
}

func writeRPCError(w http.ResponseWriter, id json.RawMessage, code int, message string, status int) {
	if id == nil {
		id = json.RawMessage("null")
	}
	writeMCPEnvelope(w, status, "text/event-stream", rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   map[string]any{"code": code, "message": message},
	})
}

func decodeMCPEnvelope(body []byte) (map[string]any, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("empty MCP response")
	}
	var direct map[string]any
	if trimmed[0] == '{' && json.Unmarshal(trimmed, &direct) == nil {
		return direct, nil
	}

	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 64*1024), 16<<20)
	var dataLines []string
	var last map[string]any
	flush := func() {
		if len(dataLines) == 0 {
			return
		}
		candidate := strings.Join(dataLines, "\n")
		var envelope map[string]any
		if json.Unmarshal([]byte(candidate), &envelope) == nil {
			last = envelope
		}
		dataLines = dataLines[:0]
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	flush()
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if last == nil {
		return nil, fmt.Errorf("no JSON MCP event found")
	}
	return last, nil
}

func writeMCPEnvelope(w http.ResponseWriter, status int, contentType string, envelope any) {
	encoded, err := json.Marshal(envelope)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "encode_response", "message": err.Error()})
		return
	}
	if strings.Contains(strings.ToLower(contentType), "text/event-stream") || contentType == "" {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", encoded)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(encoded)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func requestAllowed(req *http.Request) bool {
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	hostname := host
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		hostname = parsed
	} else {
		hostname = strings.Trim(hostname, "[]")
	}
	if !isLoopbackHost(hostname) {
		return false
	}
	origin := strings.TrimSpace(req.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return isLoopbackHost(parsed.Hostname())
}
