package generator

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// Errors returned by the Manager.
var (
	ErrRunExists   = errors.New("a run with this run_id already exists")
	ErrNotFound    = errors.New("run not found")
	ErrTooManyRuns = errors.New("maximum number of concurrent runs reached")
	ErrShutdown    = errors.New("logger is shutting down")
)

// Manager owns all test runs of this process.
type Manager struct {
	out     LineWriter
	host    Host
	limits  Limits
	maxRuns int
	log     *slog.Logger

	mu        sync.Mutex
	runs      map[string]*Run
	closed    bool
	wg        sync.WaitGroup
	baseCtx   context.Context
	cancelAll context.CancelFunc
}

// NewManager creates a Manager writing test logs to out.
func NewManager(out LineWriter, host Host, limits Limits, maxConcurrentRuns int, log *slog.Logger) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		out:       out,
		host:      host,
		limits:    limits,
		maxRuns:   maxConcurrentRuns,
		log:       log,
		runs:      map[string]*Run{},
		baseCtx:   ctx,
		cancelAll: cancel,
	}
}

// Limits returns the configured limits.
func (m *Manager) Limits() Limits { return m.limits }

// controlRecord is the JSON shape of run_start and run_summary lines.
type controlRecord struct {
	Timestamp       string            `json:"timestamp"`
	Level           string            `json:"level"`
	App             string            `json:"app"`
	Kind            string            `json:"kind"`
	RunID           string            `json:"run_id"`
	Message         string            `json:"message"`
	State           string            `json:"state,omitempty"`
	LogsPerSecond   int               `json:"logs_per_second"`
	LogLength       int               `json:"log_length"`
	DurationSeconds int               `json:"duration_seconds"`
	LevelWeights    LevelWeights      `json:"level_weights"`
	TotalExpected   int64             `json:"total_expected"`
	ExpectedByLevel map[string]int64  `json:"expected_by_level,omitempty"`
	Emitted         *int64            `json:"emitted,omitempty"`
	EmittedByLevel  map[string]int64  `json:"emitted_by_level,omitempty"`
	ElapsedSeconds  *float64          `json:"elapsed_seconds,omitempty"`
	ActualRate      *float64          `json:"actual_logs_per_second,omitempty"`
	Labels          map[string]string `json:"labels,omitempty"`
	Pod             string            `json:"pod,omitempty"`
	Namespace       string            `json:"namespace,omitempty"`
	Node            string            `json:"node,omitempty"`
}

func (m *Manager) writeControl(kind, msg string, st Status, final bool) {
	rec := controlRecord{
		Timestamp:       time.Now().UTC().Format(timeLayout),
		Level:           LevelInfo,
		App:             "monsieur-logger",
		Kind:            kind,
		RunID:           st.RunID,
		Message:         msg,
		LogsPerSecond:   st.LogsPerSecond,
		LogLength:       st.LogLength,
		DurationSeconds: st.DurationSeconds,
		LevelWeights:    *st.Levels,
		TotalExpected:   st.TotalExpected,
		ExpectedByLevel: st.ExpectedByLevel,
		Labels:          st.Labels,
		Pod:             m.host.Pod,
		Namespace:       m.host.Namespace,
		Node:            m.host.Node,
	}
	if final {
		rec.State = st.State
		rec.Emitted = &st.Emitted
		rec.EmittedByLevel = st.EmittedByLevel
		rec.ElapsedSeconds = &st.ElapsedSeconds
		rec.ActualRate = &st.ActualRate
	}
	b, err := json.Marshal(rec)
	if err != nil {
		m.log.Error("failed to marshal control record", "error", err)
		return
	}
	_ = m.out.WriteLine(b)
	_ = m.out.Flush()
}

// Start validates spec and starts a new run.
func (m *Manager) Start(spec Spec) (Status, error) {
	if err := spec.Normalize(m.limits, time.Now()); err != nil {
		return Status{}, err
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return Status{}, ErrShutdown
	}
	if _, ok := m.runs[spec.RunID]; ok {
		m.mu.Unlock()
		return Status{}, ErrRunExists
	}
	active := 0
	for _, r := range m.runs {
		if r.Status().State == StateRunning {
			active++
		}
	}
	if m.maxRuns > 0 && active >= m.maxRuns {
		m.mu.Unlock()
		return Status{}, ErrTooManyRuns
	}

	r := newRun(spec, m.host, m.out)
	var (
		ctx    context.Context
		cancel context.CancelFunc
	)
	if spec.DurationSeconds > 0 {
		// Safety net: never run much longer than requested even if stdout
		// is blocked by back-pressure.
		ctx, cancel = context.WithTimeout(m.baseCtx, time.Duration(spec.DurationSeconds)*time.Second+time.Minute)
	} else {
		ctx, cancel = context.WithCancel(m.baseCtx)
	}
	r.cancel = cancel
	r.startedAt = time.Now()
	m.runs[spec.RunID] = r
	m.wg.Add(1)
	m.mu.Unlock()

	st := r.Status()
	m.writeControl(KindRunStart, "Monsieur Logger commence le test run", st, false)
	m.log.Info("run started", "run_id", spec.RunID, "logs_per_second", spec.LogsPerSecond,
		"log_length", spec.LogLength, "duration_seconds", spec.DurationSeconds)

	go func() {
		defer m.wg.Done()
		defer cancel()
		completed := r.loop(ctx)

		r.mu.Lock()
		r.finishedAt = time.Now()
		if completed {
			r.state = StateCompleted
		} else {
			r.state = StateStopped
		}
		r.mu.Unlock()

		final := r.Status()
		m.writeControl(KindRunSummary, "Monsieur Logger a fini le test run", final, true)
		m.log.Info("run finished", "run_id", final.RunID, "state", final.State,
			"emitted", final.Emitted, "total_expected", final.TotalExpected)
		close(r.done)
	}()
	return st, nil
}

// Get returns the status of one run.
func (m *Manager) Get(id string) (Status, error) {
	m.mu.Lock()
	r, ok := m.runs[id]
	m.mu.Unlock()
	if !ok {
		return Status{}, ErrNotFound
	}
	return r.Status(), nil
}

// List returns all runs, newest first.
func (m *Manager) List() []Status {
	m.mu.Lock()
	out := make([]Status, 0, len(m.runs))
	for _, r := range m.runs {
		out = append(out, r.Status())
	}
	m.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out
}

// Stop stops a run and waits for its summary to be written.
func (m *Manager) Stop(ctx context.Context, id string) (Status, error) {
	m.mu.Lock()
	r, ok := m.runs[id]
	m.mu.Unlock()
	if !ok {
		return Status{}, ErrNotFound
	}
	r.Stop()
	select {
	case <-r.Done():
	case <-ctx.Done():
	}
	return r.Status(), nil
}

// Shutdown stops all runs and waits until their summaries are written.
func (m *Manager) Shutdown(ctx context.Context) {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	m.cancelAll()
	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
	_ = m.out.Flush()
}
