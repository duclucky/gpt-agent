package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

var projectManifestSet = map[string]bool{"package.json": true, "pyproject.toml": true, "requirements.txt": true, "Cargo.toml": true, "go.mod": true, "Dockerfile": true, "docker-compose.yml": true, "compose.yml": true, "Makefile": true}

func (n *nativeTools) gitRootFor(ctx context.Context, absolute, workspaceRoot string) (string, error) {
	code, out, _, err := runCapture(ctx, "git", []string{"-C", absolute, "rev-parse", "--show-toplevel"}, "", nil)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", nil
	}
	root, err := filepath.Abs(strings.TrimSpace(out))
	if err != nil {
		return "", err
	}
	if !pathWithin(workspaceRoot, root) {
		return "", errors.New("Resolved Git root escapes workspace containment.")
	}
	return root, nil
}
func topEntriesNative(abs string) ([]map[string]any, error) {
	ents, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(ents))
	for _, e := range ents {
		kind := "other"
		if e.IsDir() {
			kind = "directory"
		} else if e.Type().IsRegular() {
			kind = "file"
		}
		out = append(out, map[string]any{"name": e.Name(), "type": kind})
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["name"].(string) < out[j]["name"].(string) })
	return out, nil
}
func detectPackageManager(abs string) string {
	pairs := [][2]string{{"pnpm-lock.yaml", "pnpm"}, {"yarn.lock", "yarn"}, {"bun.lockb", "bun"}, {"bun.lock", "bun"}, {"package-lock.json", "npm"}}
	for _, p := range pairs {
		if _, err := os.Stat(filepath.Join(abs, p[0])); err == nil {
			return p[1]
		}
	}
	return "npm"
}
func scriptCommand(pm, script string) (string, []string) {
	switch pm {
	case "yarn", "pnpm", "bun":
		return pm, []string{"run", script}
	default:
		return "npm", []string{"run", script}
	}
}
func verificationPlanNative(abs string, manifests []string, pkg map[string]any, pm string) []map[string]any {
	has := map[string]bool{}
	for _, manifest := range manifests {
		has[manifest] = true
	}
	plan := []map[string]any{}
	scripts := map[string]any{}
	if pkg != nil {
		scripts, _ = pkg["scripts"].(map[string]any)
	}
	addNode := func(stage string, names []string, reason string) {
		for _, name := range names {
			if _, ok := scripts[name].(string); !ok {
				continue
			}
			executable, args := scriptCommand(pm, name)
			plan = append(plan, map[string]any{
				"stage": stage, "executable": executable, "args": args,
				"reason": reason, "mutates": false,
			})
			return
		}
	}
	if has["package.json"] {
		addNode("format", []string{"format:check", "format-check", "check:format"}, "Read-only formatting verification when provided by the project.")
		addNode("lint", []string{"lint", "check:lint"}, "Static lint gate from package scripts.")
		addNode("typecheck", []string{"typecheck", "type-check", "check:types", "types"}, "Type-system verification from package scripts.")
		addNode("test", []string{"test", "test:unit", "check:test"}, "Automated test gate from package scripts.")
		addNode("build", []string{"build", "check:build"}, "Compilation/bundling gate from package scripts.")
	}
	if has["pyproject.toml"] || has["requirements.txt"] {
		var text strings.Builder
		for _, file := range []string{"pyproject.toml", "requirements.txt", "requirements-dev.txt"} {
			if buf, err := os.ReadFile(filepath.Join(abs, file)); err == nil {
				text.Write(buf)
				text.WriteByte('\n')
			}
		}
		lower := strings.ToLower(text.String())
		if strings.Contains(lower, "ruff") {
			plan = append(plan, map[string]any{"stage": "lint", "executable": "ruff", "args": []string{"check", "."}, "reason": "ruff detected in Python project metadata.", "mutates": false})
		}
		if strings.Contains(lower, "mypy") {
			plan = append(plan, map[string]any{"stage": "typecheck", "executable": "python3", "args": []string{"-m", "mypy", "."}, "reason": "mypy detected in Python project metadata.", "mutates": false})
		}
		if strings.Contains(lower, "pytest") {
			plan = append(plan, map[string]any{"stage": "test", "executable": "python3", "args": []string{"-m", "pytest"}, "reason": "pytest detected in Python project metadata.", "mutates": false})
		}
	}
	if has["Cargo.toml"] {
		plan = append(plan,
			map[string]any{"stage": "format", "executable": "cargo", "args": []string{"fmt", "--check"}, "reason": "Rust formatting gate.", "mutates": false},
			map[string]any{"stage": "lint", "executable": "cargo", "args": []string{"clippy", "--all-targets", "--all-features", "--", "-D", "warnings"}, "reason": "Rust lint gate.", "mutates": false},
			map[string]any{"stage": "test", "executable": "cargo", "args": []string{"test"}, "reason": "Rust test gate.", "mutates": false},
		)
	}
	if has["go.mod"] {
		plan = append(plan,
			map[string]any{"stage": "lint", "executable": "go", "args": []string{"vet", "./..."}, "reason": "Go static analysis gate.", "mutates": false},
			map[string]any{"stage": "test", "executable": "go", "args": []string{"test", "./..."}, "reason": "Go test gate.", "mutates": false},
		)
	}
	return plan
}
func riskFlagsNative(paths []string) []string {
	flags := map[string]bool{}
	for _, p0 := range paths {
		p := strings.ToLower(filepath.ToSlash(p0))
		if regexp.MustCompile(`(^|/)(\.env|.*\.pem|.*\.key)$`).MatchString(p) {
			flags["secret-like-files"] = true
		}
		if regexp.MustCompile(`migration|migrations|schema\.(sql|prisma)|database`).MatchString(p) {
			flags["database-schema-or-migration"] = true
		}
		if regexp.MustCompile(`auth|oauth|permission|security|crypto|session`).MatchString(p) {
			flags["security-or-auth"] = true
		}
		if regexp.MustCompile(`docker|terraform|k8s|kubernetes|deploy|workflow|\.github/`).MatchString(p) {
			flags["infra-or-deployment"] = true
		}
		if regexp.MustCompile(`package-lock\.json|pnpm-lock\.yaml|yarn\.lock|bun\.lock`).MatchString(p) {
			flags["dependency-lockfile"] = true
		}
		if regexp.MustCompile(`(^|/)api/|route|controller|public|export`).MatchString(p) {
			flags["public-interface"] = true
		}
	}
	out := make([]string, 0, len(flags))
	for f := range flags {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}
func gitSnapshotNative(ctx context.Context, gitRoot string, maxChars int) any {
	if gitRoot == "" {
		return nil
	}
	type rr struct {
		code    int
		out, se string
	}
	run := func(args ...string) rr {
		c, o, s, _ := runCapture(ctx, "git", append([]string{"-C", gitRoot}, args...), "", nil)
		return rr{c, o, s}
	}
	var status, porcelain, names, num rr
	var wg sync.WaitGroup
	wg.Add(4)
	go func() { defer wg.Done(); status = run("status", "--short", "--branch") }()
	go func() { defer wg.Done(); porcelain = run("status", "--porcelain=v1", "--untracked-files=all") }()
	go func() { defer wg.Done(); names = run("diff", "HEAD", "--name-only") }()
	go func() { defer wg.Done(); num = run("diff", "HEAD", "--numstat") }()
	wg.Wait()
	seen := map[string]bool{}
	changed := []string{}
	for _, line := range strings.Split(names.out, "\n") {
		v := strings.TrimSpace(line)
		if v != "" && !seen[v] {
			seen[v] = true
			changed = append(changed, v)
		}
	}
	for _, line := range strings.Split(porcelain.out, "\n") {
		if len(line) < 4 {
			continue
		}
		v := strings.TrimSpace(line[3:])
		if i := strings.LastIndex(v, " -> "); i >= 0 {
			v = v[i+4:]
		}
		if v != "" && !seen[v] {
			seen[v] = true
			changed = append(changed, v)
		}
	}
	adds, dels := 0, 0
	for _, line := range strings.Split(num.out, "\n") {
		parts := strings.Split(line, "\t")
		if len(parts) >= 2 {
			if x, e := strconv.Atoi(parts[0]); e == nil {
				adds += x
			}
			if x, e := strconv.Atoi(parts[1]); e == nil {
				dels += x
			}
		}
	}
	flags := riskFlagsNative(changed)
	churn := adds + dels
	level := "low"
	for _, f := range flags {
		if f == "secret-like-files" || f == "database-schema-or-migration" || f == "security-or-auth" || f == "infra-or-deployment" {
			level = "high"
		}
	}
	if level != "high" {
		if len(changed) > 20 || churn > 1000 {
			level = "high"
		} else if len(flags) > 0 || len(changed) > 5 || churn > 250 {
			level = "medium"
		}
	}
	text, _, _ := truncateNativeText(firstNonEmpty(status.out, status.se), maxChars)
	return map[string]any{"status": text, "changedFiles": changed, "additions": adds, "deletions": dels, "churn": churn, "risk": map[string]any{"level": level, "flags": flags}}
}

func (n *nativeTools) projectInspect(ctx context.Context, workspaceID, path string, includeContext bool, maxContext int) (map[string]any, error) {
	if path == "" {
		path = "."
	}
	ws, rootReal, abs, err := n.resolveWorkspacePath(workspaceID, path)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(abs)
	if err != nil || !st.IsDir() {
		return nil, nativeToolError{"Project path must be a directory."}
	}
	entries, err := topEntriesNative(abs)
	if err != nil {
		return nil, err
	}
	var pkg map[string]any
	if b, e := os.ReadFile(filepath.Join(abs, "package.json")); e == nil {
		_ = json.Unmarshal(b, &pkg)
	}
	gitRoot, err := n.gitRootFor(ctx, abs, rootReal)
	if err != nil {
		return nil, err
	}
	manifests := []string{}
	for _, e := range entries {
		if e["type"] != "file" {
			continue
		}
		name := e["name"].(string)
		if projectManifestSet[name] || strings.HasSuffix(name, ".sln") || strings.HasSuffix(name, ".csproj") {
			manifests = append(manifests, name)
		}
	}
	pm := ""
	if containsString(manifests, "package.json") {
		pm = detectPackageManager(abs)
	}
	plan := verificationPlanNative(abs, manifests, pkg, pm)
	contextFiles := []map[string]any{}
	if includeContext {
		v, e := n.contextDiscoverTool(mustJSON(map[string]any{"workspaceId": workspaceID, "path": path, "maxCharsPerFile": maxContext}))
		if e != nil {
			return nil, e
		}
		contextFiles, _ = v["files"].([]map[string]any)
	}
	var scripts any
	var deps any
	if pkg != nil {
		scripts = pkg["scripts"]
		d := []string{}
		for _, field := range []string{"dependencies", "devDependencies"} {
			if m, ok := pkg[field].(map[string]any); ok {
				keys := make([]string, 0, len(m))
				for k := range m {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					if len(d) < 100 && !containsString(d, k) {
						d = append(d, k)
					}
				}
			}
		}
		deps = d
	}
	return map[string]any{"workspaceId": workspaceID, "path": path, "projectRoot": abs, "gitRoot": nilIfEmpty(gitRoot), "manifests": manifests, "topEntries": sliceMaps(entries, 200), "packageManager": nilIfEmpty(pm), "packageScripts": scripts, "dependenciesSample": deps, "verificationPlan": plan, "git": gitSnapshotNative(ctx, gitRoot, 120000), "context": contextFiles, "workspaceRoot": ws.Root}, nil
}
func (n *nativeTools) projectInspectTool(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var a struct {
		WorkspaceID, Path string
		IncludeContext    *bool
		MaxContextChars   int
	}
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	inc := true
	if a.IncludeContext != nil {
		inc = *a.IncludeContext
	}
	if a.MaxContextChars == 0 {
		a.MaxContextChars = 10000
	}
	return n.projectInspect(ctx, a.WorkspaceID, a.Path, inc, a.MaxContextChars)
}
func (n *nativeTools) projectDiffTool(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var a struct {
		WorkspaceID, Path string
		Staged            bool
		MaxChars          int
	}
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.Path == "" {
		a.Path = "."
	}
	if a.MaxChars == 0 {
		a.MaxChars = 160000
	}
	_, rootReal, abs, err := n.resolveWorkspacePath(a.WorkspaceID, a.Path)
	if err != nil {
		return nil, err
	}
	root, err := n.gitRootFor(ctx, abs, rootReal)
	if err != nil {
		return nil, err
	}
	if root == "" {
		return nil, nativeToolError{"Project path is not inside a Git repository."}
	}
	argv := []string{"-C", root, "diff", "--binary"}
	if a.Staged {
		argv = append(argv, "--cached")
	}
	code, out, se, err := runCapture(ctx, "git", argv, "", nil)
	if err != nil {
		return nil, err
	}
	text, trunc, _ := truncateNativeText(firstNonEmpty(out, se), a.MaxChars)
	return map[string]any{"gitRoot": root, "exitCode": code, "diff": text, "truncated": trunc}, nil
}

type projectCheckpointMeta struct {
	SchemaVersion                           int `json:"schemaVersion"`
	Scope, WorkspaceID, Path, GitRoot, Head string
	UntrackedPaths                          []string `json:"untrackedPaths"`
	WorkingPatchSHA256                      string   `json:"workingPatchSha256"`
	StagedPatchSHA256                       string   `json:"stagedPatchSha256"`
	CreatedAt                               string   `json:"createdAt"`
}

func (n *nativeTools) createProjectCheckpoint(ctx context.Context, workspaceID, path, label string) (map[string]any, error) {
	if path == "" {
		path = "."
	}
	if label == "" {
		label = "checkpoint"
	}
	_, rootReal, abs, err := n.resolveWorkspacePath(workspaceID, path)
	if err != nil {
		return nil, err
	}
	gitRoot, err := n.gitRootFor(ctx, abs, rootReal)
	if err != nil {
		return nil, err
	}
	if gitRoot == "" {
		return nil, nativeToolError{"Project checkpoint requires a Git repository."}
	}
	run := func(args ...string) (string, error) {
		code, out, se, e := runCapture(ctx, "git", append([]string{"-C", gitRoot}, args...), "", nil)
		if e != nil {
			return "", e
		}
		if code != 0 {
			return "", errors.New(firstNonEmpty(se, out))
		}
		return out, nil
	}
	status, _ := run("status", "--short", "--branch")
	porcelain, _ := run("status", "--porcelain=v1", "--untracked-files=all")
	diff, _ := run("diff", "--binary")
	staged, _ := run("diff", "--cached", "--binary")
	head, err := run("rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	head = strings.TrimSpace(head)
	untracked := []string{}
	for _, line := range strings.Split(porcelain, "\n") {
		if strings.HasPrefix(line, "?? ") {
			untracked = append(untracked, line[3:])
		}
	}
	id := "project-checkpoint_" + randomHex(8)
	safe := regexp.MustCompile(`[^a-zA-Z0-9._-]+`).ReplaceAllString(label, "_")
	if len(safe) > 60 {
		safe = safe[:60]
	}
	base := filepath.Join(n.dataRoot, "artifacts", id+"-"+safe)
	if err := os.MkdirAll(filepath.Dir(base), 0755); err != nil {
		return nil, err
	}
	_ = os.WriteFile(base+".status.txt", []byte(status), 0644)
	_ = os.WriteFile(base+".working.patch", []byte(diff), 0644)
	_ = os.WriteFile(base+".staged.patch", []byte(staged), 0644)
	meta := projectCheckpointMeta{2, "tracked-state-only", workspaceID, path, gitRoot, head, untracked, shaHex(diff), shaHex(staged), nowISO()}
	if err := writeJSONAtomic(base+".meta.json", &meta); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "gitRoot": gitRoot, "head": head, "scope": meta.Scope, "untrackedPaths": untracked, "statusPath": base + ".status.txt", "workingPatchPath": base + ".working.patch", "stagedPatchPath": base + ".staged.patch", "metaPath": base + ".meta.json"}, nil
}
func (n *nativeTools) projectCheckpointTool(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var a struct{ WorkspaceID, Path, Label string }
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	return n.createProjectCheckpoint(ctx, a.WorkspaceID, a.Path, a.Label)
}

type checkpointState struct {
	ID, Base        string
	Meta            projectCheckpointMeta
	Working, Staged string
}

func (n *nativeTools) loadCheckpoint(id string) (checkpointState, error) {
	if !regexp.MustCompile(`(?i)^project-checkpoint_[a-f0-9]{16}$`).MatchString(id) {
		return checkpointState{}, nativeToolError{"Invalid project checkpoint id."}
	}
	entries, err := os.ReadDir(filepath.Join(n.dataRoot, "artifacts"))
	if err != nil {
		return checkpointState{}, err
	}
	matches := []string{}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), id+"-") && strings.HasSuffix(e.Name(), ".meta.json") {
			matches = append(matches, e.Name())
		}
	}
	if len(matches) != 1 {
		if len(matches) > 1 {
			return checkpointState{}, fmt.Errorf("Checkpoint id is ambiguous: %s", id)
		}
		return checkpointState{}, fmt.Errorf("Checkpoint not found: %s", id)
	}
	metaPath := filepath.Join(n.dataRoot, "artifacts", matches[0])
	base := strings.TrimSuffix(metaPath, ".meta.json")
	var meta projectCheckpointMeta
	if err := readJSONFile(metaPath, &meta, `{}`); err != nil {
		return checkpointState{}, err
	}
	w, err := os.ReadFile(base + ".working.patch")
	if err != nil {
		return checkpointState{}, err
	}
	s, err := os.ReadFile(base + ".staged.patch")
	if err != nil {
		return checkpointState{}, err
	}
	return checkpointState{id, base, meta, string(w), string(s)}, nil
}
func applyCheckpointState(ctx context.Context, gitRoot string, state checkpointState) error {
	run := func(args []string, input string) error {
		code, out, se, err := runCapture(ctx, "git", args, "", []byte(input))
		if err != nil {
			return err
		}
		if code != 0 {
			return errors.New(firstNonEmpty(se, out))
		}
		return nil
	}
	if err := run([]string{"-C", gitRoot, "restore", "--source=HEAD", "--staged", "--worktree", "--", "."}, ""); err != nil {
		return fmt.Errorf("Failed to clean tracked state before checkpoint apply: %w", err)
	}
	if strings.TrimSpace(state.Staged) != "" {
		if err := run([]string{"-C", gitRoot, "apply", "--check", "--index", "--binary", "-"}, state.Staged); err != nil {
			return fmt.Errorf("Saved staged patch no longer applies: %w", err)
		}
		if err := run([]string{"-C", gitRoot, "apply", "--index", "--binary", "-"}, state.Staged); err != nil {
			return fmt.Errorf("Failed applying saved staged patch: %w", err)
		}
	}
	if strings.TrimSpace(state.Working) != "" {
		if err := run([]string{"-C", gitRoot, "apply", "--check", "--binary", "-"}, state.Working); err != nil {
			return fmt.Errorf("Saved working patch no longer applies: %w", err)
		}
		if err := run([]string{"-C", gitRoot, "apply", "--binary", "-"}, state.Working); err != nil {
			return fmt.Errorf("Failed applying saved working patch: %w", err)
		}
	}
	return nil
}
func (n *nativeTools) projectCheckpointRestoreTool(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var a struct{ WorkspaceID, Path, CheckpointID, Confirm string }
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.Confirm != "RESTORE" {
		return nil, nativeToolError{"Checkpoint restore requires confirm='RESTORE'."}
	}
	if a.Path == "" {
		a.Path = "."
	}
	state, err := n.loadCheckpoint(a.CheckpointID)
	if err != nil {
		return nil, err
	}
	if state.Meta.SchemaVersion != 2 || state.Meta.Head == "" {
		return nil, nativeToolError{"Checkpoint format is too old for safe restore; create a new v2 checkpoint first."}
	}
	if state.Meta.WorkspaceID != a.WorkspaceID {
		return nil, nativeToolError{"Checkpoint belongs to a different workspace."}
	}
	if shaHex(state.Working) != state.Meta.WorkingPatchSHA256 || shaHex(state.Staged) != state.Meta.StagedPatchSHA256 {
		return nil, nativeToolError{"Checkpoint patch integrity verification failed."}
	}
	_, rootReal, abs, err := n.resolveWorkspacePath(a.WorkspaceID, a.Path)
	if err != nil {
		return nil, err
	}
	gitRoot, err := n.gitRootFor(ctx, abs, rootReal)
	if err != nil {
		return nil, err
	}
	if gitRoot == "" {
		return nil, nativeToolError{"Checkpoint restore requires a Git repository."}
	}
	same, _ := filepath.Abs(state.Meta.GitRoot)
	if !strings.EqualFold(filepath.Clean(gitRoot), filepath.Clean(same)) {
		return nil, nativeToolError{"Checkpoint belongs to a different Git repository."}
	}
	code, head, se, err := runCapture(ctx, "git", []string{"-C", gitRoot, "rev-parse", "HEAD"}, "", nil)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, errors.New(se)
	}
	head = strings.TrimSpace(head)
	if head != state.Meta.Head {
		return nil, fmt.Errorf("Checkpoint HEAD mismatch: saved %s, current %s. Refusing cross-commit restore.", state.Meta.Head, head)
	}
	safety, err := n.createProjectCheckpoint(ctx, a.WorkspaceID, a.Path, "pre-restore-"+a.CheckpointID)
	if err != nil {
		return nil, err
	}
	if err := applyCheckpointState(ctx, gitRoot, state); err != nil {
		safetyState, _ := n.loadCheckpoint(safety["id"].(string))
		if rollbackErr := applyCheckpointState(ctx, gitRoot, safetyState); rollbackErr != nil {
			return nil, fmt.Errorf("%v Automatic rollback also failed: %v", err, rollbackErr)
		}
		return nil, fmt.Errorf("%v Original tracked state was restored from safety checkpoint %s.", err, safety["id"])
	}
	return map[string]any{"restored": true, "checkpointId": a.CheckpointID, "gitRoot": gitRoot, "head": head, "scope": state.Meta.Scope, "checkpointUntrackedPaths": state.Meta.UntrackedPaths, "safetyCheckpoint": safety}, nil
}

func (n *nativeTools) projectVerifyTool(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var a struct {
		WorkspaceID, Path string
		Stages            []string
		StopOnFailure     *bool
		TimeoutMSPerStep  int
		Network           bool
	}
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.Path == "" {
		a.Path = "."
	}
	stop := true
	if a.StopOnFailure != nil {
		stop = *a.StopOnFailure
	}
	if a.TimeoutMSPerStep == 0 {
		a.TimeoutMSPerStep = 300000
	}
	project, err := n.projectInspect(ctx, a.WorkspaceID, a.Path, false, 10000)
	if err != nil {
		return nil, err
	}
	plan, _ := project["verificationPlan"].([]map[string]any)
	wanted := map[string]bool{}
	for _, s := range a.Stages {
		wanted[s] = true
	}
	filtered := []map[string]any{}
	for _, step := range plan {
		stage := step["stage"].(string)
		if len(wanted) == 0 || wanted[stage] {
			filtered = append(filtered, step)
		}
	}
	results := []map[string]any{}
	for _, step := range filtered {
		exe := step["executable"].(string)
		argv := toStringSlice(step["args"])
		r, err := n.processes.runSafe(ctx, a.WorkspaceID, a.Path, exe, argv, nil, a.Network, a.TimeoutMSPerStep)
		if err != nil {
			return nil, err
		}
		results = append(results, map[string]any{"stage": step["stage"], "command": map[string]any{"executable": exe, "args": argv}, "reason": step["reason"], "result": r})
		if code, _ := r["exitCode"].(int); code != 0 && stop {
			break
		}
	}
	passed := len(results) == len(filtered)
	if passed {
		for _, r := range results {
			if code, ok := r["result"].(map[string]any)["exitCode"].(int); ok && code != 0 {
				passed = false
			}
		}
	}
	planned := []string{}
	for _, x := range filtered {
		planned = append(planned, x["stage"].(string))
	}
	return map[string]any{"workspaceId": a.WorkspaceID, "path": a.Path, "projectRoot": project["projectRoot"], "gitRoot": project["gitRoot"], "requestedStages": a.Stages, "planned": planned, "executed": len(results), "passed": passed, "results": results}, nil
}

func (n *nativeTools) codingBriefTool(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var a struct {
		WorkspaceID, Path, Task string
		MaxSkills, MaxMemories  int
	}
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.Path == "" {
		a.Path = "."
	}
	if a.MaxSkills == 0 {
		a.MaxSkills = 8
	}
	if a.MaxMemories == 0 && !rawHasJSONKey(raw, "maxMemories") {
		a.MaxMemories = 8
	}
	project, err := n.projectInspect(ctx, a.WorkspaceID, a.Path, true, 10000)
	if err != nil {
		return nil, err
	}
	scope := projectLearningScopeNative(project)
	memories := []map[string]any{}
	if a.MaxMemories > 0 {
		relevant, _ := n.learning.search(firstNonEmpty(a.Task, a.Path), a.MaxMemories, nil, []string{scope, "global"})
		recent, _ := n.learning.search("", a.MaxMemories, nil, []string{scope})
		seen := map[string]bool{}
		for _, m := range append(relevant, recent...) {
			id, _ := m["id"].(string)
			if id != "" && !seen[id] {
				seen[id] = true
				memories = append(memories, m)
				if len(memories) >= a.MaxMemories {
					break
				}
			}
		}
	}
	all, _ := n.learning.listSkills("", false)
	matched := []map[string]any{}
	if a.Task != "" {
		matched, _ = n.learning.listSkills(a.Task, false)
	}
	if n.evolution != nil && len(matched) > 0 {
		for i, skill := range matched {
			slug, _ := skill["slug"].(string)
			baseScore, _ := skill["score"].(float64)
			adjusted, feedback := n.evolution.skillAdjustedScore(slug, baseScore)
			copy := cloneMap(skill)
			copy["baseScore"] = baseScore
			copy["score"] = adjusted
			copy["outcomeFeedback"] = feedback
			matched[i] = copy
		}
		sort.SliceStable(matched, func(i, j int) bool {
			left, _ := matched[i]["score"].(float64)
			right, _ := matched[j]["score"].(float64)
			return left > right
		})
	}
	bySlug := map[string]map[string]any{}
	for _, s := range all {
		bySlug[s["slug"].(string)] = s
	}
	selected := []map[string]any{}
	seenSkills := map[string]bool{}
	for _, s := range matched {
		score, _ := s["score"].(float64)
		if score >= .08 {
			copy := cloneMap(s)
			copy["selectionReason"] = "task-match"
			selected = append(selected, copy)
			seenSkills[s["slug"].(string)] = true
			if len(selected) >= a.MaxSkills {
				break
			}
		}
	}
	for _, slug := range []string{"gpt-agent-repo-surgeon", "gpt-agent-verification-gate", "gpt-agent-learning-loop"} {
		if len(selected) >= a.MaxSkills {
			break
		}
		if seenSkills[slug] {
			continue
		}
		if s := bySlug[slug]; s != nil {
			copy := cloneMap(s)
			copy["score"] = float64(0)
			copy["selectionReason"] = "core"
			selected = append(selected, copy)
			seenSkills[slug] = true
		}
	}
	skills := []map[string]any{}
	skillSlugs := []string{}
	for _, s := range selected {
		slug := s["slug"].(string)
		loaded, err := n.learning.getSkill(slug, true)
		if err != nil {
			return nil, err
		}
		copy := cloneMap(s)
		copy["metadata"] = loaded["metadata"]
		copy["content"] = loaded["content"]
		skills = append(skills, copy)
		skillSlugs = append(skillSlugs, slug)
	}
	learningState := map[string]any{
		"scope":                    scope,
		"automaticCapture":         n.evolution != nil,
		"completionReviewRequired": a.Task != "",
		"tool":                     "gpt_agent_learn",
		"rule":                     "Runtime automatically captures task/tool/verification outcomes and adapts skill ranking. Use gpt_agent_learn for semantic promotion when an evidence-backed candidate contains a durable reusable fact, decision, root-cause lesson, or skill improvement.",
		"neverPersist":             []string{"secrets", "credentials", "transient logs", "temporary process state", "unverified hypotheses", "one-off command output"},
	}
	if n.evolution != nil && a.Task != "" {
		learningState["session"] = n.evolution.beginTask(a.WorkspaceID, a.Path, scope, a.Task, skillSlugs)
		context := n.evolution.context(scope, a.Task, 6)
		learningState["candidates"] = context["candidates"]
		learningState["similarOutcomes"] = context["similarOutcomes"]
		learningState["skillAdaptation"] = "feedback-weighted"
	}
	return map[string]any{"skillPackVersion": codingSkillPackVersion, "task": a.Task, "project": project, "memories": memories, "skills": skills, "learning": learningState}, nil
}

func (n *nativeTools) fastContextTool(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var a struct {
		WorkspaceID, Path                             string
		Queries, Files                                []string
		IncludeContext                                *bool
		IncludeMap                                    bool
		MaxResultsPerQuery, MaxFileChars, MaxMapFiles int
	}
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.Path == "" {
		a.Path = "."
	}
	inc := true
	if a.IncludeContext != nil {
		inc = *a.IncludeContext
	}
	if a.MaxResultsPerQuery == 0 {
		a.MaxResultsPerQuery = 40
	}
	if a.MaxFileChars == 0 {
		a.MaxFileChars = 12000
	}
	if a.MaxMapFiles == 0 {
		a.MaxMapFiles = 80
	}
	if a.MaxResultsPerQuery < 1 || a.MaxResultsPerQuery > 200 {
		return nil, nativeToolError{"maxResultsPerQuery must be between 1 and 200"}
	}
	if a.MaxFileChars < 1000 || a.MaxFileChars > 50000 {
		return nil, nativeToolError{"maxFileChars must be between 1000 and 50000"}
	}
	if a.MaxMapFiles < 1 || a.MaxMapFiles > 200 {
		return nil, nativeToolError{"maxMapFiles must be between 1 and 200"}
	}
	a.Queries = uniqueLimited(a.Queries, 8)
	a.Files = uniqueLimited(a.Files, 12)
	var project map[string]any
	var pErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); project, pErr = n.projectInspect(ctx, a.WorkspaceID, a.Path, inc, 6000) }()
	searches := make([]map[string]any, len(a.Queries))
	for i, q := range a.Queries {
		wg.Add(1)
		go func(i int, q string) {
			defer wg.Done()
			r, e := n.searchText(ctx, a.WorkspaceID, a.Path, q, "", a.MaxResultsPerQuery, false, false)
			if e != nil {
				searches[i] = map[string]any{"query": q, "error": e.Error()}
			} else {
				searches[i] = map[string]any{"query": q, "result": r}
			}
		}(i, q)
	}
	readFiles := make([]map[string]any, len(a.Files))
	for i, f := range a.Files {
		wg.Add(1)
		go func(i int, f string) {
			defer wg.Done()
			target, e := joinProjectPathNative(a.Path, f)
			if e != nil {
				readFiles[i] = map[string]any{"path": f, "error": e.Error()}
				return
			}
			r, e := n.readFile(a.WorkspaceID, target, a.MaxFileChars, nil, nil)
			if e != nil {
				readFiles[i] = map[string]any{"path": f, "error": e.Error()}
			} else {
				readFiles[i] = r
			}
		}(i, f)
	}
	var repo any
	if a.IncludeMap {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := n.repoMapTool(ctx, mustJSON(map[string]any{"workspaceId": a.WorkspaceID, "path": a.Path, "maxFiles": a.MaxMapFiles}))
			if e == nil {
				repo = r
			}
		}()
	}
	wg.Wait()
	if pErr != nil {
		return nil, pErr
	}
	return map[string]any{"workspaceId": a.WorkspaceID, "path": a.Path, "project": project, "searches": searches, "files": readFiles, "repoMap": repo}, nil
}
func joinProjectPathNative(base, child string) (string, error) {
	base = filepath.ToSlash(filepath.Clean(filepath.FromSlash(base)))
	if base == "" {
		base = "."
	}
	child = filepath.ToSlash(strings.TrimPrefix(child, "./"))
	if filepath.IsAbs(filepath.FromSlash(child)) {
		return "", nativeToolError{"Fast-context file paths must be relative to the selected project."}
	}
	target := filepath.ToSlash(filepath.Clean(filepath.Join(filepath.FromSlash(base), filepath.FromSlash(child))))
	rel, err := filepath.Rel(filepath.FromSlash(base), filepath.FromSlash(target))
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", nativeToolError{"Fast-context file path escapes the selected project."}
	}
	return target, nil
}

func containsString(items []string, s string) bool {
	for _, x := range items {
		if x == s {
			return true
		}
	}
	return false
}
func sliceMaps(items []map[string]any, max int) []map[string]any {
	if len(items) > max {
		return items[:max]
	}
	return items
}
func toStringSlice(v any) []string {
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, i := range x {
			out = append(out, fmt.Sprint(i))
		}
		return out
	}
	return nil
}
func uniqueLimited(items []string, max int) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, x := range items {
		v := strings.TrimSpace(x)
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
			if len(out) >= max {
				break
			}
		}
	}
	return out
}
func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
