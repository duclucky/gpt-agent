package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTunnelIDFormat(t *testing.T) {
	valid := "tunnel_0123456789abcdef0123456789abcdef"
	if !tunnelIDRE.MatchString(valid) {
		t.Fatalf("expected valid tunnel id: %s", valid)
	}
	for _, invalid := range []string{
		"tunnel_1234",
		"tunnel_0123456789ABCDEF0123456789ABCDEF",
		"0123456789abcdef0123456789abcdef",
		"tunnel_0123456789abcdef0123456789abcdeg",
	} {
		if tunnelIDRE.MatchString(invalid) {
			t.Fatalf("unexpected valid tunnel id: %s", invalid)
		}
	}
}

func TestSanitizeOutput(t *testing.T) {
	secret := "rk-" + "example-secret-value-1234567890"
	other := "sk-" + "example-other-secret-123456"
	got := sanitizeOutput("before "+secret+" after "+other, secret)
	if strings.Contains(got, secret) || strings.Contains(got, other) {
		t.Fatalf("secret-like value leaked: %q", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("expected redaction marker: %q", got)
	}
}

func TestReadTunnelID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profile.yaml")
	want := "tunnel_0123456789abcdef0123456789abcdef"
	if err := os.WriteFile(path, []byte("control_plane:\n  tunnel_id: "+want+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readTunnelID(path); got != want {
		t.Fatalf("readTunnelID() = %q, want %q", got, want)
	}
}

func TestAuthorizedRequiresLoopbackTokenAndSameOrigin(t *testing.T) {
	a := &app{token: "abc"}
	req := httptest.NewRequest("GET", "http://127.0.0.1:9999/?token=abc", nil)
	req.RemoteAddr = "127.0.0.1:32100"
	if !a.authorized(req) {
		t.Fatal("expected loopback request with token to be authorized")
	}

	badToken := httptest.NewRequest("GET", "http://127.0.0.1:9999/?token=nope", nil)
	badToken.RemoteAddr = "127.0.0.1:32100"
	if a.authorized(badToken) {
		t.Fatal("wrong token should be rejected")
	}

	badOrigin := httptest.NewRequest("POST", "http://127.0.0.1:9999/api/save?token=abc", nil)
	badOrigin.RemoteAddr = "127.0.0.1:32100"
	badOrigin.Host = "127.0.0.1:9999"
	badOrigin.Header.Set("Origin", "https://example.invalid")
	if a.authorized(badOrigin) {
		t.Fatal("cross-origin request should be rejected")
	}
}
