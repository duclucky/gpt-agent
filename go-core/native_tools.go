package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

var ignoredTreeDirs = map[string]struct{}{
	".git": {}, "node_modules": {}, ".next": {}, "dist": {}, "build": {},
	"coverage": {}, ".venv": {}, "venv": {}, "target": {}, "bin": {}, "obj": {},
}

var nativeReadToolNames = []string{
	"gpt_agent_list_files",
	"gpt_agent_read_file",
	"gpt_agent_read_file_range",
	"gpt_agent_search_text",
	"gpt_agent_git_status",
	"gpt_agent_git_diff",
}

type nativeTools struct {
	cfg        runtimeConfig
	audit      *auditWriter
	rgPath     string
	appRoot    string
	dataRoot   string
	configPath string
	processes  *processManager
	learning   *learningStore
	evolution  *learningEvolution
	lsp        *lspManager
	dap        *dapManager
}

type nativeToolError struct {
	message string
}

func (e nativeToolError) Error() string { return e.message }

func newNativeTools(cfg runtimeConfig, audit *auditWriter, roots ...string) *nativeTools {
	rgPath, _ := exec.LookPath("rg")
	appRoot, dataRoot, configPath := "", "", ""
	if len(roots) > 0 {
		appRoot = roots[0]
	}
	if len(roots) > 1 {
		dataRoot = roots[1]
	}
	if len(roots) > 2 {
		configPath = roots[2]
	}
	if dataRoot == "" && audit != nil {
		dataRoot = filepath.Dir(audit.path)
	}
	if appRoot == "" {
		appRoot, _ = os.Getwd()
	}
	if configPath == "" {
		configPath = filepath.Join(dataRoot, "config.json")
	}
	n := &nativeTools{cfg: cfg, audit: audit, rgPath: rgPath, appRoot: appRoot, dataRoot: dataRoot, configPath: configPath}
	n.processes = newProcessManager(n)
	n.learning = newLearningStore(dataRoot)
	n.evolution = newLearningEvolution(dataRoot)
	n.lsp = newLSPManager(n)
	n.dap = newDAPManager(n)
	return n
}

func (n *nativeTools) names() []string {
	out := append([]string(nil), nativeReadToolNames...)
	out = append(out, nativeExtraToolNames...)
	return out
}

func (n *nativeTools) supports(name string) bool {
	for _, candidate := range nativeReadToolNames {
		if candidate == name {
			return true
		}
	}
	return supportsNativeExtra(name)
}

func (n *nativeTools) invoke(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	started := time.Now()
	payload, err := n.call(ctx, name, raw)
	if err != nil {
		if n.audit != nil {
			_, _ = n.audit.append(map[string]any{
				"type": "tool.error", "tool": name, "durationMs": time.Since(started).Milliseconds(), "error": err.Error(),
			})
		}
		if n.evolution != nil {
			n.evolution.observeTool(name, raw, nil, err)
		}
		return nil, err
	}
	if n.audit != nil {
		if _, auditErr := n.audit.append(map[string]any{
			"type": "tool.success", "tool": name, "durationMs": time.Since(started).Milliseconds(), "argsDigest": hashRawJSON(raw),
		}); auditErr != nil {
			if n.evolution != nil {
				n.evolution.observeTool(name, raw, nil, auditErr)
			}
			return nil, auditErr
		}
	}
	if n.evolution != nil {
		n.evolution.observeTool(name, raw, payload, nil)
	}
	return payload, nil
}

func (n *nativeTools) call(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	switch name {
	case "gpt_agent_list_files":
		var args struct {
			WorkspaceID string `json:"workspaceId"`
			Path        string `json:"path"`
			MaxFiles    int    `json:"maxFiles"`
			MaxDepth    int    `json:"maxDepth"`
		}
		if err := decodeNativeArgs(raw, &args); err != nil {
			return nil, err
		}
		if args.Path == "" {
			args.Path = "."
		}
		if args.MaxFiles == 0 && !rawHasJSONKey(raw, "maxFiles") {
			args.MaxFiles = 2000
		}
		if args.MaxDepth == 0 && !rawHasJSONKey(raw, "maxDepth") {
			args.MaxDepth = 8
		}
		if args.WorkspaceID == "" {
			return nil, nativeToolError{"workspaceId is required"}
		}
		if args.MaxFiles < 1 || args.MaxFiles > 10000 {
			return nil, nativeToolError{"maxFiles must be between 1 and 10000"}
		}
		if args.MaxDepth < 0 || args.MaxDepth > 20 {
			return nil, nativeToolError{"maxDepth must be between 0 and 20"}
		}
		return n.listFiles(args.WorkspaceID, args.Path, args.MaxFiles, args.MaxDepth)

	case "gpt_agent_read_file":
		var args struct {
			WorkspaceID string `json:"workspaceId"`
			Path        string `json:"path"`
			MaxChars    int    `json:"maxChars"`
		}
		if err := decodeNativeArgs(raw, &args); err != nil {
			return nil, err
		}
		if args.MaxChars == 0 && !rawHasJSONKey(raw, "maxChars") {
			args.MaxChars = 120000
		}
		if args.WorkspaceID == "" || args.Path == "" {
			return nil, nativeToolError{"workspaceId and path are required"}
		}
		if args.MaxChars < 1000 || args.MaxChars > 300000 {
			return nil, nativeToolError{"maxChars must be between 1000 and 300000"}
		}
		return n.readFile(args.WorkspaceID, args.Path, args.MaxChars, nil, nil)

	case "gpt_agent_read_file_range":
		var args struct {
			WorkspaceID string `json:"workspaceId"`
			Path        string `json:"path"`
			StartLine   int    `json:"startLine"`
			EndLine     int    `json:"endLine"`
			MaxChars    int    `json:"maxChars"`
		}
		if err := decodeNativeArgs(raw, &args); err != nil {
			return nil, err
		}
		if args.MaxChars == 0 && !rawHasJSONKey(raw, "maxChars") {
			args.MaxChars = 120000
		}
		if args.WorkspaceID == "" || args.Path == "" || args.StartLine < 1 || args.EndLine < 1 {
			return nil, nativeToolError{"workspaceId, path, startLine and endLine are required"}
		}
		if args.MaxChars < 1000 || args.MaxChars > 300000 {
			return nil, nativeToolError{"maxChars must be between 1000 and 300000"}
		}
		return n.readFile(args.WorkspaceID, args.Path, args.MaxChars, &args.StartLine, &args.EndLine)

	case "gpt_agent_search_text":
		var args struct {
			WorkspaceID   string `json:"workspaceId"`
			Path          string `json:"path"`
			Query         string `json:"query"`
			Glob          string `json:"glob"`
			MaxResults    int    `json:"maxResults"`
			Regex         bool   `json:"regex"`
			CaseSensitive bool   `json:"caseSensitive"`
		}
		if err := decodeNativeArgs(raw, &args); err != nil {
			return nil, err
		}
		if args.Path == "" {
			args.Path = "."
		}
		if args.MaxResults == 0 && !rawHasJSONKey(raw, "maxResults") {
			args.MaxResults = 200
		}
		if args.WorkspaceID == "" || args.Query == "" {
			return nil, nativeToolError{"workspaceId and query are required"}
		}
		if args.MaxResults < 1 || args.MaxResults > 1000 {
			return nil, nativeToolError{"maxResults must be between 1 and 1000"}
		}
		return n.searchText(ctx, args.WorkspaceID, args.Path, args.Query, args.Glob, args.MaxResults, args.Regex, args.CaseSensitive)

	case "gpt_agent_git_status":
		var args struct {
			WorkspaceID string `json:"workspaceId"`
		}
		if err := decodeNativeArgs(raw, &args); err != nil {
			return nil, err
		}
		if args.WorkspaceID == "" {
			return nil, nativeToolError{"workspaceId is required"}
		}
		exitCode, stdout, stderr, err := n.runGit(ctx, args.WorkspaceID, "status", "--short", "--branch")
		if err != nil {
			return nil, err
		}
		output := stdout
		if output == "" {
			output = stderr
		}
		return map[string]any{"exitCode": exitCode, "output": output}, nil

	case "gpt_agent_git_diff":
		var args struct {
			WorkspaceID string `json:"workspaceId"`
			Staged      bool   `json:"staged"`
			Path        string `json:"path"`
		}
		if err := decodeNativeArgs(raw, &args); err != nil {
			return nil, err
		}
		if args.WorkspaceID == "" {
			return nil, nativeToolError{"workspaceId is required"}
		}
		gitArgs := []string{"diff"}
		if args.Staged {
			gitArgs = append(gitArgs, "--cached")
		}
		if args.Path != "" {
			gitArgs = append(gitArgs, "--", args.Path)
		}
		exitCode, stdout, stderr, err := n.runGit(ctx, args.WorkspaceID, gitArgs...)
		if err != nil {
			return nil, err
		}
		output := stdout
		if output == "" {
			output = stderr
		}
		truncated, wasTruncated, _ := truncateNativeText(output, n.cfg.Server.MaxToolOutputChars)
		return map[string]any{"exitCode": exitCode, "diff": truncated, "truncated": wasTruncated}, nil
	}
	return n.callNativeExtra(ctx, name, raw)
}

func decodeNativeArgs(raw json.RawMessage, target any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		trimmed = []byte("{}")
	}
	if err := json.Unmarshal(trimmed, target); err != nil {
		return nativeToolError{"invalid tool arguments: " + err.Error()}
	}
	return nil
}

func rawHasJSONKey(raw json.RawMessage, key string) bool {
	var value map[string]json.RawMessage
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	_, ok := value[key]
	return ok
}

func (n *nativeTools) workspace(workspaceID string) (workspaceConfig, error) {
	for _, ws := range n.cfg.Workspaces {
		if ws.ID != workspaceID {
			continue
		}
		info, err := os.Stat(ws.Root)
		if err != nil || !info.IsDir() {
			return workspaceConfig{}, fmt.Errorf("Workspace root does not exist: %s", ws.Root)
		}
		return ws, nil
	}
	return workspaceConfig{}, fmt.Errorf("Unknown workspace '%s'.", workspaceID)
}

func (n *nativeTools) resolveWorkspacePath(workspaceID, relativePath string) (workspaceConfig, string, string, error) {
	ws, err := n.workspace(workspaceID)
	if err != nil {
		return workspaceConfig{}, "", "", err
	}
	rootReal, err := filepath.EvalSymlinks(ws.Root)
	if err != nil {
		return workspaceConfig{}, "", "", err
	}
	requested := relativePath
	if requested == "" {
		requested = "."
	}
	if !filepath.IsAbs(requested) {
		requested = filepath.Join(ws.Root, requested)
	}
	requested, err = filepath.Abs(requested)
	if err != nil {
		return workspaceConfig{}, "", "", err
	}
	canonical, err := canonicalForContainment(requested)
	if err != nil {
		return workspaceConfig{}, "", "", err
	}
	if !pathWithin(rootReal, canonical) {
		return workspaceConfig{}, "", "", fmt.Errorf("Path escapes workspace '%s': %s", workspaceID, relativePath)
	}
	if !n.cfg.Security.AllowSecretFiles {
		base := filepath.Base(requested)
		rel, _ := filepath.Rel(ws.Root, requested)
		rel = filepath.ToSlash(rel)
		for _, pattern := range n.cfg.Security.SecretFilePatterns {
			matchedBase, _ := wildcardMatch(pattern, base)
			matchedRel, _ := wildcardMatch(pattern, rel)
			if matchedBase || matchedRel {
				return workspaceConfig{}, "", "", fmt.Errorf("Secret-like path blocked by policy: %s", relativePath)
			}
		}
	}
	return ws, rootReal, requested, nil
}

func canonicalForContainment(target string) (string, error) {
	current := target
	for {
		if _, err := os.Stat(current); err == nil {
			realExisting, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			if current == target {
				return realExisting, nil
			}
			rel, err := filepath.Rel(current, target)
			if err != nil {
				return "", err
			}
			return filepath.Clean(filepath.Join(realExisting, rel)), nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("No existing parent for %s", target)
		}
		current = parent
	}
}

func pathWithin(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel == "." || rel == "" || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel))
}

func wildcardMatch(pattern, value string) (bool, error) {
	var out strings.Builder
	out.WriteString("(?i)^")
	for _, r := range pattern {
		switch r {
		case '*':
			out.WriteString(".*")
		case '?':
			out.WriteByte('.')
		default:
			out.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	out.WriteByte('$')
	rx, err := regexp.Compile(out.String())
	if err != nil {
		return false, err
	}
	return rx.MatchString(value), nil
}

func (n *nativeTools) listFiles(workspaceID, relativePath string, maxFiles, maxDepth int) (map[string]any, error) {
	ws, _, absolute, err := n.resolveWorkspacePath(workspaceID, relativePath)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, nativeToolError{"Path is not a directory."}
	}
	files := make([]string, 0, min(maxFiles, 512))
	var walk func(string, int)
	walk = func(dir string, depth int) {
		if len(files) >= maxFiles || depth > maxDepth {
			return
		}
		entries, readErr := os.ReadDir(dir)
		if readErr != nil {
			return
		}
		for _, entry := range entries {
			if len(files) >= maxFiles {
				break
			}
			if _, ignored := ignoredTreeDirs[entry.Name()]; ignored {
				continue
			}
			abs := filepath.Join(dir, entry.Name())
			if entry.IsDir() {
				walk(abs, depth+1)
				continue
			}
			if entry.Type().IsRegular() {
				rel, relErr := filepath.Rel(ws.Root, abs)
				if relErr == nil {
					files = append(files, filepath.ToSlash(rel))
				}
			}
		}
	}
	walk(absolute, 0)
	return map[string]any{"workspaceId": workspaceID, "root": ws.Root, "relativePath": relativePath, "count": len(files), "files": files}, nil
}

func (n *nativeTools) readFile(workspaceID, relativePath string, maxChars int, startLine, endLine *int) (map[string]any, error) {
	_, _, absolute, err := n.resolveWorkspacePath(workspaceID, relativePath)
	if err != nil {
		return nil, err
	}
	buf, err := os.ReadFile(absolute)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(buf)
	text := strings.ReplaceAll(string(bytes.ToValidUTF8(buf, []byte("\uFFFD"))), "\r\n", "\n")
	selected := text
	var rangeValue any
	if startLine != nil || endLine != nil {
		lines := strings.Split(text, "\n")
		start := 1
		if startLine != nil && *startLine > start {
			start = *startLine
		}
		end := len(lines)
		if endLine != nil && *endLine < end {
			end = *endLine
		}
		var builder strings.Builder
		if start <= end && start <= len(lines) {
			for i := start; i <= end; i++ {
				if i > start {
					builder.WriteByte('\n')
				}
				fmt.Fprintf(&builder, "%d: %s", i, lines[i-1])
			}
		}
		selected = builder.String()
		rangeValue = map[string]any{"start": start, "end": end, "totalLines": len(lines)}
	}
	content, truncated, originalChars := truncateNativeText(selected, maxChars)
	return map[string]any{
		"path": relativePath, "sha256": hex.EncodeToString(sum[:]), "bytes": len(buf), "range": rangeValue,
		"content": content, "truncated": truncated, "originalChars": originalChars,
	}, nil
}

func truncateNativeText(text string, maxChars int) (string, bool, int) {
	if maxChars <= 0 {
		maxChars = 120000
	}
	units := utf16.Encode([]rune(text))
	original := len(units)
	if original <= maxChars {
		return text, false, original
	}
	head := int(float64(maxChars) * 0.65)
	tail := maxChars - head
	return string(utf16.Decode(units[:head])) + fmt.Sprintf("\n\n... [TRUNCATED %d CHARS] ...\n\n", original-maxChars) + string(utf16.Decode(units[original-tail:])), true, original
}

type rgJSONEvent struct {
	Type string `json:"type"`
	Data struct {
		Path struct {
			Text string `json:"text"`
		} `json:"path"`
		Lines struct {
			Text string `json:"text"`
		} `json:"lines"`
		LineNumber int `json:"line_number"`
		Submatches []struct {
			Start int `json:"start"`
			End   int `json:"end"`
			Match struct {
				Text string `json:"text"`
			} `json:"match"`
		} `json:"submatches"`
	} `json:"data"`
}

func (n *nativeTools) searchText(ctx context.Context, workspaceID, relativePath, query, glob string, maxResults int, regexMode, caseSensitive bool) (map[string]any, error) {
	if maxResults < 1 || maxResults > 1000 {
		return nil, nativeToolError{"maxResults must be between 1 and 1000"}
	}
	if n.rgPath == "" {
		return n.searchTextGoFallback(ctx, workspaceID, relativePath, query, glob, maxResults, regexMode, caseSensitive)
	}
	ws, _, searchRoot, err := n.resolveWorkspacePath(workspaceID, relativePath)
	if err != nil {
		return nil, err
	}
	args := []string{"--json", "--line-number", "--column", "--no-heading"}
	if !regexMode {
		args = append(args, "--fixed-strings")
	}
	if !caseSensitive {
		args = append(args, "-i")
	}
	if glob != "" {
		args = append(args, "-g", glob)
	}
	if !n.cfg.Security.AllowSecretFiles {
		for _, pattern := range n.cfg.Security.SecretFilePatterns {
			if strings.TrimSpace(pattern) != "" {
				args = append(args, "-g", "!"+pattern)
			}
		}
	}
	args = append(args, "--", query, ".")

	cmd := exec.CommandContext(ctx, n.rgPath, args...)
	cmd.Dir = searchRoot
	cmd.Env = os.Environ()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return n.searchTextGoFallback(ctx, workspaceID, relativePath, query, glob, maxResults, regexMode, caseSensitive)
	}

	var stderrBuf bytes.Buffer
	var stderrWG sync.WaitGroup
	stderrWG.Add(1)
	go func() {
		defer stderrWG.Done()
		_, _ = stderrBuf.ReadFrom(stderr)
	}()

	matches := make([]map[string]any, 0, min(maxResults, 64))
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 16<<20)
	stoppedAtLimit := false
	for scanner.Scan() {
		var event rgJSONEvent
		if json.Unmarshal(scanner.Bytes(), &event) != nil || event.Type != "match" {
			continue
		}
		matchAbs := filepath.Join(searchRoot, filepath.FromSlash(event.Data.Path.Text))
		rel, relErr := filepath.Rel(ws.Root, matchAbs)
		if relErr != nil {
			continue
		}
		submatches := make([]map[string]any, 0, len(event.Data.Submatches))
		for _, sub := range event.Data.Submatches {
			submatches = append(submatches, map[string]any{"start": sub.Start, "end": sub.End, "match": sub.Match.Text})
		}
		matches = append(matches, map[string]any{
			"path": filepath.ToSlash(rel), "line": event.Data.LineNumber,
			"text": strings.TrimSuffix(strings.TrimSuffix(event.Data.Lines.Text, "\n"), "\r"), "submatches": submatches,
		})
		if len(matches) >= maxResults {
			stoppedAtLimit = true
			_ = cmd.Process.Kill()
			break
		}
	}
	scanErr := scanner.Err()
	waitErr := cmd.Wait()
	stderrWG.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if scanErr != nil && !stoppedAtLimit {
		return nil, scanErr
	}
	if waitErr != nil && !stoppedAtLimit {
		var exitErr *exec.ExitError
		if !errors.As(waitErr, &exitErr) {
			return nil, waitErr
		}
	}
	return map[string]any{
		"engine": "ripgrep", "matches": matches, "stderr": stderrBuf.String(), "workspaceId": workspaceID, "path": relativePath,
	}, nil
}

func (n *nativeTools) runGit(ctx context.Context, workspaceID string, args ...string) (int, string, string, error) {
	ws, err := n.workspace(workspaceID)
	if err != nil {
		return 0, "", "", err
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = ws.Root
	cmd.Env = os.Environ()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err == nil {
		return 0, stdout.String(), stderr.String(), nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), stdout.String(), stderr.String(), nil
	}
	return 0, stdout.String(), stderr.String(), err
}

func normalizeNativeConfig(cfg *runtimeConfig) error {
	seen := make(map[string]bool, len(cfg.Workspaces))
	filtered := make([]workspaceConfig, 0, len(cfg.Workspaces))
	for _, ws := range cfg.Workspaces {
		if ws.Enabled != nil && !*ws.Enabled {
			continue
		}
		if ws.ID == "" || ws.Root == "" {
			return errors.New("each workspace needs id and root")
		}
		if seen[ws.ID] {
			return fmt.Errorf("duplicate workspace id: %s", ws.ID)
		}
		seen[ws.ID] = true
		root := expandNativeHome(ws.Root)
		abs, err := filepath.Abs(root)
		if err != nil {
			return err
		}
		ws.Root = abs
		filtered = append(filtered, ws)
	}
	if len(filtered) == 0 {
		return errors.New("at least one workspace is required")
	}
	cfg.Workspaces = filtered
	if cfg.Server.MaxToolOutputChars == 0 {
		cfg.Server.MaxToolOutputChars = 160000
	}
	if cfg.Security.SecretFilePatterns == nil {
		cfg.Security.SecretFilePatterns = []string{".env", ".env.*", "*.pem", "*.key", "id_rsa", "id_ed25519"}
	}
	return nil
}

func expandNativeHome(value string) string {
	if value == "~" || strings.HasPrefix(value, "~/") || strings.HasPrefix(value, "~\\") {
		home, err := os.UserHomeDir()
		if err == nil {
			if value == "~" {
				return home
			}
			return filepath.Join(home, value[2:])
		}
	}
	return value
}
