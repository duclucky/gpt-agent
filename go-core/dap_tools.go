package main

import (
	"context"
	"encoding/json"
)

func (n *nativeTools) debugPythonStartTool(ctx context.Context, raw json.RawMessage) (any, error) {
	var a struct {
		WorkspaceID, Program, Cwd string
		Args                      []string
		Breakpoints               []map[string]any
		StopOnEntry               bool
		JustMyCode                *bool
	}
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.Cwd == "" {
		a.Cwd = "."
	}
	just := true
	if a.JustMyCode != nil {
		just = *a.JustMyCode
	}
	return n.dap.startPython(ctx, a.WorkspaceID, a.Program, a.Args, a.Cwd, a.Breakpoints, a.StopOnEntry, just)
}
func (n *nativeTools) debugStatusTool() (any, error) {
	return map[string]any{"sessions": n.dap.status()}, nil
}
func (n *nativeTools) debugEventsTool(raw json.RawMessage) (any, error) {
	var a struct{ SessionID string }
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	return n.dap.events(a.SessionID)
}
func (n *nativeTools) debugThreadsTool(ctx context.Context, raw json.RawMessage) (any, error) {
	var a struct{ SessionID string }
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	return n.dap.request(ctx, a.SessionID, "threads", map[string]any{})
}
func (n *nativeTools) debugStackTool(ctx context.Context, raw json.RawMessage) (any, error) {
	var a struct {
		SessionID                    string
		ThreadID, StartFrame, Levels int
	}
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.Levels == 0 {
		a.Levels = 50
	}
	return n.dap.request(ctx, a.SessionID, "stackTrace", map[string]any{"threadId": a.ThreadID, "startFrame": a.StartFrame, "levels": a.Levels})
}
func (n *nativeTools) debugScopesTool(ctx context.Context, raw json.RawMessage) (any, error) {
	var a struct {
		SessionID string
		FrameID   int
	}
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	return n.dap.request(ctx, a.SessionID, "scopes", map[string]any{"frameId": a.FrameID})
}
func (n *nativeTools) debugVariablesTool(ctx context.Context, raw json.RawMessage) (any, error) {
	var a struct {
		SessionID                        string
		VariablesReference, Start, Count int
	}
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.Count == 0 {
		a.Count = 200
	}
	return n.dap.request(ctx, a.SessionID, "variables", map[string]any{"variablesReference": a.VariablesReference, "start": a.Start, "count": a.Count})
}
func (n *nativeTools) debugEvaluateTool(ctx context.Context, raw json.RawMessage) (any, error) {
	var a struct {
		SessionID, Expression, Context string
		FrameID                        *int
	}
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.Context == "" {
		a.Context = "repl"
	}
	args := map[string]any{"expression": a.Expression, "context": a.Context}
	if a.FrameID != nil {
		args["frameId"] = *a.FrameID
	}
	return n.dap.request(ctx, a.SessionID, "evaluate", args)
}
func (n *nativeTools) debugControlTool(ctx context.Context, raw json.RawMessage, command string) (any, error) {
	var a struct {
		SessionID string
		ThreadID  int
	}
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	return n.dap.request(ctx, a.SessionID, command, map[string]any{"threadId": a.ThreadID})
}
func (n *nativeTools) debugStopTool(raw json.RawMessage) (any, error) {
	var a struct{ SessionID string }
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	return n.dap.stop(a.SessionID)
}
