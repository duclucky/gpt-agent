package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const runtimeVersion = "0.1.0"

type workspaceConfig struct {
	ID      string `json:"id"`
	Root    string `json:"root"`
	Enabled *bool  `json:"enabled,omitempty"`
}

type lspServerConfig struct {
	Extensions            []string       `json:"extensions"`
	ServerID              string         `json:"serverId"`
	Command               string         `json:"command"`
	Args                  []string       `json:"args"`
	LanguageID            string         `json:"languageId"`
	InitializationOptions map[string]any `json:"initializationOptions,omitempty"`
}

type debugAdapterConfig struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

type runtimeConfig struct {
	Server struct {
		Host               string `json:"host"`
		Port               int    `json:"port"`
		MaxToolOutputChars int    `json:"maxToolOutputChars"`
	} `json:"server"`
	Security struct {
		AllowSecretFiles                   bool     `json:"allowSecretFiles"`
		SecretFilePatterns                 []string `json:"secretFilePatterns"`
		HTTPAllowedHosts                   []string `json:"httpAllowedHosts"`
		SafeNetworkDefault                 bool     `json:"safeNetworkDefault"`
		MaskSecretFilesInSafeProcesses     *bool    `json:"maskSecretFilesInSafeProcesses,omitempty"`
		RequireProjectCwdForDriveWorkspace *bool    `json:"requireProjectCwdForDriveWorkspace,omitempty"`
		WindowsSandboxMemoryMB             int      `json:"windowsSandboxMemoryMb"`
	} `json:"security"`
	Workspaces    []workspaceConfig             `json:"workspaces"`
	SafeCommands  map[string][]string           `json:"safeCommands"`
	LSPServers    map[string]lspServerConfig    `json:"lspServers"`
	DebugAdapters map[string]debugAdapterConfig `json:"debugAdapters"`
}

func main() {
	if err := run(); err != nil {
		log.Printf("fatal: %v", err)
		os.Exit(1)
	}
}

func run() error {
	logOutput, closeLog, err := configureRuntimeLog()
	if err != nil {
		return err
	}
	defer closeLog()
	log.SetOutput(logOutput)

	appRoot, err := resolveAppRoot()
	if err != nil {
		return err
	}
	cfg, configPath, err := loadRuntimeConfig(appRoot)
	if err != nil {
		return err
	}
	if err := normalizeNativeConfig(&cfg); err != nil {
		return fmt.Errorf("normalize runtime config: %w", err)
	}
	dataRoot, err := resolveDataRoot(appRoot)
	if err != nil {
		return err
	}
	catalog, err := loadToolCatalog()
	if err != nil {
		return err
	}
	if len(catalog.Tools) != 83 {
		return fmt.Errorf("embedded tool catalog has %d tools; expected 83", len(catalog.Tools))
	}
	if cfg.Server.Host == "" {
		cfg.Server.Host = "127.0.0.1"
	}
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8765
	}
	if !isLoopbackHost(cfg.Server.Host) {
		return fmt.Errorf("refuse non-loopback server host %q", cfg.Server.Host)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	auditLog := newAuditWriter(dataRoot)
	native := newNativeTools(cfg, auditLog, appRoot, dataRoot, configPath)
	if got := len(native.names()); got != 79 {
		return fmt.Errorf("Go-native tool dispatcher has %d non-job tools; expected 79", got)
	}
	jobs := newJobManager(native.invoke, native.supports)
	gateway := newGateway(jobs, native, catalog, configPath)
	server := &http.Server{
		Addr:              net.JoinHostPort(cfg.Server.Host, strconv.Itoa(cfg.Server.Port)),
		Handler:           gateway.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
	}

	closeManagers := func() {
		native.dap.close()
		native.lsp.close()
		native.processes.close()
	}
	defer closeManagers()

	listenErr := make(chan error, 1)
	go func() {
		log.Printf("GPT Agent v%s (Go %s/%s, 83 MCP tools, direct ChatGPT local developer runtime)", runtimeVersion, runtime.GOOS, runtime.GOARCH)
		log.Printf("MCP:    http://%s/mcp", server.Addr)
		log.Printf("Health: http://%s/healthz", server.Addr)
		listenErr <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = server.Shutdown(shutdownCtx)
		return nil
	case err := <-listenErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func configureRuntimeLog() (io.Writer, func(), error) {
	logPath := strings.TrimSpace(os.Getenv("GPT_AGENT_LOG_PATH"))
	if logPath == "" {
		return os.Stdout, func() {}, nil
	}
	absPath, err := filepath.Abs(logPath)
	if err != nil {
		return nil, func() {}, fmt.Errorf("resolve runtime log path %q: %w", logPath, err)
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return nil, func() {}, fmt.Errorf("create runtime log directory: %w", err)
	}
	if err := rotateLegacyUTF16Log(absPath); err != nil {
		return nil, func() {}, err
	}
	file, err := os.OpenFile(absPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, func() {}, fmt.Errorf("open runtime log %s: %w", absPath, err)
	}
	return file, func() { _ = file.Close() }, nil
}

func rotateLegacyUTF16Log(logPath string) error {
	file, err := os.Open(logPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("inspect runtime log %s: %w", logPath, err)
	}
	var bom [2]byte
	n, readErr := file.Read(bom[:])
	closeErr := file.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return fmt.Errorf("inspect runtime log encoding %s: %w", logPath, readErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close runtime log probe %s: %w", logPath, closeErr)
	}
	if n < len(bom) || bom != [2]byte{0xFF, 0xFE} {
		return nil
	}

	legacyPath := logPath + ".legacy-utf16"
	if _, statErr := os.Stat(legacyPath); statErr == nil {
		legacyPath += "." + time.Now().UTC().Format("20060102T150405.000000000Z")
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("inspect legacy runtime log target %s: %w", legacyPath, statErr)
	}
	if err := os.Rename(logPath, legacyPath); err != nil {
		return fmt.Errorf("preserve legacy UTF-16 runtime log as %s: %w", legacyPath, err)
	}
	return nil
}

func resolveAppRoot() (string, error) {
	if root := strings.TrimSpace(os.Getenv("GPT_AGENT_APP_ROOT")); root != "" {
		return filepath.Abs(root)
	}
	return os.Getwd()
}

func resolveDataRoot(appRoot string) (string, error) {
	dataRoot := strings.TrimSpace(os.Getenv("GPT_AGENT_HOME"))
	if dataRoot == "" {
		dataRoot = filepath.Join(appRoot, "data")
	}
	return filepath.Abs(dataRoot)
}

func loadRuntimeConfig(appRoot string) (runtimeConfig, string, error) {
	var cfg runtimeConfig
	dataRoot, err := resolveDataRoot(appRoot)
	if err != nil {
		return cfg, "", err
	}
	configPath := strings.TrimSpace(os.Getenv("GPT_AGENT_CONFIG"))
	if configPath == "" {
		configPath = filepath.Join(dataRoot, "config.json")
	}
	buf, err := os.ReadFile(configPath)
	if err != nil {
		return cfg, configPath, fmt.Errorf("read config %s: %w", configPath, err)
	}
	if err := json.Unmarshal(buf, &cfg); err != nil {
		return cfg, configPath, fmt.Errorf("parse config %s: %w", configPath, err)
	}
	return cfg, configPath, nil
}

func isLoopbackHost(host string) bool {
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "127.0.0.1", "localhost", "::1":
		return true
	default:
		return false
	}
}
