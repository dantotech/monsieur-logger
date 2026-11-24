package generator

import (
	"fmt"
	"regexp"
	"time"
)

// Level names as they appear in the "level" field of every log line.
const (
	LevelDebug = "debug"
	LevelInfo  = "info"
	LevelWarn  = "warn"
	LevelError = "error"
)

// Levels lists all supported levels in a stable order.
var Levels = []string{LevelDebug, LevelInfo, LevelWarn, LevelError}

// Limits bounds what a single run may request.
type Limits struct {
	MaxLogsPerSecond   int
	MaxLogLength       int
	MaxDurationSeconds int
}

// DefaultLimits are used when no explicit limits are configured.
var DefaultLimits = Limits{
	MaxLogsPerSecond:   100000,
	MaxLogLength:       1024 * 1024,
	MaxDurationSeconds: 7 * 24 * 3600,
}

// LevelWeights controls which levels are emitted and in which ratio.
// A weight of 0 disables the level. Example: {info:8, warn:1, error:1}
// emits 80% info, 10% warn and 10% error logs. The distribution is
// deterministic (round-robin over a fixed pattern), so the expected count
// per level can be computed exactly for log-loss analysis.
type LevelWeights struct {
	Debug int `json:"debug"`
	Info  int `json:"info"`
	Warn  int `json:"warn"`
	Error int `json:"error"`
}

func (w LevelWeights) get(level string) int {
	switch level {
	case LevelDebug:
		return w.Debug
	case LevelInfo:
		return w.Info
	case LevelWarn:
		return w.Warn
	case LevelError:
		return w.Error
	}
	return 0
}

func (w LevelWeights) total() int { return w.Debug + w.Info + w.Warn + w.Error }

// pattern expands the weights into an interleaved sequence of levels.
// For {debug:1, info:3, warn:1, error:1} it returns
// [debug info warn error info info] (weighted round robin), so short runs
// still contain a mix of all enabled levels.
func (w LevelWeights) pattern() []string {
	remaining := map[string]int{}
	for _, l := range Levels {
		remaining[l] = w.get(l)
	}
	out := make([]string, 0, w.total())
	for len(out) < w.total() {
		for _, l := range Levels {
			if remaining[l] > 0 {
				out = append(out, l)
				remaining[l]--
			}
		}
	}
	return out
}

// Spec describes a test run. It is the request body of POST /api/v1/runs.
type Spec struct {
	// RunID identifies the test run in every log line (run_id field).
	// Generated when empty.
	RunID string `json:"run_id"`
	// LogsPerSecond is the target emission rate.
	LogsPerSecond int `json:"logs_per_second"`
	// LogLength is the exact length in bytes of the "message" field.
	LogLength int `json:"log_length"`
	// DurationSeconds is how long the run lasts. 0 runs until stopped.
	DurationSeconds int `json:"duration_seconds"`
	// Levels controls the level distribution. Nil means all levels with weight 1.
	Levels *LevelWeights `json:"levels,omitempty"`
	// Labels are free-form key/value pairs added to every test log line.
	Labels map[string]string `json:"labels,omitempty"`
}

var runIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// Normalize fills defaults and validates the spec against limits.
func (s *Spec) Normalize(lim Limits, now time.Time) error {
	if s.RunID == "" {
		s.RunID = NewRunID(now)
	}
	if !runIDPattern.MatchString(s.RunID) {
		return fmt.Errorf("run_id must match %s", runIDPattern)
	}
	if s.LogsPerSecond == 0 {
		s.LogsPerSecond = 10
	}
	if s.LogsPerSecond < 1 || s.LogsPerSecond > lim.MaxLogsPerSecond {
		return fmt.Errorf("logs_per_second must be between 1 and %d", lim.MaxLogsPerSecond)
	}
	if s.LogLength == 0 {
		s.LogLength = 256
	}
	if s.LogLength < 1 || s.LogLength > lim.MaxLogLength {
		return fmt.Errorf("log_length must be between 1 and %d", lim.MaxLogLength)
	}
	if s.DurationSeconds < 0 || s.DurationSeconds > lim.MaxDurationSeconds {
		return fmt.Errorf("duration_seconds must be between 0 (until stopped) and %d", lim.MaxDurationSeconds)
	}
	if s.Levels == nil {
		s.Levels = &LevelWeights{Debug: 1, Info: 1, Warn: 1, Error: 1}
	}
	w := *s.Levels
	if w.Debug < 0 || w.Info < 0 || w.Warn < 0 || w.Error < 0 {
		return fmt.Errorf("level weights must not be negative")
	}
	if w.total() == 0 {
		return fmt.Errorf("at least one level weight must be greater than 0")
	}
	if w.total() > 1000 {
		return fmt.Errorf("sum of level weights must not exceed 1000")
	}
	if len(s.Labels) > 20 {
		return fmt.Errorf("at most 20 labels are allowed")
	}
	for k, v := range s.Labels {
		if k == "" || len(k) > 63 || len(v) > 256 {
			return fmt.Errorf("label keys must be 1-63 chars and values at most 256 chars")
		}
	}
	return nil
}

// TotalExpected is the number of test log lines the run will emit,
// or 0 if the run is unbounded.
func (s *Spec) TotalExpected() int64 {
	return int64(s.LogsPerSecond) * int64(s.DurationSeconds)
}

// ExpectedByLevel returns how many lines of each level a complete run emits.
// It returns nil for unbounded runs.
func (s *Spec) ExpectedByLevel() map[string]int64 {
	total := s.TotalExpected()
	if total == 0 {
		return nil
	}
	return countByLevel(s.Levels.pattern(), total)
}

// countByLevel counts the levels of sequence numbers 1..n.
func countByLevel(pattern []string, n int64) map[string]int64 {
	out := map[string]int64{}
	for _, l := range Levels {
		out[l] = 0
	}
	p := int64(len(pattern))
	full, rest := n/p, n%p
	for i, l := range pattern {
		out[l] += full
		if int64(i) < rest {
			out[l]++
		}
	}
	return out
}
