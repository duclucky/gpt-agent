package main

import (
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	learningEvolutionVersion = 1
	maxLearningHistory       = 240
	maxLearningCandidates    = 240
	maxFailureClasses        = 8
)

type learningTaskSession struct {
	ID                   string   `json:"id"`
	WorkspaceID          string   `json:"workspaceId"`
	Path                 string   `json:"path"`
	Scope                string   `json:"scope"`
	Task                 string   `json:"task"`
	TaskFingerprint      string   `json:"taskFingerprint"`
	SelectedSkills       []string `json:"selectedSkills"`
	StartedAt            string   `json:"startedAt"`
	UpdatedAt            string   `json:"updatedAt"`
	LastMutationAt       string   `json:"lastMutationAt,omitempty"`
	LastVerifiedAt       string   `json:"lastVerifiedAt,omitempty"`
	ToolSuccesses        int      `json:"toolSuccesses"`
	ToolErrors           int      `json:"toolErrors"`
	VerificationPasses   int      `json:"verificationPasses"`
	VerificationFailures int      `json:"verificationFailures"`
	Mutations            int      `json:"mutations"`
	EventCount           int      `json:"eventCount"`
	FailureClasses       []string `json:"failureClasses,omitempty"`
}

type learningTaskOutcome struct {
	ID                   string   `json:"id"`
	WorkspaceID          string   `json:"workspaceId"`
	Path                 string   `json:"path"`
	Scope                string   `json:"scope"`
	Task                 string   `json:"task"`
	TaskFingerprint      string   `json:"taskFingerprint"`
	SelectedSkills       []string `json:"selectedSkills"`
	StartedAt            string   `json:"startedAt"`
	CompletedAt          string   `json:"completedAt"`
	Outcome              string   `json:"outcome"`
	Verified             bool     `json:"verified"`
	ToolSuccesses        int      `json:"toolSuccesses"`
	ToolErrors           int      `json:"toolErrors"`
	VerificationPasses   int      `json:"verificationPasses"`
	VerificationFailures int      `json:"verificationFailures"`
	Mutations            int      `json:"mutations"`
	FailureClasses       []string `json:"failureClasses,omitempty"`
}

type learningCandidate struct {
	ID              string         `json:"id"`
	Type            string         `json:"type"`
	Scope           string         `json:"scope"`
	Task            string         `json:"task"`
	TaskFingerprint string         `json:"taskFingerprint"`
	Confidence      float64        `json:"confidence"`
	Occurrences     int            `json:"occurrences"`
	Status          string         `json:"status"`
	Evidence        map[string]any `json:"evidence"`
	CreatedAt       string         `json:"createdAt"`
	UpdatedAt       string         `json:"updatedAt"`
}

type skillOutcomeStats struct {
	Successes    int    `json:"successes"`
	Failures     int    `json:"failures"`
	Corrections  int    `json:"corrections"`
	Incomplete   int    `json:"incomplete"`
	LastOutcome  string `json:"lastOutcome,omitempty"`
	LastUpdateAt string `json:"lastUpdateAt,omitempty"`
}

type learningEvolutionDB struct {
	Version    int                          `json:"version"`
	Active     *learningTaskSession         `json:"active,omitempty"`
	History    []learningTaskOutcome        `json:"history"`
	Candidates []learningCandidate          `json:"candidates"`
	Skills     map[string]skillOutcomeStats `json:"skills"`
}

type learningEvolution struct {
	mu   sync.Mutex
	path string
	db   learningEvolutionDB
}

var learningSecretAssignment = regexp.MustCompile(`(?i)\b(api[_-]?key|access[_-]?token|refresh[_-]?token|token|password|passwd|secret|private[_-]?key)\s*[:=]\s*([^\s,;]+)`)
var learningAuthorizationBearer = regexp.MustCompile(`(?i)\b(authorization\s*[:=]\s*bearer)\s+[^\s,;]+`)
var learningKnownToken = regexp.MustCompile(`(?i)\b(?:sk-|rk-|ghp_|github_pat_|xox[baprs]-)[A-Za-z0-9_\-]{8,}`)
var learningPrivateKeyBlock = regexp.MustCompile(`(?is)-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----.*?-----END (?:RSA |EC |OPENSSH )?PRIVATE KEY-----`)

func newLearningEvolution(dataRoot string) *learningEvolution {
	e := &learningEvolution{path: filepath.Join(dataRoot, "learning", "evolution.json")}
	e.db = learningEvolutionDB{Version: learningEvolutionVersion, History: []learningTaskOutcome{}, Candidates: []learningCandidate{}, Skills: map[string]skillOutcomeStats{}}
	var loaded learningEvolutionDB
	if readJSONFile(e.path, &loaded, `{"version":1,"history":[],"candidates":[],"skills":{}}`) == nil {
		if loaded.Version == 0 {
			loaded.Version = learningEvolutionVersion
		}
		if loaded.History == nil {
			loaded.History = []learningTaskOutcome{}
		}
		if loaded.Candidates == nil {
			loaded.Candidates = []learningCandidate{}
		}
		if loaded.Skills == nil {
			loaded.Skills = map[string]skillOutcomeStats{}
		}
		e.db = loaded
	}
	return e
}

func sanitizeLearningTask(task string) string {
	v := normalizeText(task)
	if v == "" {
		return ""
	}
	v = learningPrivateKeyBlock.ReplaceAllString(v, "[REDACTED_PRIVATE_KEY]")
	v = learningAuthorizationBearer.ReplaceAllString(v, `$1 [REDACTED]`)
	v = learningSecretAssignment.ReplaceAllString(v, `$1=[REDACTED]`)
	v = learningKnownToken.ReplaceAllString(v, "[REDACTED_TOKEN]")
	runes := []rune(v)
	if len(runes) > 1800 {
		v = string(runes[:1800]) + "…"
	}
	return v
}

func correctionLikeTask(task string) bool {
	v := normalizeSearch(task)
	phrases := []string{
		"sai roi", "khong dung", "van loi", "lam lai", "sua lai", "khong on", "chua dung", "van sai", "xau qua",
		"still broken", "still failing", "not correct", "wrong result", "redo", "regression", "did not fix", "does not work",
	}
	for _, p := range phrases {
		if strings.Contains(v, p) {
			return true
		}
	}
	return false
}

func (e *learningEvolution) saveLocked() error {
	e.db.Version = learningEvolutionVersion
	return writeJSONAtomic(e.path, &e.db)
}

func (e *learningEvolution) beginTask(workspaceID, path, scope, task string, selectedSkills []string) map[string]any {
	e.mu.Lock()
	defer e.mu.Unlock()

	task = sanitizeLearningTask(task)
	if task == "" {
		return map[string]any{"active": false, "automaticCapture": true}
	}
	scope = normalizeScope(scope)
	fp := shaHex(scope + "\n" + normalizeSearch(task))
	selectedSkills = uniqueNormalized(selectedSkills)

	if e.db.Active != nil && e.db.Active.Scope == scope && e.db.Active.TaskFingerprint == fp {
		e.db.Active.SelectedSkills = uniqueNormalized(append(e.db.Active.SelectedSkills, selectedSkills...))
		e.db.Active.UpdatedAt = nowISO()
		_ = e.saveLocked()
		return e.sessionSummaryLocked(e.db.Active)
	}

	correction := correctionLikeTask(task)
	if e.db.Active != nil {
		e.finalizeActiveLocked(correction && e.db.Active.Scope == scope)
	} else if correction {
		e.applyCorrectionToRecentLocked(scope)
	}

	now := nowISO()
	e.db.Active = &learningTaskSession{
		ID:              uuidV4(),
		WorkspaceID:     workspaceID,
		Path:            firstNonEmpty(path, "."),
		Scope:           scope,
		Task:            task,
		TaskFingerprint: fp,
		SelectedSkills:  selectedSkills,
		StartedAt:       now,
		UpdatedAt:       now,
	}
	_ = e.saveLocked()
	return e.sessionSummaryLocked(e.db.Active)
}

func (e *learningEvolution) applyCorrectionToRecentLocked(scope string) {
	for i := len(e.db.History) - 1; i >= 0; i-- {
		h := &e.db.History[i]
		if h.Scope != scope || h.Outcome == "corrected" {
			continue
		}
		completed, err := time.Parse(time.RFC3339Nano, h.CompletedAt)
		if err != nil || time.Since(completed) > 48*time.Hour {
			return
		}
		h.Outcome = "corrected"
		h.Verified = false
		for _, slug := range h.SelectedSkills {
			s := e.db.Skills[slug]
			if s.Successes > 0 {
				s.Successes--
			}
			s.Failures++
			s.Corrections++
			s.LastOutcome = "corrected"
			s.LastUpdateAt = nowISO()
			e.db.Skills[slug] = s
		}
		e.upsertCandidateLocked("correction-review", *h, .92, map[string]any{
			"selectedSkills": h.SelectedSkills,
			"reason":         "A follow-up task strongly resembles an explicit correction of the recent outcome.",
		})
		return
	}
}

func (e *learningEvolution) observeTool(name string, raw json.RawMessage, payload any, toolErr error) {
	if e == nil || ignoreEvolutionTool(name) {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.db.Active == nil {
		return
	}
	s := e.db.Active
	s.EventCount++
	s.UpdatedAt = nowISO()
	meaningful := false

	if toolErr != nil {
		s.ToolErrors++
		s.FailureClasses = appendUniqueLimited(s.FailureClasses, classifyLearningFailure(toolErr), maxFailureClasses)
		meaningful = true
	} else {
		s.ToolSuccesses++
	}

	if mutationSignal(name, raw) && toolErr == nil {
		s.Mutations++
		s.LastMutationAt = nowISO()
		s.LastVerifiedAt = ""
		meaningful = true
	}

	if isVerify, passed := verificationSignal(name, raw, payload, toolErr); isVerify {
		meaningful = true
		if passed {
			s.VerificationPasses++
			s.LastVerifiedAt = nowISO()
		} else {
			s.VerificationFailures++
			s.LastVerifiedAt = ""
		}
	}

	if meaningful || s.EventCount%10 == 0 {
		_ = e.saveLocked()
	}
}

func ignoreEvolutionTool(name string) bool {
	if strings.HasPrefix(name, "gpt_agent_memory_") || strings.HasPrefix(name, "gpt_agent_skill_") || strings.HasPrefix(name, "gpt_agent_learning_") {
		return true
	}
	switch name {
	case "gpt_agent_learn", "gpt_agent_coding_skills_install", "gpt_agent_coding_brief", "gpt_agent_audit_tail", "gpt_agent_audit_verify":
		return true
	}
	return false
}

func mutationSignal(name string, raw json.RawMessage) bool {
	switch name {
	case "gpt_agent_write_file", "gpt_agent_replace_text", "gpt_agent_make_directory", "gpt_agent_move_path", "gpt_agent_delete_path", "gpt_agent_apply_git_patch", "gpt_agent_project_checkpoint_restore":
		return true
	case "gpt_agent_full_shell":
		var a struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(raw, &a) != nil {
			return false
		}
		return shellLikelyMutates(a.Command)
	case "gpt_agent_run_command":
		if looksLikeVerificationCommand(raw) {
			return false
		}
		var a struct {
			Executable string   `json:"executable"`
			Args       []string `json:"args"`
		}
		if json.Unmarshal(raw, &a) != nil {
			return false
		}
		return commandLikelyMutates(a.Executable, a.Args)
	}
	return false
}

func shellLikelyMutates(command string) bool {
	v := strings.ToLower(command)
	markers := []string{
		"set-content", "add-content", "out-file", "writealltext", "writealllines", "remove-item", "move-item", "copy-item", "rename-item", "new-item",
		"git add", "git commit", "git reset", "git checkout", "git restore", "git clean", "git mv", "git rm",
		"go fmt", "cargo fmt", "npm install", "npm ci", "pnpm install", "yarn install",
		"sed -i", "perl -pi", " rm ", " mv ", " cp ", " mkdir ", " touch ",
	}
	padded := " " + strings.Join(strings.Fields(v), " ") + " "
	for _, marker := range markers {
		if strings.Contains(padded, marker) {
			return true
		}
	}
	if strings.Contains(padded, " gofmt ") && strings.Contains(padded, " -w ") {
		return true
	}
	if strings.Contains(v, ">") && !strings.Contains(v, "2>") {
		return true
	}
	return false
}

func commandLikelyMutates(executable string, args []string) bool {
	exe := strings.ToLower(filepath.Base(strings.TrimSpace(executable)))
	joined := strings.ToLower(strings.Join(args, " "))
	switch strings.TrimSuffix(exe, ".exe") {
	case "gofmt":
		return strings.Contains(" "+joined+" ", " -w ")
	case "go":
		return strings.HasPrefix(joined, "fmt ") || strings.HasPrefix(joined, "generate ") || strings.HasPrefix(joined, "mod tidy")
	case "git":
		for _, verb := range []string{"add", "commit", "reset", "checkout", "restore", "clean", "mv", "rm", "merge", "rebase", "cherry-pick"} {
			if strings.HasPrefix(joined, verb+" ") || joined == verb {
				return true
			}
		}
	case "npm", "npm.cmd", "pnpm", "pnpm.cmd", "yarn", "yarn.cmd":
		return strings.HasPrefix(joined, "install") || strings.HasPrefix(joined, "ci") || strings.HasPrefix(joined, "update")
	case "rm", "del", "erase", "mv", "cp", "mkdir", "touch":
		return true
	}
	return false
}

func verificationSignal(name string, raw json.RawMessage, payload any, toolErr error) (bool, bool) {
	switch name {
	case "gpt_agent_project_verify":
		if toolErr != nil {
			return true, false
		}
		if m, ok := payload.(map[string]any); ok {
			passed, _ := m["passed"].(bool)
			return true, passed
		}
		return true, false
	case "gpt_agent_self_check":
		if toolErr != nil {
			return true, false
		}
		if m, ok := payload.(map[string]any); ok {
			okValue, _ := m["ok"].(bool)
			return true, okValue
		}
		return true, false
	case "gpt_agent_run_command":
		if !looksLikeVerificationCommand(raw) {
			return false, false
		}
		if toolErr != nil {
			return true, false
		}
		return true, extractLearningExitCode(payload) == 0
	}
	return false, false
}

func looksLikeVerificationCommand(raw json.RawMessage) bool {
	var a struct {
		Executable string   `json:"executable"`
		Args       []string `json:"args"`
	}
	if json.Unmarshal(raw, &a) != nil {
		return false
	}
	text := normalizeSearch(a.Executable + " " + strings.Join(a.Args, " "))
	markers := []string{" test", "pytest", "go test", "go vet", "cargo test", "cargo clippy", "npm test", "npm run test", "npm run lint", "npm run build", "typecheck", "type-check", "lint", "check", "build"}
	for _, marker := range markers {
		if strings.Contains(" "+text, marker) {
			return true
		}
	}
	return false
}

func extractLearningExitCode(payload any) int {
	m, ok := payload.(map[string]any)
	if !ok {
		return -1
	}
	v := m["exitCode"]
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	}
	return -1
}

func classifyLearningFailure(err error) string {
	if err == nil {
		return "none"
	}
	v := strings.ToLower(err.Error())
	switch {
	case strings.Contains(v, "timeout") || strings.Contains(v, "deadline"):
		return "timeout"
	case strings.Contains(v, "permission") || strings.Contains(v, "access denied"):
		return "permission"
	case strings.Contains(v, "secret-like") || strings.Contains(v, "blocked by policy"):
		return "policy"
	case strings.Contains(v, "not found") || strings.Contains(v, "does not exist") || strings.Contains(v, "unknown"):
		return "not-found"
	case strings.Contains(v, "syntax") || strings.Contains(v, "parse"):
		return "syntax"
	case strings.Contains(v, "compile") || strings.Contains(v, "build"):
		return "build"
	case strings.Contains(v, "test") || strings.Contains(v, "assert"):
		return "test"
	case strings.Contains(v, "network") || strings.Contains(v, "connection"):
		return "network"
	default:
		return "tool-error"
	}
}

func appendUniqueLimited(items []string, value string, limit int) []string {
	for _, existing := range items {
		if existing == value {
			return items
		}
	}
	if len(items) >= limit {
		return items
	}
	return append(items, value)
}

func (e *learningEvolution) finalizeActiveLocked(correction bool) {
	if e.db.Active == nil {
		return
	}
	s := e.db.Active
	verified := s.LastVerifiedAt != "" && (s.LastMutationAt == "" || isoAfterOrEqual(s.LastVerifiedAt, s.LastMutationAt))
	outcome := "incomplete"
	switch {
	case correction:
		outcome = "corrected"
		verified = false
	case verified:
		outcome = "verified"
	case s.VerificationFailures > 0:
		outcome = "failed"
	}
	h := learningTaskOutcome{
		ID: s.ID, WorkspaceID: s.WorkspaceID, Path: s.Path, Scope: s.Scope, Task: s.Task, TaskFingerprint: s.TaskFingerprint,
		SelectedSkills: append([]string(nil), s.SelectedSkills...), StartedAt: s.StartedAt, CompletedAt: nowISO(), Outcome: outcome, Verified: verified,
		ToolSuccesses: s.ToolSuccesses, ToolErrors: s.ToolErrors, VerificationPasses: s.VerificationPasses,
		VerificationFailures: s.VerificationFailures, Mutations: s.Mutations, FailureClasses: append([]string(nil), s.FailureClasses...),
	}
	e.db.History = append(e.db.History, h)
	if len(e.db.History) > maxLearningHistory {
		e.db.History = e.db.History[len(e.db.History)-maxLearningHistory:]
	}

	for _, slug := range h.SelectedSkills {
		stats := e.db.Skills[slug]
		switch outcome {
		case "verified":
			stats.Successes++
		case "failed":
			stats.Failures++
		case "corrected":
			stats.Failures++
			stats.Corrections++
		default:
			stats.Incomplete++
		}
		stats.LastOutcome = outcome
		stats.LastUpdateAt = h.CompletedAt
		e.db.Skills[slug] = stats
	}

	switch outcome {
	case "verified":
		if h.ToolErrors > 0 || h.VerificationFailures > 0 {
			e.upsertCandidateLocked("repair-pattern", h, .78, map[string]any{
				"toolErrors":           h.ToolErrors,
				"verificationFailures": h.VerificationFailures,
				"failureClasses":       h.FailureClasses,
				"selectedSkills":       h.SelectedSkills,
			})
		}
	case "failed":
		e.upsertCandidateLocked("skill-review", h, .70, map[string]any{
			"verificationFailures": h.VerificationFailures,
			"failureClasses":       h.FailureClasses,
			"selectedSkills":       h.SelectedSkills,
		})
	case "corrected":
		e.upsertCandidateLocked("correction-review", h, .92, map[string]any{
			"failureClasses": h.FailureClasses,
			"selectedSkills": h.SelectedSkills,
		})
	}

	repeats := 0
	for _, prior := range e.db.History {
		if prior.Scope == h.Scope && prior.TaskFingerprint == h.TaskFingerprint {
			repeats++
		}
	}
	if repeats >= 2 {
		confidence := math.Min(.90, .62+float64(repeats)*.06)
		e.upsertCandidateLocked("recurring-task", h, confidence, map[string]any{"occurrences": repeats, "selectedSkills": h.SelectedSkills})
	}

	e.db.Active = nil
	_ = e.saveLocked()
}

func isoAfterOrEqual(a, b string) bool {
	ta, ea := time.Parse(time.RFC3339Nano, a)
	tb, eb := time.Parse(time.RFC3339Nano, b)
	if ea != nil || eb != nil {
		return a >= b
	}
	return !ta.Before(tb)
}

func (e *learningEvolution) upsertCandidateLocked(kind string, h learningTaskOutcome, confidence float64, evidence map[string]any) {
	key := kind + "\n" + h.Scope + "\n" + h.TaskFingerprint
	for i := range e.db.Candidates {
		c := &e.db.Candidates[i]
		if c.Type+"\n"+c.Scope+"\n"+c.TaskFingerprint == key && c.Status == "pending" {
			c.Occurrences++
			if confidence > c.Confidence {
				c.Confidence = confidence
			}
			c.Evidence = evidence
			c.UpdatedAt = nowISO()
			return
		}
	}
	now := nowISO()
	e.db.Candidates = append(e.db.Candidates, learningCandidate{
		ID: uuidV4(), Type: kind, Scope: h.Scope, Task: h.Task, TaskFingerprint: h.TaskFingerprint,
		Confidence: confidence, Occurrences: 1, Status: "pending", Evidence: evidence, CreatedAt: now, UpdatedAt: now,
	})
	if len(e.db.Candidates) > maxLearningCandidates {
		e.db.Candidates = e.db.Candidates[len(e.db.Candidates)-maxLearningCandidates:]
	}
}

func candidateIDSet(items []string) map[string]bool {
	out := map[string]bool{}
	for _, item := range items {
		if v := strings.TrimSpace(item); v != "" {
			out[v] = true
		}
	}
	return out
}

func (e *learningEvolution) validateCandidateResolution(scope string, promotedIDs, dismissedIDs []string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.validateCandidateResolutionLocked(normalizeScope(scope), candidateIDSet(promotedIDs), candidateIDSet(dismissedIDs))
}

func (e *learningEvolution) validateCandidateResolutionLocked(scope string, promoted, dismissed map[string]bool) error {
	for id := range promoted {
		if dismissed[id] {
			return fmt.Errorf("candidate cannot be both promoted and dismissed: %s", id)
		}
	}
	wanted := map[string]bool{}
	for id := range promoted {
		wanted[id] = true
	}
	for id := range dismissed {
		wanted[id] = true
	}
	if len(wanted) == 0 {
		return nil
	}
	found := map[string]bool{}
	for _, c := range e.db.Candidates {
		if !wanted[c.ID] {
			continue
		}
		if c.Scope != scope {
			return fmt.Errorf("candidate belongs to a different learning scope: %s", c.ID)
		}
		if c.Status != "pending" {
			return fmt.Errorf("candidate is already resolved: %s", c.ID)
		}
		found[c.ID] = true
	}
	for id := range wanted {
		if !found[id] {
			return fmt.Errorf("learning candidate not found: %s", id)
		}
	}
	return nil
}

func (e *learningEvolution) resolveCandidates(scope string, promotedIDs, dismissedIDs []string) (map[string]any, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	scope = normalizeScope(scope)
	promoted, dismissed := candidateIDSet(promotedIDs), candidateIDSet(dismissedIDs)
	if err := e.validateCandidateResolutionLocked(scope, promoted, dismissed); err != nil {
		return nil, err
	}
	now := nowISO()
	promotedOut, dismissedOut := []string{}, []string{}
	for i := range e.db.Candidates {
		c := &e.db.Candidates[i]
		switch {
		case promoted[c.ID]:
			c.Status = "promoted"
			c.UpdatedAt = now
			promotedOut = append(promotedOut, c.ID)
		case dismissed[c.ID]:
			c.Status = "dismissed"
			c.UpdatedAt = now
			dismissedOut = append(dismissedOut, c.ID)
		}
	}
	if len(promotedOut)+len(dismissedOut) > 0 {
		if err := e.saveLocked(); err != nil {
			return nil, err
		}
	}
	return map[string]any{"promoted": promotedOut, "dismissed": dismissedOut}, nil
}

func (e *learningEvolution) skillAdjustedScore(slug string, base float64) (float64, map[string]any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	stats := e.db.Skills[slug]
	resolved := stats.Successes + stats.Failures
	if resolved == 0 {
		return base, map[string]any{"observations": 0, "factor": 1.0}
	}
	reliability := (float64(stats.Successes) + 2) / (float64(resolved) + 4)
	correctionPenalty := math.Min(.12, float64(stats.Corrections)*.03)
	factor := .88 + reliability*.24 - correctionPenalty
	factor = math.Max(.72, math.Min(1.12, factor))
	adjusted := math.Max(0, math.Min(1, base*factor))
	adjusted = math.Round(adjusted*10000) / 10000
	return adjusted, map[string]any{
		"observations": resolved, "successes": stats.Successes, "failures": stats.Failures, "corrections": stats.Corrections,
		"reliability": math.Round(reliability*10000) / 10000, "factor": math.Round(factor*10000) / 10000,
	}
}

func (e *learningEvolution) context(scope, task string, maxItems int) map[string]any {
	e.mu.Lock()
	defer e.mu.Unlock()
	if maxItems <= 0 {
		maxItems = 6
	}
	scope = normalizeScope(scope)
	task = sanitizeLearningTask(task)

	type scoredCandidate struct {
		Score float64
		Item  learningCandidate
	}
	candidates := []scoredCandidate{}
	for _, c := range e.db.Candidates {
		if c.Status != "pending" || c.Scope != scope {
			continue
		}
		relevance := .25
		if task != "" {
			relevance = scoreText(task, c.Task+" "+c.Type)
		}
		score := c.Confidence*.65 + relevance*.35
		candidates = append(candidates, scoredCandidate{Score: score, Item: c})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Score > candidates[j].Score })
	candidateOut := []map[string]any{}
	for i, x := range candidates {
		if i >= maxItems {
			break
		}
		candidateOut = append(candidateOut, map[string]any{
			"id": x.Item.ID, "type": x.Item.Type, "task": x.Item.Task, "confidence": x.Item.Confidence,
			"occurrences": x.Item.Occurrences, "evidence": x.Item.Evidence, "score": math.Round(x.Score*10000) / 10000,
		})
	}

	type scoredHistory struct {
		Score float64
		Item  learningTaskOutcome
	}
	history := []scoredHistory{}
	for _, h := range e.db.History {
		if h.Scope != scope {
			continue
		}
		relevance := 0.0
		if task != "" {
			relevance = scoreText(task, h.Task)
		}
		if relevance > 0 || task == "" {
			history = append(history, scoredHistory{Score: relevance, Item: h})
		}
	}
	sort.Slice(history, func(i, j int) bool {
		if history[i].Score != history[j].Score {
			return history[i].Score > history[j].Score
		}
		return history[i].Item.CompletedAt > history[j].Item.CompletedAt
	})
	historyOut := []map[string]any{}
	for i, x := range history {
		if i >= maxItems {
			break
		}
		historyOut = append(historyOut, map[string]any{
			"task": x.Item.Task, "outcome": x.Item.Outcome, "verified": x.Item.Verified, "completedAt": x.Item.CompletedAt,
			"selectedSkills": x.Item.SelectedSkills, "failureClasses": x.Item.FailureClasses, "score": math.Round(x.Score*10000) / 10000,
		})
	}
	return map[string]any{"candidates": candidateOut, "similarOutcomes": historyOut}
}

func (e *learningEvolution) sessionSummaryLocked(s *learningTaskSession) map[string]any {
	if s == nil {
		return map[string]any{"active": false, "automaticCapture": true}
	}
	return map[string]any{
		"active": true, "automaticCapture": true, "sessionId": s.ID, "scope": s.Scope, "taskFingerprint": s.TaskFingerprint,
		"selectedSkills": s.SelectedSkills, "startedAt": s.StartedAt,
	}
}

func (e *learningEvolution) stats() map[string]any {
	e.mu.Lock()
	defer e.mu.Unlock()
	counts := map[string]int{}
	for _, h := range e.db.History {
		counts[h.Outcome]++
	}
	pending := 0
	candidateStatuses := map[string]int{}
	for _, c := range e.db.Candidates {
		status := firstNonEmpty(c.Status, "pending")
		candidateStatuses[status]++
		if status == "pending" {
			pending++
		}
	}
	active := false
	activeVerified := false
	if e.db.Active != nil {
		active = true
		activeVerified = e.db.Active.LastVerifiedAt != "" && (e.db.Active.LastMutationAt == "" || isoAfterOrEqual(e.db.Active.LastVerifiedAt, e.db.Active.LastMutationAt))
	}
	return map[string]any{
		"enabled": true, "automaticCapture": true, "activeSession": active, "activeVerified": activeVerified,
		"historyCount": len(e.db.History), "outcomes": counts, "pendingCandidates": pending, "candidateStatuses": candidateStatuses, "skillOutcomeCount": len(e.db.Skills), "statePath": e.path,
	}
}
