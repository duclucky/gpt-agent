package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type orderedJSONField struct {
	Key      string
	RawKey   []byte
	RawValue []byte
}

func parseOrderedJSONObject(line []byte) ([]orderedJSONField, error) {
	b := bytes.TrimSpace(line)
	if len(b) < 2 || b[0] != '{' || b[len(b)-1] != '}' {
		return nil, errors.New("audit record is not a JSON object")
	}
	i := 1
	fields := []orderedJSONField{}
	for {
		for i < len(b)-1 && (b[i] == ' ' || b[i] == '\t' || b[i] == '\r' || b[i] == '\n') {
			i++
		}
		if i >= len(b)-1 {
			break
		}
		if b[i] != '"' {
			return nil, fmt.Errorf("audit object key expected at byte %d", i)
		}
		keyStart := i
		i++
		escaped := false
		for i < len(b) {
			c := b[i]
			i++
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			if c == '"' {
				break
			}
		}
		if i > len(b) {
			return nil, errors.New("unterminated audit object key")
		}
		rawKey := append([]byte(nil), b[keyStart:i]...)
		var key string
		if json.Unmarshal(rawKey, &key) != nil {
			return nil, errors.New("invalid audit object key")
		}
		for i < len(b) && isJSONSpace(b[i]) {
			i++
		}
		if i >= len(b) || b[i] != ':' {
			return nil, errors.New("audit object missing colon")
		}
		i++
		for i < len(b) && isJSONSpace(b[i]) {
			i++
		}
		valueStart := i
		depth := 0
		inString := false
		escaped = false
		for i < len(b)-1 {
			c := b[i]
			if inString {
				if escaped {
					escaped = false
				} else if c == '\\' {
					escaped = true
				} else if c == '"' {
					inString = false
				}
				i++
				continue
			}
			switch c {
			case '"':
				inString = true
			case '{', '[':
				depth++
			case '}', ']':
				if depth > 0 {
					depth--
				}
			case ',':
				if depth == 0 {
					goto valueDone
				}
			}
			i++
		}
	valueDone:
		;
		rawValue := bytes.TrimSpace(b[valueStart:i])
		if len(rawValue) == 0 {
			return nil, fmt.Errorf("empty value for audit field %s", key)
		}
		fields = append(fields, orderedJSONField{Key: key, RawKey: rawKey, RawValue: append([]byte(nil), rawValue...)})
		for i < len(b) && isJSONSpace(b[i]) {
			i++
		}
		if i < len(b)-1 && b[i] == ',' {
			i++
			continue
		}
		break
	}
	return fields, nil
}
func isJSONSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\r' || b == '\n' }
func rebuildOrderedObject(fields []orderedJSONField, skip string, replacements map[string][]byte, appendFields []orderedJSONField) []byte {
	var out bytes.Buffer
	out.WriteByte('{')
	first := true
	for _, f := range fields {
		if f.Key == skip {
			continue
		}
		if !first {
			out.WriteByte(',')
		}
		first = false
		out.Write(f.RawKey)
		out.WriteByte(':')
		if v, ok := replacements[f.Key]; ok {
			out.Write(v)
		} else {
			out.Write(f.RawValue)
		}
	}
	for _, f := range appendFields {
		if !first {
			out.WriteByte(',')
		}
		first = false
		out.Write(f.RawKey)
		out.WriteByte(':')
		out.Write(f.RawValue)
	}
	out.WriteByte('}')
	return out.Bytes()
}

type auditVerifyResult struct {
	OK       bool
	Count    int
	LastHash string
	Reason   string
}

func verifyAuditRaw(raw []byte) auditVerifyResult {
	lines := bytes.Split(bytes.TrimSpace(raw), []byte("\n"))
	if len(bytes.TrimSpace(raw)) == 0 {
		return auditVerifyResult{OK: true}
	}
	prev := ""
	count := 0
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		fields, err := parseOrderedJSONObject(line)
		if err != nil {
			return auditVerifyResult{OK: false, Count: count, Reason: err.Error()}
		}
		var eventHash, eventID string
		var previous *string
		for _, f := range fields {
			switch f.Key {
			case "eventHash":
				_ = json.Unmarshal(f.RawValue, &eventHash)
			case "eventId":
				_ = json.Unmarshal(f.RawValue, &eventID)
			case "previousEventHash":
				if bytes.Equal(f.RawValue, []byte("null")) {
					previous = nil
				} else {
					var s string
					if json.Unmarshal(f.RawValue, &s) == nil {
						previous = &s
					}
				}
			}
		}
		expectedPrev := ""
		if previous != nil {
			expectedPrev = *previous
		}
		if expectedPrev != prev {
			return auditVerifyResult{OK: false, Count: count, Reason: fmt.Sprintf("Broken previous hash at %s", eventID)}
		}
		without := rebuildOrderedObject(fields, "eventHash", nil, nil)
		sum := sha256.Sum256(without)
		expected := hex.EncodeToString(sum[:])
		if expected != eventHash {
			return auditVerifyResult{OK: false, Count: count, Reason: fmt.Sprintf("Hash mismatch at %s", eventID)}
		}
		prev = eventHash
		count++
	}
	return auditVerifyResult{OK: true, Count: count, LastHash: prev}
}

func (n *nativeTools) auditTailTool(raw json.RawMessage) (map[string]any, error) {
	var a struct {
		Limit int `json:"limit"`
	}
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.Limit == 0 {
		a.Limit = 100
	}
	if a.Limit < 1 || a.Limit > 1000 {
		return nil, nativeToolError{"limit must be between 1 and 1000"}
	}
	buf, err := os.ReadFile(n.audit.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]any{"auditPath": n.audit.path, "events": []any{}}, nil
		}
		return nil, err
	}
	lines := bytes.Split(bytes.TrimSpace(buf), []byte("\n"))
	if len(lines) > a.Limit {
		lines = lines[len(lines)-a.Limit:]
	}
	events := make([]any, 0, len(lines))
	for _, line := range lines {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var v any
		if err := json.Unmarshal(line, &v); err != nil {
			return nil, err
		}
		events = append(events, v)
	}
	return map[string]any{"auditPath": n.audit.path, "events": events}, nil
}
func (n *nativeTools) auditVerifyTool() (map[string]any, error) {
	buf, err := os.ReadFile(n.audit.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]any{"ok": true, "count": 0, "lastHash": nil}, nil
		}
		return nil, err
	}
	r := verifyAuditRaw(buf)
	out := map[string]any{"ok": r.OK, "count": r.Count}
	if r.LastHash != "" {
		out["lastHash"] = r.LastHash
	} else {
		out["lastHash"] = nil
	}
	if r.Reason != "" {
		out["reason"] = r.Reason
	}
	return out, nil
}
func (n *nativeTools) auditRepairTool(raw json.RawMessage) (map[string]any, error) {
	var a struct {
		Confirm string `json:"confirm"`
	}
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.Confirm != "REPAIR" {
		return nil, nativeToolError{"Audit repair requires confirm='REPAIR'."}
	}
	return n.repairAuditChain()
}

func (n *nativeTools) repairAuditChain() (map[string]any, error) {
	a := n.audit
	a.mu.Lock()
	release, err := a.acquireLock()
	if err != nil {
		a.mu.Unlock()
		return nil, err
	}
	buf, readErr := os.ReadFile(a.path)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		release()
		a.mu.Unlock()
		return nil, readErr
	}
	before := verifyAuditRaw(buf)
	if before.OK {
		release()
		a.mu.Unlock()
		return map[string]any{"repaired": false, "reason": "audit chain already valid", "verify": map[string]any{"ok": true, "count": before.Count, "lastHash": nilIfEmpty(before.LastHash)}}, nil
	}
	backupDir := filepath.Join(filepath.Dir(a.path), "audit-repair-backups")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		release()
		a.mu.Unlock()
		return nil, err
	}
	sum := sha256.Sum256(buf)
	backupHash := hex.EncodeToString(sum[:])
	stamp := strings.NewReplacer(":", "-", ".", "-").Replace(time.Now().UTC().Format(time.RFC3339Nano))
	backupPath := filepath.Join(backupDir, stamp+"-"+backupHash[:12]+".audit.jsonl")
	if err := os.WriteFile(backupPath, buf, 0644); err != nil {
		release()
		a.mu.Unlock()
		return nil, err
	}
	lines := bytes.Split(bytes.TrimSpace(buf), []byte("\n"))
	prev := ""
	changed := 0
	repaired := make([][]byte, 0, len(lines))
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		fields, e := parseOrderedJSONObject(line)
		if e != nil {
			release()
			a.mu.Unlock()
			return nil, e
		}
		var oldPrev, oldHash string
		hadPrev := false
		for _, f := range fields {
			switch f.Key {
			case "previousEventHash":
				hadPrev = true
				if !bytes.Equal(f.RawValue, []byte("null")) {
					_ = json.Unmarshal(f.RawValue, &oldPrev)
				}
			case "eventHash":
				_ = json.Unmarshal(f.RawValue, &oldHash)
			}
		}
		prevRaw := []byte("null")
		if prev != "" {
			prevRaw, _ = json.Marshal(prev)
		}
		repl := map[string][]byte{"previousEventHash": prevRaw}
		baseFields := fields
		if !hadPrev {
			baseFields = append(baseFields, orderedJSONField{Key: "previousEventHash", RawKey: []byte(`"previousEventHash"`), RawValue: prevRaw})
		}
		without := rebuildOrderedObject(baseFields, "eventHash", repl, nil)
		h := sha256.Sum256(without)
		newHash := hex.EncodeToString(h[:])
		if oldPrev != prev || oldHash != newHash {
			changed++
		}
		hashRaw, _ := json.Marshal(newHash)
		full := rebuildOrderedObject(baseFields, "eventHash", repl, []orderedJSONField{{Key: "eventHash", RawKey: []byte(`"eventHash"`), RawValue: hashRaw}})
		repaired = append(repaired, full)
		prev = newHash
	}
	var repairedBuf []byte
	if len(repaired) > 0 {
		repairedBuf = append(bytes.Join(repaired, []byte("\n")), '\n')
	}
	tmp := a.path + "." + randomHex(8) + ".tmp"
	if err := os.WriteFile(tmp, repairedBuf, 0644); err != nil {
		release()
		a.mu.Unlock()
		return nil, err
	}
	if err := os.Rename(tmp, a.path); err != nil {
		release()
		a.mu.Unlock()
		return nil, err
	}
	release()
	a.mu.Unlock()
	event, err := a.append(map[string]any{"type": "audit.chain.repair", "originalCount": len(repaired), "changedRecords": changed, "originalBackupPath": backupPath, "originalBackupSha256": backupHash, "previousVerifyReason": before.Reason})
	if err != nil {
		return nil, err
	}
	afterBuf, err := os.ReadFile(a.path)
	if err != nil {
		return nil, err
	}
	after := verifyAuditRaw(afterBuf)
	if !after.OK {
		return nil, fmt.Errorf("Audit chain repair did not verify: %s", after.Reason)
	}
	return map[string]any{"repaired": true, "originalCount": len(repaired), "changedRecords": changed, "backupPath": backupPath, "backupSha256": backupHash, "repairEventId": event["eventId"], "verify": map[string]any{"ok": after.OK, "count": after.Count, "lastHash": after.LastHash}}, nil
}
