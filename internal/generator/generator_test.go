package generator

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

type memWriter struct {
	mu    sync.Mutex
	lines [][]byte
}

func (m *memWriter) WriteLine(b []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lines = append(m.lines, bytes.Clone(b))
	return nil
}
func (m *memWriter) Flush() error { return nil }

func TestPattern(t *testing.T) {
	got := strings.Join(LevelWeights{Debug: 1, Info: 3, Warn: 1, Error: 1}.pattern(), ",")
	want := "debug,info,warn,error,info,info"
	if got != want {
		t.Fatalf("pattern = %s, want %s", got, want)
	}
}

func TestExpectedByLevel(t *testing.T) {
	s := Spec{LogsPerSecond: 7, DurationSeconds: 1, Levels: &LevelWeights{Info: 2, Error: 1}}
	got := s.ExpectedByLevel()
	if got[LevelInfo] != 5 || got[LevelError] != 2 || got[LevelDebug] != 0 {
		t.Fatalf("unexpected counts %v", got)
	}
}

func TestNormalize(t *testing.T) {
	s := Spec{}
	if err := s.Normalize(DefaultLimits, time.Now()); err != nil {
		t.Fatal(err)
	}
	if s.RunID == "" || s.LogsPerSecond != 10 || s.LogLength != 256 || s.Levels == nil {
		t.Fatalf("defaults not applied: %+v", s)
	}
	bad := []Spec{
		{RunID: "has space"},
		{LogsPerSecond: -1},
		{LogsPerSecond: DefaultLimits.MaxLogsPerSecond + 1},
		{DurationSeconds: -5},
		{Levels: &LevelWeights{}},
	}
	for i, b := range bad {
		if err := b.Normalize(DefaultLimits, time.Now()); err == nil {
			t.Errorf("case %d: expected error", i)
		}
	}
}

func TestRunEmitsExactCountAndValidJSON(t *testing.T) {
	out := &memWriter{}
	mgr := NewManager(out, Host{Pod: "p", Node: "n"}, DefaultLimits, 5, slog.New(slog.NewTextHandler(io.Discard, nil)))
	st, err := mgr.Start(Spec{
		RunID: "t1", LogsPerSecond: 200, LogLength: 50, DurationSeconds: 1,
		Levels: &LevelWeights{Info: 3, Warn: 1}, Labels: map[string]string{"scenario": "unit"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Start(Spec{RunID: "t1"}); err != ErrRunExists {
		t.Fatalf("expected ErrRunExists, got %v", err)
	}

	r := mgr.runs[st.RunID]
	select {
	case <-r.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("run did not finish")
	}

	var tests, starts, summaries int
	var lastSeq int64
	for _, l := range out.lines {
		var rec map[string]any
		if err := json.Unmarshal(l, &rec); err != nil {
			t.Fatalf("invalid json %q: %v", l, err)
		}
		switch rec["kind"] {
		case KindTest:
			tests++
			seq := int64(rec["seq"].(float64))
			if seq != lastSeq+1 {
				t.Fatalf("seq gap: %d after %d", seq, lastSeq)
			}
			lastSeq = seq
			if msg := rec["message"].(string); len(msg) != 50 {
				t.Fatalf("message length %d", len(msg))
			}
			if rec["run_id"] != "t1" || rec["pod"] != "p" || rec["node"] != "n" {
				t.Fatalf("missing fields: %v", rec)
			}
			if lv := rec["level"]; lv != "info" && lv != "warn" {
				t.Fatalf("unexpected level %v", lv)
			}
		case KindRunStart:
			starts++
		case KindRunSummary:
			summaries++
			if rec["state"] != StateCompleted || rec["emitted"].(float64) != 200 {
				t.Fatalf("bad summary: %v", rec)
			}
		}
	}
	if tests != 200 || starts != 1 || summaries != 1 {
		t.Fatalf("tests=%d starts=%d summaries=%d", tests, starts, summaries)
	}
	final, _ := mgr.Get("t1")
	if final.EmittedByLevel[LevelInfo] != 150 || final.EmittedByLevel[LevelWarn] != 50 {
		t.Fatalf("by level: %v", final.EmittedByLevel)
	}
}

func TestStopUnboundedRun(t *testing.T) {
	out := &memWriter{}
	mgr := NewManager(out, Host{}, DefaultLimits, 5, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := mgr.Start(Spec{RunID: "forever", LogsPerSecond: 100}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	st, err := mgr.Stop(t.Context(), "forever")
	if err != nil {
		t.Fatal(err)
	}
	if st.State != StateStopped || st.Emitted == 0 {
		t.Fatalf("unexpected status %+v", st)
	}
}

func TestShortLogLength(t *testing.T) {
	spec := Spec{RunID: "x", LogsPerSecond: 1, LogLength: 3, DurationSeconds: 1}
	if err := spec.Normalize(DefaultLimits, time.Now()); err != nil {
		t.Fatal(err)
	}
	r := newRun(spec, Host{}, &memWriter{})
	var rec map[string]any
	if err := json.Unmarshal(r.appendLine(nil, time.Now(), 123456, "info", 1), &rec); err != nil {
		t.Fatal(err)
	}
	if rec["message"] != "[se" {
		t.Fatalf("message = %q", rec["message"])
	}
}
