package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	jobStateQueued    = "queued"
	jobStateRunning   = "running"
	jobStateSucceeded = "succeeded"
	jobStateFailed    = "failed"
	jobStateTimedOut  = "timed_out"
	maxJobs           = 64
)

type toolInvoker func(context.Context, string, json.RawMessage) (any, error)
type toolSupport func(string) bool

type toolExecutionResult struct {
	Payload any
	Err     error
}

type jobRecord struct {
	ID         string
	Tool       string
	State      string
	CreatedAt  time.Time
	StartedAt  time.Time
	FinishedAt time.Time
	Result     any
	Error      string
}

type jobManager struct {
	invoke   toolInvoker
	supports toolSupport
	mu       sync.RWMutex
	jobs     map[string]*jobRecord
}

func newJobManager(invoke toolInvoker, supports toolSupport) *jobManager {
	return &jobManager{invoke: invoke, supports: supports, jobs: make(map[string]*jobRecord)}
}

func (m *jobManager) start(tool string, arguments json.RawMessage, maxRun time.Duration) (*jobRecord, error) {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return nil, errors.New("tool is required")
	}
	if strings.HasPrefix(tool, "gpt_agent_job_") {
		return nil, errors.New("job tools cannot recursively schedule job tools")
	}
	if m.invoke == nil {
		return nil, errors.New("Go tool dispatcher is unavailable")
	}
	if m.supports != nil && !m.supports(tool) {
		return nil, fmt.Errorf("unknown or non-native tool: %s", tool)
	}
	if len(arguments) == 0 || string(arguments) == "null" {
		arguments = json.RawMessage(`{}`)
	}
	var object map[string]any
	if err := json.Unmarshal(arguments, &object); err != nil {
		return nil, errors.New("arguments must be a JSON object")
	}
	if maxRun <= 0 {
		maxRun = 30 * time.Minute
	}
	if maxRun > 30*time.Minute {
		maxRun = 30 * time.Minute
	}

	id, err := newJobID()
	if err != nil {
		return nil, err
	}
	record := &jobRecord{ID: id, Tool: tool, State: jobStateQueued, CreatedAt: time.Now().UTC()}

	if err := m.store(record); err != nil {
		return nil, err
	}

	argsCopy := append(json.RawMessage(nil), arguments...)
	go m.execute(record, argsCopy, maxRun)
	return cloneJob(record), nil
}

func (m *jobManager) adoptRunning(tool string, startedAt time.Time, done <-chan toolExecutionResult, runCtx context.Context, cancel context.CancelFunc) (*jobRecord, error) {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return nil, errors.New("tool is required")
	}
	if strings.HasPrefix(tool, "gpt_agent_job_") {
		return nil, errors.New("job tools cannot recursively schedule job tools")
	}
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	} else {
		startedAt = startedAt.UTC()
	}
	id, err := newJobID()
	if err != nil {
		return nil, err
	}
	record := &jobRecord{ID: id, Tool: tool, State: jobStateRunning, CreatedAt: startedAt, StartedAt: startedAt}
	if err := m.store(record); err != nil {
		return nil, err
	}
	go func() {
		if cancel != nil {
			defer cancel()
		}
		result := <-done
		m.complete(record, result.Payload, result.Err, runCtx)
	}()
	return cloneJob(record), nil
}

func (m *jobManager) store(record *jobRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneLocked()
	if len(m.jobs) >= maxJobs {
		return fmt.Errorf("job capacity reached (%d); collect existing results first", maxJobs)
	}
	m.jobs[record.ID] = record
	return nil
}

func (m *jobManager) execute(record *jobRecord, arguments json.RawMessage, maxRun time.Duration) {
	m.mu.Lock()
	record.State = jobStateRunning
	record.StartedAt = time.Now().UTC()
	m.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), maxRun)
	defer cancel()
	payload, err := m.invoke(ctx, record.Tool, arguments)
	m.complete(record, payload, err, ctx)
}

func (m *jobManager) complete(record *jobRecord, payload any, err error, runCtx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	record.FinishedAt = time.Now().UTC()
	if err != nil {
		timedOut := errors.Is(err, context.DeadlineExceeded)
		if runCtx != nil {
			timedOut = timedOut || errors.Is(runCtx.Err(), context.DeadlineExceeded)
		}
		if timedOut {
			record.State = jobStateTimedOut
		} else {
			record.State = jobStateFailed
		}
		record.Error = err.Error()
		record.Result = toolResultObject(nil, err)
		return
	}
	record.Result = toolResultObject(payload, nil)
	record.State = jobStateSucceeded
}

func (m *jobManager) get(id string) (*jobRecord, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	record, ok := m.jobs[id]
	if !ok {
		return nil, false
	}
	return cloneJob(record), true
}

func (m *jobManager) list() []*jobRecord {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := make([]*jobRecord, 0, len(m.jobs))
	for _, record := range m.jobs {
		items = append(items, cloneJob(record))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	return items
}

func (m *jobManager) pruneLocked() {
	if len(m.jobs) < maxJobs {
		return
	}
	completed := make([]*jobRecord, 0, len(m.jobs))
	for _, record := range m.jobs {
		if record.State != jobStateRunning && record.State != jobStateQueued {
			completed = append(completed, record)
		}
	}
	sort.Slice(completed, func(i, j int) bool { return completed[i].FinishedAt.Before(completed[j].FinishedAt) })
	for len(m.jobs) >= maxJobs && len(completed) > 0 {
		delete(m.jobs, completed[0].ID)
		completed = completed[1:]
	}
}

func cloneJob(record *jobRecord) *jobRecord {
	if record == nil {
		return nil
	}
	copy := *record
	return &copy
}

func jobSummary(record *jobRecord) map[string]any {
	return map[string]any{
		"jobId":           record.ID,
		"tool":            record.Tool,
		"state":           record.State,
		"createdAt":       record.CreatedAt.Format(time.RFC3339Nano),
		"startedAt":       timeOrNil(record.StartedAt),
		"finishedAt":      timeOrNil(record.FinishedAt),
		"durationMs":      jobDurationMS(record),
		"resultAvailable": record.State == jobStateSucceeded || record.State == jobStateFailed || record.State == jobStateTimedOut,
		"error":           emptyToNil(record.Error),
		"engine":          "go",
	}
}

func jobDurationMS(record *jobRecord) any {
	if record.StartedAt.IsZero() {
		return nil
	}
	end := record.FinishedAt
	if end.IsZero() {
		end = time.Now().UTC()
	}
	return end.Sub(record.StartedAt).Milliseconds()
}

func newJobID() (string, error) {
	buf := make([]byte, 10)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "job_" + hex.EncodeToString(buf), nil
}

func truncateString(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max] + "…"
}

func emptyToNil(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func timeOrNil(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.Format(time.RFC3339Nano)
}
