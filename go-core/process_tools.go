package main

import (
	"context"
	"encoding/json"
)

const defaultCommandTimeoutMS = 30 * 60 * 1000

func (n *nativeTools) runCommandTool(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var a struct {
		WorkspaceID, Cwd, Executable string
		Args                         []string
		Network                      bool
		TimeoutMS                    int
	}
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.Cwd == "" {
		a.Cwd = "."
	}
	if a.TimeoutMS == 0 {
		a.TimeoutMS = defaultCommandTimeoutMS
	}
	return n.processes.runSafe(ctx, a.WorkspaceID, a.Cwd, a.Executable, a.Args, nil, a.Network, a.TimeoutMS)
}
func (n *nativeTools) untrustedRunTool(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var a struct {
		WorkspaceID, Cwd, Executable string
		Args                         []string
		Env                          map[string]string
		TimeoutMS                    int
	}
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.Cwd == "" {
		a.Cwd = "."
	}
	if a.TimeoutMS == 0 {
		a.TimeoutMS = defaultCommandTimeoutMS
	}
	if a.Env == nil {
		a.Env = map[string]string{}
	}
	return n.processes.runUntrusted(ctx, a.WorkspaceID, a.Cwd, a.Executable, a.Args, a.Env, a.TimeoutMS)
}
func (n *nativeTools) startProcessTool(raw json.RawMessage) (map[string]any, error) {
	var a struct {
		WorkspaceID, Cwd, Executable string
		Args                         []string
		Network                      bool
	}
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.Cwd == "" {
		a.Cwd = "."
	}
	return n.processes.startSafe(a.WorkspaceID, a.Cwd, a.Executable, a.Args, nil, a.Network)
}
func (n *nativeTools) processStatusTool(raw json.RawMessage) (map[string]any, error) {
	var a struct {
		ProcessID string `json:"processId"`
	}
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	return n.processes.status(a.ProcessID)
}
func (n *nativeTools) processLogsTool(raw json.RawMessage) (map[string]any, error) {
	var a struct {
		ProcessID string `json:"processId"`
		TailChars int    `json:"tailChars"`
	}
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	return n.processes.logs(a.ProcessID, a.TailChars)
}
func (n *nativeTools) stopProcessTool(raw json.RawMessage) (map[string]any, error) {
	var a struct {
		ProcessID string `json:"processId"`
	}
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	return n.processes.stop(a.ProcessID)
}
func (n *nativeTools) processListTool() (map[string]any, error) { return n.processes.list() }
func (n *nativeTools) fullShellTool(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var a struct {
		WorkspaceID, Cwd, Command string
		TimeoutMS                 int
	}
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.Cwd == "" {
		a.Cwd = "."
	}
	if a.TimeoutMS == 0 {
		a.TimeoutMS = defaultCommandTimeoutMS
	}
	return n.processes.runFullShell(ctx, a.WorkspaceID, a.Cwd, a.Command, a.TimeoutMS)
}
