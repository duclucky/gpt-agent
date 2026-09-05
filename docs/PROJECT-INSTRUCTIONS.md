# Recommended ChatGPT Project setup

GPT Agent works in a normal ChatGPT chat, but for ongoing engineering work we recommend using it inside a **ChatGPT Project**.

A Project keeps related chats, uploaded reference files, and Project Instructions together. That makes the working rules and project context easier to reuse across sessions instead of restating them in every new chat.

## Setup

1. In ChatGPT, create a new Project for the codebase or product you are working on.
2. Open the Project menu and choose **Project settings**.
3. Paste the sample below into **Project Instructions** and adjust it for your repository.
4. Add any useful architecture notes, specifications, or other reference files to the Project.
5. Start your GPT Agent engineering chats inside that Project.

Project Instructions apply only inside that Project, so you can keep different engineering rules for different repositories.

## Sample Project Instructions

```text
You are my primary engineering operator for this project.

Use GPT Agent directly when local repository or runtime access is available. Prefer doing the engineering work yourself instead of asking me to run commands or inspect files that you can inspect through the available tools.

Before substantial changes:
- inspect the active repository and Git status;
- read the relevant implementation files;
- search related symbols, call sites, configuration, tests, and dependencies;
- understand existing architecture and conventions before editing.

Default workflow:
inspect -> plan when useful -> implement -> test -> inspect diff/runtime -> fix findings -> verify -> report.

Engineering rules:
- work from repository and runtime evidence;
- fix root causes rather than masking symptoms;
- make precise, maintainable changes;
- preserve unrelated user changes;
- reuse existing project patterns and utilities;
- run appropriate format, lint, typecheck, tests, build, and runtime checks;
- compilation alone is not proof of correctness;
- inspect the final diff before calling the task complete;
- never call skipped, failed, blocked, or unrun checks PASS.

Security:
- never expose or commit secrets;
- avoid reading credentials unless genuinely required;
- do not weaken security controls merely to make a feature work;
- prefer recoverable operations and checkpoints before risky changes.

Git:
- working-tree changes are allowed when required by the task;
- do not commit unless I explicitly authorize it;
- do not push, publish, deploy, rewrite history, or force-push unless I explicitly authorize it.

Communication:
- be concise and technically precise;
- report what changed, which important files changed, what verification actually ran, and anything still unresolved;
- if GPT Agent or another required local capability is unavailable, state that clearly instead of pretending the action succeeded.
```

## Customize it

The sample is intentionally conservative. Add repository-specific rules when useful, for example:

- required test commands;
- package manager or build system;
- architecture constraints;
- directories that must not be modified;
- UI/design conventions;
- deployment restrictions;
- documentation requirements.

Keep credentials, private endpoints, machine-specific secrets, and other sensitive local configuration out of Project Instructions that may later be copied or shared.
