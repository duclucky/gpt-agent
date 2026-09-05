package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

func (n *nativeTools) statusTool(raw json.RawMessage) (map[string]any, error) {
	var args map[string]any
	if err := decodeNativeArgs(raw, &args); err != nil {
		return nil, err
	}
	hostname, _ := os.Hostname()
	workspaces := make([]map[string]any, 0, len(n.cfg.Workspaces))
	for _, ws := range n.cfg.Workspaces {
		exists := false
		git := false
		if st, err := os.Stat(ws.Root); err == nil && st.IsDir() {
			exists = true
			if st, err := os.Stat(filepath.Join(ws.Root, ".git")); err == nil && st.IsDir() {
				git = true
			}
		}
		workspaces = append(workspaces, map[string]any{"id": ws.ID, "root": ws.Root, "exists": exists, "git": git, "performanceWarning": nil})
	}
	grant := n.readFullShellGrant()
	return map[string]any{
		"version": runtimeVersion, "appRoot": n.appRoot, "configPath": n.configPath,
		"go": runtime.Version(), "platform": runtime.GOOS + " " + runtime.GOARCH, "wsl": false,
		"hostname": hostname, "fullShell": grant, "workspaces": workspaces,
		"core":    map[string]any{"engine": "go", "version": runtimeVersion, "pid": os.Getpid(), "nativeToolCount": len(n.names())},
		"backend": map[string]any{"engine": "none", "ready": true, "required": false},
		"learning": func() map[string]any {
			if n.evolution != nil {
				return n.evolution.stats()
			}
			return map[string]any{"enabled": false}
		}(),
	}, nil
}

func (n *nativeTools) readFullShellGrant() map[string]any {
	path := filepath.Join(n.dataRoot, "full-shell.grant.json")
	buf, err := os.ReadFile(path)
	if err != nil {
		return map[string]any{"active": false, "reason": "no grant"}
	}
	var grant struct {
		ConfirmedByUser bool   `json:"confirmedByUser"`
		ExpiresAt       string `json:"expiresAt"`
	}
	if json.Unmarshal(buf, &grant) != nil || !grant.ConfirmedByUser {
		return map[string]any{"active": false, "reason": "grant expired or invalid"}
	}
	expires, err := time.Parse(time.RFC3339, grant.ExpiresAt)
	if err != nil || !expires.After(time.Now()) {
		return map[string]any{"active": false, "reason": "grant expired or invalid"}
	}
	return map[string]any{"active": true, "expiresAt": grant.ExpiresAt}
}

func (n *nativeTools) workspaceInspectTool(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var args struct {
		WorkspaceID string `json:"workspaceId"`
	}
	if err := decodeNativeArgs(raw, &args); err != nil {
		return nil, err
	}
	ws, err := n.workspace(args.WorkspaceID)
	if err != nil {
		return nil, err
	}
	tree, err := n.listFiles(args.WorkspaceID, ".", 500, 5)
	if err != nil {
		return nil, err
	}
	entries, _ := os.ReadDir(ws.Root)
	manifestSet := map[string]bool{"package.json": true, "pyproject.toml": true, "requirements.txt": true, "Cargo.toml": true, "go.mod": true, "Dockerfile": true, "docker-compose.yml": true, "compose.yml": true}
	manifests := []string{}
	for _, e := range entries {
		if !e.IsDir() && (manifestSet[e.Name()] || strings.HasSuffix(e.Name(), ".sln") || strings.HasSuffix(e.Name(), ".csproj")) {
			manifests = append(manifests, e.Name())
		}
	}
	var packageScripts any
	var dependencies any
	if buf, readErr := os.ReadFile(filepath.Join(ws.Root, "package.json")); readErr == nil {
		var pkg map[string]any
		if json.Unmarshal(buf, &pkg) == nil {
			packageScripts = pkg["scripts"]
			seen := map[string]bool{}
			deps := []string{}
			for _, field := range []string{"dependencies", "devDependencies"} {
				if m, ok := pkg[field].(map[string]any); ok {
					keys := make([]string, 0, len(m))
					for k := range m {
						keys = append(keys, k)
					}
					sort.Strings(keys)
					for _, k := range keys {
						if !seen[k] && len(deps) < 100 {
							seen[k] = true
							deps = append(deps, k)
						}
					}
				}
			}
			dependencies = deps
		}
	}
	code, out, stderr, runErr := n.runGit(ctx, args.WorkspaceID, "status", "--short", "--branch")
	var git any
	if runErr == nil {
		if out == "" {
			out = stderr
		}
		git = map[string]any{"exitCode": code, "output": out}
	}
	return map[string]any{"workspaceId": args.WorkspaceID, "root": ws.Root, "manifests": manifests, "packageScripts": packageScripts, "dependenciesSample": dependencies, "git": git, "tree": tree}, nil
}

func (n *nativeTools) codeOutlineTool(raw json.RawMessage) (map[string]any, error) {
	var args struct{ WorkspaceID, Path string }
	if err := decodeNativeArgs(raw, &args); err != nil {
		return nil, err
	}
	_, _, abs, err := n.resolveWorkspacePath(args.WorkspaceID, args.Path)
	if err != nil {
		return nil, err
	}
	buf, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	text := strings.ReplaceAll(string(buf), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	ext := strings.ToLower(filepath.Ext(args.Path))
	type rule struct {
		kind string
		rx   *regexp.Regexp
	}
	rules := []rule{}
	add := func(kind, pattern string) { rules = append(rules, rule{kind, regexp.MustCompile(pattern)}) }
	switch ext {
	case ".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs":
		add("class", `^\s*(?:export\s+)?class\s+([A-Za-z_$][\w$]*)`)
		add("function", `^\s*(?:export\s+)?(?:async\s+)?function\s+([A-Za-z_$][\w$]*)`)
		add("arrow", `^\s*(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=`)
		add("interface", `^\s*(?:export\s+)?interface\s+([A-Za-z_$][\w$]*)`)
		add("type", `^\s*(?:export\s+)?type\s+([A-Za-z_$][\w$]*)\s*=`)
	case ".py":
		add("class", `^\s*class\s+([A-Za-z_]\w*)`)
		add("function", `^\s*(?:async\s+)?def\s+([A-Za-z_]\w*)`)
	case ".go":
		add("function", `^\s*func\s+(?:\([^)]*\)\s*)?([A-Za-z_]\w*)`)
		add("type", `^\s*type\s+([A-Za-z_]\w*)\s+`)
	case ".rs":
		add("function", `^\s*(?:pub\s+)?(?:async\s+)?fn\s+([A-Za-z_]\w*)`)
		add("struct", `^\s*(?:pub\s+)?struct\s+([A-Za-z_]\w*)`)
		add("enum", `^\s*(?:pub\s+)?enum\s+([A-Za-z_]\w*)`)
		add("trait", `^\s*(?:pub\s+)?trait\s+([A-Za-z_]\w*)`)
	}
	symbols := []map[string]any{}
	for i, line := range lines {
		for _, r := range rules {
			if m := r.rx.FindStringSubmatch(line); len(m) > 1 {
				symbols = append(symbols, map[string]any{"kind": r.kind, "name": m[1], "line": i + 1, "preview": strings.TrimSpace(line)})
			}
		}
	}
	return map[string]any{"path": args.Path, "languageHint": ext, "symbols": symbols}, nil
}

func (n *nativeTools) structuralSearchTool(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var args struct {
		WorkspaceID, Path, Pattern, Language string
		Globs                                []string `json:"globs"`
	}
	if err := decodeNativeArgs(raw, &args); err != nil {
		return nil, err
	}
	if args.Path == "" {
		args.Path = "."
	}
	if args.Pattern == "" {
		return nil, nativeToolError{"pattern is required"}
	}
	_, _, abs, err := n.resolveWorkspacePath(args.WorkspaceID, args.Path)
	if err != nil {
		return nil, err
	}
	cmdName := ""
	if _, e := exec.LookPath("ast-grep"); e == nil {
		cmdName = "ast-grep"
	} else if _, e := exec.LookPath("sg"); e == nil {
		cmdName = "sg"
	}
	if cmdName == "" {
		return map[string]any{"available": false, "installHint": "Install ast-grep CLI and ensure `ast-grep` or `sg` is on PATH.", "fallbackHint": "Use gpt_agent_search_text until ast-grep is installed."}, nil
	}
	argv := []string{"run", "--pattern", args.Pattern, "--json=stream"}
	if args.Language != "" {
		argv = append(argv, "--lang", args.Language)
	}
	for _, g := range args.Globs {
		argv = append(argv, "--globs", g)
	}
	argv = append(argv, ".")
	code, out, stderr, err := runCapture(ctx, cmdName, argv, abs, nil)
	if err != nil {
		return nil, err
	}
	text, truncated, _ := truncateNativeText(out, n.cfg.Server.MaxToolOutputChars)
	return map[string]any{"available": true, "engine": cmdName, "workspaceId": args.WorkspaceID, "path": args.Path, "exitCode": code, "output": text, "stderr": stderr, "truncated": truncated}, nil
}

func (n *nativeTools) repoMapTool(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var args struct {
		WorkspaceID, Path string
		MaxFiles          int `json:"maxFiles"`
	}
	if err := decodeNativeArgs(raw, &args); err != nil {
		return nil, err
	}
	if args.Path == "" {
		args.Path = "."
	}
	if args.MaxFiles == 0 {
		args.MaxFiles = 120
	}
	if args.MaxFiles < 1 || args.MaxFiles > 300 {
		return nil, nativeToolError{"maxFiles must be between 1 and 300"}
	}
	ws, _, projectRoot, err := n.resolveWorkspacePath(args.WorkspaceID, args.Path)
	if err != nil {
		return nil, err
	}
	tree, err := n.listFiles(args.WorkspaceID, args.Path, 5000, 12)
	if err != nil {
		return nil, err
	}
	files, _ := tree["files"].([]string)
	sourceExt := map[string]bool{".js": true, ".jsx": true, ".ts": true, ".tsx": true, ".mjs": true, ".cjs": true, ".py": true, ".go": true, ".rs": true, ".c": true, ".cc": true, ".cpp": true, ".cxx": true, ".h": true, ".hpp": true, ".java": true, ".kt": true, ".cs": true}
	preferred := []string{}
	for _, f := range files {
		if sourceExt[strings.ToLower(filepath.Ext(f))] && len(preferred) < args.MaxFiles {
			preferred = append(preferred, f)
		}
	}
	type result struct {
		idx   int
		value map[string]any
	}
	ch := make(chan result, len(preferred))
	sem := make(chan struct{}, 12)
	var wg sync.WaitGroup
	for i, f := range preferred {
		wg.Add(1)
		go func(i int, f string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			v, e := n.codeOutlineTool(mustJSON(map[string]any{"workspaceId": args.WorkspaceID, "path": f}))
			if e == nil {
				if s, ok := v["symbols"].([]map[string]any); ok && len(s) > 0 {
					if len(s) > 80 {
						s = s[:80]
					}
					ch <- result{i, map[string]any{"path": f, "symbols": s}}
				}
			}
		}(i, f)
	}
	wg.Wait()
	close(ch)
	collected := make([]result, 0, len(ch))
	for r := range ch {
		collected = append(collected, r)
	}
	sort.Slice(collected, func(i, j int) bool { return collected[i].idx < collected[j].idx })
	outlines := make([]map[string]any, 0, len(collected))
	for _, r := range collected {
		outlines = append(outlines, r.value)
	}
	base, _ := filepath.Rel(ws.Root, projectRoot)
	base = filepath.ToSlash(base)
	if base == "." {
		base = ""
	}
	top := []string{}
	for _, f := range files {
		rel := f
		if base != "" {
			relPath, e := filepath.Rel(filepath.FromSlash(base), filepath.FromSlash(f))
			if e == nil {
				rel = filepath.ToSlash(relPath)
			}
		}
		if rel != "" && !strings.Contains(rel, "/") {
			top = append(top, f)
			if len(top) >= 200 {
				break
			}
		}
	}
	return map[string]any{"workspaceId": args.WorkspaceID, "path": args.Path, "projectRoot": projectRoot, "totalFilesSeen": tree["count"], "sourceFilesMapped": len(outlines), "topLevel": top, "outlines": outlines}, nil
}

func (n *nativeTools) writeFileTool(raw json.RawMessage) (map[string]any, error) {
	var a struct {
		WorkspaceID, Path, Content, ExpectedSHA256 string
		CreateParents                              *bool `json:"createParents"`
	}
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.WorkspaceID == "" || a.Path == "" {
		return nil, nativeToolError{"workspaceId and path are required"}
	}
	create := true
	if a.CreateParents != nil {
		create = *a.CreateParents
	}
	_, _, abs, err := n.resolveWorkspacePath(a.WorkspaceID, a.Path)
	if err != nil {
		return nil, err
	}
	existed := false
	before := ""
	if buf, e := os.ReadFile(abs); e == nil {
		existed = true
		s := sha256.Sum256(buf)
		before = hex.EncodeToString(s[:])
		if a.ExpectedSHA256 != "" && before != a.ExpectedSHA256 {
			return nil, fmt.Errorf("CONFLICT: expected %s, actual %s", a.ExpectedSHA256, before)
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return nil, e
	} else if a.ExpectedSHA256 != "" {
		return nil, nativeToolError{"CONFLICT: expectedSha256 supplied but file does not exist."}
	}
	if create {
		if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
			return nil, err
		}
	}
	if err := os.WriteFile(abs, []byte(a.Content), 0644); err != nil {
		return nil, err
	}
	s := sha256.Sum256([]byte(a.Content))
	after := hex.EncodeToString(s[:])
	_, _ = n.audit.append(map[string]any{"type": "file.write", "workspaceId": a.WorkspaceID, "path": a.Path, "existed": existed, "beforeSha256": nilIfEmpty(before), "afterSha256": after, "bytes": len([]byte(a.Content))})
	return map[string]any{"path": a.Path, "created": !existed, "beforeSha256": nilIfEmpty(before), "afterSha256": after, "bytes": len([]byte(a.Content))}, nil
}

func (n *nativeTools) replaceTextTool(raw json.RawMessage) (map[string]any, error) {
	var a struct {
		WorkspaceID, Path, OldText, NewText, ExpectedSHA256 string
		ReplaceAll                                          bool `json:"replaceAll"`
	}
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.OldText == "" {
		return nil, nativeToolError{"oldText is required"}
	}
	_, _, abs, err := n.resolveWorkspacePath(a.WorkspaceID, a.Path)
	if err != nil {
		return nil, err
	}
	buf, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	before := string(buf)
	sum := sha256.Sum256(buf)
	beforeHash := hex.EncodeToString(sum[:])
	if a.ExpectedSHA256 != "" && a.ExpectedSHA256 != beforeHash {
		return nil, fmt.Errorf("CONFLICT: expected %s, actual %s", a.ExpectedSHA256, beforeHash)
	}
	count := strings.Count(before, a.OldText)
	if count == 0 {
		return nil, nativeToolError{"oldText was not found."}
	}
	if !a.ReplaceAll && count != 1 {
		return nil, fmt.Errorf("oldText occurs %d times; use replaceAll=true or a more specific match.", count)
	}
	after := before
	if a.ReplaceAll {
		after = strings.ReplaceAll(before, a.OldText, a.NewText)
	} else {
		after = strings.Replace(before, a.OldText, a.NewText, 1)
	}
	if err := os.WriteFile(abs, []byte(after), 0644); err != nil {
		return nil, err
	}
	s2 := sha256.Sum256([]byte(after))
	afterHash := hex.EncodeToString(s2[:])
	replaced := 1
	if a.ReplaceAll {
		replaced = count
	}
	_, _ = n.audit.append(map[string]any{"type": "file.replace", "workspaceId": a.WorkspaceID, "path": a.Path, "occurrences": replaced, "beforeSha256": beforeHash, "afterSha256": afterHash})
	return map[string]any{"path": a.Path, "replaced": replaced, "beforeSha256": beforeHash, "afterSha256": afterHash}, nil
}

func (n *nativeTools) makeDirectoryTool(raw json.RawMessage) (map[string]any, error) {
	var a struct{ WorkspaceID, Path string }
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	_, _, abs, err := n.resolveWorkspacePath(a.WorkspaceID, a.Path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0755); err != nil {
		return nil, err
	}
	_, _ = n.audit.append(map[string]any{"type": "directory.create", "workspaceId": a.WorkspaceID, "path": a.Path})
	return map[string]any{"created": true, "path": a.Path}, nil
}
func (n *nativeTools) movePathTool(raw json.RawMessage) (map[string]any, error) {
	var a struct {
		WorkspaceID, From, To string
		Overwrite             bool
	}
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	_, _, src, err := n.resolveWorkspacePath(a.WorkspaceID, a.From)
	if err != nil {
		return nil, err
	}
	_, _, dst, err := n.resolveWorkspacePath(a.WorkspaceID, a.To)
	if err != nil {
		return nil, err
	}
	if _, e := os.Stat(dst); e == nil && !a.Overwrite {
		return nil, nativeToolError{"Destination exists."}
	}
	if a.Overwrite {
		if _, e := os.Stat(dst); e == nil {
			if err := os.RemoveAll(dst); err != nil {
				return nil, err
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return nil, err
	}
	if err := os.Rename(src, dst); err != nil {
		return nil, err
	}
	_, _ = n.audit.append(map[string]any{"type": "path.move", "workspaceId": a.WorkspaceID, "from": a.From, "to": a.To, "overwrite": a.Overwrite})
	return map[string]any{"moved": true, "from": a.From, "to": a.To}, nil
}
func (n *nativeTools) deletePathTool(raw json.RawMessage) (map[string]any, error) {
	var a struct {
		WorkspaceID, Path, Confirm string
		Recursive                  bool
	}
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.Confirm != "DELETE" {
		return nil, nativeToolError{"confirm must be DELETE"}
	}
	ws, _, abs, err := n.resolveWorkspacePath(a.WorkspaceID, a.Path)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(filepath.Clean(abs), filepath.Clean(ws.Root)) {
		return nil, nativeToolError{"Refuse deleting workspace root."}
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if st.IsDir() && a.Recursive {
		err = os.RemoveAll(abs)
	} else {
		err = os.Remove(abs)
	}
	if err != nil {
		return nil, err
	}
	_, _ = n.audit.append(map[string]any{"type": "path.delete", "workspaceId": a.WorkspaceID, "path": a.Path, "recursive": a.Recursive})
	return map[string]any{"deleted": true, "path": a.Path}, nil
}

func (n *nativeTools) gitLogTool(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var a struct {
		WorkspaceID string
		MaxCount    int `json:"maxCount"`
	}
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.MaxCount == 0 {
		a.MaxCount = 30
	}
	if a.MaxCount < 1 || a.MaxCount > 200 {
		return nil, nativeToolError{"maxCount must be between 1 and 200"}
	}
	code, out, stderr, err := n.runGit(ctx, a.WorkspaceID, "log", fmt.Sprintf("--max-count=%d", a.MaxCount), "--date=iso", "--pretty=format:%h%x09%ad%x09%an%x09%s")
	if err != nil {
		return nil, err
	}
	if out == "" {
		out = stderr
	}
	return map[string]any{"exitCode": code, "output": out}, nil
}
func (n *nativeTools) checkpointTool(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var a struct{ WorkspaceID, Label string }
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.Label == "" {
		a.Label = "checkpoint"
	}
	statusCode, status, se, err := n.runGit(ctx, a.WorkspaceID, "status", "--short", "--branch")
	_ = statusCode
	if err != nil {
		return nil, err
	}
	if status == "" {
		status = se
	}
	_, diff, _, err := n.runGit(ctx, a.WorkspaceID, "diff", "--binary")
	if err != nil {
		return nil, err
	}
	_, staged, _, err := n.runGit(ctx, a.WorkspaceID, "diff", "--cached", "--binary")
	if err != nil {
		return nil, err
	}
	id := "checkpoint_" + randomHex(8)
	safe := regexp.MustCompile(`[^a-zA-Z0-9._-]+`).ReplaceAllString(a.Label, "_")
	if len(safe) > 60 {
		safe = safe[:60]
	}
	base := filepath.Join(n.dataRoot, "artifacts", id+"-"+safe)
	if err := os.MkdirAll(filepath.Dir(base), 0755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(base+".status.txt", []byte(status), 0644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(base+".working.patch", []byte(diff), 0644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(base+".staged.patch", []byte(staged), 0644); err != nil {
		return nil, err
	}
	_, _ = n.audit.append(map[string]any{"type": "checkpoint.create", "workspaceId": a.WorkspaceID, "id": id, "label": a.Label})
	return map[string]any{"id": id, "statusPath": base + ".status.txt", "workingPatchPath": base + ".working.patch", "stagedPatchPath": base + ".staged.patch"}, nil
}
func (n *nativeTools) applyGitPatchTool(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var a struct{ WorkspaceID, Patch string }
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.Patch == "" {
		return nil, nativeToolError{"patch is required"}
	}
	ws, err := n.workspace(a.WorkspaceID)
	if err != nil {
		return nil, err
	}
	code, out, se, err := runCapture(ctx, "git", []string{"apply", "--check", "--whitespace=nowarn", "-"}, ws.Root, []byte(a.Patch))
	if err != nil {
		return nil, err
	}
	if code != 0 {
		if se == "" {
			se = out
		}
		return nil, fmt.Errorf("git apply --check failed:\n%s", se)
	}
	code, out, se, err = runCapture(ctx, "git", []string{"apply", "--whitespace=nowarn", "-"}, ws.Root, []byte(a.Patch))
	if err != nil {
		return nil, err
	}
	if code != 0 {
		if se == "" {
			se = out
		}
		return nil, fmt.Errorf("git apply failed:\n%s", se)
	}
	sum := sha256.Sum256([]byte(a.Patch))
	hash := hex.EncodeToString(sum[:])
	_, _ = n.audit.append(map[string]any{"type": "git.applyPatch", "workspaceId": a.WorkspaceID, "patchSha256": hash})
	return map[string]any{"applied": true, "patchSha256": hash}, nil
}

func (n *nativeTools) contextDiscoverTool(raw json.RawMessage) (map[string]any, error) {
	var a struct {
		WorkspaceID, Path string
		MaxCharsPerFile   int `json:"maxCharsPerFile"`
	}
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.Path == "" {
		a.Path = "."
	}
	if a.MaxCharsPerFile == 0 {
		a.MaxCharsPerFile = 12000
	}
	_, _, root, err := n.resolveWorkspacePath(a.WorkspaceID, a.Path)
	if err != nil {
		return nil, err
	}
	names := []string{".gpt-agent/PROJECT.md", ".gpt-agent/ARCHITECTURE.md", ".gpt-agent/CURRENT_STATE.md", ".gpt-agent/DECISIONS.md", ".gpt-agent/TODO.md", "AGENTS.md", "SOUL.md", ".hermes.md", ".cursorrules"}
	files := []map[string]any{}
	for _, name := range names {
		abs := filepath.Join(root, filepath.FromSlash(name))
		st, e := os.Stat(abs)
		if e != nil {
			if errors.Is(e, os.ErrNotExist) {
				continue
			}
			return nil, e
		}
		if !st.Mode().IsRegular() {
			continue
		}
		buf, e := os.ReadFile(abs)
		if e != nil {
			return nil, e
		}
		text, trunc, orig := truncateNativeText(string(buf), a.MaxCharsPerFile)
		rel := filepath.ToSlash(filepath.Join(a.Path, filepath.FromSlash(name)))
		rel = strings.TrimPrefix(rel, "./")
		files = append(files, map[string]any{"path": rel, "content": text, "truncated": trunc, "originalChars": orig})
	}
	return map[string]any{"workspaceId": a.WorkspaceID, "path": a.Path, "files": files}, nil
}

func (n *nativeTools) sqliteSchemaTool(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var a struct{ WorkspaceID, Path string }
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	_, _, abs, err := n.resolveWorkspacePath(a.WorkspaceID, a.Path)
	if err != nil {
		return nil, err
	}
	sql := "SELECT type,name,tbl_name,sql FROM sqlite_master WHERE type IN ('table','view','index','trigger') ORDER BY type,name;"
	code, out, se, err := runCapture(ctx, "sqlite3", []string{"-readonly", "-json", abs, sql}, "", nil)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, errors.New(firstNonEmpty(se, "sqlite3 failed"))
	}
	objects := []any{}
	if strings.TrimSpace(out) != "" {
		if err := json.Unmarshal([]byte(out), &objects); err != nil {
			return nil, err
		}
	}
	return map[string]any{"path": a.Path, "objects": objects}, nil
}
func (n *nativeTools) sqliteQueryTool(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var a struct {
		WorkspaceID, Path, SQL string
		MaxChars               int `json:"maxChars"`
	}
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.MaxChars == 0 {
		a.MaxChars = 80000
	}
	if err := assertReadonlySQL(a.SQL); err != nil {
		return nil, err
	}
	_, _, abs, err := n.resolveWorkspacePath(a.WorkspaceID, a.Path)
	if err != nil {
		return nil, err
	}
	code, out, se, err := runCapture(ctx, "sqlite3", []string{"-readonly", "-json", abs, a.SQL}, "", nil)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, errors.New(firstNonEmpty(se, "sqlite3 failed"))
	}
	limit := a.MaxChars
	if n.cfg.Server.MaxToolOutputChars < limit {
		limit = n.cfg.Server.MaxToolOutputChars
	}
	text, trunc, _ := truncateNativeText(out, limit)
	var rows any
	if !trunc {
		if strings.TrimSpace(out) == "" {
			rows = []any{}
		} else if json.Unmarshal([]byte(out), &rows) != nil {
			rows = nil
		}
	}
	result := map[string]any{"path": a.Path, "rows": rows, "truncated": trunc}
	if rows == nil {
		result["raw"] = text
	}
	return result, nil
}

func assertReadonlySQL(sql string) error {
	stripped := regexp.MustCompile(`(?m)--.*$`).ReplaceAllString(sql, "")
	stripped = regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(stripped, "")
	stripped = strings.TrimSpace(stripped)
	m := regexp.MustCompile(`^([A-Za-z]+)`).FindStringSubmatch(stripped)
	if len(m) < 2 {
		return nativeToolError{"Only read-only SQLite statements are allowed: SELECT, PRAGMA, EXPLAIN, WITH."}
	}
	head := strings.ToUpper(m[1])
	if head != "SELECT" && head != "PRAGMA" && head != "EXPLAIN" && head != "WITH" {
		return nativeToolError{"Only read-only SQLite statements are allowed: SELECT, PRAGMA, EXPLAIN, WITH."}
	}
	if regexp.MustCompile(`(?i)\b(INSERT|UPDATE|DELETE|DROP|ALTER|CREATE|REPLACE|ATTACH|DETACH|VACUUM|REINDEX)\b`).MatchString(stripped) {
		return nativeToolError{"Potentially mutating SQLite statement blocked."}
	}
	return nil
}

func (n *nativeTools) httpRequestTool(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var a struct {
		URL, Method, Body string
		Headers           map[string]string
		TimeoutMS         int `json:"timeoutMs"`
	}
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.Method == "" {
		a.Method = "GET"
	}
	if a.Headers == nil {
		a.Headers = map[string]string{}
	}
	if a.TimeoutMS == 0 {
		a.TimeoutMS = 15000
	}
	u, err := url.Parse(a.URL)
	if err != nil {
		return nil, err
	}
	if !n.allowedURL(u) {
		return nil, fmt.Errorf("HTTP host blocked. Allowed: %s", strings.Join(n.cfg.Security.HTTPAllowedHosts, ", "))
	}
	req, err := http.NewRequestWithContext(ctx, a.Method, u.String(), strings.NewReader(a.Body))
	if err != nil {
		return nil, err
	}
	for k, v := range a.Headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{
		Timeout: time.Duration(a.TimeoutMS) * time.Millisecond,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("HTTP redirect limit exceeded")
			}
			if !n.allowedURL(req.URL) {
				return fmt.Errorf("HTTP redirect host blocked. Allowed: %s", strings.Join(n.cfg.Security.HTTPAllowedHosts, ", "))
			}
			return nil
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	buf, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	text, trunc, _ := truncateNativeText(string(buf), n.cfg.Server.MaxToolOutputChars)
	headers := map[string]string{}
	for k, v := range resp.Header {
		headers[strings.ToLower(k)] = strings.Join(v, ", ")
	}
	return map[string]any{"url": resp.Request.URL.String(), "status": resp.StatusCode, "statusText": http.StatusText(resp.StatusCode), "headers": headers, "body": text, "truncated": trunc}, nil
}
func (n *nativeTools) allowedURL(u *url.URL) bool {
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, h := range n.cfg.Security.HTTPAllowedHosts {
		if strings.ToLower(h) == host {
			return true
		}
	}
	return false
}

func isLoopbackHTTPURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	host := strings.TrimSpace(u.Hostname())
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func loopbackOnlyHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("HTTP redirect limit exceeded")
			}
			if !isLoopbackHTTPURL(req.URL.String()) {
				return errors.New("HTTP redirect left the loopback boundary")
			}
			return nil
		},
	}
}

func (n *nativeTools) waitPortTool(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var a struct {
		Host                        string
		Port, TimeoutMS, IntervalMS int
	}
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.Host == "" {
		a.Host = "127.0.0.1"
	}
	if a.TimeoutMS == 0 {
		a.TimeoutMS = 30000
	}
	if a.IntervalMS == 0 {
		a.IntervalMS = 250
	}
	started := time.Now()
	deadline := started.Add(time.Duration(a.TimeoutMS) * time.Millisecond)
	addr := net.JoinHostPort(a.Host, strconv.Itoa(a.Port))
	for time.Now().Before(deadline) {
		d := net.Dialer{Timeout: time.Second}
		c, e := d.DialContext(ctx, "tcp", addr)
		if e == nil {
			_ = c.Close()
			return map[string]any{"open": true, "host": a.Host, "port": a.Port, "waitedMs": time.Since(started).Milliseconds()}, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(a.IntervalMS) * time.Millisecond):
		}
	}
	return map[string]any{"open": false, "host": a.Host, "port": a.Port, "timeoutMs": a.TimeoutMS}, nil
}

func runCapture(ctx context.Context, executable string, args []string, cwd string, stdin []byte) (int, string, string, error) {
	cmd := exec.CommandContext(ctx, executable, args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	cmd.Env = os.Environ()
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, se bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &se
	err := cmd.Run()
	if err == nil {
		return 0, out.String(), se.String(), nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), out.String(), se.String(), nil
	}
	return 0, out.String(), se.String(), err
}
func mustJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
