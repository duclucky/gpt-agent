package main

import (
	"context"
	"encoding/json"
)

func (n *nativeTools) lspStatusTool() (any, error) { return n.lsp.status(), nil }
func (n *nativeTools) lspDefinitionTool(ctx context.Context, raw json.RawMessage) (any, error) {
	var a struct {
		WorkspaceID, Path string
		Line, Character   int
	}
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	return n.lsp.positionTool(ctx, "definition", a.WorkspaceID, a.Path, a.Line, a.Character, true, "")
}
func (n *nativeTools) lspReferencesTool(ctx context.Context, raw json.RawMessage) (any, error) {
	var a struct {
		WorkspaceID, Path  string
		Line, Character    int
		IncludeDeclaration *bool
	}
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	include := true
	if a.IncludeDeclaration != nil {
		include = *a.IncludeDeclaration
	}
	return n.lsp.positionTool(ctx, "references", a.WorkspaceID, a.Path, a.Line, a.Character, include, "")
}
func (n *nativeTools) lspHoverTool(ctx context.Context, raw json.RawMessage) (any, error) {
	var a struct {
		WorkspaceID, Path string
		Line, Character   int
	}
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	return n.lsp.positionTool(ctx, "hover", a.WorkspaceID, a.Path, a.Line, a.Character, true, "")
}
func (n *nativeTools) lspSymbolsTool(ctx context.Context, raw json.RawMessage) (any, error) {
	var a struct{ WorkspaceID, Path string }
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	return n.lsp.symbols(ctx, a.WorkspaceID, a.Path)
}
func (n *nativeTools) lspWorkspaceSymbolsTool(ctx context.Context, raw json.RawMessage) (any, error) {
	var a struct{ WorkspaceID, Query, LanguageHintPath string }
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	return n.lsp.workspaceSymbols(ctx, a.WorkspaceID, a.Query, a.LanguageHintPath)
}
func (n *nativeTools) lspDiagnosticsTool(ctx context.Context, raw json.RawMessage) (any, error) {
	var a struct {
		WorkspaceID, Path string
		WaitMS            int
	}
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.WaitMS == 0 {
		a.WaitMS = 1200
	}
	return n.lsp.diagnostics(ctx, a.WorkspaceID, a.Path, a.WaitMS)
}
func (n *nativeTools) lspRenameTool(ctx context.Context, raw json.RawMessage) (any, error) {
	var a struct {
		WorkspaceID, Path, NewName string
		Line, Character            int
	}
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	return n.lsp.positionTool(ctx, "rename", a.WorkspaceID, a.Path, a.Line, a.Character, true, a.NewName)
}
