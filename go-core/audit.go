package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	auditLockTimeout = 15 * time.Second
	auditLockStale   = 120 * time.Second
)

type auditWriter struct {
	path     string
	lockPath string
	hostname string
	mu       sync.Mutex
}

type auditLockOwner struct {
	Token      string `json:"token"`
	PID        int    `json:"pid"`
	Hostname   string `json:"hostname"`
	AcquiredAt string `json:"acquiredAt"`
}

func newAuditWriter(dataRoot string) *auditWriter {
	hostname, _ := os.Hostname()
	path := filepath.Join(dataRoot, "audit.jsonl")
	return &auditWriter{path: path, lockPath: path + ".lock", hostname: hostname}
}

func (a *auditWriter) append(event map[string]any) (map[string]any, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	release, err := a.acquireLock()
	if err != nil {
		return nil, err
	}
	defer release()

	previousHash, err := discoverAuditTailHash(a.path)
	if err != nil {
		previousHash, err = a.recoverCorruptTail(err)
		if err != nil {
			return nil, err
		}
	}

	record := make(map[string]any, len(event)+4)
	for key, value := range event {
		switch key {
		case "eventId", "timestamp", "previousEventHash", "eventHash":
			continue
		default:
			record[key] = value
		}
	}
	record["eventId"] = "evt_" + randomHex(8)
	record["timestamp"] = time.Now().UTC().Format(time.RFC3339Nano)
	if previousHash == "" {
		record["previousEventHash"] = nil
	} else {
		record["previousEventHash"] = previousHash
	}

	hash, err := hashAuditRecord(record)
	if err != nil {
		return nil, err
	}
	record["eventHash"] = hash
	serialized, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	serialized = append(serialized, '\n')
	if err := os.MkdirAll(filepath.Dir(a.path), 0o755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(a.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	_, writeErr := file.Write(serialized)
	closeErr := file.Close()
	if writeErr != nil {
		return nil, writeErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return record, nil
}

func (a *auditWriter) recoverCorruptTail(cause error) (string, error) {
	raw, err := os.ReadFile(a.path)
	if err != nil {
		return "", err
	}
	if len(raw) == 0 {
		return "", cause
	}

	validEnd := 0
	previousHash := ""
	for offset := 0; offset < len(raw); {
		relativeEnd := bytes.IndexByte(raw[offset:], '\n')
		lineEnd := len(raw)
		if relativeEnd >= 0 {
			lineEnd = offset + relativeEnd + 1
		}
		line := bytes.TrimSpace(raw[offset:lineEnd])
		if len(line) == 0 {
			validEnd = lineEnd
			offset = lineEnd
			continue
		}
		var record map[string]any
		if json.Unmarshal(line, &record) != nil {
			break
		}
		hash, _ := record["eventHash"].(string)
		if strings.TrimSpace(hash) == "" {
			break
		}
		previousHash = hash
		validEnd = lineEnd
		offset = lineEnd
	}
	if validEnd >= len(raw) {
		return "", cause
	}

	backupDir := filepath.Join(filepath.Dir(a.path), "audit-corruption-backups")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return "", err
	}
	backupName := time.Now().UTC().Format("20060102T150405.000000000Z") + "-" + randomHex(4) + ".audit.jsonl"
	backupPath := filepath.Join(backupDir, backupName)
	if err := os.WriteFile(backupPath, raw, 0o644); err != nil {
		return "", err
	}

	recovery := map[string]any{
		"type":             "audit.tail.recovery",
		"eventId":          "evt_" + randomHex(8),
		"timestamp":        time.Now().UTC().Format(time.RFC3339Nano),
		"corruptTailBytes": len(raw) - validEnd,
		"backupFile":       backupName,
	}
	if previousHash == "" {
		recovery["previousEventHash"] = nil
	} else {
		recovery["previousEventHash"] = previousHash
	}
	recoveryHash, err := hashAuditRecord(recovery)
	if err != nil {
		return "", err
	}
	recovery["eventHash"] = recoveryHash
	encodedRecovery, err := json.Marshal(recovery)
	if err != nil {
		return "", err
	}

	recovered := append([]byte(nil), raw[:validEnd]...)
	if len(recovered) > 0 && recovered[len(recovered)-1] != '\n' {
		recovered = append(recovered, '\n')
	}
	recovered = append(recovered, encodedRecovery...)
	recovered = append(recovered, '\n')
	file, err := os.OpenFile(a.path, os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", err
	}
	_, writeErr := file.Write(recovered)
	closeErr := file.Close()
	if writeErr != nil {
		return "", writeErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	return recoveryHash, nil
}

func (a *auditWriter) acquireLock() (func(), error) {
	if err := os.MkdirAll(filepath.Dir(a.path), 0o755); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(auditLockTimeout)
	for {
		token := randomHex(16)
		err := os.Mkdir(a.lockPath, 0o755)
		if err == nil {
			owner := auditLockOwner{
				Token:      token,
				PID:        os.Getpid(),
				Hostname:   a.hostname,
				AcquiredAt: time.Now().UTC().Format(time.RFC3339Nano),
			}
			encoded, marshalErr := json.Marshal(owner)
			if marshalErr == nil {
				marshalErr = os.WriteFile(filepath.Join(a.lockPath, "owner.json"), encoded, 0o644)
			}
			if marshalErr != nil {
				_ = os.RemoveAll(a.lockPath)
				return nil, marshalErr
			}
			return func() { _ = a.releaseLock(token) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		stale, staleErr := a.staleLock()
		if staleErr == nil && stale {
			_ = os.RemoveAll(a.lockPath)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out waiting for audit lock: %s", a.lockPath)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (a *auditWriter) releaseLock(token string) error {
	owner, err := readAuditLockOwner(a.lockPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if owner.Token != token {
		return nil
	}
	return os.RemoveAll(a.lockPath)
}

func (a *auditWriter) staleLock() (bool, error) {
	info, err := os.Stat(a.lockPath)
	if err != nil {
		return false, err
	}
	owner, ownerErr := readAuditLockOwner(a.lockPath)
	if ownerErr == nil && owner.Hostname == a.hostname && owner.PID > 0 {
		alive, aliveErr := processAlive(owner.PID)
		if aliveErr == nil {
			return !alive, nil
		}
		return false, nil
	}
	return time.Since(info.ModTime()) > auditLockStale, nil
}

func readAuditLockOwner(lockPath string) (auditLockOwner, error) {
	var owner auditLockOwner
	buf, err := os.ReadFile(filepath.Join(lockPath, "owner.json"))
	if err != nil {
		return owner, err
	}
	if err := json.Unmarshal(buf, &owner); err != nil {
		return owner, err
	}
	return owner, nil
}

func discoverAuditTailHash(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if info.Size() == 0 {
		return "", nil
	}

	end := info.Size()
	one := []byte{0}
	for end > 0 {
		if _, err := file.ReadAt(one, end-1); err != nil {
			return "", err
		}
		if one[0] != '\n' && one[0] != '\r' {
			break
		}
		end--
	}
	if end == 0 {
		return "", nil
	}

	const chunkSize int64 = 64 * 1024
	position := end
	chunks := make([][]byte, 0, 2)
	for position > 0 {
		start := position - chunkSize
		if start < 0 {
			start = 0
		}
		buf := make([]byte, position-start)
		n, readErr := file.ReadAt(buf, start)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return "", readErr
		}
		buf = buf[:n]
		if idx := bytes.LastIndexByte(buf, '\n'); idx >= 0 {
			chunks = append([][]byte{append([]byte(nil), buf[idx+1:]...)}, chunks...)
			break
		}
		chunks = append([][]byte{append([]byte(nil), buf...)}, chunks...)
		position = start
	}
	line := strings.TrimSpace(string(bytes.Join(chunks, nil)))
	if line == "" {
		return "", nil
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(line), &record); err != nil {
		return "", err
	}
	hash, _ := record["eventHash"].(string)
	return hash, nil
}

func hashAuditRecord(record map[string]any) (string, error) {
	copyRecord := make(map[string]any, len(record))
	for key, value := range record {
		if key != "eventHash" {
			copyRecord[key] = value
		}
	}
	encoded, err := json.Marshal(copyRecord)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func hashRawJSON(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		trimmed = []byte("{}")
	}
	var value any
	if json.Unmarshal(trimmed, &value) == nil {
		if normalized, err := json.Marshal(value); err == nil {
			trimmed = normalized
		}
	}
	sum := sha256.Sum256(trimmed)
	return hex.EncodeToString(sum[:])
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buf)
}
