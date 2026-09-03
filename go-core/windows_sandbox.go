package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const sandboxWorkspace = `C:\GPT AgentWorkspace`
const sandboxControl = `C:\GPT AgentControl`
const sandboxResult = `C:\GPT AgentResult`
const sandboxToolRoot = `C:\GPT AgentTools`

type sandboxMapping struct {
	HostRoot, SandboxRoot string
	ReadOnly              bool
}
type sandboxCommand struct {
	Executable string
	Support    []string
}

func windowsSandboxExe() string {
	root := firstNonEmpty(os.Getenv("SystemRoot"), os.Getenv("WINDIR"), `C:\Windows`)
	return filepath.Join(root, "System32", "WindowsSandbox.exe")
}
func resolveWinExecutable(name string) (string, error) {
	if filepath.IsAbs(name) {
		return filepath.EvalSymlinks(name)
	}
	names := []string{name}
	if strings.EqualFold(name, "python3") {
		names = []string{"python3.exe", "python.exe"}
	}
	for _, candidate := range names {
		if path, err := exec.LookPath(candidate); err == nil {
			return filepath.EvalSymlinks(path)
		}
	}
	return "", fmt.Errorf("Windows Sandbox could not resolve executable '%s'.", name)
}
func rootForExecutable(abs string) string {
	norm := filepath.Clean(abs)
	lower := strings.ToLower(norm)
	sep := string(filepath.Separator)

	markers := []string{
		sep + ".venv-tools",
		sep + "go-sdk" + sep + "go",
		sep + "tooling-node",
	}
	for _, marker := range markers {
		if i := strings.Index(lower, strings.ToLower(marker)); i >= 0 {
			return norm[:i+len(marker)]
		}
	}

	rootAfterChild := func(marker string) string {
		i := strings.Index(lower, strings.ToLower(marker))
		if i < 0 {
			return ""
		}
		start := i + len(marker)
		tail := norm[start:]
		if j := strings.Index(tail, sep); j >= 0 {
			return norm[:start+j]
		}
		return norm
	}

	if root := rootAfterChild(sep + ".rustup" + sep + "toolchains" + sep); root != "" {
		return root
	}
	if i := strings.Index(lower, strings.ToLower(sep+"program files"+sep+"git"+sep)); i >= 0 {
		return norm[:i+len(sep+"program files"+sep+"git")]
	}
	if root := rootAfterChild(sep + "clangd" + sep); root != "" {
		return root
	}
	if root := rootAfterChild(sep + "microsoft" + sep + "winget" + sep + "packages" + sep); root != "" {
		return root
	}
	if i := strings.Index(lower, strings.ToLower(sep+"program files"+sep+"dotnet"+sep)); i >= 0 {
		return norm[:i+len(sep+"program files"+sep+"dotnet")]
	}
	return filepath.Dir(norm)
}

func withinFold(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	return err == nil && (rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)))
}
func compactSandboxRoots(roots []string) []string {
	unique := map[string]string{}
	for _, r := range roots {
		a, _ := filepath.Abs(r)
		unique[strings.ToLower(a)] = a
	}
	list := make([]string, 0, len(unique))
	for _, r := range unique {
		list = append(list, r)
	}
	sort.Slice(list, func(i, j int) bool { return len(list[i]) < len(list[j]) })
	out := []string{}
	for _, r := range list {
		covered := false
		for _, p := range out {
			if withinFold(p, r) {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, r)
		}
	}
	return out
}
func buildSandboxTools(n *nativeTools) ([]sandboxMapping, map[string]sandboxCommand, error) {
	names := make([]string, 0, len(n.cfg.SafeCommands))
	for name := range n.cfg.SafeCommands {
		names = append(names, name)
	}
	sort.Strings(names)
	roots := []string{}
	hostCommands := map[string]struct {
		exe     string
		support []string
	}{}
	systemRoot := strings.ToLower(firstNonEmpty(os.Getenv("SystemRoot"), os.Getenv("WINDIR"), `C:\Windows`))
	for _, name := range names {
		if name == "npm" {
			node, err := resolveWinExecutable("node.exe")
			if err != nil {
				continue
			}
			npm := filepath.Join(filepath.Dir(node), "node_modules", "npm", "bin", "npm-cli.js")
			if _, err := os.Stat(npm); err != nil {
				continue
			}
			hostCommands[name] = struct {
				exe     string
				support []string
			}{node, []string{npm}}
			for _, p := range []string{node, npm} {
				if !strings.HasPrefix(strings.ToLower(p), systemRoot+`\`) {
					roots = append(roots, rootForExecutable(p))
				}
			}
			continue
		}
		exe, err := resolveWinExecutable(name)
		if err != nil {
			continue
		}
		hostCommands[name] = struct {
			exe     string
			support []string
		}{exe, nil}
		if !strings.HasPrefix(strings.ToLower(exe), systemRoot+`\`) {
			roots = append(roots, rootForExecutable(exe))
		}
	}
	home, _ := os.UserHomeDir()
	rustup := filepath.Join(home, ".rustup")
	for name := range hostCommands {
		if name == "cargo" || name == "rustc" || name == "rust-analyzer" {
			if _, err := os.Stat(rustup); err == nil {
				roots = append(roots, rustup)
			}
			break
		}
	}
	roots = compactSandboxRoots(roots)
	mappings := make([]sandboxMapping, len(roots))
	for i, r := range roots {
		mappings[i] = sandboxMapping{r, filepath.Join(sandboxToolRoot, fmt.Sprintf("t%d", i)), true}
	}
	translate := func(v string) string { return translateSandboxValue(v, mappings, "") }
	commands := map[string]sandboxCommand{}
	for name, item := range hostCommands {
		support := make([]string, len(item.support))
		for i, p := range item.support {
			support[i] = translate(p)
		}
		commands[name] = sandboxCommand{translate(item.exe), support}
	}
	return mappings, commands, nil
}
func translateSandboxValue(value string, mappings []sandboxMapping, sourceRoot string) string {
	out := value
	repls := append([]sandboxMapping(nil), mappings...)
	if sourceRoot != "" {
		repls = append(repls, sandboxMapping{sourceRoot, sandboxWorkspace, false})
	}
	sort.Slice(repls, func(i, j int) bool { return len(repls[i].HostRoot) > len(repls[j].HostRoot) })
	for _, m := range repls {
		out = replaceFold(out, m.HostRoot, m.SandboxRoot)
	}
	return out
}
func replaceFold(s, old, new string) string {
	lower, needle := strings.ToLower(s), strings.ToLower(old)
	for {
		idx := strings.Index(lower, needle)
		if idx < 0 {
			return s
		}
		s = s[:idx] + new + s[idx+len(old):]
		lower = strings.ToLower(s)
	}
}
func buildWSB(control, workspace, result string, mappings []sandboxMapping, memory int) string {
	if memory < 1024 {
		memory = 1024
	}
	if memory > 8192 {
		memory = 8192
	}
	all := []sandboxMapping{{control, sandboxControl, true}, {workspace, sandboxWorkspace, false}, {result, sandboxResult, false}}
	all = append(all, mappings...)
	var folders strings.Builder
	for _, m := range all {
		fmt.Fprintf(&folders, "    <MappedFolder>\n      <HostFolder>%s</HostFolder>\n      <SandboxFolder>%s</SandboxFolder>\n      <ReadOnly>%t</ReadOnly>\n    </MappedFolder>\n", html.EscapeString(m.HostRoot), html.EscapeString(m.SandboxRoot), m.ReadOnly)
	}
	return fmt.Sprintf(`<Configuration>
  <VGpu>Disable</VGpu>
  <Networking>Disable</Networking>
  <AudioInput>Disable</AudioInput>
  <VideoInput>Disable</VideoInput>
  <PrinterRedirection>Disable</PrinterRedirection>
  <ClipboardRedirection>Disable</ClipboardRedirection>
  <MemoryInMB>%d</MemoryInMB>
  <MappedFolders>
%s  </MappedFolders>
  <LogonCommand><Command>powershell.exe -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File %s\runner.ps1</Command></LogonCommand>
</Configuration>
`, memory, folders.String(), sandboxControl)
}

const sandboxRunner = `$ErrorActionPreference = "Stop"
$RequestPath = "C:\GPT AgentControl\request.json"
$ResultPath = "C:\GPT AgentResult\result.json"
function Quote-WindowsArg([string]$Value) { if ($null -eq $Value -or $Value.Length -eq 0) { return '""' }; if ($Value -notmatch '[\s"]') { return $Value }; $sb=New-Object System.Text.StringBuilder;[void]$sb.Append('"');$slashes=0;foreach($ch in $Value.ToCharArray()){if($ch -eq '\'){$slashes++;continue};if($ch -eq '"'){[void]$sb.Append(('\' * ($slashes*2+1)));[void]$sb.Append('"');$slashes=0;continue};if($slashes -gt 0){[void]$sb.Append(('\'*$slashes));$slashes=0};[void]$sb.Append($ch)};if($slashes -gt 0){[void]$sb.Append(('\'*($slashes*2)))};[void]$sb.Append('"');return $sb.ToString() }
function Truncate-Text([string]$Text,[int]$MaxChars){if($null -eq $Text){return ""};if($Text.Length -le $MaxChars){return $Text};$head=[Math]::Floor($MaxChars*.65);$tail=$MaxChars-$head;return $Text.Substring(0,$head)+[Environment]::NewLine+[Environment]::NewLine+"... [TRUNCATED "+($Text.Length-$MaxChars)+" CHARS] ..."+[Environment]::NewLine+[Environment]::NewLine+$Text.Substring($Text.Length-$tail)}
$request=Get-Content -Raw -LiteralPath $RequestPath|ConvertFrom-Json;$psi=New-Object System.Diagnostics.ProcessStartInfo;$psi.FileName=[string]$request.executable;$psi.Arguments=(@($request.args)|%{Quote-WindowsArg ([string]$_)}) -join ' ';$psi.WorkingDirectory="C:\GPT AgentWorkspace";$psi.UseShellExecute=$false;$psi.CreateNoWindow=$true;$psi.RedirectStandardOutput=$true;$psi.RedirectStandardError=$true;$psi.EnvironmentVariables.Clear();foreach($property in $request.env.PSObject.Properties){$psi.EnvironmentVariables[[string]$property.Name]=[string]$property.Value};$started=[DateTimeOffset]::UtcNow;$timedOut=$false;$exitCode=$null;$stdout="";$stderr="";try{$process=New-Object System.Diagnostics.Process;$process.StartInfo=$psi;if(-not $process.Start()){throw "Sandbox child process failed to start."};$stdoutTask=$process.StandardOutput.ReadToEndAsync();$stderrTask=$process.StandardError.ReadToEndAsync();if(-not $process.WaitForExit([int]$request.timeoutMs)){$timedOut=$true;try{& taskkill.exe /PID $process.Id /T /F|Out-Null}catch{};try{$process.WaitForExit(5000)|Out-Null}catch{}}else{$exitCode=$process.ExitCode};$stdout=$stdoutTask.GetAwaiter().GetResult();$stderr=$stderrTask.GetAwaiter().GetResult()}catch{$stderr=$_.Exception.Message};$result=[ordered]@{ok=(-not $timedOut -and $null -ne $exitCode);exitCode=$exitCode;timedOut=$timedOut;stdout=Truncate-Text $stdout ([int]$request.maxChars);stderr=Truncate-Text $stderr ([int]$request.maxChars);startedAt=$started.ToString("o");endedAt=[DateTimeOffset]::UtcNow.ToString("o")};$json=$result|ConvertTo-Json -Compress -Depth 5;$bytes=(New-Object System.Text.UTF8Encoding($false)).GetBytes($json);$stream=[IO.File]::Open($ResultPath,[IO.FileMode]::Create,[IO.FileAccess]::Write,[IO.FileShare]::Read);try{$stream.Write($bytes,0,$bytes.Length);$stream.Flush();try{Start-Process shutdown.exe -ArgumentList '/s','/t','1','/f' -WindowStyle Hidden|Out-Null}catch{};Start-Sleep -Seconds 20}finally{$stream.Dispose()}`

func runWindowsSandbox(ctx context.Context, n *nativeTools, workspaceRoot, sourceRoot, scratch, executable string, args []string, extra map[string]string, timeoutMS int) (map[string]any, error) {
	if runtime.GOOS != "windows" {
		return nil, errors.New("Windows Sandbox backend requires win32.")
	}
	sandboxExe := windowsSandboxExe()
	if _, err := os.Stat(sandboxExe); err != nil {
		return nil, errors.New("Windows Sandbox is not installed. Enable the Containers-DisposableClientVM Windows feature and reboot.")
	}
	control := filepath.Join(scratch, "control")
	resultDir := filepath.Join(scratch, "result")
	_ = os.MkdirAll(control, 0755)
	_ = os.MkdirAll(resultDir, 0755)
	mappings, commands, err := buildSandboxTools(n)
	if err != nil {
		return nil, err
	}
	name := strings.ToLower(executableName(executable))
	requestedArgs := append([]string(nil), args...)
	var requestedExe string
	if name == "npm" {
		cmd, ok := commands["npm"]
		if !ok || len(cmd.Support) == 0 {
			return nil, errors.New("Windows Sandbox could not resolve the external npm-cli.js")
		}
		requestedExe = cmd.Executable
		requestedArgs = append([]string{cmd.Support[0]}, requestedArgs...)
	} else if cmd, ok := commands[name]; ok {
		requestedExe = cmd.Executable
	} else {
		host, err := resolveWinExecutable(executable)
		if err != nil {
			return nil, err
		}
		root := rootForExecutable(host)
		mapping := sandboxMapping{root, filepath.Join(sandboxToolRoot, fmt.Sprintf("t%d", len(mappings))), true}
		mappings = append(mappings, mapping)
		requestedExe = translateSandboxValue(host, mappings, "")
	}
	for i, v := range requestedArgs {
		requestedArgs[i] = translateSandboxValue(v, mappings, sourceRoot)
	}
	systemRoot := firstNonEmpty(os.Getenv("SystemRoot"), os.Getenv("WINDIR"), `C:\Windows`)
	pathDirs := []string{}
	for _, cmd := range commands {
		pathDirs = append(pathDirs, filepath.Dir(cmd.Executable))
	}
	pathDirs = uniqueLimited(pathDirs, 500)
	pathDirs = append(pathDirs, filepath.Join(systemRoot, "System32"), systemRoot, filepath.Join(systemRoot, "System32", "WindowsPowerShell", "v1.0"))
	safeEnv := map[string]string{"SystemRoot": systemRoot, "WINDIR": systemRoot, "COMSPEC": filepath.Join(systemRoot, "System32", "cmd.exe"), "PATHEXT": ".COM;.EXE;.BAT;.CMD", "PATH": strings.Join(pathDirs, ";"), "HOME": sandboxWorkspace + `\.home`, "USERPROFILE": sandboxWorkspace + `\.home`, "TEMP": sandboxWorkspace + `\.tmp`, "TMP": sandboxWorkspace + `\.tmp`, "TMPDIR": sandboxWorkspace + `\.tmp`, "XDG_CACHE_HOME": sandboxWorkspace + `\.cache`, "npm_config_cache": sandboxWorkspace + `\.npm-cache`, "PIP_CACHE_DIR": sandboxWorkspace + `\.pip-cache`, "GOCACHE": sandboxWorkspace + `\.go-cache`, "GOPATH": sandboxWorkspace + `\.go`, "CARGO_HOME": sandboxWorkspace + `\.cargo`}
	home, _ := os.UserHomeDir()
	rustupHost := filepath.Join(home, ".rustup")
	for _, m := range mappings {
		if strings.EqualFold(filepath.Clean(m.HostRoot), filepath.Clean(rustupHost)) {
			safeEnv["RUSTUP_HOME"] = m.SandboxRoot
		}
	}
	for k, v := range extra {
		safeEnv[k] = translateSandboxValue(v, mappings, sourceRoot)
	}
	if timeoutMS == 0 {
		timeoutMS = defaultCommandTimeoutMS
	}
	request := map[string]any{"executable": requestedExe, "args": requestedArgs, "env": safeEnv, "timeoutMs": min(max(timeoutMS, 1000), 30*60*1000), "maxChars": min(max(n.cfg.Server.MaxToolOutputChars, 1000), 300000)}
	buf, _ := json.MarshalIndent(request, "", "  ")
	if err := os.WriteFile(filepath.Join(control, "request.json"), buf, 0644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(control, "runner.ps1"), []byte(sandboxRunner), 0644); err != nil {
		return nil, err
	}
	memory := n.cfg.Security.WindowsSandboxMemoryMB
	if memory == 0 {
		memory = 2048
	}
	configPath := filepath.Join(scratch, "gpt-agent-untrusted-"+randomHex(8)+".wsb")
	if err := os.WriteFile(configPath, []byte(buildWSB(control, workspaceRoot, resultDir, mappings, memory)), 0644); err != nil {
		return nil, err
	}
	cmd := exec.Command(sandboxExe, configPath)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	defer killProcessTree(cmd.Process.Pid)
	resultPath := filepath.Join(resultDir, "result.json")
	deadline := time.Now().Add(time.Duration(max(timeoutMS+90000, 120000)) * time.Millisecond)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(resultPath); err == nil && len(strings.TrimSpace(string(b))) > 0 {
			var result map[string]any
			if err := json.Unmarshal(b, &result); err != nil {
				return nil, err
			}
			stdout, sto, _ := truncateNativeText(fmt.Sprint(result["stdout"]), n.cfg.Server.MaxToolOutputChars)
			stderr, ste, _ := truncateNativeText(fmt.Sprint(result["stderr"]), n.cfg.Server.MaxToolOutputChars)
			return map[string]any{"exitCode": result["exitCode"], "timedOut": result["timedOut"], "stdout": stdout, "stderr": stderr, "stdoutTruncated": sto, "stderrTruncated": ste}, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return nil, fmt.Errorf("Windows Sandbox did not produce a result within %d ms", max(timeoutMS+90000, 120000))
}
