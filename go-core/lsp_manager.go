package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type rpcOutcome struct {
	result any
	err    error
}
type jsonRPCProcess struct {
	command       string
	args          []string
	cwd           string
	cmd           *exec.Cmd
	stdin         io.WriteCloser
	writeMu       sync.Mutex
	mu            sync.Mutex
	pending       map[string]chan rpcOutcome
	notifications []map[string]any
	nextID        atomic.Int64
	closed        bool
}

func newJSONRPCProcess(command string, args []string, cwd string) *jsonRPCProcess {
	return &jsonRPCProcess{command: command, args: args, cwd: cwd, pending: map[string]chan rpcOutcome{}}
}
func (p *jsonRPCProcess) start() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd != nil && !p.closed {
		return nil
	}
	cmd := exec.Command(p.command, p.args...)
	cmd.Dir = p.cwd
	cmd.Env = os.Environ()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	p.cmd = cmd
	p.stdin = stdin
	p.closed = false
	go p.readLoop(stdout)
	go p.stderrLoop(stderr)
	go func() {
		err := cmd.Wait()
		if err == nil {
			err = fmt.Errorf("LSP exited with code 0")
		} else {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				err = fmt.Errorf("LSP exited with code %d", ee.ExitCode())
			}
		}
		p.failAll(err)
	}()
	return nil
}
func (p *jsonRPCProcess) readLoop(r io.Reader) {
	br := bufio.NewReaderSize(r, 64*1024)
	for {
		length := 0
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				p.failAll(err)
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				break
			}
			if strings.HasPrefix(strings.ToLower(line), "content-length:") {
				length, _ = strconv.Atoi(strings.TrimSpace(strings.SplitN(line, ":", 2)[1]))
			}
		}
		if length <= 0 {
			p.failAll(errors.New("LSP framing missing Content-Length."))
			return
		}
		body := make([]byte, length)
		if _, err := io.ReadFull(br, body); err != nil {
			p.failAll(err)
			return
		}
		var msg map[string]any
		if json.Unmarshal(body, &msg) != nil {
			continue
		}
		p.handleMessage(msg)
	}
}
func (p *jsonRPCProcess) stderrLoop(r io.Reader) {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 64*1024), 2<<20)
	for s.Scan() {
		p.addNotification(map[string]any{"method": "window/logMessage", "params": map[string]any{"type": 1, "message": s.Text() + "\n"}})
	}
}
func idKey(v any) string {
	switch x := v.(type) {
	case float64:
		return strconv.FormatInt(int64(x), 10)
	case json.Number:
		return x.String()
	default:
		return fmt.Sprint(v)
	}
}
func (p *jsonRPCProcess) handleMessage(msg map[string]any) {
	if id, has := msg["id"]; has && (msg["result"] != nil || msg["error"] != nil || hasMapKey(msg, "result") || hasMapKey(msg, "error")) {
		key := idKey(id)
		p.mu.Lock()
		ch := p.pending[key]
		delete(p.pending, key)
		p.mu.Unlock()
		if ch != nil {
			if er, ok := msg["error"].(map[string]any); ok && er != nil {
				ch <- rpcOutcome{err: fmt.Errorf("LSP error %v: %v", er["code"], er["message"])}
			} else {
				ch <- rpcOutcome{result: msg["result"]}
			}
			close(ch)
		}
		return
	}
	if _, has := msg["id"]; has {
		if method, _ := msg["method"].(string); method != "" {
			go p.replyServerRequest(msg)
		}
		return
	}
	if _, ok := msg["method"].(string); ok {
		p.addNotification(msg)
	}
}
func hasMapKey(m map[string]any, k string) bool { _, ok := m[k]; return ok }
func (p *jsonRPCProcess) replyServerRequest(msg map[string]any) {
	method, _ := msg["method"].(string)
	var result any = nil
	switch method {
	case "workspace/configuration":
		if params, ok := msg["params"].(map[string]any); ok {
			if items, ok := params["items"].([]any); ok {
				result = make([]any, len(items))
			}
		}
	case "workspace/workspaceFolders":
		result = []any{map[string]any{"uri": fileURI(p.cwd), "name": filepath.Base(p.cwd)}}
	case "client/registerCapability", "client/unregisterCapability":
		result = nil
	}
	_ = p.send(map[string]any{"jsonrpc": "2.0", "id": msg["id"], "result": result})
}
func (p *jsonRPCProcess) addNotification(msg map[string]any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.notifications = append(p.notifications, msg)
	if len(p.notifications) > 1000 {
		p.notifications = p.notifications[len(p.notifications)-1000:]
	}
}
func (p *jsonRPCProcess) send(obj any) error {
	if err := p.start(); err != nil {
		return err
	}
	payload, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	if _, err := fmt.Fprintf(p.stdin, "Content-Length: %d\r\n\r\n", len(payload)); err != nil {
		return err
	}
	_, err = p.stdin.Write(payload)
	return err
}
func (p *jsonRPCProcess) request(ctx context.Context, method string, params any, timeout time.Duration) (any, error) {
	id := p.nextID.Add(1)
	key := strconv.FormatInt(id, 10)
	ch := make(chan rpcOutcome, 1)
	p.mu.Lock()
	p.pending[key] = ch
	p.mu.Unlock()
	if err := p.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		p.mu.Lock()
		delete(p.pending, key)
		p.mu.Unlock()
		return nil, err
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case out := <-ch:
		return out.result, out.err
	case <-timer.C:
		p.mu.Lock()
		delete(p.pending, key)
		p.mu.Unlock()
		return nil, fmt.Errorf("LSP request timed out: %s", method)
	case <-ctx.Done():
		p.mu.Lock()
		delete(p.pending, key)
		p.mu.Unlock()
		return nil, ctx.Err()
	}
}
func (p *jsonRPCProcess) notify(method string, params any) error {
	return p.send(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}
func (p *jsonRPCProcess) takeNotifications(method string) []map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	hits := []map[string]any{}
	keep := p.notifications[:0]
	for _, x := range p.notifications {
		if x["method"] == method {
			hits = append(hits, x)
		} else {
			keep = append(keep, x)
		}
	}
	p.notifications = keep
	return hits
}
func (p *jsonRPCProcess) failAll(err error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	pending := p.pending
	p.pending = map[string]chan rpcOutcome{}
	p.mu.Unlock()
	for _, ch := range pending {
		ch <- rpcOutcome{err: err}
		close(ch)
	}
}
func (p *jsonRPCProcess) stop() {
	if p == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _ = p.request(ctx, "shutdown", map[string]any{}, 3*time.Second)
	_ = p.notify("exit", map[string]any{})
	p.mu.Lock()
	cmd := p.cmd
	p.closed = true
	p.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		killProcessTree(cmd.Process.Pid)
	}
}

type lspDocument struct {
	Version    int
	Hash       string
	LanguageID string
}
type lspSession struct {
	workspaceRoot         string
	config                lspServerConfig
	initializationOptions map[string]any
	rpc                   *jsonRPCProcess
	initialized           bool
	initMu                sync.Mutex
	opened                map[string]lspDocument
	openedMu              sync.Mutex
	sessionID             string
}

func (s *lspSession) init(ctx context.Context) error {
	s.initMu.Lock()
	defer s.initMu.Unlock()
	if s.initialized {
		return nil
	}
	if err := s.rpc.start(); err != nil {
		return err
	}
	root := fileURI(s.workspaceRoot)
	params := map[string]any{
		"processId": os.Getpid(),
		"clientInfo": map[string]any{
			"name":    "GPT Agent",
			"version": runtimeVersion,
		},
		"rootUri": root,
		"workspaceFolders": []any{
			map[string]any{"uri": root, "name": filepath.Base(s.workspaceRoot)},
		},
		"initializationOptions": s.initializationOptions,
		"capabilities": map[string]any{
			"workspace": map[string]any{
				"workspaceFolders": true,
				"symbol":           map[string]any{"dynamicRegistration": false},
			},
			"textDocument": map[string]any{
				"synchronization": map[string]any{
					"dynamicRegistration": false,
					"didSave":             false,
					"willSave":            false,
					"willSaveWaitUntil":   false,
				},
				"definition": map[string]any{"dynamicRegistration": false, "linkSupport": true},
				"references": map[string]any{"dynamicRegistration": false},
				"hover": map[string]any{
					"dynamicRegistration": false,
					"contentFormat":       []string{"markdown", "plaintext"},
				},
				"documentSymbol": map[string]any{
					"dynamicRegistration":               false,
					"hierarchicalDocumentSymbolSupport": true,
				},
				"publishDiagnostics": map[string]any{"relatedInformation": true, "versionSupport": true},
				"rename":             map[string]any{"dynamicRegistration": false, "prepareSupport": false},
			},
		},
	}
	if _, err := s.rpc.request(ctx, "initialize", params, 20*time.Second); err != nil {
		return err
	}
	if err := s.rpc.notify("initialized", map[string]any{}); err != nil {
		return err
	}
	s.initialized = true
	return nil
}
func (s *lspSession) sync(ctx context.Context, abs, language string) (string, error) {
	if err := s.init(ctx); err != nil {
		return "", err
	}
	buf, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(buf)
	hash := hex.EncodeToString(sum[:])
	uri := fileURI(abs)
	s.openedMu.Lock()
	prev, ok := s.opened[uri]
	if !ok {
		s.opened[uri] = lspDocument{1, hash, language}
		s.openedMu.Unlock()
		err = s.rpc.notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": uri, "languageId": language, "version": 1, "text": string(buf)}})
		return uri, err
	}
	if prev.Hash != hash {
		prev.Version++
		prev.Hash = hash
		prev.LanguageID = language
		s.opened[uri] = prev
		s.openedMu.Unlock()
		err = s.rpc.notify("textDocument/didChange", map[string]any{"textDocument": map[string]any{"uri": uri, "version": prev.Version}, "contentChanges": []any{map[string]any{"text": string(buf)}}})
		return uri, err
	}
	s.openedMu.Unlock()
	return uri, nil
}

type lspManager struct {
	n        *nativeTools
	mu       sync.Mutex
	sessions map[string]*lspSession
}

func newLSPManager(n *nativeTools) *lspManager {
	return &lspManager{n: n, sessions: map[string]*lspSession{}}
}
func fileURI(abs string) string {
	p := filepath.ToSlash(abs)
	if runtime.GOOS == "windows" && len(p) >= 2 && p[1] == ':' {
		p = "/" + p
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}
func pathFromFileURI(uri string) string {
	u, err := url.Parse(uri)
	if err != nil {
		return uri
	}
	p := u.Path
	if runtime.GOOS == "windows" && len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	return filepath.FromSlash(p)
}
func (m *lspManager) serverFor(rel string) (string, lspServerConfig, error) {
	ext := strings.ToLower(filepath.Ext(rel))
	for language, cfg := range m.n.cfg.LSPServers {
		for _, x := range cfg.Extensions {
			if strings.ToLower(x) == ext {
				return language, cfg, nil
			}
		}
	}
	return "", lspServerConfig{}, fmt.Errorf("No LSP configured for extension '%s'.", ext)
}
func lspMarkers(id string) []string {
	switch id {
	case "typescript":
		return []string{"tsconfig.json", "jsconfig.json", "package.json", ".git"}
	case "pyright":
		return []string{"pyproject.toml", "setup.cfg", "setup.py", "requirements.txt", ".git"}
	case "gopls":
		return []string{"go.work", "go.mod", ".git"}
	case "rust-analyzer":
		return []string{"Cargo.toml", ".git"}
	case "clangd":
		return []string{"compile_commands.json", "CMakeLists.txt", ".git"}
	default:
		return []string{".git"}
	}
}
func (m *lspManager) rootFor(ws workspaceConfig, candidate string, cfg lspServerConfig) string {
	root, _ := filepath.EvalSymlinks(ws.Root)
	current := candidate
	if st, e := os.Stat(current); e != nil || !st.IsDir() {
		current = filepath.Dir(current)
	}
	current, _ = filepath.Abs(current)
	if !pathWithin(root, current) {
		current = root
	}
	id := cfg.ServerID
	if id == "" {
		id = cfg.Command
	}
	for pathWithin(root, current) {
		for _, marker := range lspMarkers(id) {
			if _, err := os.Stat(filepath.Join(current, marker)); err == nil {
				return current
			}
		}
		if strings.EqualFold(current, root) {
			break
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return root
}
func (m *lspManager) get(workspaceID, rel string) (*lspSession, lspServerConfig, workspaceConfig, error) {
	language, cfg, err := m.serverFor(rel)
	if err != nil {
		return nil, cfg, workspaceConfig{}, err
	}
	ws, _, abs, err := m.n.resolveWorkspacePath(workspaceID, rel)
	if err != nil {
		return nil, cfg, ws, err
	}
	root := m.rootFor(ws, abs, cfg)
	serverID := cfg.ServerID
	if serverID == "" {
		serverID = language
	}
	key := workspaceID + ":" + serverID + ":" + root
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[key]
	if s == nil || s.rpc.closed {
		options := cloneAnyMap(cfg.InitializationOptions)
		s = &lspSession{workspaceRoot: root, config: cfg, initializationOptions: options, rpc: newJSONRPCProcess(cfg.Command, cfg.Args, root), opened: map[string]lspDocument{}, sessionID: "lsp_" + randomHex(8)}
		m.sessions[key] = s
	}
	return s, cfg, ws, nil
}
func normalizeLSP(v any, workspaceRoot string) any {
	switch x := v.(type) {
	case []any:
		out := make([]any, len(x))
		for i, y := range x {
			out[i] = normalizeLSP(y, workspaceRoot)
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for k, y := range x {
			if k == "uri" {
				if s, ok := y.(string); ok && strings.HasPrefix(s, "file:") {
					p := pathFromFileURI(s)
					rel, e := filepath.Rel(workspaceRoot, p)
					if e == nil {
						out["path"] = filepath.ToSlash(rel)
						continue
					}
				}
			}
			out[k] = normalizeLSP(y, workspaceRoot)
		}
		return out
	default:
		return v
	}
}
func cloneAnyMap(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	b, _ := json.Marshal(in)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

func (m *lspManager) positionTool(ctx context.Context, kind, workspaceID, rel string, line, character int, includeDecl bool, newName string) (any, error) {
	_, _, abs, err := m.n.resolveWorkspacePath(workspaceID, rel)
	if err != nil {
		return nil, err
	}
	s, cfg, ws, err := m.get(workspaceID, rel)
	if err != nil {
		return nil, err
	}
	uri, err := s.sync(ctx, abs, cfg.LanguageID)
	if err != nil {
		return nil, err
	}
	params := map[string]any{"textDocument": map[string]any{"uri": uri}, "position": map[string]any{"line": line, "character": character}}
	method := ""
	switch kind {
	case "definition":
		method = "textDocument/definition"
	case "references":
		method = "textDocument/references"
		params["context"] = map[string]any{"includeDeclaration": includeDecl}
	case "hover":
		method = "textDocument/hover"
	case "rename":
		method = "textDocument/rename"
		params["newName"] = newName
	}
	v, err := s.rpc.request(ctx, method, params, 15*time.Second)
	if err != nil {
		return nil, err
	}
	if kind == "hover" {
		return v, nil
	}
	return normalizeLSP(v, ws.Root), nil
}
func (m *lspManager) symbols(ctx context.Context, workspaceID, rel string) (any, error) {
	_, _, abs, err := m.n.resolveWorkspacePath(workspaceID, rel)
	if err != nil {
		return nil, err
	}
	s, cfg, ws, err := m.get(workspaceID, rel)
	if err != nil {
		return nil, err
	}
	uri, err := s.sync(ctx, abs, cfg.LanguageID)
	if err != nil {
		return nil, err
	}
	v, err := s.rpc.request(ctx, "textDocument/documentSymbol", map[string]any{"textDocument": map[string]any{"uri": uri}}, 15*time.Second)
	if err != nil {
		return nil, err
	}
	return normalizeLSP(v, ws.Root), nil
}
func (m *lspManager) workspaceSymbols(ctx context.Context, workspaceID, query, hint string) (any, error) {
	if hint == "" {
		hint = "index.ts"
	}
	s, _, ws, err := m.get(workspaceID, hint)
	if err != nil {
		return nil, err
	}
	if err := s.init(ctx); err != nil {
		return nil, err
	}
	v, err := s.rpc.request(ctx, "workspace/symbol", map[string]any{"query": query}, 20*time.Second)
	if err != nil {
		return nil, err
	}
	return normalizeLSP(v, ws.Root), nil
}
func (m *lspManager) diagnostics(ctx context.Context, workspaceID, rel string, wait int) (any, error) {
	_, _, abs, err := m.n.resolveWorkspacePath(workspaceID, rel)
	if err != nil {
		return nil, err
	}
	s, cfg, _, err := m.get(workspaceID, rel)
	if err != nil {
		return nil, err
	}
	uri, err := s.sync(ctx, abs, cfg.LanguageID)
	if err != nil {
		return nil, err
	}
	if wait < 100 {
		wait = 100
	}
	if wait > 5000 {
		wait = 5000
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(time.Duration(wait) * time.Millisecond):
	}
	hits := s.rpc.takeNotifications("textDocument/publishDiagnostics")
	var last any = []any{}
	for _, x := range hits {
		params, _ := x["params"].(map[string]any)
		if params["uri"] == uri {
			last = params["diagnostics"]
		}
	}
	return last, nil
}
func (m *lspManager) status() map[string]any {
	configured := map[string]any{}
	for k, v := range m.n.cfg.LSPServers {
		configured[k] = map[string]any{"serverId": v.ServerID, "command": v.Command, "args": v.Args, "extensions": v.Extensions}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	active := []map[string]any{}
	for key, s := range m.sessions {
		active = append(active, map[string]any{"key": key, "sessionId": s.sessionID, "root": s.workspaceRoot, "serverId": firstNonEmpty(s.config.ServerID, s.config.Command), "initialized": s.initialized, "closed": s.rpc.closed})
	}
	return map[string]any{"configured": configured, "active": active}
}
func (m *lspManager) close() {
	m.mu.Lock()
	sessions := m.sessions
	m.sessions = map[string]*lspSession{}
	m.mu.Unlock()
	for _, s := range sessions {
		s.rpc.stop()
	}
}
