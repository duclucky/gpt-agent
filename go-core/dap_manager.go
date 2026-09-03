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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type dapOutcome struct {
	body any
	err  error
}
type dapClient struct {
	command string
	args    []string
	cwd     string
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	writeMu sync.Mutex
	mu      sync.Mutex
	pending map[int64]chan dapOutcome
	events  []map[string]any
	seq     atomic.Int64
	closed  bool
}

func newDAPClient(command string, args []string, cwd string) *dapClient {
	return &dapClient{command: command, args: args, cwd: cwd, pending: map[int64]chan dapOutcome{}}
}
func (c *dapClient) start() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cmd != nil && !c.closed {
		return nil
	}
	cmd := exec.Command(c.command, c.args...)
	cmd.Dir = c.cwd
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
	c.cmd = cmd
	c.stdin = stdin
	c.closed = false
	go c.readLoop(stdout)
	go c.stderrLoop(stderr)
	go func() {
		err := cmd.Wait()
		if err == nil {
			err = fmt.Errorf("DAP adapter exited with code 0")
		} else {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				err = fmt.Errorf("DAP adapter exited with code %d", ee.ExitCode())
			}
		}
		c.fail(err)
	}()
	return nil
}
func (c *dapClient) readLoop(r io.Reader) {
	br := bufio.NewReaderSize(r, 64*1024)
	for {
		length := 0
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				c.fail(err)
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				break
			}
			if strings.HasPrefix(strings.ToLower(line), "content-length:") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					length, _ = strconv.Atoi(strings.TrimSpace(parts[1]))
				}
			}
		}
		if length <= 0 {
			c.fail(errors.New("DAP framing missing Content-Length."))
			return
		}
		body := make([]byte, length)
		if _, err := io.ReadFull(br, body); err != nil {
			c.fail(err)
			return
		}
		var msg map[string]any
		if json.Unmarshal(body, &msg) != nil {
			continue
		}
		c.handle(msg)
	}
}
func (c *dapClient) stderrLoop(r io.Reader) {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 64*1024), 2<<20)
	for s.Scan() {
		c.addEvent(map[string]any{"event": "adapterStderr", "body": map[string]any{"output": s.Text() + "\n"}})
	}
}
func (c *dapClient) handle(msg map[string]any) {
	kind, _ := msg["type"].(string)
	switch kind {
	case "response":
		seq := int64(intFromAny(msg["request_seq"]))
		c.mu.Lock()
		ch := c.pending[seq]
		delete(c.pending, seq)
		c.mu.Unlock()
		if ch == nil {
			return
		}
		success, _ := msg["success"].(bool)
		if success {
			body := msg["body"]
			if body == nil {
				body = map[string]any{}
			}
			ch <- dapOutcome{body: body}
		} else {
			message, _ := msg["message"].(string)
			if message == "" {
				message = fmt.Sprintf("DAP %v failed", msg["command"])
			}
			ch <- dapOutcome{err: errors.New(message)}
		}
		close(ch)
	case "event":
		c.addEvent(msg)
	case "request":
		go c.handleAdapterRequest(msg)
	}
}
func (c *dapClient) addEvent(msg map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, msg)
	if len(c.events) > 2000 {
		c.events = c.events[len(c.events)-2000:]
	}
}
func (c *dapClient) handleAdapterRequest(msg map[string]any) {
	cmd, _ := msg["command"].(string)
	if cmd == "runInTerminal" {
		_ = c.sendResponse(msg, true, map[string]any{"processId": nil, "shellProcessId": nil}, "")
	} else {
		_ = c.sendResponse(msg, false, map[string]any{}, "Unsupported adapter request: "+cmd)
	}
}
func (c *dapClient) sendRaw(obj any) error {
	if err := c.start(); err != nil {
		return err
	}
	payload, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := fmt.Fprintf(c.stdin, "Content-Length: %d\r\n\r\n", len(payload)); err != nil {
		return err
	}
	_, err = c.stdin.Write(payload)
	return err
}
func (c *dapClient) sendResponse(req map[string]any, success bool, body any, message string) error {
	seq := c.seq.Add(1)
	obj := map[string]any{"seq": seq, "type": "response", "request_seq": req["seq"], "success": success, "command": req["command"], "body": body}
	if message != "" {
		obj["message"] = message
	}
	return c.sendRaw(obj)
}
func (c *dapClient) request(ctx context.Context, command string, args any, timeout time.Duration) (any, error) {
	seq := c.seq.Add(1)
	ch := make(chan dapOutcome, 1)
	c.mu.Lock()
	c.pending[seq] = ch
	c.mu.Unlock()
	if err := c.sendRaw(map[string]any{"seq": seq, "type": "request", "command": command, "arguments": args}); err != nil {
		c.mu.Lock()
		delete(c.pending, seq)
		c.mu.Unlock()
		return nil, err
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case out := <-ch:
		return out.body, out.err
	case <-timer.C:
		c.mu.Lock()
		delete(c.pending, seq)
		c.mu.Unlock()
		return nil, fmt.Errorf("DAP request timed out: %s", command)
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, seq)
		c.mu.Unlock()
		return nil, ctx.Err()
	}
}
func (c *dapClient) waitEvent(ctx context.Context, event string, timeout time.Duration) (map[string]any, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		for i, e := range c.events {
			if e["event"] == event {
				hit := e
				c.events = append(c.events[:i], c.events[i+1:]...)
				c.mu.Unlock()
				return hit, nil
			}
		}
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return nil, fmt.Errorf("Timed out waiting for DAP event '%s'.", event)
}
func (c *dapClient) drainEvents() []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := append([]map[string]any(nil), c.events...)
	c.events = nil
	return out
}
func (c *dapClient) fail(err error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	pending := c.pending
	c.pending = map[int64]chan dapOutcome{}
	c.mu.Unlock()
	for _, ch := range pending {
		ch <- dapOutcome{err: err}
		close(ch)
	}
}
func (c *dapClient) stop() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _ = c.request(ctx, "disconnect", map[string]any{"restart": false, "terminateDebuggee": true}, 3*time.Second)
	c.mu.Lock()
	cmd := c.cmd
	c.closed = true
	c.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		killProcessTree(cmd.Process.Pid)
	}
}

type dapSession struct {
	ID, Type, WorkspaceID, Program, CreatedAt string
	Client                                    *dapClient
}
type dapManager struct {
	n        *nativeTools
	mu       sync.Mutex
	sessions map[string]*dapSession
}

func newDAPManager(n *nativeTools) *dapManager {
	return &dapManager{n: n, sessions: map[string]*dapSession{}}
}
func (m *dapManager) adapterCommand(cfg debugAdapterConfig) (string, []string, error) {
	cmd := cfg.Command
	if cmd == "__GPT_AGENT_TOOLS_PYTHON__" {
		if m.n.appRoot == "" {
			return "", nil, errors.New("GPT Agent app root is unavailable")
		}
		if runtimeGOOSWindows() {
			cmd = filepath.Join(m.n.appRoot, ".venv-tools", "Scripts", "python.exe")
		} else {
			cmd = filepath.Join(m.n.appRoot, ".venv-tools", "bin", "python")
		}
	}
	if cmd == "" {
		return "", nil, errors.New("Python debug adapter is not configured.")
	}
	if filepath.IsAbs(cmd) {
		if _, err := os.Stat(cmd); err != nil {
			return "", nil, err
		}
		return cmd, cfg.Args, nil
	}
	resolved, err := exec.LookPath(cmd)
	if err != nil {
		return "", nil, err
	}
	return resolved, cfg.Args, nil
}
func runtimeGOOSWindows() bool { return filepath.Separator == '\\' }
func (m *dapManager) get(id string) (*dapSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil {
		return nil, fmt.Errorf("Unknown debug session: %s", id)
	}
	return s, nil
}
func (m *dapManager) startPython(ctx context.Context, workspaceID, program string, args []string, cwd string, breakpoints []map[string]any, stopOnEntry, justMyCode bool) (map[string]any, error) {
	adapter, ok := m.n.cfg.DebugAdapters["python"]
	if !ok {
		return nil, errors.New("Python debug adapter is not configured.")
	}
	ws, err := m.n.workspace(workspaceID)
	if err != nil {
		return nil, err
	}
	_, _, progAbs, err := m.n.resolveWorkspacePath(workspaceID, program)
	if err != nil {
		return nil, err
	}
	if cwd == "" {
		cwd = "."
	}
	_, _, cwdAbs, err := m.n.resolveWorkspacePath(workspaceID, cwd)
	if err != nil {
		return nil, err
	}
	command, adapterArgs, err := m.adapterCommand(adapter)
	if err != nil {
		return nil, err
	}
	client := newDAPClient(command, adapterArgs, ws.Root)
	if err := client.start(); err != nil {
		return nil, err
	}
	id := "debug_" + randomHex(8)
	session := &dapSession{ID: id, Type: "python", WorkspaceID: workspaceID, Program: program, CreatedAt: nowISO(), Client: client}
	m.mu.Lock()
	m.sessions[id] = session
	m.mu.Unlock()
	cleanup := func() { m.mu.Lock(); delete(m.sessions, id); m.mu.Unlock(); client.stop() }
	capabilities, err := client.request(ctx, "initialize", map[string]any{"clientID": "gpt-agent-runtime", "clientName": "GPT Agent", "adapterID": "python", "pathFormat": "path", "linesStartAt1": true, "columnsStartAt1": true, "supportsVariableType": true, "supportsVariablePaging": true, "supportsRunInTerminalRequest": false, "locale": "en-US"}, 15*time.Second)
	if err != nil {
		cleanup()
		return nil, err
	}
	launchCh := make(chan dapOutcome, 1)
	go func() {
		body, e := client.request(ctx, "launch", map[string]any{"name": "GPT Agent Python Debug", "type": "python", "request": "launch", "program": progAbs, "cwd": cwdAbs, "args": args, "console": "internalConsole", "justMyCode": justMyCode, "stopOnEntry": stopOnEntry}, 30*time.Second)
		launchCh <- dapOutcome{body: body, err: e}
	}()
	if _, err := client.waitEvent(ctx, "initialized", 15*time.Second); err != nil {
		cleanup()
		return nil, err
	}
	grouped := map[string][]map[string]any{}
	order := []string{}
	for _, bp := range breakpoints {
		path, _ := bp["path"].(string)
		_, _, abs, err := m.n.resolveWorkspacePath(workspaceID, path)
		if err != nil {
			cleanup()
			return nil, err
		}
		if _, seen := grouped[abs]; !seen {
			order = append(order, abs)
		}
		item := map[string]any{"line": intFromAny(bp["line"])}
		if col := intFromAny(bp["column"]); col > 0 {
			item["column"] = col
		}
		grouped[abs] = append(grouped[abs], item)
	}
	bpResults := []map[string]any{}
	for _, abs := range order {
		body, e := client.request(ctx, "setBreakpoints", map[string]any{"source": map[string]any{"path": abs}, "breakpoints": grouped[abs]}, 15*time.Second)
		if e != nil {
			cleanup()
			return nil, e
		}
		rel, _ := filepath.Rel(ws.Root, abs)
		bpResults = append(bpResults, map[string]any{"path": filepath.ToSlash(rel), "result": body})
	}
	if _, err := client.request(ctx, "configurationDone", map[string]any{}, 15*time.Second); err != nil {
		cleanup()
		return nil, err
	}
	launch := <-launchCh
	if launch.err != nil {
		cleanup()
		return nil, launch.err
	}
	_, _ = m.n.audit.append(map[string]any{"type": "debug.start", "workspaceId": workspaceID, "sessionId": id, "program": program, "breakpoints": len(breakpoints)})
	return map[string]any{"sessionId": id, "capabilities": capabilities, "breakpoints": bpResults}, nil
}
func (m *dapManager) request(ctx context.Context, id, command string, args map[string]any) (any, error) {
	s, err := m.get(id)
	if err != nil {
		return nil, err
	}
	return s.Client.request(ctx, command, args, 15*time.Second)
}
func (m *dapManager) events(id string) (map[string]any, error) {
	s, err := m.get(id)
	if err != nil {
		return nil, err
	}
	return map[string]any{"events": s.Client.drainEvents()}, nil
}
func (m *dapManager) stop(id string) (map[string]any, error) {
	s, err := m.get(id)
	if err != nil {
		return nil, err
	}
	s.Client.stop()
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
	_, _ = m.n.audit.append(map[string]any{"type": "debug.stop", "sessionId": id, "workspaceId": s.WorkspaceID})
	return map[string]any{"stopped": true, "sessionId": id}, nil
}
func (m *dapManager) status() []map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]map[string]any, 0, len(m.sessions))
	for _, s := range m.sessions {
		s.Client.mu.Lock()
		closed := s.Client.closed
		s.Client.mu.Unlock()
		out = append(out, map[string]any{"sessionId": s.ID, "type": s.Type, "workspaceId": s.WorkspaceID, "program": s.Program, "createdAt": s.CreatedAt, "closed": closed})
	}
	return out
}
func (m *dapManager) close() {
	m.mu.Lock()
	sessions := m.sessions
	m.sessions = map[string]*dapSession{}
	m.mu.Unlock()
	for _, s := range sessions {
		s.Client.stop()
	}
}

func dapJSONMap(raw json.RawMessage, target *map[string]any) error {
	if err := decodeNativeArgs(raw, target); err != nil {
		return err
	}
	return nil
}
func toBreakpointMaps(v any) []map[string]any {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(arr))
	for _, x := range arr {
		if m, ok := x.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}
func cloneJSON(v any) any {
	b, _ := json.Marshal(v)
	var out any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	_ = dec.Decode(&out)
	return out
}
