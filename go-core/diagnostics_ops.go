package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"
)

var toolchainCacheState struct {
	sync.Mutex
	value map[string]any
	until time.Time
}

type commandProbe struct {
	Name string
	Args []string
}

var toolchainProbes = []commandProbe{
	{"git", []string{"--version"}},
	{"node", []string{"--version"}}, {"npm", []string{"--version"}},
	{"rg", []string{"--version"}}, {"fdfind", []string{"--version"}}, {"jq", []string{"--version"}},
	{"ast-grep", []string{"--version"}}, {"sg", []string{"--version"}},
	{"typescript-language-server", []string{"--version"}}, {"pyright-langserver", []string{"--version"}},
	{"bash-language-server", []string{"--version"}}, {"clangd", []string{"--version"}},
	{"rust-analyzer", []string{"--version"}}, {"gopls", []string{"version"}},
	{"go", []string{"version"}}, {"cargo", []string{"--version"}}, {"rustc", []string{"--version"}},
	{"docker", []string{"--version"}}, {"sqlite3", []string{"--version"}},
	{"shellcheck", []string{"--version"}}, {"semgrep", []string{"--version"}},
	{"forge", []string{"--version"}}, {"cast", []string{"--version"}}, {"anvil", []string{"--version"}},
	{"solana", []string{"--version"}}, {"solana-test-validator", []string{"--version"}},
	{"anchor", []string{"--version"}}, {"stellar", []string{"--version"}}, {"uv", []string{"--version"}},
}

func probeFirstLine(ctx context.Context, command string, args ...string) string {
	probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	code, stdout, stderr, err := runCapture(probeCtx, command, args, "", nil)
	if err != nil || code != 0 {
		return ""
	}
	text := strings.TrimSpace(firstNonEmpty(stdout, stderr))
	if text == "" {
		return "ok"
	}
	if i := strings.IndexAny(text, "\r\n"); i >= 0 {
		text = text[:i]
	}
	return strings.TrimSpace(text)
}

func executablePathNative(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	if filepath.IsAbs(command) {
		if st, err := os.Stat(command); err == nil && !st.IsDir() {
			return command
		}
		return ""
	}
	path, err := exec.LookPath(command)
	if err != nil {
		return ""
	}
	return path
}

func workspacePerformanceWarningNative(root string) any {
	if runtime.GOOS != "linux" {
		return nil
	}
	lower := strings.ToLower(filepath.ToSlash(root))
	if strings.HasPrefix(lower, "/mnt/") && len(lower) > len("/mnt/x/") {
		return "Workspace is on a Windows-mounted filesystem (/mnt/*). Move active repositories under /home/<user>/... for faster file I/O, watchers, Git and container workloads."
	}
	return nil
}

func (n *nativeTools) toolchainStatusTool(ctx context.Context) (map[string]any, error) {
	toolchainCacheState.Lock()
	if toolchainCacheState.value != nil && time.Now().Before(toolchainCacheState.until) {
		cached := cloneMap(toolchainCacheState.value)
		toolchainCacheState.Unlock()
		return cached, nil
	}
	toolchainCacheState.Unlock()

	type probeResult struct{ name, value string }
	ch := make(chan probeResult, len(toolchainProbes))
	var wg sync.WaitGroup
	for _, item := range toolchainProbes {
		item := item
		wg.Add(1)
		go func() {
			defer wg.Done()
			if executablePathNative(item.Name) == "" {
				return
			}
			if value := probeFirstLine(ctx, item.Name, item.Args...); value != "" {
				ch <- probeResult{item.Name, value}
			}
		}()
	}
	wg.Wait()
	close(ch)

	tools := map[string]any{}
	for item := range ch {
		if item.name == "sg" {
			if _, exists := tools["ast-grep"]; exists {
				continue
			}
		}
		tools[item.name] = item.value
	}

	toolsVenv := filepath.Join(n.appRoot, ".venv-tools")
	py := filepath.Join(toolsVenv, "bin", "python")
	if runtime.GOOS == "windows" {
		py = filepath.Join(toolsVenv, "Scripts", "python.exe")
	}
	debugpy := false
	if executablePathNative(py) != "" {
		debugpy = probeFirstLine(ctx, py, "-c", "import debugpy; print(debugpy.__version__)") != ""
	}

	grouped := map[string]map[string]any{}
	for language, server := range n.cfg.LSPServers {
		if strings.TrimSpace(server.Command) == "" {
			continue
		}
		entry := grouped[server.Command]
		if entry == nil {
			entry = map[string]any{"command": server.Command, "languages": []string{}}
			grouped[server.Command] = entry
		}
		languages := entry["languages"].([]string)
		entry["languages"] = append(languages, language)
	}
	commands := make([]string, 0, len(grouped))
	for command := range grouped {
		commands = append(commands, command)
	}
	sort.Strings(commands)
	languageServers := make([]map[string]any, 0, len(commands))
	for _, command := range commands {
		entry := grouped[command]
		path := executablePathNative(command)
		entry["available"] = path != ""
		entry["path"] = nilIfEmpty(path)
		languageServers = append(languageServers, entry)
	}

	workspaces := make([]map[string]any, 0, len(n.cfg.Workspaces))
	for _, ws := range n.cfg.Workspaces {
		workspaces = append(workspaces, map[string]any{"id": ws.ID, "root": ws.Root, "performanceWarning": workspacePerformanceWarningNative(ws.Root)})
	}

	value := map[string]any{
		"platform":        runtime.GOOS,
		"wsl":             false,
		"toolsVenv":       toolsVenv,
		"debugpy":         debugpy,
		"tools":           tools,
		"languageServers": languageServers,
		"workspaces":      workspaces,
	}
	toolchainCacheState.Lock()
	toolchainCacheState.value = cloneMap(value)
	toolchainCacheState.until = time.Now().Add(15 * time.Second)
	toolchainCacheState.Unlock()
	return value, nil
}

type selfCheckItem map[string]any

func selfCheckResult(name, status string, details map[string]any) selfCheckItem {
	out := selfCheckItem{"name": name, "status": status}
	for key, value := range details {
		if key == "name" || key == "status" {
			continue
		}
		out[key] = value
	}
	return out
}

func mcpToolsListHealth(listStatus int, rawTools []any) (bool, bool) {
	names := map[string]bool{}
	for _, rawTool := range rawTools {
		if tool, ok := rawTool.(map[string]any); ok {
			names[fmt.Sprint(tool["name"])] = true
		}
	}
	requiredToolsPresent := names["gpt_agent_status"] && names["gpt_agent_self_check"] && names["gpt_agent_read_file"] && names["gpt_agent_git_diff"] && names["gpt_agent_run_command"] && names["gpt_agent_lsp_status"] && names["gpt_agent_learning_stats"] && names["gpt_agent_project_inspect"]
	return listStatus == 200 && len(rawTools) == 83 && requiredToolsPresent, requiredToolsPresent
}

func resolveManifestEntry(appRoot, name string) (string, error) {
	rootReal, err := filepath.EvalSymlinks(appRoot)
	if err != nil {
		return "", err
	}
	candidate, err := canonicalForContainment(filepath.Join(appRoot, filepath.FromSlash(name)))
	if err != nil {
		return "", err
	}
	if !pathWithin(rootReal, candidate) {
		return "", fmt.Errorf("manifest entry escapes application root: %s", name)
	}
	return candidate, nil
}

func (n *nativeTools) checkDistributionManifest() map[string]any {
	manifestPath := filepath.Join(n.appRoot, "MANIFEST.sha256.json")
	buf, err := os.ReadFile(manifestPath)
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error(), "mismatches": []string{}}
	}
	var manifest struct {
		Version string `json:"version"`
		Files   map[string]struct {
			SHA256 string `json:"sha256"`
			Bytes  int64  `json:"bytes"`
		} `json:"files"`
	}
	if err := json.Unmarshal(buf, &manifest); err != nil {
		return map[string]any{"ok": false, "error": err.Error(), "mismatches": []string{}}
	}
	mismatches := []string{}
	names := make([]string, 0, len(manifest.Files))
	for name := range manifest.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		meta := manifest.Files[name]
		entryPath, pathErr := resolveManifestEntry(n.appRoot, name)
		if pathErr != nil {
			mismatches = append(mismatches, name)
			if len(mismatches) >= 20 {
				break
			}
			continue
		}
		file, err := os.ReadFile(entryPath)
		if err != nil {
			mismatches = append(mismatches, name)
		} else {
			sum := sha256.Sum256(file)
			if int64(len(file)) != meta.Bytes || hex.EncodeToString(sum[:]) != meta.SHA256 {
				mismatches = append(mismatches, name)
			}
		}
		if len(mismatches) >= 20 {
			break
		}
	}
	return map[string]any{"ok": len(mismatches) == 0, "version": manifest.Version, "fileCount": len(manifest.Files), "mismatches": mismatches}
}

func mcpRequestNative(ctx context.Context, port int, method string, params any, id int) (int, map[string]any, error) {
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/mcp", port), strings.NewReader(string(body)))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	envelope, err := decodeMCPEnvelope(raw)
	return resp.StatusCode, envelope, err
}

func (n *nativeTools) existingSelfCheckWorkspace(preferred string) *workspaceConfig {
	ordered := make([]workspaceConfig, 0, len(n.cfg.Workspaces))
	for _, ws := range n.cfg.Workspaces {
		if preferred != "" && ws.ID == preferred {
			ordered = append(ordered, ws)
		}
	}
	for _, ws := range n.cfg.Workspaces {
		if ws.ID == "gpt-agent-src" && ws.ID != preferred {
			ordered = append(ordered, ws)
		}
	}
	for _, ws := range n.cfg.Workspaces {
		if ws.ID != preferred && ws.ID != "gpt-agent-src" {
			ordered = append(ordered, ws)
		}
	}
	for i := range ordered {
		ws := ordered[i]
		if isDriveWideRoot(ws.Root) {
			continue
		}
		if st, err := os.Stat(ws.Root); err == nil && st.IsDir() {
			return &ws
		}
	}
	return nil
}

func allowedFirstArg(rules []string, first string) bool {
	for _, rule := range rules {
		if rule == "*" || strings.EqualFold(rule, first) {
			return true
		}
	}
	return false
}

func (n *nativeTools) harmlessSafeProbe(ctx context.Context, ws workspaceConfig) (map[string]any, error) {
	candidates := []struct {
		exe  string
		args []string
	}{
		{"go", []string{"version"}},
		{"git", []string{"--version"}},
		{"python", []string{"--version"}},
		{"python3", []string{"--version"}},
	}
	for _, item := range candidates {
		rules, ok := n.cfg.SafeCommands[executableName(item.exe)]
		if !ok || len(item.args) == 0 || !allowedFirstArg(rules, strings.ToLower(item.args[0])) {
			continue
		}
		if executablePathNative(item.exe) == "" {
			continue
		}
		return n.processes.runSafe(ctx, ws.ID, ".", item.exe, item.args, nil, false, 15000)
	}
	return nil, fmt.Errorf("no harmless configured SAFE command is available for probe")
}

func (n *nativeTools) selfCheckTool(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var args struct {
		Deep        bool   `json:"deep"`
		WorkspaceID string `json:"workspaceId"`
	}
	if err := decodeNativeArgs(raw, &args); err != nil {
		return nil, err
	}
	checks := []selfCheckItem{}
	push := func(name, status string, details map[string]any) {
		checks = append(checks, selfCheckResult(name, status, details))
	}

	detectedWSL := runtime.GOOS == "linux" && (os.Getenv("WSL_DISTRO_NAME") != "" || os.Getenv("WSL_INTEROP") != "")
	push("platform-detection", "pass", map[string]any{"platform": runtime.GOOS, "detectedWsl": detectedWSL})

	if runtime.GOOS == "windows" {
		available := false
		if st, err := os.Stat(windowsSandboxExe()); err == nil && !st.IsDir() {
			available = true
		}
		status := "fail"
		if available {
			status = "pass"
		}
		push("untrusted-backend", status, map[string]any{"backend": "windows-sandbox", "available": available, "kernelIsolated": available, "networkIsolated": available})
	}

	healthStatus := 0
	var healthBody map[string]any
	healthCtx, healthCancel := context.WithTimeout(ctx, 5*time.Second)
	req, _ := http.NewRequestWithContext(healthCtx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/healthz", n.cfg.Server.Port), nil)
	if resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req); err == nil {
		healthStatus = resp.StatusCode
		_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&healthBody)
		_ = resp.Body.Close()
	}
	healthCancel()
	runtimeOK := healthStatus == http.StatusOK && healthBody != nil && healthBody["ok"] == true && fmt.Sprint(healthBody["version"]) == runtimeVersion
	status := "fail"
	if runtimeOK {
		status = "pass"
	}
	push("runtime-health", status, map[string]any{"httpStatus": healthStatus, "body": healthBody})

	catalog, catalogErr := loadToolCatalog()
	missingNative := []string{}
	expectedNative := 0
	if catalogErr == nil {
		for _, name := range catalog.names() {
			if strings.HasPrefix(name, "gpt_agent_job_") {
				continue
			}
			expectedNative++
			if !n.supports(name) {
				missingNative = append(missingNative, name)
			}
		}
	}
	goCoreOK := catalogErr == nil && expectedNative == len(n.names()) && len(missingNative) == 0
	goCoreStatus := "fail"
	if goCoreOK {
		goCoreStatus = "pass"
	}
	push("go-core", goCoreStatus, map[string]any{"engine": "go", "nativeToolCount": len(n.names()), "expectedNativeToolCount": expectedNative, "missingNativeTools": missingNative, "nodeCompatibilityRequired": false})

	evolutionStats := map[string]any{"enabled": false, "automaticCapture": false}
	if n.evolution != nil {
		evolutionStats = n.evolution.stats()
	}
	evolutionOK := evolutionStats["enabled"] == true && evolutionStats["automaticCapture"] == true
	evolutionStatus := "fail"
	if evolutionOK {
		evolutionStatus = "pass"
	}
	push("learning-evolution", evolutionStatus, evolutionStats)

	probeCtx, cancelProbe := context.WithTimeout(ctx, 6*time.Second)
	initializeStatus, initialize, initializeErr := mcpRequestNative(probeCtx, n.cfg.Server.Port, "initialize", map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "gpt-agent-self-check", "version": runtimeVersion}}, 9101)
	if initializeErr != nil {
		push("mcp-initialize", "fail", map[string]any{"error": initializeErr.Error()})
	} else {
		result, _ := initialize["result"].(map[string]any)
		serverInfo, _ := result["serverInfo"].(map[string]any)
		ok := initializeStatus == 200 && fmt.Sprint(serverInfo["name"]) == "GPT Agent"
		s := "fail"
		if ok {
			s = "pass"
		}
		push("mcp-initialize", s, map[string]any{"httpStatus": initializeStatus, "protocolVersion": result["protocolVersion"], "serverInfo": serverInfo})
	}
	listStatus, listEnvelope, listErr := mcpRequestNative(probeCtx, n.cfg.Server.Port, "tools/list", map[string]any{}, 9102)
	cancelProbe()
	if listErr != nil {
		push("mcp-tools-list", "fail", map[string]any{"error": listErr.Error()})
	} else {
		result, _ := listEnvelope["result"].(map[string]any)
		rawTools, _ := result["tools"].([]any)
		ok, requiredToolsPresent := mcpToolsListHealth(listStatus, rawTools)
		s := "fail"
		if ok {
			s = "pass"
		}
		push("mcp-tools-list", s, map[string]any{"httpStatus": listStatus, "toolCount": len(rawTools), "requiredToolsPresent": requiredToolsPresent})
	}

	readyStatus := 0
	tunnelURL := firstNonEmpty(os.Getenv("GPT_AGENT_TUNNEL_READY_URL"), "http://127.0.0.1:8080/readyz")
	tunnelURLAllowed := isLoopbackHTTPURL(tunnelURL)
	if tunnelURLAllowed {
		tunnelCtx, tunnelCancel := context.WithTimeout(ctx, 5*time.Second)
		if req, err := http.NewRequestWithContext(tunnelCtx, http.MethodGet, tunnelURL, nil); err == nil {
			if resp, err := loopbackOnlyHTTPClient(5 * time.Second).Do(req); err == nil {
				readyStatus = resp.StatusCode
				_ = resp.Body.Close()
			}
		}
		tunnelCancel()
	}
	if runtime.GOOS == "windows" {
		taskCtx, taskCancel := context.WithTimeout(ctx, 5*time.Second)
		code, _, _, _ := runCapture(taskCtx, "schtasks.exe", []string{"/Query", "/TN", "GPT Agent Tunnel", "/FO", "LIST"}, "", nil)
		taskCancel()
		if !tunnelURLAllowed {
			push("tunnel", "fail", map[string]any{"reason": "Tunnel ready URL must use an HTTP(S) loopback host."})
		} else if code != 0 && readyStatus == 0 {
			push("tunnel", "skip", map[string]any{"reason": "GPT Agent Tunnel scheduled task is not installed"})
		} else {
			s := "fail"
			if readyStatus == 200 {
				s = "pass"
			}
			push("tunnel", s, map[string]any{"scheduledTask": code == 0, "readyHttpStatus": readyStatus})
		}
	}

	if audit, err := n.auditVerifyTool(); err != nil {
		push("audit-chain", "fail", map[string]any{"error": err.Error()})
	} else {
		s := "fail"
		if ok, _ := audit["ok"].(bool); ok {
			s = "pass"
		}
		push("audit-chain", s, audit)
	}

	manifest := n.checkDistributionManifest()
	manifestOK, _ := manifest["ok"].(bool)
	manifestStatus := "fail"
	if manifestOK && fmt.Sprint(manifest["version"]) == runtimeVersion {
		manifestStatus = "pass"
	}
	push("distribution-manifest", manifestStatus, manifest)

	buildInfo, buildOK := debug.ReadBuildInfo()
	depDetails := map[string]any{"available": buildOK, "module": "gpt-agent/core-runtime", "dependencyCount": 0}
	depStatus := "fail"
	if buildOK {
		depDetails["module"] = buildInfo.Main.Path
		depDetails["dependencyCount"] = len(buildInfo.Deps)
		depDetails["goVersion"] = buildInfo.GoVersion
		depStatus = "pass"
	}
	push("dependency-tree", depStatus, depDetails)
	if args.Deep {
		if executablePathNative("govulncheck") == "" {
			push("dependency-audit", "skip", map[string]any{"reason": "govulncheck is not installed"})
		} else {
			auditCtx, auditCancel := context.WithTimeout(ctx, 120*time.Second)
			code, stdout, stderr, err := runCapture(auditCtx, "govulncheck", []string{"./..."}, filepath.Join(n.appRoot, "go-core"), nil)
			auditCancel()
			s := "fail"
			if err == nil && code == 0 {
				s = "pass"
			}
			output, _, _ := truncateNativeText(firstNonEmpty(stdout, stderr), 4000)
			details := map[string]any{"exitCode": code, "output": output}
			if err != nil {
				details["error"] = err.Error()
			}
			push("dependency-audit", s, details)
		}
	} else {
		push("dependency-audit", "skip", map[string]any{"reason": "Run with deep=true for the optional govulncheck advisory scan."})
	}

	toolchain, _ := n.toolchainStatusTool(ctx)
	servers, _ := toolchain["languageServers"].([]map[string]any)
	allServers := true
	for _, server := range servers {
		if available, _ := server["available"].(bool); !available {
			allServers = false
			break
		}
	}
	lspStatus := "fail"
	if allServers {
		lspStatus = "pass"
	}
	push("configured-language-servers", lspStatus, map[string]any{"servers": servers})

	if ws := n.existingSelfCheckWorkspace(args.WorkspaceID); ws == nil {
		push("safe-sandbox", "skip", map[string]any{"reason": "No eligible existing workspace was available for a harmless SAFE probe."})
	} else if probe, err := n.harmlessSafeProbe(ctx, *ws); err != nil {
		push("safe-sandbox", "skip", map[string]any{"workspaceId": ws.ID, "reason": err.Error()})
	} else {
		exitCode := intFromAny(probe["exitCode"])
		s := "fail"
		if exitCode == 0 {
			s = "pass"
		}
		if runtime.GOOS == "windows" && exitCode == 0 && probe["networkIsolated"] != true {
			s = "warn"
		}
		push("safe-sandbox", s, map[string]any{"workspaceId": ws.ID, "sandboxed": probe["sandboxed"], "networkRequested": probe["network"], "networkIsolated": probe["networkIsolated"], "exitCode": exitCode})
	}

	var source *workspaceConfig
	for i := range n.cfg.Workspaces {
		if n.cfg.Workspaces[i].ID == "gpt-agent-src" {
			source = &n.cfg.Workspaces[i]
			break
		}
	}
	if source == nil {
		push("source-git", "skip", map[string]any{"reason": "No gpt-agent-src workspace is configured."})
	} else {
		gitCtx, gitCancel := context.WithTimeout(ctx, 10*time.Second)
		code, stdout, stderr, err := runCapture(gitCtx, "git", []string{"status", "--short", "--branch"}, source.Root, nil)
		gitCancel()
		if err != nil || code != 0 {
			details := map[string]any{"exitCode": code, "stderr": strings.TrimSpace(stderr)}
			if err != nil {
				details["error"] = err.Error()
			}
			push("source-git", "fail", details)
		} else {
			lines := []string{}
			for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
				if strings.TrimSpace(line) != "" {
					lines = append(lines, line)
				}
			}
			s := "pass"
			if len(lines) > 1 {
				s = "warn"
			}
			push("source-git", s, map[string]any{"workspaceId": source.ID, "gitStatus": strings.TrimSpace(stdout)})
		}
	}

	counts := map[string]int{}
	for _, check := range checks {
		counts[fmt.Sprint(check["status"])]++
	}
	return map[string]any{
		"ok":          counts["fail"] == 0,
		"version":     runtimeVersion,
		"generatedAt": time.Now().UTC().Format(time.RFC3339Nano),
		"deep":        args.Deep,
		"counts":      counts,
		"checks":      checks,
	}, nil
}
