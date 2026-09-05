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
	"net/url"
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
	if !isLoopbackProbeURL(url) {
		return false
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	client := &http.Client{
		Timeout: 2 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 || !isLoopbackProbeURL(req.URL.String()) {
				return errors.New("HTTP redirect left the loopback boundary")
			}
			return nil
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	return resp.StatusCode == http.StatusOK
}

func isLoopbackProbeURL(raw string) bool {
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
<style>
:root{color-scheme:dark;font-family:ui-sans-serif,system-ui,-apple-system,"Segoe UI",sans-serif;--bg:#0b0d10;--surface:#12161c;--surface2:#171c23;--border:#2a323d;--text:#f2f5f8;--muted:#aab4c0;--subtle:#8591a0;--primary:#f4f7fa;--primaryText:#11151a;--ok:#9ce0bd;--okBorder:#315b47;--warn:#e4cc8a;--warnBorder:#665832;--bad:#f0b4b8;--badBorder:#6c3c42;--focus:#8bb5ff}*{box-sizing:border-box}html,body{background:var(--bg)}body{margin:0;min-height:100vh;color:var(--text);font-size:16px;line-height:1.6}.shell{width:min(880px,100%);margin:0 auto;padding:40px 20px 72px}.hero{margin-bottom:24px}.eyebrow{margin:0 0 8px;color:#9fb0c3;font-size:12px;font-weight:700;letter-spacing:.14em;text-transform:uppercase}.hero h1{max-width:700px;margin:0;font-size:clamp(30px,5vw,44px);line-height:1.1;letter-spacing:-.025em}.lede{max-width:680px;margin:14px 0 0;color:var(--muted)}.progress{display:grid;grid-template-columns:repeat(4,1fr);gap:8px;margin:28px 0 18px;padding:0;list-style:none}.progress li{min-width:0;border-top:2px solid var(--border);padding-top:9px;color:var(--subtle);font-size:13px}.progress li[aria-current="step"]{border-color:var(--primary);color:var(--text)}.progress-num{display:block;margin-bottom:2px;font-size:11px;font-weight:800;letter-spacing:.08em;text-transform:uppercase}.panel,.step-card{margin:14px 0;border:1px solid var(--border);border-radius:14px;background:var(--surface);padding:22px}.panel h2,.step-card h2{margin:0;font-size:20px;line-height:1.3}.panel p,.step-card p{margin:7px 0 0;color:var(--muted)}.status-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:8px;margin-top:18px}.status-item{min-width:0;border:1px solid var(--border);border-radius:10px;background:#0f1318;padding:11px 12px}.status-label{display:block;color:var(--muted);font-size:13px}.status-value{display:block;margin-top:2px;font-size:14px;font-weight:700}.status-item.ok{border-color:var(--okBorder)}.status-item.ok .status-value{color:var(--ok)}.status-item.warn{border-color:var(--warnBorder)}.status-item.warn .status-value{color:var(--warn)}.status-item.bad{border-color:var(--badBorder)}.status-item.bad .status-value{color:var(--bad)}.step-row{display:grid;grid-template-columns:42px minmax(0,1fr);gap:16px}.step-num{display:grid;place-items:center;width:38px;height:38px;border:1px solid #3b4653;border-radius:50%;background:var(--surface2);font-weight:800}.step-body{min-width:0}.instruction-list{margin:14px 0 0;padding-left:22px;color:var(--muted)}.instruction-list li{margin:6px 0}.actions{display:flex;flex-wrap:wrap;gap:10px;margin:16px 0}.btn,button{min-height:44px;border:1px solid #46515e;border-radius:9px;background:var(--surface2);color:var(--text);padding:9px 14px;font:inherit;font-weight:700;line-height:1.2;text-decoration:none;cursor:pointer;touch-action:manipulation}.btn{display:inline-flex;align-items:center;justify-content:center}.btn.primary,button.primary{border-color:var(--primary);background:var(--primary);color:var(--primaryText)}button:disabled{cursor:not-allowed;opacity:.55}.btn:hover,button:not(:disabled):hover{border-color:#6c7887}.btn:focus-visible,button:focus-visible,input:focus-visible,summary:focus-visible{outline:3px solid var(--focus);outline-offset:2px}label{display:block;margin:18px 0 6px;font-weight:700}.field-row{display:flex;gap:8px;align-items:stretch}.field-row input{min-width:0;flex:1}input{width:100%;min-height:46px;border:1px solid #3b4653;border-radius:9px;background:#0e1217;color:var(--text);padding:10px 12px;font:inherit}input[aria-invalid="true"]{border-color:var(--badBorder)}.helper{margin-top:7px;color:var(--subtle);font-size:13px}.security-note{margin-top:14px;border-left:2px solid #46515e;padding-left:12px;color:var(--muted);font-size:14px}code,.mono{font-family:ui-monospace,SFMono-Regular,Consolas,"Liberation Mono",monospace}.example{overflow-wrap:anywhere}.save-row{margin-top:18px}.save-row .primary{min-width:190px}.notice{margin-top:14px;border-left:3px solid #60758c;padding:9px 12px;color:var(--muted)}.notice.error{border-color:var(--badBorder);color:var(--bad)}.notice.success-note{border-color:var(--okBorder);color:var(--ok)}.hidden{display:none!important}.technical{margin-top:14px;border:1px solid var(--border);border-radius:10px;background:#0e1217}.technical summary{min-height:44px;padding:10px 12px;color:var(--muted);font-weight:700;cursor:pointer}.result{max-height:260px;margin:0;border-top:1px solid var(--border);overflow:auto;padding:12px;white-space:pre-wrap;color:#c4ced9;font:12px/1.55 ui-monospace,SFMono-Regular,Consolas,monospace}.success-card{border-color:var(--okBorder);background:#111a16}.success-badge{display:inline-block;margin-bottom:8px;color:var(--ok);font-size:12px;font-weight:800;letter-spacing:.08em;text-transform:uppercase}.tunnel-copy{display:flex;gap:8px;align-items:center;margin:16px 0}.tunnel-id{min-width:0;flex:1;border:1px solid var(--okBorder);border-radius:9px;background:#0c1410;padding:10px 12px;overflow-wrap:anywhere;color:#d9f5e5}.success-title:focus{outline:none}.path{margin-top:14px;border:1px solid var(--border);border-radius:9px;background:#0e1217;padding:12px;color:var(--text)}.foot{margin-top:24px;color:var(--subtle);font-size:12px}@media(max-width:620px){.shell{padding:28px 14px 56px}.progress{grid-template-columns:repeat(2,1fr);row-gap:12px}.panel,.step-card{padding:18px}.status-grid{grid-template-columns:1fr}.step-row{grid-template-columns:1fr}.field-row,.tunnel-copy{align-items:stretch;flex-direction:column}.field-row button,.tunnel-copy button,.actions .btn,.actions button,.save-row .primary{width:100%}.actions{flex-direction:column}}@media(prefers-reduced-motion:reduce){*{scroll-behavior:auto!important}}
</style></head><body><main class="shell">
<header class="hero"><p class="eyebrow">GPT Agent · local setup</p><h1>Connect GPT Agent to ChatGPT</h1><p class="lede">GPT Agent is already installed on this computer. Complete these four steps once, then ChatGPT can use the local developer tools you chose to expose.</p></header>
<ol class="progress" aria-label="Setup progress"><li id="progress1" aria-current="step"><span class="progress-num">Step 1</span>Choose Tunnel</li><li id="progress2"><span class="progress-num">Step 2</span>Create key</li><li id="progress3"><span class="progress-num">Step 3</span>Connect</li><li id="progress4"><span class="progress-num">Step 4</span>Finish in ChatGPT</li></ol>
<section class="panel" aria-labelledby="readinessTitle"><h2 id="readinessTitle">This computer</h2><p>GPT Agent checks these automatically. You only need to act if an item says <strong>Needs attention</strong>.</p><div id="status" class="status-grid" role="status" aria-live="polite" aria-atomic="true"><div class="status-item warn"><span class="status-label">Local setup</span><span class="status-value">Checking…</span></div></div></section>
<section class="step-card" aria-labelledby="step1Title"><div class="step-row"><div class="step-num" aria-hidden="true">1</div><div class="step-body"><h2 id="step1Title">Choose your OpenAI Tunnel</h2><p>The Tunnel is the private connection between this computer and your ChatGPT workspace.</p><ol class="instruction-list"><li>Click <strong>Open Platform Tunnels</strong>.</li><li>Create a Tunnel, or open the one you want to use with this ChatGPT workspace.</li><li>Copy its ID. It starts with <code>tunnel_</code>.</li></ol><div class="actions"><a class="btn" rel="noreferrer" target="_blank" href="` + platformTunnelsURL + `">Open Platform Tunnels</a><a class="btn" rel="noreferrer" target="_blank" href="` + rolesURL + `">Open Roles &amp; Permissions</a></div><label for="tunnelId">Tunnel ID</label><input id="tunnelId" aria-describedby="tunnelHint" autocomplete="off" spellcheck="false" placeholder="tunnel_0123456789abcdef0123456789abcdef"><div class="helper example" id="tunnelHint">Example: <code>tunnel_0123456789abcdef0123456789abcdef</code></div></div></div></section>
<section class="step-card" aria-labelledby="step2Title"><div class="step-row"><div class="step-num" aria-hidden="true">2</div><div class="step-body"><h2 id="step2Title">Create a restricted runtime key</h2><p>This key lets the official OpenAI tunnel client use the Tunnel. It does not give GPT Agent general Admin access.</p><ol class="instruction-list"><li>Click <strong>Open Runtime API Keys</strong> and create a <strong>Restricted</strong> key.</li><li>For <strong>Tunnels</strong>, allow only <strong>Read</strong> and <strong>Use</strong>.</li><li>Copy the new key and paste it below.</li></ol><div class="actions"><a class="btn" rel="noreferrer" target="_blank" href="` + runtimeKeysURL + `">Open Runtime API Keys</a></div><label for="apiKey">Runtime API key</label><div class="field-row"><input id="apiKey" type="password" aria-describedby="keyHint" autocomplete="new-password" spellcheck="false" placeholder="Paste runtime API key"><button id="toggleKey" type="button" aria-controls="apiKey" aria-pressed="false">Show</button></div><div class="helper" id="keyHint">The key stays on this computer in an ACL-restricted secret file. It is never written into this repository or stored directly in the Tunnel profile.</div></div></div></section>
<section class="step-card" aria-labelledby="step3Title"><div class="step-row"><div class="step-num" aria-hidden="true">3</div><div class="step-body"><h2 id="step3Title">Connect this computer</h2><p>GPT Agent will validate the Tunnel and key, save the local Tunnel settings, start the connection, and confirm that it is healthy.</p><div class="save-row"><button id="save" class="primary" type="button">Save and connect</button></div><div id="saveMessage" class="notice hidden" role="status" aria-live="polite" aria-atomic="true"></div><details id="doctorWrap" class="technical hidden"><summary>Technical validation details</summary><pre id="doctor" class="result"></pre></details></div></div></section>
<section id="finishCard" class="step-card success-card hidden" aria-labelledby="step4Title"><div class="step-row"><div class="step-num" aria-hidden="true">4</div><div class="step-body"><span class="success-badge">Local connection ready</span><h2 id="step4Title" class="success-title" tabindex="-1">Finish in ChatGPT</h2><p>The local side is connected. Use this same Tunnel in ChatGPT.</p><div class="tunnel-copy"><div id="finalTunnelId" class="tunnel-id mono" aria-label="Connected Tunnel ID"></div><button id="copyTunnel" type="button">Copy Tunnel ID</button></div><div class="path"><strong>In ChatGPT:</strong> Settings → Connectors → <strong>Connection: Tunnel</strong> → select this Tunnel, or paste the Tunnel ID if asked.</div><p class="security-note">This is the current Connector/Tunnel flow, not the legacy plugin setup.</p><div class="actions"><a class="btn primary" rel="noreferrer" target="_blank" href="` + chatGPTConnectURL + `">Open ChatGPT Connectors</a><button id="finish" type="button">Finish setup</button></div></div></div></section>
<p class="foot">This one-time wizard runs only on 127.0.0.1 with a random port and one-time URL token. Independent community project; not an official OpenAI product.</p></main>
<script>
const token="__TOKEN__";const q=u=>u+(u.includes('?')?'&':'?')+'token='+encodeURIComponent(token);const $=id=>document.getElementById(id);const tunnelPattern=/^tunnel_[0-9a-f]{32}$/;let hasStoredRuntimeKey=false;
function statusItem(label,ok,okText,pendingText,attention){const item=document.createElement('div');item.className='status-item '+(ok?'ok':attention?'bad':'warn');const name=document.createElement('span');name.className='status-label';name.textContent=label;const value=document.createElement('span');value.className='status-value';value.textContent=ok?okText:pendingText;item.append(name,value);return item}
function renderStatus(s){$('status').replaceChildren(statusItem('GPT Agent runtime',s.runtimeHealthy,'Ready','Needs attention',true),statusItem('OpenAI tunnel client',s.tunnelClientPresent,'Installed','Needs attention',true),statusItem('Tunnel settings',s.profilePresent,'Saved','Not saved yet',false),statusItem('Runtime key',s.hasRuntimeKey,'Saved locally','Not saved yet',false),statusItem('Auto-start',s.tunnelTaskPresent,'Enabled','Not enabled yet',false),statusItem('Tunnel connection',s.tunnelReady,'Connected','Not connected yet',false))}
function setProgress(step){for(let i=1;i<=4;i++){const el=$('progress'+i);if(i===step)el.setAttribute('aria-current','step');else el.removeAttribute('aria-current')}}
function showMessage(text,kind){const el=$('saveMessage');el.textContent=text;el.className='notice'+(kind==='error'?' error':kind==='success'?' success-note':'')}
async function refresh(){try{const r=await fetch(q('/api/status'),{cache:'no-store'});if(!r.ok)throw new Error('status '+r.status);const s=await r.json();renderStatus(s);hasStoredRuntimeKey=Boolean(s.hasRuntimeKey);if(s.tunnelId&&!$('tunnelId').value)$('tunnelId').value=s.tunnelId;if(s.hasRuntimeKey)$('keyHint').textContent='A runtime key is already saved on this computer. Leave this field blank to reuse it, or paste a new key to replace it.'}catch(e){$('status').replaceChildren(statusItem('Local setup',false,'','Could not check this computer. Retry in a moment.',true))}}
function showSuccess(id){setProgress(4);$('finalTunnelId').textContent=id;$('finishCard').classList.remove('hidden');showMessage('Connection confirmed. Finish the last step in ChatGPT.','success');$('step4Title').focus()}
async function copyText(text,button){if(!text)return;const original=button.textContent;try{await navigator.clipboard.writeText(text);button.textContent='Copied';setTimeout(()=>{button.textContent=original},1400)}catch(e){showMessage('Could not copy automatically. Select the Tunnel ID and copy it manually.','error')}}
$('toggleKey').addEventListener('click',()=>{const input=$('apiKey');const showing=input.type==='text';input.type=showing?'password':'text';$('toggleKey').textContent=showing?'Show':'Hide';$('toggleKey').setAttribute('aria-pressed',String(!showing))});$('copyTunnel').addEventListener('click',()=>copyText($('finalTunnelId').textContent,$('copyTunnel')));
$('save').addEventListener('click',async()=>{const b=$('save');const tunnelID=$('tunnelId').value.trim();const apiKey=$('apiKey').value.trim();$('tunnelId').setAttribute('aria-invalid','false');$('apiKey').setAttribute('aria-invalid','false');if(!tunnelPattern.test(tunnelID)){$('tunnelId').setAttribute('aria-invalid','true');showMessage('Paste the Tunnel ID from OpenAI Platform. It must start with tunnel_ and contain the full 32-character ID.','error');$('tunnelId').focus();return}if(!apiKey&&!hasStoredRuntimeKey){$('apiKey').setAttribute('aria-invalid','true');showMessage('Paste the Restricted runtime API key from Step 2.','error');$('apiKey').focus();return}$('finishCard').classList.add('hidden');setProgress(3);b.disabled=true;b.textContent='Checking and connecting…';b.setAttribute('aria-busy','true');$('doctorWrap').classList.add('hidden');$('doctorWrap').removeAttribute('open');$('doctor').textContent='';showMessage('Checking the Tunnel, permissions, and local connection…','');try{const r=await fetch(q('/api/save'),{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({tunnelId:tunnelID,apiKey})});const d=await r.json();if(d.doctor){$('doctor').textContent=d.doctor;$('doctorWrap').classList.remove('hidden')}if(!r.ok||!d.ok){showMessage(d.message||'Connection failed. Check the values above and try again.','error');return}showSuccess(d.tunnelId);await refresh()}catch(e){showMessage('The local setup request failed. Make sure GPT Agent is still running, then try again.','error')}finally{$('apiKey').value='';$('apiKey').type='password';$('toggleKey').textContent='Show';$('toggleKey').setAttribute('aria-pressed','false');b.disabled=false;b.textContent='Save and connect';b.removeAttribute('aria-busy')}});
$('finish').addEventListener('click',async()=>{try{await fetch(q('/api/finish'),{method:'POST'})}catch(e){}$('finish').disabled=true;$('finish').textContent='Done. You can close this tab.'});refresh();setInterval(refresh,5000);
</script></body></html>`
