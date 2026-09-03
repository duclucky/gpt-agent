package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	platformTunnelsURL = "https://platform.openai.com/settings/organization/tunnels"
	runtimeKeysURL     = "https://platform.openai.com/settings/organization/api-keys"
	rolesURL           = "https://platform.openai.com/settings/organization/people/roles"
	chatGPTConnectURL  = "https://chatgpt.com/#settings/Connectors"
)

var tunnelIDRE = regexp.MustCompile(`^tunnel_[0-9a-f]{32}$`)
var tunnelIDExtractRE = regexp.MustCompile(`tunnel_[0-9a-f]{32}`)
var keyLikeRE = regexp.MustCompile(`(?i)\b(?:sk|sess|rk)-[A-Za-z0-9._-]{10,}\b`)

type app struct {
	installRoot string
	token       string
	server      *http.Server
	doneOnce    sync.Once
	done        chan struct{}
}

type statusResponse struct {
	RuntimeHealthy      bool   `json:"runtimeHealthy"`
	TunnelClientPresent bool   `json:"tunnelClientPresent"`
	ProfilePresent      bool   `json:"profilePresent"`
	HasRuntimeKey       bool   `json:"hasRuntimeKey"`
	TunnelTaskPresent   bool   `json:"tunnelTaskPresent"`
	TunnelReady         bool   `json:"tunnelReady"`
	TunnelID            string `json:"tunnelId,omitempty"`
	ReadyURL            string `json:"readyUrl,omitempty"`
}

type saveRequest struct {
	TunnelID string `json:"tunnelId"`
	APIKey   string `json:"apiKey"`
}

type saveResponse struct {
	OK       bool   `json:"ok"`
	Message  string `json:"message"`
	Doctor   string `json:"doctor,omitempty"`
	ReadyURL string `json:"readyUrl,omitempty"`
	TunnelID string `json:"tunnelId,omitempty"`
}

func main() {
	installRoot := flag.String("install-root", `C:\GPTAgent`, "GPT Agent installation root")
	noOpen := flag.Bool("no-open", false, "do not open the browser automatically")
	timeout := flag.Duration("timeout", 30*time.Minute, "maximum wizard lifetime")
	flag.Parse()

	root, err := filepath.Abs(*installRoot)
	if err != nil {
		fatal(err)
	}
	token, err := randomToken(32)
	if err != nil {
		fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		fatal(fmt.Errorf("start loopback setup server: %w", err))
	}
	a := &app{installRoot: root, token: token, done: make(chan struct{})}
	mux := http.NewServeMux()
	mux.HandleFunc("/", a.handleIndex)
	mux.HandleFunc("/api/status", a.handleStatus)
	mux.HandleFunc("/api/save", a.handleSave)
	mux.HandleFunc("/api/finish", a.handleFinish)
	a.server = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}

	url := fmt.Sprintf("http://127.0.0.1:%d/?token=%s", listener.Addr().(*net.TCPAddr).Port, token)
	fmt.Printf("GPT Agent setup wizard: %s\n", url)
	if !*noOpen {
		if err := openBrowser(url); err != nil {
			fmt.Fprintf(os.Stderr, "Could not open the browser automatically: %v\n", err)
		}
	}

	serveErr := make(chan error, 1)
	go func() {
		err := a.server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serveErr <- err
	}()

	timer := time.NewTimer(*timeout)
	defer timer.Stop()
	select {
	case <-a.done:
	case <-timer.C:
		fmt.Fprintln(os.Stderr, "Setup wizard timed out. GPT Agent remains installed; rerun Configure-OpenAITunnel.ps1 to continue.")
	case err := <-serveErr:
		if err != nil {
			fatal(err)
		}
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = a.server.Shutdown(ctx)
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "GPT Agent setup error: %v\n", err)
	os.Exit(1)
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (a *app) authorized(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || host != "127.0.0.1" {
		return false
	}
	if r.URL.Query().Get("token") != a.token {
		return false
	}
	if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
		expected := "http://" + r.Host
		if origin != expected {
			return false
		}
	}
	return true
}

func secureHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
}

func (a *app) reject(w http.ResponseWriter) {
	secureHeaders(w)
	http.Error(w, "Forbidden", http.StatusForbidden)
}

func (a *app) handleIndex(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(r) {
		a.reject(w)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	secureHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	page := strings.ReplaceAll(setupHTML, "__TOKEN__", html.EscapeString(a.token))
	_, _ = io.WriteString(w, page)
}

func (a *app) handleStatus(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(r) {
		a.reject(w)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, a.status(r.Context()))
}

func (a *app) handleSave(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(r) {
		a.reject(w)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var req saveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, saveResponse{OK: false, Message: "Invalid setup request."})
		return
	}
	resp, err := a.configure(r.Context(), req)
	if err != nil {
		resp.OK = false
		resp.Message = err.Error()
		writeJSON(w, http.StatusBadRequest, resp)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (a *app) handleFinish(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(r) {
		a.reject(w)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	a.doneOnce.Do(func() { close(a.done) })
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	secureHeaders(w)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (a *app) status(ctx context.Context) statusResponse {
	out := statusResponse{}
	out.RuntimeHealthy = probeHTTP(ctx, "http://127.0.0.1:8765/healthz")
	out.TunnelClientPresent = fileExists(filepath.Join(a.installRoot, "bin", "tunnel-client.exe"))
	profile := filepath.Join(a.installRoot, "config", "tunnel-client", "gpt-agent.yaml")
	secret := filepath.Join(a.installRoot, "config", "secrets", "control-plane-api-key.txt")
	out.ProfilePresent = fileExists(profile)
	out.HasRuntimeKey = fileExists(secret)
	out.TunnelID = readTunnelID(profile)
	out.TunnelTaskPresent = taskExists(ctx, "GPT Agent Tunnel")
	readyURLFile := filepath.Join(a.installRoot, "data", "tunnel-health.url")
	if raw, err := os.ReadFile(readyURLFile); err == nil {
		out.ReadyURL = strings.TrimSpace(string(raw))
		if out.ReadyURL != "" {
			out.TunnelReady = probeHTTP(ctx, strings.TrimRight(out.ReadyURL, "/")+"/readyz")
		}
	}
	return out
}

func (a *app) configure(parent context.Context, req saveRequest) (saveResponse, error) {
	var resp saveResponse
	tunnelID := strings.TrimSpace(req.TunnelID)
	apiKey := strings.TrimSpace(req.APIKey)
	if !tunnelIDRE.MatchString(tunnelID) {
		return resp, errors.New("Tunnel ID must match tunnel_ followed by 32 lowercase hexadecimal characters.")
	}
	bin := filepath.Join(a.installRoot, "bin", "tunnel-client.exe")
	if !fileExists(bin) {
		return resp, errors.New("tunnel-client.exe is missing. Rerun the GPT Agent installer without -SkipTunnelDownload.")
	}
	if !probeHTTP(parent, "http://127.0.0.1:8765/healthz") {
		return resp, errors.New("GPT Agent runtime is not healthy on 127.0.0.1:8765. Start GPT Agent Runtime and retry.")
	}

	profileDir := filepath.Join(a.installRoot, "config", "tunnel-client")
	secretsDir := filepath.Join(a.installRoot, "config", "secrets")
	secretFile := filepath.Join(secretsDir, "control-plane-api-key.txt")
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		return resp, fmt.Errorf("create tunnel profile directory: %w", err)
	}
	if err := os.MkdirAll(secretsDir, 0o700); err != nil {
		return resp, fmt.Errorf("create secret directory: %w", err)
	}
	if apiKey != "" {
		if strings.ContainsAny(apiKey, "\r\n\x00") || len(apiKey) < 16 {
			return resp, errors.New("Runtime API key does not look valid.")
		}
		if err := writeSecretFile(secretFile, apiKey); err != nil {
			return resp, err
		}
	} else if !fileExists(secretFile) {
		return resp, errors.New("Runtime API key is required the first time. Create a Restricted runtime key with Tunnels Read + Use, then paste it here.")
	}

	ctx, cancel := context.WithTimeout(parent, 90*time.Second)
	defer cancel()
	initArgs := []string{
		"init", "--profile", "gpt-agent", "--profile-dir", profileDir, "--force",
		"--tunnel-id", tunnelID,
		"--mcp-server-url", "http://127.0.0.1:8765/mcp",
		"--control-plane-api-key-ref", "file:" + secretFile,
		"--health-listen-addr", "127.0.0.1:0",
	}
	initOut, err := runCapture(ctx, bin, initArgs...)
	if err != nil {
		return resp, fmt.Errorf("tunnel-client init failed: %s", sanitizeOutput(initOut, apiKey))
	}
	doctorOut, err := runCapture(ctx, bin, "doctor", "--profile", "gpt-agent", "--profile-dir", profileDir, "--explain")
	doctorOut = sanitizeOutput(doctorOut, apiKey)
	resp.Doctor = truncate(doctorOut, 12000)
	if err != nil {
		return resp, errors.New("tunnel-client doctor failed. Check tunnel scope, Tunnels Read + Use permission, and the runtime key. Details are shown below.")
	}

	healthURLFile := filepath.Join(a.installRoot, "data", "tunnel-health.url")
	_ = os.Remove(healthURLFile)
	_ = endTask(ctx, "GPT Agent Tunnel")
	if err := registerTunnelTask(ctx, a.installRoot); err != nil {
		return resp, err
	}
	if err := startTask(ctx, "GPT Agent Tunnel"); err != nil {
		return resp, err
	}
	readyURL, err := waitTunnelReady(ctx, healthURLFile, 60*time.Second)
	if err != nil {
		return resp, err
	}

	resp.OK = true
	resp.TunnelID = tunnelID
	resp.ReadyURL = readyURL
	resp.Message = "Local tunnel is ready. Finish by opening ChatGPT Connectors, choose Connection: Tunnel, then select or paste this same tunnel ID."
	return resp, nil
}

func writeSecretFile(path, secret string) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.TrimSpace(secret)), 0o600); err != nil {
		return fmt.Errorf("write runtime key: %w", err)
	}
	if runtime.GOOS == "windows" {
		current, err := user.Current()
		if err != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("resolve current Windows user: %w", err)
		}
		if out, err := runCapture(context.Background(), "icacls.exe", tmp, "/inheritance:r", "/grant:r", current.Username+":(R,W)", "SYSTEM:(F)"); err != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("restrict runtime key ACL: %s", sanitizeOutput(out, secret))
		}
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("activate runtime key file: %w", err)
	}
	return nil
}

func registerTunnelTask(ctx context.Context, installRoot string) error {
	shell, err := exec.LookPath("pwsh.exe")
	if err != nil {
		shell, err = exec.LookPath("powershell.exe")
		if err != nil {
			return errors.New("PowerShell is required to register the GPT Agent Tunnel task.")
		}
	}
	runner := filepath.Join(installRoot, "runtime", "scripts", "windows", "Run-NativeTunnel.ps1")
	if !fileExists(runner) {
		return fmt.Errorf("tunnel runner is missing: %s", runner)
	}
	action := fmt.Sprintf(`"%s" -NoLogo -NoProfile -NonInteractive -WindowStyle Hidden -ExecutionPolicy Bypass -File "%s" -InstallRoot "%s"`, shell, runner, installRoot)
	out, err := runCapture(ctx, "schtasks.exe", "/Create", "/TN", "GPT Agent Tunnel", "/SC", "ONLOGON", "/TR", action, "/F")
	if err != nil {
		return fmt.Errorf("register GPT Agent Tunnel task: %s", sanitizeOutput(out, ""))
	}
	return nil
}

func taskExists(ctx context.Context, name string) bool {
	c, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, err := runCapture(c, "schtasks.exe", "/Query", "/TN", name, "/FO", "LIST")
	return err == nil
}

func endTask(ctx context.Context, name string) error {
	_, err := runCapture(ctx, "schtasks.exe", "/End", "/TN", name)
	return err
}

func startTask(ctx context.Context, name string) error {
	out, err := runCapture(ctx, "schtasks.exe", "/Run", "/TN", name)
	if err != nil {
		return fmt.Errorf("start %s task: %s", name, sanitizeOutput(out, ""))
	}
	return nil
}

func waitTunnelReady(ctx context.Context, healthURLFile string, max time.Duration) (string, error) {
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
		raw, err := os.ReadFile(healthURLFile)
		if err == nil {
			base := strings.TrimSpace(string(raw))
			if base != "" && probeHTTP(ctx, strings.TrimRight(base, "/")+"/readyz") {
				return base, nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return "", errors.New("Tunnel task started but /readyz did not become healthy within 60 seconds. Check C:\\GPTAgent\\logs\\tunnel.log and rerun the wizard.")
}

func probeHTTP(parent context.Context, url string) bool {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	return resp.StatusCode == http.StatusOK
}

func readTunnelID(profile string) string {
	raw, err := os.ReadFile(profile)
	if err != nil {
		return ""
	}
	return tunnelIDExtractRE.FindString(string(raw))
}

func runCapture(ctx context.Context, exe string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func sanitizeOutput(s, secret string) string {
	if secret != "" {
		s = strings.ReplaceAll(s, secret, "[REDACTED]")
	}
	return keyLikeRE.ReplaceAllString(s, "[REDACTED]")
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n... output truncated ..."
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

func openBrowser(url string) error {
	if runtime.GOOS != "windows" {
		return errors.New("automatic browser launch is currently supported on Windows only")
	}
	return exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", url).Start()
}

const setupHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>GPT Agent Setup</title>
<style>:root{font-family:Inter,ui-sans-serif,system-ui,-apple-system,"Segoe UI",sans-serif;color:#e8edf5;background:#0b0d12}*{box-sizing:border-box}body{margin:0;background:radial-gradient(circle at top,#171c28,#0b0d12 45%);min-height:100vh}.wrap{max-width:920px;margin:0 auto;padding:40px 22px 70px}.hero{margin-bottom:28px}.eyebrow{font-size:12px;text-transform:uppercase;letter-spacing:.16em;color:#8da2c8;font-weight:700}.hero h1{font-size:38px;line-height:1.05;margin:8px 0 12px}.hero p{color:#abb6c8;max-width:760px;line-height:1.6}.card{background:rgba(19,23,32,.94);border:1px solid #293142;border-radius:18px;padding:22px;margin:16px 0;box-shadow:0 18px 60px rgba(0,0,0,.2)}.step{display:flex;gap:14px;align-items:flex-start}.num{width:32px;height:32px;border-radius:50%;background:#20283a;display:flex;align-items:center;justify-content:center;font-weight:800;flex:none}.grow{flex:1}h2{font-size:18px;margin:4px 0 8px}p,li{color:#b6c1d3;line-height:1.55}.actions{display:flex;flex-wrap:wrap;gap:10px;margin:14px 0}.btn,button{display:inline-flex;align-items:center;justify-content:center;border:1px solid #3a465f;background:#20283a;color:#f3f6fb;text-decoration:none;border-radius:10px;padding:10px 14px;font-weight:700;cursor:pointer}.btn.primary,button.primary{background:#edf2f8;color:#10131a;border-color:#edf2f8}.btn:hover,button:hover{filter:brightness(1.08)}label{display:block;font-weight:700;margin:14px 0 7px}input{width:100%;background:#0f131b;color:#f7f9fc;border:1px solid #354057;border-radius:10px;padding:12px 13px;font:inherit;outline:none}input:focus{border-color:#7f99c9;box-shadow:0 0 0 3px rgba(127,153,201,.15)}.hint{font-size:13px;color:#8e9bb0}.status{display:grid;grid-template-columns:repeat(auto-fit,minmax(180px,1fr));gap:8px;margin-top:12px}.pill{background:#10151e;border:1px solid #273044;padding:10px 12px;border-radius:10px;font-size:13px}.ok{border-color:#285a45;color:#b9efd4}.bad{border-color:#61363a;color:#f1bec2}.warn{border-color:#65562e;color:#efdca5}.result{white-space:pre-wrap;background:#0b0f16;border:1px solid #273044;border-radius:10px;padding:13px;max-height:260px;overflow:auto;color:#c6d0df;font:12px/1.5 ui-monospace,SFMono-Regular,Consolas,monospace}.hidden{display:none}.notice{border-left:3px solid #7f99c9;padding-left:12px;color:#aebbd0}.success{border-color:#2f7355;background:rgba(25,60,45,.35)}.error{border-color:#81434a;background:rgba(65,28,34,.35)}.foot{margin-top:24px;color:#78869d;font-size:12px}code{background:#0d1118;border:1px solid #293142;border-radius:5px;padding:1px 5px}</style></head>
<body><div class="wrap"><div class="hero"><div class="eyebrow">GPT Agent · local setup</div><h1>Connect your local developer runtime to ChatGPT</h1><p>This wizard only runs on <code>127.0.0.1</code>. Your runtime API key is never written into source code or the tunnel profile; it is stored in a local ACL-restricted secret file and the profile keeps only a <code>file:</code> reference.</p></div>
<div class="card"><h2>Local readiness</h2><div id="status" class="status"><div class="pill warn">Checking…</div></div></div>
<div class="card"><div class="step"><div class="num">1</div><div class="grow"><h2>Create or choose an OpenAI tunnel</h2><p>Open Platform Tunnels and create/select the tunnel that should connect this machine. Attach the correct ChatGPT workspace scope so it can appear in the connector picker.</p><div class="actions"><a class="btn" rel="noreferrer" target="_blank" href="` + platformTunnelsURL + `">Open Platform Tunnels</a><a class="btn" rel="noreferrer" target="_blank" href="` + rolesURL + `">Open Roles & Permissions</a></div><label for="tunnelId">Tunnel ID</label><input id="tunnelId" autocomplete="off" spellcheck="false" placeholder="tunnel_0123456789abcdef0123456789abcdef"><div class="hint">Expected format: tunnel_ + 32 lowercase hexadecimal characters.</div></div></div></div>
<div class="card"><div class="step"><div class="num">2</div><div class="grow"><h2>Create a Restricted runtime API key</h2><p>Create a runtime API key with <strong>Tunnels Read + Use</strong>. Do not use an Admin API key for the long-lived daemon. An admin key is not needed when you create the tunnel in the Platform UI.</p><div class="actions"><a class="btn" rel="noreferrer" target="_blank" href="` + runtimeKeysURL + `">Open Runtime API Keys</a></div><label for="apiKey">Runtime API key</label><input id="apiKey" type="password" autocomplete="new-password" spellcheck="false" placeholder="Paste runtime API key"><div class="hint" id="keyHint">The key is sent only to this one-time loopback wizard and saved locally with restricted ACLs.</div></div></div></div>
<div class="card"><div class="step"><div class="num">3</div><div class="grow"><h2>Save, validate, and start the tunnel</h2><p>The wizard will create the native tunnel-client profile, run <code>doctor --explain</code>, register the <code>GPT Agent Tunnel</code> logon task, start it, and wait for <code>/readyz</code>.</p><div class="actions"><button id="save" class="primary">Save and connect</button></div><div id="saveMessage" class="notice hidden"></div><div id="doctorWrap" class="hidden"><h3>Validation output</h3><div id="doctor" class="result"></div></div></div></div></div>
<div id="finishCard" class="card success hidden"><div class="step"><div class="num">4</div><div class="grow"><h2>Finish in ChatGPT</h2><p>Your local tunnel is healthy. ChatGPT currently exposes this under <strong>Settings → Connectors</strong>, not as a legacy “plugin”. Choose <strong>Connection: Tunnel</strong>, then select the tunnel or paste the same tunnel ID.</p><div class="actions"><a class="btn primary" rel="noreferrer" target="_blank" href="` + chatGPTConnectURL + `">Open ChatGPT Connectors</a><button id="finish">Finish setup</button></div><div class="hint">Keep the GPT Agent Runtime and GPT Agent Tunnel tasks enabled for future local tool calls.</div></div></div></div><div class="foot">Independent community project. Not an official OpenAI product.</div></div>
<script>const token="__TOKEN__";const q=u=>u+(u.includes('?')?'&':'?')+'token='+encodeURIComponent(token);const $=id=>document.getElementById(id);function pill(label,ok){return '<div class="pill '+(ok?'ok':'bad')+'">'+label+': '+(ok?'ready':'not ready')+'</div>'}async function refresh(){try{const r=await fetch(q('/api/status'),{cache:'no-store'});const s=await r.json();$('status').innerHTML=[pill('Runtime',s.runtimeHealthy),pill('tunnel-client',s.tunnelClientPresent),pill('Profile',s.profilePresent),pill('Runtime key',s.hasRuntimeKey),pill('Tunnel task',s.tunnelTaskPresent),pill('Tunnel',s.tunnelReady)].join('');if(s.tunnelId&&!$('tunnelId').value)$('tunnelId').value=s.tunnelId;if(s.hasRuntimeKey)$('keyHint').textContent='A local runtime key already exists. Leave this field blank to reuse it, or paste a new key to rotate it.';if(s.tunnelReady)showSuccess(s.tunnelId)}catch(e){$('status').innerHTML='<div class="pill bad">Status check failed</div>'}}function showSuccess(id){$('finishCard').classList.remove('hidden');$('saveMessage').className='notice';$('saveMessage').classList.remove('hidden');$('saveMessage').textContent='Local tunnel is ready for '+id+'.'}$('save').addEventListener('click',async()=>{const b=$('save');b.disabled=true;b.textContent='Validating…';$('saveMessage').classList.add('hidden');$('doctorWrap').classList.add('hidden');try{const r=await fetch(q('/api/save'),{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({tunnelId:$('tunnelId').value.trim(),apiKey:$('apiKey').value.trim()})});const d=await r.json();$('apiKey').value='';$('saveMessage').classList.remove('hidden');$('saveMessage').className='notice '+(d.ok?'':'error');$('saveMessage').textContent=d.message||'Setup failed.';if(d.doctor){$('doctorWrap').classList.remove('hidden');$('doctor').textContent=d.doctor}if(d.ok){showSuccess(d.tunnelId);await refresh()}}catch(e){$('saveMessage').classList.remove('hidden');$('saveMessage').className='notice error';$('saveMessage').textContent='Setup request failed: '+e.message}finally{b.disabled=false;b.textContent='Save and connect'}});$('finish').addEventListener('click',async()=>{try{await fetch(q('/api/finish'),{method:'POST'})}catch(e){}$('finish').disabled=true;$('finish').textContent='Done. You can close this tab.'});refresh();setInterval(refresh,5000);</script></body></html>`
