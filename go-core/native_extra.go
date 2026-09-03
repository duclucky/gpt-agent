package main

import (
	"context"
	"encoding/json"
	"fmt"
)

var nativeExtraToolNames = []string{
	"gpt_agent_status",
	"gpt_agent_workspace_inspect",
	"gpt_agent_code_outline",
	"gpt_agent_structural_search",
	"gpt_agent_repo_map",
	"gpt_agent_write_file",
	"gpt_agent_replace_text",
	"gpt_agent_make_directory",
	"gpt_agent_move_path",
	"gpt_agent_delete_path",
	"gpt_agent_git_log",
	"gpt_agent_checkpoint",
	"gpt_agent_apply_git_patch",
	"gpt_agent_context_discover",
	"gpt_agent_toolchain_status",
	"gpt_agent_self_check",
	"gpt_agent_http_request",
	"gpt_agent_sqlite_schema",
	"gpt_agent_sqlite_query",
	"gpt_agent_audit_tail",
	"gpt_agent_audit_verify",
	"gpt_agent_audit_repair",
	"gpt_agent_wait_port",
	"gpt_agent_memory_remember",
	"gpt_agent_memory_search",
	"gpt_agent_memory_forget",
	"gpt_agent_skill_upsert",
	"gpt_agent_skill_list",
	"gpt_agent_skill_get",
	"gpt_agent_skill_record_usage",
	"gpt_agent_skill_pin",
	"gpt_agent_skill_archive",
	"gpt_agent_skill_restore",
	"gpt_agent_learn",
	"gpt_agent_skill_curate",
	"gpt_agent_learning_stats",
	"gpt_agent_coding_skills_install",
	"gpt_agent_project_inspect",
	"gpt_agent_project_diff",
	"gpt_agent_project_checkpoint",
	"gpt_agent_project_checkpoint_restore",
	"gpt_agent_project_verify",
	"gpt_agent_coding_brief",
	"gpt_agent_fast_context",
	"gpt_agent_run_command",
	"gpt_agent_untrusted_run",
	"gpt_agent_start_process",
	"gpt_agent_process_status",
	"gpt_agent_process_logs",
	"gpt_agent_stop_process",
	"gpt_agent_full_shell",
	"gpt_agent_process_list",
	"gpt_agent_lsp_status",
	"gpt_agent_lsp_definition",
	"gpt_agent_lsp_references",
	"gpt_agent_lsp_hover",
	"gpt_agent_lsp_symbols",
	"gpt_agent_lsp_workspace_symbols",
	"gpt_agent_lsp_diagnostics",
	"gpt_agent_lsp_rename_preview",
	"gpt_agent_debug_python_start",
	"gpt_agent_debug_status",
	"gpt_agent_debug_events",
	"gpt_agent_debug_threads",
	"gpt_agent_debug_stack",
	"gpt_agent_debug_scopes",
	"gpt_agent_debug_variables",
	"gpt_agent_debug_evaluate",
	"gpt_agent_debug_continue",
	"gpt_agent_debug_next",
	"gpt_agent_debug_step_in",
	"gpt_agent_debug_pause",
	"gpt_agent_debug_stop",
}

func supportsNativeExtra(name string) bool {
	for _, candidate := range nativeExtraToolNames {
		if candidate == name {
			return true
		}
	}
	return false
}

func (n *nativeTools) callNativeExtra(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	switch name {
	case "gpt_agent_status":
		return n.statusTool(raw)
	case "gpt_agent_workspace_inspect":
		return n.workspaceInspectTool(ctx, raw)
	case "gpt_agent_code_outline":
		return n.codeOutlineTool(raw)
	case "gpt_agent_structural_search":
		return n.structuralSearchTool(ctx, raw)
	case "gpt_agent_repo_map":
		return n.repoMapTool(ctx, raw)
	case "gpt_agent_write_file":
		return n.writeFileTool(raw)
	case "gpt_agent_replace_text":
		return n.replaceTextTool(raw)
	case "gpt_agent_make_directory":
		return n.makeDirectoryTool(raw)
	case "gpt_agent_move_path":
		return n.movePathTool(raw)
	case "gpt_agent_delete_path":
		return n.deletePathTool(raw)
	case "gpt_agent_git_log":
		return n.gitLogTool(ctx, raw)
	case "gpt_agent_checkpoint":
		return n.checkpointTool(ctx, raw)
	case "gpt_agent_apply_git_patch":
		return n.applyGitPatchTool(ctx, raw)
	case "gpt_agent_context_discover":
		return n.contextDiscoverTool(raw)
	case "gpt_agent_toolchain_status":
		return n.toolchainStatusTool(ctx)
	case "gpt_agent_self_check":
		return n.selfCheckTool(ctx, raw)
	case "gpt_agent_http_request":
		return n.httpRequestTool(ctx, raw)
	case "gpt_agent_sqlite_schema":
		return n.sqliteSchemaTool(ctx, raw)
	case "gpt_agent_sqlite_query":
		return n.sqliteQueryTool(ctx, raw)
	case "gpt_agent_audit_tail":
		return n.auditTailTool(raw)
	case "gpt_agent_audit_verify":
		return n.auditVerifyTool()
	case "gpt_agent_audit_repair":
		return n.auditRepairTool(raw)
	case "gpt_agent_wait_port":
		return n.waitPortTool(ctx, raw)
	case "gpt_agent_memory_remember":
		return n.memoryRememberTool(raw)
	case "gpt_agent_memory_search":
		return n.memorySearchTool(raw)
	case "gpt_agent_memory_forget":
		return n.memoryForgetTool(raw)
	case "gpt_agent_skill_upsert":
		return n.skillUpsertTool(raw)
	case "gpt_agent_skill_list":
		return n.skillListTool(raw)
	case "gpt_agent_skill_get":
		return n.skillGetTool(raw)
	case "gpt_agent_skill_record_usage":
		return n.skillUsageTool(raw)
	case "gpt_agent_skill_pin":
		return n.skillPinTool(raw)
	case "gpt_agent_skill_archive":
		return n.skillArchiveTool(raw)
	case "gpt_agent_skill_restore":
		return n.skillRestoreTool(raw)
	case "gpt_agent_learn":
		return n.learnTool(ctx, raw)
	case "gpt_agent_skill_curate":
		return n.skillCurateTool(raw)
	case "gpt_agent_learning_stats":
		return n.learningStatsTool()
	case "gpt_agent_coding_skills_install":
		return n.codingSkillsInstallTool()
	case "gpt_agent_project_inspect":
		return n.projectInspectTool(ctx, raw)
	case "gpt_agent_project_diff":
		return n.projectDiffTool(ctx, raw)
	case "gpt_agent_project_checkpoint":
		return n.projectCheckpointTool(ctx, raw)
	case "gpt_agent_project_checkpoint_restore":
		return n.projectCheckpointRestoreTool(ctx, raw)
	case "gpt_agent_project_verify":
		return n.projectVerifyTool(ctx, raw)
	case "gpt_agent_coding_brief":
		return n.codingBriefTool(ctx, raw)
	case "gpt_agent_fast_context":
		return n.fastContextTool(ctx, raw)
	case "gpt_agent_run_command":
		return n.runCommandTool(ctx, raw)
	case "gpt_agent_untrusted_run":
		return n.untrustedRunTool(ctx, raw)
	case "gpt_agent_start_process":
		return n.startProcessTool(raw)
	case "gpt_agent_process_status":
		return n.processStatusTool(raw)
	case "gpt_agent_process_logs":
		return n.processLogsTool(raw)
	case "gpt_agent_stop_process":
		return n.stopProcessTool(raw)
	case "gpt_agent_full_shell":
		return n.fullShellTool(ctx, raw)
	case "gpt_agent_process_list":
		return n.processListTool()
	case "gpt_agent_lsp_status":
		return n.lspStatusTool()
	case "gpt_agent_lsp_definition":
		return n.lspDefinitionTool(ctx, raw)
	case "gpt_agent_lsp_references":
		return n.lspReferencesTool(ctx, raw)
	case "gpt_agent_lsp_hover":
		return n.lspHoverTool(ctx, raw)
	case "gpt_agent_lsp_symbols":
		return n.lspSymbolsTool(ctx, raw)
	case "gpt_agent_lsp_workspace_symbols":
		return n.lspWorkspaceSymbolsTool(ctx, raw)
	case "gpt_agent_lsp_diagnostics":
		return n.lspDiagnosticsTool(ctx, raw)
	case "gpt_agent_lsp_rename_preview":
		return n.lspRenameTool(ctx, raw)
	case "gpt_agent_debug_python_start":
		return n.debugPythonStartTool(ctx, raw)
	case "gpt_agent_debug_status":
		return n.debugStatusTool()
	case "gpt_agent_debug_events":
		return n.debugEventsTool(raw)
	case "gpt_agent_debug_threads":
		return n.debugThreadsTool(ctx, raw)
	case "gpt_agent_debug_stack":
		return n.debugStackTool(ctx, raw)
	case "gpt_agent_debug_scopes":
		return n.debugScopesTool(ctx, raw)
	case "gpt_agent_debug_variables":
		return n.debugVariablesTool(ctx, raw)
	case "gpt_agent_debug_evaluate":
		return n.debugEvaluateTool(ctx, raw)
	case "gpt_agent_debug_continue":
		return n.debugControlTool(ctx, raw, "continue")
	case "gpt_agent_debug_next":
		return n.debugControlTool(ctx, raw, "next")
	case "gpt_agent_debug_step_in":
		return n.debugControlTool(ctx, raw, "stepIn")
	case "gpt_agent_debug_pause":
		return n.debugControlTool(ctx, raw, "pause")
	case "gpt_agent_debug_stop":
		return n.debugStopTool(raw)
	default:
		return nil, fmt.Errorf("unknown Go-native tool: %s", name)
	}
}
