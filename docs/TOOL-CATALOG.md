# Tool Catalog

GPT Agent v0.1.0 exposes 83 MCP tools. All tool names use the `gpt_agent_` prefix.

## Project orientation and context

- `gpt_agent_status`
- `gpt_agent_workspace_inspect`
- `gpt_agent_project_inspect`
- `gpt_agent_fast_context`
- `gpt_agent_repo_map`
- `gpt_agent_context_discover`
- `gpt_agent_coding_brief`
- `gpt_agent_coding_skills_install`

## Files and code search

- `gpt_agent_list_files`
- `gpt_agent_read_file`
- `gpt_agent_read_file_range`
- `gpt_agent_search_text`
- `gpt_agent_code_outline`
- `gpt_agent_structural_search`
- `gpt_agent_write_file`
- `gpt_agent_replace_text`
- `gpt_agent_make_directory`
- `gpt_agent_move_path`
- `gpt_agent_delete_path`

## Git and checkpoints

- `gpt_agent_git_status`
- `gpt_agent_git_diff`
- `gpt_agent_git_log`
- `gpt_agent_checkpoint`
- `gpt_agent_apply_git_patch`
- `gpt_agent_project_diff`
- `gpt_agent_project_checkpoint`
- `gpt_agent_project_checkpoint_restore`
- `gpt_agent_project_verify`

## Commands and managed processes

- `gpt_agent_run_command`
- `gpt_agent_untrusted_run`
- `gpt_agent_full_shell`
- `gpt_agent_start_process`
- `gpt_agent_process_status`
- `gpt_agent_process_logs`
- `gpt_agent_process_list`
- `gpt_agent_stop_process`
- `gpt_agent_wait_port`
- `gpt_agent_http_request`

SAFE command execution is preferred. FULL SHELL is available only while a local grant is active.

## Language servers

- `gpt_agent_lsp_status`
- `gpt_agent_lsp_definition`
- `gpt_agent_lsp_references`
- `gpt_agent_lsp_hover`
- `gpt_agent_lsp_symbols`
- `gpt_agent_lsp_workspace_symbols`
- `gpt_agent_lsp_diagnostics`
- `gpt_agent_lsp_rename_preview`

The Windows installer configures language servers that are actually available on the machine. Missing optional servers are removed from the generated local config instead of being advertised as usable.

## SQLite

- `gpt_agent_sqlite_schema`
- `gpt_agent_sqlite_query`

The query tool accepts read-only forms such as SELECT, PRAGMA, EXPLAIN, and WITH.

## Python debugger

- `gpt_agent_debug_python_start`
- `gpt_agent_debug_status`
- `gpt_agent_debug_events`
- `gpt_agent_debug_threads`
- `gpt_agent_debug_stack`
- `gpt_agent_debug_scopes`
- `gpt_agent_debug_variables`
- `gpt_agent_debug_evaluate`
- `gpt_agent_debug_continue`
- `gpt_agent_debug_next`
- `gpt_agent_debug_step_in`
- `gpt_agent_debug_pause`
- `gpt_agent_debug_stop`

## Memory and reusable skills

- `gpt_agent_memory_remember`
- `gpt_agent_memory_search`
- `gpt_agent_memory_forget`
- `gpt_agent_skill_upsert`
- `gpt_agent_skill_list`
- `gpt_agent_skill_get`
- `gpt_agent_skill_record_usage`
- `gpt_agent_skill_pin`
- `gpt_agent_skill_archive`
- `gpt_agent_skill_restore`
- `gpt_agent_skill_curate`
- `gpt_agent_learning_stats`
- `gpt_agent_learn`

The learning layer is for durable, verified project context and reusable procedures. `gpt_agent_coding_brief` opens an automatic learning session and returns pending evidence candidates plus similar prior outcomes. Review candidates before persisting anything: use `gpt_agent_learn` with `candidateIds` when promoting durable learning, or `dismissCandidateIds` for reviewed noise/stale evidence. Credentials, raw tool arguments/results, and transient secrets should never be persisted there. See [SELF-LEARNING.md](SELF-LEARNING.md).

## Diagnostics and audit

- `gpt_agent_toolchain_status`
- `gpt_agent_self_check`
- `gpt_agent_audit_tail`
- `gpt_agent_audit_verify`
- `gpt_agent_audit_repair`

## Async jobs

Long-running native calls can be handed off into recoverable jobs:

- `gpt_agent_job_start`
- `gpt_agent_job_status`
- `gpt_agent_job_result`
- `gpt_agent_job_list`

This avoids losing work when a tool execution approaches a tunnel/request deadline.

## Intentionally absent from v0.1.0

GPT Agent v0.1.0 does not include browser automation, another coding-agent harness, or a model-provider gateway. Those are outside the scope of the first community release.
