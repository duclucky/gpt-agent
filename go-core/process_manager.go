package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

type managedProcess struct {
	Cmd      *exec.Cmd
	Meta     map[string]any
	MetaPath string
}
type psWorker struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout <-chan string
	stderr <-chan string
}
type processManager struct {
	n         *nativeTools
	mu        sync.Mutex
	live      map[string]*managedProcess
	shellMu   sync.Mutex
	shell     *psWorker
	sandboxMu sync.Mutex
}

var processIDRE = regexp.MustCompile(`^proc_[0-9a-f]{16}$`)

func newProcessManager(n *nativeTools) *processManager {
	return &processManager{n: n, live: map[string]*managedProcess{}}
}

func validateProcessID(id string) error {
	if !processIDRE.MatchString(id) {
		return fmt.Errorf("Invalid process id: %s", id)
	}
	return nil
}

func executableName(exe string) string {
	base := strings.ToLower(filepath.Base(exe))
	for _, ext := range []string{".exe", ".cmd", ".bat"} {
		base = strings.TrimSuffix(base, ext)
	}
	return base
}
func (p *processManager) checkSafe(exe string, args []string) error {
	name := executableName(exe)
	rules, ok := p.n.cfg.SafeCommands[name]
	if !ok {
		return fmt.Errorf("Executable '%s' is not permitted in SAFE mode.", name)
	}
	for _, r := range rules {
		if r == "*" {
			return nil
		}
	}
	first := ""
	if len(args) > 0 {
		first = strings.ToLower(args[0])
	}
	for _, r := range rules {
		if strings.ToLower(r) == first {
			return nil
		}
	}
	return fmt.Errorf("SAFE mode blocks '%s %s'. Allowed first arguments: %s", name, first, strings.Join(rules, ", "))
}
func isDriveWideRoot(path string) bool {
	if runtime.GOOS == "windows" {
		vol := filepath.VolumeName(path)
		if vol != "" {
			return strings.EqualFold(filepath.Clean(path), filepath.Clean(vol+`\`))
		}
	}
	return regexpDriveRoot(path)
}
func regexpDriveRoot(path string) bool {
	v := filepath.ToSlash(filepath.Clean(path))
	return len(v) == 6 && strings.HasPrefix(v, "/mnt/")
}
func (p *processManager) resolveCwd(workspaceID, cwd string) (string, error) {
	if cwd == "" {
		cwd = "."
	}
	ws, _, abs, err := p.n.resolveWorkspacePath(workspaceID, cwd)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(abs)
	if err != nil || !st.IsDir() {
		return "", fmt.Errorf("Command cwd must be an existing directory inside workspace '%s': %s", workspaceID, cwd)
	}
	rootReal, _ := filepath.EvalSymlinks(ws.Root)
	cwdReal, _ := filepath.EvalSymlinks(abs)
	require := true
	if p.n.cfg.Security.RequireProjectCwdForDriveWorkspace != nil {
		require = *p.n.cfg.Security.RequireProjectCwdForDriveWorkspace
	}
	if require && isDriveWideRoot(rootReal) && strings.EqualFold(filepath.Clean(rootReal), filepath.Clean(cwdReal)) {
		return "", errors.New("SAFE execution on a drive-wide workspace requires an explicit project cwd below the drive root.")
	}
	return cwdReal, nil
}
func safeEnvironment(extra map[string]string) ([]string, error) {
	keys := []string{"PATH", "LANG", "LC_ALL", "LC_CTYPE", "TZ", "USER", "LOGNAME", "TERM", "NO_COLOR", "CI"}
	if runtime.GOOS == "windows" {
		keys = append(keys, "SystemRoot", "WINDIR", "COMSPEC", "PATHEXT")
	}
	m := map[string]string{}
	for _, k := range keys {
		if v, ok := os.LookupEnv(k); ok {
			m[k] = v
		}
	}
	for k, v := range extra {
		m[k] = v
	}
	if runtime.GOOS == "windows" {
		root := filepath.Join(os.TempDir(), "gpt-agent-safe")
		home := filepath.Join(root, "home")
		cache := filepath.Join(root, "cache")
		localAppData := filepath.Join(root, "local-app-data")
		roamingAppData := filepath.Join(root, "roaming-app-data")
		goCache := filepath.Join(cache, "go-build")
		for _, dir := range []string{root, home, cache, localAppData, roamingAppData, goCache} {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return nil, fmt.Errorf("create SAFE environment directory %s: %w", dir, err)
			}
		}
		m["HOME"] = home
		m["USERPROFILE"] = home
		m["LOCALAPPDATA"] = localAppData
		m["APPDATA"] = roamingAppData
		m["XDG_CACHE_HOME"] = cache
		m["GOCACHE"] = goCache
		m["TMPDIR"] = root
		m["TEMP"] = root
		m["TMP"] = root
	} else {
		root := filepath.Join(os.TempDir(), "gpt-agent-safe")
		home := filepath.Join(root, "home")
		cache := filepath.Join(root, "cache")
		goCache := filepath.Join(cache, "go-build")
		for _, dir := range []string{root, home, cache, goCache} {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return nil, fmt.Errorf("create SAFE environment directory %s: %w", dir, err)
			}
		}
		m["HOME"] = home
		m["XDG_CACHE_HOME"] = cache
		m["GOCACHE"] = goCache
		m["TMPDIR"] = root
	}
	out := make([]string, 0, len(m))
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	for _, k := range ks {
		out = append(out, k+"="+m[k])
	}
	return out, nil
}
func envMap(list []string) map[string]string {
	m := map[string]string{}
	for _, v := range list {
		if i := strings.IndexByte(v, '='); i >= 0 {
			m[v[:i]] = v[i+1:]
		}
	}
	return m
}
func resolveWindowsCommand(exe string, args []string) (string, []string, error) {
	if runtime.GOOS != "windows" {
		return exe, args, nil
	}
	if executableName(exe) != "npm" {
		return exe, args, nil
	}
	node, err := exec.LookPath("node.exe")
	if err != nil {
		return "", nil, errors.New("npm SAFE execution requires the external Node/npm toolchain, but node.exe was not found")
	}
	npmCLI := filepath.Join(filepath.Dir(node), "node_modules", "npm", "bin", "npm-cli.js")
	if _, err := os.Stat(npmCLI); err != nil {
		return "", nil, fmt.Errorf("npm-cli.js not found beside external node toolchain: %s", npmCLI)
	}
	return node, append([]string{npmCLI}, args...), nil
}

func collectCommand(ctx context.Context, exe string, args []string, cwd string, env []string, timeoutMS, max int) (map[string]any, error) {
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
	defer cancel()
	cmd := exec.Command(exe, args...)
	cmd.Dir = cwd
	cmd.Env = env
	var out, se bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &se
	started := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var waitErr error
	timed := false
	select {
	case waitErr = <-done:
	case <-runCtx.Done():
		timed = true
		killProcessTree(cmd.Process.Pid)
		select {
		case waitErr = <-done:
		case <-time.After(5 * time.Second):
			waitErr = runCtx.Err()
		}
	}
	if timed {
		return nil, fmt.Errorf("Command timed out after %d ms", timeoutMS)
	}
	exitCode := 0
	if waitErr != nil {
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) {
			exitCode = ee.ExitCode()
		} else {
			return nil, waitErr
		}
	}
	stdout, sto, _ := truncateNativeText(out.String(), max)
	stderr, ste, _ := truncateNativeText(se.String(), max)
	return map[string]any{"exitCode": exitCode, "stdout": stdout, "stderr": stderr, "stdoutTruncated": sto, "stderrTruncated": ste, "durationMs": time.Since(started).Milliseconds()}, nil
}
func (p *processManager) runSafe(ctx context.Context, workspaceID, cwd, executable string, args []string, extra map[string]string, network bool, timeoutMS int) (map[string]any, error) {
	if err := p.checkSafe(executable, args); err != nil {
		return nil, err
	}
	cwdReal, err := p.resolveCwd(workspaceID, cwd)
	if err != nil {
		return nil, err
	}
	if timeoutMS == 0 {
		timeoutMS = defaultCommandTimeoutMS
	}
	exe, argv, err := resolveWindowsCommand(executable, args)
	if err != nil {
		return nil, err
	}
	env, err := safeEnvironment(extra)
	if err != nil {
		return nil, err
	}
	result, err := collectCommand(ctx, exe, argv, cwdReal, env, timeoutMS, p.n.cfg.Server.MaxToolOutputChars)
	if err != nil {
		return nil, err
	}
	rec := map[string]any{"mode": "SAFE", "workspaceId": workspaceID, "cwd": firstNonEmpty(cwd, "."), "cwdAbsolute": cwdReal, "executable": executable, "args": args, "exitCode": result["exitCode"], "sandboxed": false, "network": network, "networkIsolated": false, "maskedSecretFiles": 0, "durationMs": result["durationMs"], "stdout": result["stdout"], "stderr": result["stderr"], "stdoutTruncated": result["stdoutTruncated"], "stderrTruncated": result["stderrTruncated"]}
	_, _ = p.n.audit.append(mergeMap(map[string]any{"type": "process.run"}, rec))
	return rec, nil
}

func (p *processManager) startSafe(workspaceID, cwd, executable string, args []string, extra map[string]string, network bool) (map[string]any, error) {
	if err := p.checkSafe(executable, args); err != nil {
		return nil, err
	}
	cwdReal, err := p.resolveCwd(workspaceID, cwd)
	if err != nil {
		return nil, err
	}
	exe, argv, err := resolveWindowsCommand(executable, args)
	if err != nil {
		return nil, err
	}
	env, err := safeEnvironment(extra)
	if err != nil {
		return nil, err
	}
	id := "proc_" + randomHex(8)
	dir := filepath.Join(p.n.dataRoot, "processes")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	logPath := filepath.Join(dir, id+".log")
	metaPath := filepath.Join(dir, id+".json")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(exe, argv...)
	cmd.Dir = cwdReal
	cmd.Env = env
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		logFile.Close()
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		logFile.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return nil, err
	}
	meta := map[string]any{"id": id, "pid": cmd.Process.Pid, "workspaceId": workspaceID, "cwd": firstNonEmpty(cwd, "."), "cwdAbsolute": cwdReal, "executable": executable, "args": args, "sandboxed": false, "network": network, "networkIsolated": false, "maskedSecretFiles": 0, "startedAt": nowISO(), "status": "running", "exitCode": nil, "endedAt": nil, "logPath": logPath}
	if err := writeJSONAtomic(metaPath, meta); err != nil {
		killProcessTree(cmd.Process.Pid)
		logFile.Close()
		return nil, err
	}
	mp := &managedProcess{Cmd: cmd, Meta: meta, MetaPath: metaPath}
	p.mu.Lock()
	p.live[id] = mp
	p.mu.Unlock()
	var writeMu sync.Mutex
	copyStream := func(label string, r io.Reader) {
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 64*1024), 2<<20)
		for scanner.Scan() {
			writeMu.Lock()
			fmt.Fprintf(logFile, "[%s] [%s] %s\n", nowISO(), label, scanner.Text())
			_ = logFile.Sync()
			writeMu.Unlock()
		}
	}
	go copyStream("stdout", stdout)
	go copyStream("stderr", stderr)
	go func() {
		err := cmd.Wait()
		code := 0
		if err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				code = ee.ExitCode()
			} else {
				code = 1
			}
		}
		writeMu.Lock()
		_ = logFile.Close()
		writeMu.Unlock()
		p.mu.Lock()
		delete(p.live, id)
		p.mu.Unlock()
		meta["status"] = "exited"
		meta["exitCode"] = code
		meta["endedAt"] = nowISO()
		_ = writeJSONAtomic(metaPath, meta)
	}()
	_, _ = p.n.audit.append(map[string]any{"type": "process.start", "processId": id, "workspaceId": workspaceID, "cwd": cwd, "executable": executable, "args": args, "pid": cmd.Process.Pid, "sandboxed": false, "network": network})
	return cloneMap(meta), nil
}
func (p *processManager) status(id string) (map[string]any, error) {
	if err := validateProcessID(id); err != nil {
		return nil, err
	}
	p.mu.Lock()
	if cur := p.live[id]; cur != nil {
		v := cloneMap(cur.Meta)
		p.mu.Unlock()
		return v, nil
	}
	p.mu.Unlock()
	var meta map[string]any
	if err := readJSONFile(filepath.Join(p.n.dataRoot, "processes", id+".json"), &meta, `{}`); err != nil || len(meta) == 0 {
		return nil, fmt.Errorf("Unknown process id: %s", id)
	}
	return meta, nil
}
func (p *processManager) logs(id string, tail int) (map[string]any, error) {
	meta, err := p.status(id)
	if err != nil {
		return nil, err
	}
	if tail == 0 {
		tail = 30000
	}
	if tail < 1000 {
		tail = 1000
	}
	if tail > 160000 {
		tail = 160000
	}
	logPath := filepath.Join(p.n.dataRoot, "processes", id+".log")
	buf, _ := os.ReadFile(logPath)
	text := string(buf)
	units := []rune(text)
	if len(units) > tail {
		text = string(units[len(units)-tail:])
	}
	out := cloneMap(meta)
	out["log"] = text
	return out, nil
}
func (p *processManager) list() (map[string]any, error) {
	dir := filepath.Join(p.n.dataRoot, "processes")
	_ = os.MkdirAll(dir, 0755)
	ents, _ := os.ReadDir(dir)
	items := []map[string]any{}
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var m map[string]any
		if readJSONFile(filepath.Join(dir, e.Name()), &m, `{}`) == nil && len(m) > 0 {
			items = append(items, m)
		}
	}
	sort.Slice(items, func(i, j int) bool { return fmt.Sprint(items[i]["startedAt"]) > fmt.Sprint(items[j]["startedAt"]) })
	if len(items) > 200 {
		items = items[:200]
	}
	return map[string]any{"processes": items}, nil
}
func (p *processManager) stop(id string) (map[string]any, error) {
	meta, err := p.status(id)
	if err != nil {
		return nil, err
	}
	pid := intFromAny(meta["pid"])
	killProcessTree(pid)
	_, _ = p.n.audit.append(map[string]any{"type": "process.stop", "processId": id, "pid": pid})
	return map[string]any{"stopped": true, "id": id, "pid": pid}, nil
}

const psWorkerScript = `$ErrorActionPreference = 'Continue'
[Console]::InputEncoding = [System.Text.UTF8Encoding]::new($false)
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$childShell = (Get-Process -Id $PID).Path
while (($line = [Console]::In.ReadLine()) -ne $null) {
  if ([string]::IsNullOrWhiteSpace($line)) { continue }
  $id='invalid'; $code=1
  try {
    $req=$line|ConvertFrom-Json; $id=[string]$req.id; $cwd=[string]$req.cwd; $command=[string]$req.command
    $postlude=@'

$__gpt_agent_command_ok=$?
if ($LASTEXITCODE -is [int] -and $LASTEXITCODE -ne 0) { exit [int]$LASTEXITCODE }
if (-not $__gpt_agent_command_ok) { exit 1 }
'@
    $psi=[System.Diagnostics.ProcessStartInfo]::new(); $psi.FileName=$childShell; $psi.Arguments='-NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -Command -'; $psi.WorkingDirectory=$cwd; $psi.UseShellExecute=$false; $psi.CreateNoWindow=$true; $psi.RedirectStandardInput=$true; $psi.RedirectStandardOutput=$true; $psi.RedirectStandardError=$true
    $child=[System.Diagnostics.Process]::new(); $child.StartInfo=$psi
    try {
      if (-not $child.Start()) { throw 'Failed to start isolated PowerShell command process' }
      $stdoutTask=$child.StandardOutput.ReadToEndAsync(); $stderrTask=$child.StandardError.ReadToEndAsync()
      $child.StandardInput.Write($command); $child.StandardInput.Write($postlude); $child.StandardInput.Close()
      $child.WaitForExit(); $stdout=$stdoutTask.GetAwaiter().GetResult(); $stderr=$stderrTask.GetAwaiter().GetResult(); $code=$child.ExitCode
      if ($stdout.Length -gt 0) {[Console]::Out.Write($stdout); if (-not $stdout.EndsWith([string][char]10)) {[Console]::Out.WriteLine()}}
      if ($stderr.Length -gt 0) {[Console]::Error.Write($stderr); if (-not $stderr.EndsWith([string][char]10)) {[Console]::Error.WriteLine()}}
    } finally { if ($null -ne $child) {$child.Dispose()} }
  } catch {[Console]::Error.WriteLine($_.ToString());$code=1}
  [Console]::Out.WriteLine(('__GPT_AGENT_PS_END_{0}__:{1}' -f $id,$code)); [Console]::Error.WriteLine(('__GPT_AGENT_PS_ERR_END_{0}__' -f $id))
}`

func streamLines(r io.Reader) <-chan string {
	ch := make(chan string, 128)
	go func() {
		defer close(ch)
		s := bufio.NewScanner(r)
		s.Buffer(make([]byte, 64*1024), 4<<20)
		for s.Scan() {
			ch <- s.Text()
		}
	}()
	return ch
}
func startShellWorkerProcess(exe, cwd string) (*psWorker, error) {
	cmd := exec.Command(exe, "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", psWorkerScript)
	cmd.Dir = cwd
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	se, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = out.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = out.Close()
		_ = se.Close()
		return nil, err
	}
	return &psWorker{cmd: cmd, stdin: stdin, stdout: streamLines(out), stderr: streamLines(se)}, nil
}

func (p *processManager) startShellWorker(cwd string) (*psWorker, error) {
	candidates := []string{}
	if path, err := exec.LookPath("pwsh.exe"); err == nil {
		candidates = append(candidates, path)
	}
	if path, err := exec.LookPath("powershell.exe"); err == nil {
		duplicate := false
		for _, candidate := range candidates {
			if strings.EqualFold(candidate, path) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			candidates = append(candidates, path)
		}
	}
	if len(candidates) == 0 {
		return nil, errors.New("PowerShell executable not found")
	}
	startErrors := make([]string, 0, len(candidates))
	for _, exe := range candidates {
		worker, err := startShellWorkerProcess(exe, cwd)
		if err == nil {
			return worker, nil
		}
		startErrors = append(startErrors, fmt.Sprintf("%s: %v", filepath.Base(exe), err))
	}
	return nil, fmt.Errorf("failed to start PowerShell worker: %s", strings.Join(startErrors, "; "))
}
func (p *processManager) resetShell() {
	if p.shell != nil {
		killProcessTree(p.shell.cmd.Process.Pid)
		_ = p.shell.stdin.Close()
		p.shell = nil
	}
}

func (p *processManager) runPOSIXFullShell(ctx context.Context, workspaceID, cwd, cwdReal, command string, timeoutMS int, grant map[string]any) (map[string]any, error) {
	candidates := []string{"zsh", "bash", "sh"}
	var shell string
	for _, candidate := range candidates {
		if path, err := exec.LookPath(candidate); err == nil {
			shell = path
			break
		}
	}
	if shell == "" {
		return nil, errors.New("No POSIX shell found; expected zsh, bash, or sh")
	}
	started := time.Now()
	result, err := collectCommand(ctx, shell, []string{"-lc", command}, cwdReal, os.Environ(), timeoutMS, p.n.cfg.Server.MaxToolOutputChars)
	if err != nil {
		return nil, err
	}
	rec := map[string]any{
		"mode": "FULL_SHELL", "workspaceId": workspaceID, "cwd": firstNonEmpty(cwd, "."), "cwdAbsolute": cwdReal,
		"shell": filepath.Base(shell), "command": command, "exitCode": result["exitCode"], "durationMs": time.Since(started).Milliseconds(),
		"stdout": result["stdout"], "stderr": result["stderr"], "grantExpiresAt": grant["expiresAt"],
	}
	_, _ = p.n.audit.append(mergeMap(map[string]any{"type": "process.fullShell"}, rec))
	return rec, nil
}

func (p *processManager) runFullShell(ctx context.Context, workspaceID, cwd, command string, timeoutMS int) (map[string]any, error) {
	grant := p.n.readFullShellGrant()
	active, _ := grant["active"].(bool)
	if !active {
		hint := "scripts/windows/Enable-FullShell.ps1"
		if runtime.GOOS == "darwin" {
			hint = "scripts/macos/enable-full-shell.sh"
		}
		return nil, fmt.Errorf("FULL SHELL is locked. Run %s locally.", hint)
	}
	cwdReal, err := p.resolveCwd(workspaceID, cwd)
	if err != nil {
		return nil, err
	}
	if timeoutMS == 0 {
		timeoutMS = defaultCommandTimeoutMS
	}
	if runtime.GOOS != "windows" {
		return p.runPOSIXFullShell(ctx, workspaceID, cwd, cwdReal, command, timeoutMS, grant)
	}
	p.shellMu.Lock()
	defer p.shellMu.Unlock()
	if p.shell == nil || p.shell.cmd.ProcessState != nil {
		p.shell, err = p.startShellWorker(cwdReal)
		if err != nil {
			return nil, err
		}
	}
	id := "ps_" + randomHex(8)
	req, _ := json.Marshal(map[string]any{"id": id, "cwd": cwdReal, "command": command})
	if _, err := p.shell.stdin.Write(append(req, '\n')); err != nil {
		p.resetShell()
		return nil, err
	}
	timer := time.NewTimer(time.Duration(timeoutMS) * time.Millisecond)
	defer timer.Stop()
	outLines, seLines := []string{}, []string{}
	outDone, seDone := false, false
	exitCode := 1
	started := time.Now()
	for !(outDone && seDone) {
		select {
		case line, ok := <-p.shell.stdout:
			if !ok {
				p.resetShell()
				return nil, errors.New("PowerShell worker exited")
			}
			marker := "__GPT_AGENT_PS_END_" + id + "__:"
			if strings.HasPrefix(line, marker) {
				exitCode, _ = strconv.Atoi(strings.TrimPrefix(line, marker))
				outDone = true
			} else {
				outLines = append(outLines, line)
			}
		case line, ok := <-p.shell.stderr:
			if !ok {
				p.resetShell()
				return nil, errors.New("PowerShell worker exited")
			}
			if line == "__GPT_AGENT_PS_ERR_END_"+id+"__" {
				seDone = true
			} else {
				seLines = append(seLines, line)
			}
		case <-timer.C:
			p.resetShell()
			return nil, fmt.Errorf("Command timed out after %d ms", timeoutMS)
		case <-ctx.Done():
			p.resetShell()
			return nil, ctx.Err()
		}
	}
	stdout, _, _ := truncateNativeText(strings.Join(outLines, "\n"), p.n.cfg.Server.MaxToolOutputChars)
	stderr, _, _ := truncateNativeText(strings.Join(seLines, "\n"), p.n.cfg.Server.MaxToolOutputChars)
	rec := map[string]any{"mode": "FULL_SHELL", "workspaceId": workspaceID, "cwd": firstNonEmpty(cwd, "."), "cwdAbsolute": cwdReal, "shell": filepath.Base(p.shell.cmd.Path), "command": command, "exitCode": exitCode, "durationMs": time.Since(started).Milliseconds(), "stdout": stdout, "stderr": stderr, "grantExpiresAt": grant["expiresAt"]}
	_, _ = p.n.audit.append(mergeMap(map[string]any{"type": "process.fullShell"}, rec))
	return rec, nil
}
func (p *processManager) close() {
	p.shellMu.Lock()
	p.resetShell()
	p.shellMu.Unlock()
	p.mu.Lock()
	for _, m := range p.live {
		killProcessTree(m.Cmd.Process.Pid)
	}
	p.mu.Unlock()
}

func findSecretLikeFiles(root string, patterns []string, maxFiles int) ([]string, error) {
	found := []string{}
	type node struct{ abs, rel string }
	q := []node{{root, ""}}
	dirs := 0
	skip := map[string]bool{".git": true, "node_modules": true, ".venv": true, "venv": true, "dist": true, "build": true, ".next": true, "target": true, "coverage": true}
	for len(q) > 0 {
		x := q[0]
		q = q[1:]
		dirs++
		if dirs > 5000 {
			return nil, errors.New("SAFE secret masking scan exceeded 5000 directories; use a narrower project cwd.")
		}
		ents, e := os.ReadDir(x.abs)
		if e != nil {
			continue
		}
		for _, ent := range ents {
			rel := ent.Name()
			if x.rel != "" {
				rel = filepath.ToSlash(filepath.Join(x.rel, ent.Name()))
			}
			abs := filepath.Join(x.abs, ent.Name())
			if ent.IsDir() {
				if !skip[ent.Name()] {
					q = append(q, node{abs, rel})
				}
				continue
			}
			if !ent.Type().IsRegular() {
				continue
			}
			for _, pat := range patterns {
				a, _ := wildcardMatch(pat, ent.Name())
				b, _ := wildcardMatch(pat, rel)
				if a || b {
					found = append(found, rel)
					if len(found) > maxFiles {
						return nil, fmt.Errorf("SAFE secret masking found more than %d secret-like files; use a narrower cwd or FULL SHELL only when explicitly required.", maxFiles)
					}
					break
				}
			}
		}
	}
	return found, nil
}
func copyDisposableWorkspace(src, dst string, secrets []string) error {
	secret := map[string]bool{}
	for _, s := range secrets {
		secret[filepath.ToSlash(s)] = true
	}
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, e := filepath.Rel(src, path)
		if e != nil {
			return e
		}
		if rel == "." {
			return os.MkdirAll(dst, 0755)
		}
		relSlash := filepath.ToSlash(rel)
		if relSlash == ".git" || strings.HasPrefix(relSlash, ".git/") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if secret[relSlash] {
			return nil
		}
		info, e := d.Info()
		if e != nil {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			real, e := filepath.EvalSymlinks(path)
			if e != nil {
				return nil
			}
			if !pathWithin(src, real) {
				return nil
			}
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		in, e := os.Open(path)
		if e != nil {
			return e
		}
		defer in.Close()
		out, e := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
		if e != nil {
			return e
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}

func (p *processManager) runUntrusted(ctx context.Context, workspaceID, cwd, executable string, args []string, extra map[string]string, timeoutMS int) (map[string]any, error) {
	if runtime.GOOS != "windows" {
		return nil, fmt.Errorf("UNTRUSTED is unavailable on %s: no verified local isolation backend is configured", runtime.GOOS)
	}
	if err := p.checkSafe(executable, args); err != nil {
		return nil, err
	}
	cwdReal, err := p.resolveCwd(workspaceID, cwd)
	if err != nil {
		return nil, err
	}
	secrets, err := findSecretLikeFiles(cwdReal, p.n.cfg.Security.SecretFilePatterns, 200)
	if err != nil {
		return nil, err
	}
	scratch, err := os.MkdirTemp("", "gpt-agent-untrusted-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(scratch)
	disposable := filepath.Join(scratch, "workspace")
	if err := copyDisposableWorkspace(cwdReal, disposable, secrets); err != nil {
		return nil, err
	}
	started := time.Now()
	p.sandboxMu.Lock()
	result, err := runWindowsSandbox(ctx, p.n, disposable, cwdReal, scratch, executable, args, extra, timeoutMS)
	p.sandboxMu.Unlock()
	if err != nil {
		return nil, err
	}
	rec := map[string]any{"mode": "UNTRUSTED", "workspaceId": workspaceID, "cwd": firstNonEmpty(cwd, "."), "sourceRoot": cwdReal, "executable": executable, "args": args, "exitCode": result["exitCode"], "timedOut": result["timedOut"], "disposable": true, "sourceWritable": false, "sandboxed": true, "backend": "windows-sandbox", "kernelIsolated": true, "network": false, "networkIsolated": true, "sanitizedSecretFiles": len(secrets), "durationMs": time.Since(started).Milliseconds(), "stdout": result["stdout"], "stderr": result["stderr"], "stdoutTruncated": result["stdoutTruncated"], "stderrTruncated": result["stderrTruncated"]}
	_, _ = p.n.audit.append(mergeMap(map[string]any{"type": "process.untrusted"}, rec))
	return rec, nil
}
func mergeMap(a, b map[string]any) map[string]any {
	out := cloneMap(a)
	for k, v := range b {
		out[k] = v
	}
	return out
}
func intFromAny(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case float64:
		return int(x)
	case json.Number:
		i, _ := x.Int64()
		return int(i)
	case string:
		i, _ := strconv.Atoi(x)
		return i
	}
	return 0
}
