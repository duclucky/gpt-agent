---
name: gpt-agent-operator
description: Operate GPT Agent as a direct local developer runtime for ChatGPT with project-scoped inspection, safe execution, verification, learning, and explicit permission boundaries.
---
# GPT Agent Operator
Use GPT Agent as the local execution and code-intelligence layer. ChatGPT is the only reasoning/agent loop.
## Workflow
1. Start non-trivial coding work with `gpt_agent_coding_brief` or `gpt_agent_fast_context` for the exact project path.
2. Inspect Git state, relevant source, call sites, configuration, tests, and durable `.gpt-agent` context before editing.
3. Prefer targeted writes with SHA-256 optimistic checks. Create a checkpoint before broad or risky Git-backed changes.
4. Prefer specialized tools and `gpt_agent_run_command`; use `gpt_agent_full_shell` only when a time-bounded local grant is active and safer tools are insufficient.
5. Use LSP, structural search, compiler/test/build checks, DAP, HTTP, SQLite, and process tools as appropriate to verify behavior.
6. Review the final diff and risk signals. Do not claim completion without concrete verification evidence.
7. Review learning candidates before they repeat: use `gpt_agent_learn` with `candidateIds` only when promoting durable verified learning, or `dismissCandidateIds` for reviewed noise/stale evidence.
## Boundaries
- Work only inside configured workspaces.
- Never persist secrets, credentials, private keys, transient logs, guesses, or unverified hypotheses.
- Do not commit, push, publish, deploy, or touch production without explicit user authorization.
- SAFE network access is off by default. Suspicious code belongs in `gpt_agent_untrusted_run` / Windows Sandbox when available.
- GPT Agent contains no embedded LLM and no secondary coding agent.
