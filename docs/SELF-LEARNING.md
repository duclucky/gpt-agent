# Self-learning and adaptive engineering memory

GPT Agent includes a bounded self-learning loop for engineering work. It does **not** retrain or modify ChatGPT model weights. Instead, the local Go runtime learns from verified task outcomes and improves future retrieval of project memory and reusable engineering skills.

## Flow

```text
Task
  ↓
gpt_agent_coding_brief
  ↓
project memory + reusable skills + prior outcomes
  ↓
engineering tool calls
  ↓
bounded evidence capture
  ↓
verified / failed / corrected / incomplete
  ↓
learning candidate
  ↓
semantic review
  ├─ promote → durable memory / SKILL.md
  └─ dismiss → candidate closed without persistence
  ↓
better future retrieval + feedback-weighted skill ranking
```

## What is captured automatically

After `gpt_agent_coding_brief` opens a learning session, GPT Agent records bounded evidence such as:

- whether later tool calls succeeded or failed;
- broad failure classes such as timeout, build, test, policy, or network;
- whether the task mutated project state;
- whether a verification step passed or failed;
- which reusable skills were selected for the task.

The runtime finalizes the previous task when the next learning task begins. Outcomes are classified as:

- `verified`: verification happened after the latest mutation;
- `failed`: a verification gate failed and was not subsequently verified;
- `corrected`: the next task strongly indicates that the recent result required correction;
- `incomplete`: the task ended without enough evidence for the other states.

## Evidence candidates

Repeated failures/corrections, verified repair sequences, and recurring tasks can create learning candidates. Candidates start as `pending` and are deliberately **not** converted to durable memory automatically.

Review candidates semantically:

- Put a reviewed candidate ID in `candidateIds` when calling `gpt_agent_learn` together with the durable memory or skill change that represents the real reusable lesson. The candidate becomes `promoted`.
- Put a reviewed candidate ID in `dismissCandidateIds` when it is noise, stale, too task-specific, or otherwise not worth persisting. The candidate becomes `dismissed`.

Resolved candidates no longer appear in future `gpt_agent_coding_brief` candidate retrieval.

## Skill adaptation

Reusable skills receive empirical outcome feedback from tasks in which they were selected. Verified outcomes improve their reliability signal; failures and explicit corrections reduce it. `gpt_agent_coding_brief` combines this feedback with normal task relevance when ranking skills.

The adjustment is intentionally bounded. A small amount of history cannot make a skill dominate or disappear completely.

## Privacy boundary

The evolution state is designed to store bounded evidence rather than transcripts.

It does not persist raw tool arguments or raw tool results. Task text is sanitized before persistence and bounded in length. Sanitization covers:

- common credential assignments such as `API_KEY=...`, `token=...`, and `password=...`;
- `Authorization: Bearer ...` values;
- common token prefixes such as `sk-`, `rk-`, `ghp_`, `github_pat_`, and Slack-style token prefixes;
- private-key blocks.

Do not intentionally put credentials or private data into learning memory. `gpt_agent_learn` should contain only durable verified facts, decisions, root-cause lessons, or reusable procedures.

## Example

A typical repair loop looks like this:

1. `gpt_agent_coding_brief` starts a task and retrieves relevant memory/skills.
2. A code change is made.
3. A test fails, the root cause is corrected, and verification then passes.
4. The next task finalizes the prior outcome as `verified` and may create a `repair-pattern` candidate.
5. If the pattern is genuinely reusable, ChatGPT calls `gpt_agent_learn` with the candidate ID plus a concise durable lesson.
6. Future coding briefs can retrieve that lesson and adjust skill ranking based on the accumulated outcome history.

The goal is not to remember everything. The goal is to retain only evidence-backed engineering knowledge that improves later work.
