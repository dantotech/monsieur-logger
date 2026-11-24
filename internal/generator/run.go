package generator

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Run states.
const (
	StateRunning   = "running"
	StateCompleted = "completed"
	StateStopped   = "stopped"
)

// Record kinds written to the "kind" field.
const (
	KindTest       = "test"
	KindRunStart   = "run_start"
	KindRunSummary = "run_summary"
)

const timeLayout = "2006-01-02T15:04:05.000000000Z07:00"

// LineWriter is where log lines go (see logwriter.Writer).
type LineWriter interface {
	WriteLine([]byte) error
	Flush() error
}

// Host describes where the logger runs; added to every line when set.
type Host struct {
	Pod       string
	Namespace string
	Node      string
}

// NewRunID returns an ID like "run-20261124-101500-3f9a1c".
func NewRunID(now time.Time) string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return "run-" + now.UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b)
}

// Status is a snapshot of a run, returned by the API.
type Status struct {
	Spec
	State           string           `json:"state"`
	TotalExpected   int64            `json:"total_expected"`
	ExpectedByLevel map[string]int64 `json:"expected_by_level,omitempty"`
	Emitted         int64            `json:"emitted"`
	EmittedByLevel  map[string]int64 `json:"emitted_by_level"`
	StartedAt       time.Time        `json:"started_at"`
	FinishedAt      *time.Time       `json:"finished_at,omitempty"`
	ElapsedSeconds  float64          `json:"elapsed_seconds"`
	ActualRate      float64          `json:"actual_logs_per_second"`
}

// Run is a single, running or finished, log generation job.
type Run struct {
	spec    Spec
	pattern []string
	out     LineWriter

	prefix   []byte // static JSON fields between run_id and seq
	suffix   []byte // static JSON fields after total_expected
	filler   string // message body template
	expected int64

	cancel context.CancelFunc
	done   chan struct{}

	emitted        atomic.Int64
	emittedByLevel map[string]*atomic.Int64

	mu         sync.Mutex
	state      string
	startedAt  time.Time
	finishedAt time.Time
}

func newRun(spec Spec, host Host, out LineWriter) *Run {
	r := &Run{
		spec:           spec,
		pattern:        spec.Levels.pattern(),
		out:            out,
		expected:       spec.TotalExpected(),
		done:           make(chan struct{}),
		emittedByLevel: map[string]*atomic.Int64{},
		state:          StateRunning,
	}
	for _, l := range Levels {
		r.emittedByLevel[l] = new(atomic.Int64)
	}

	// Pre-render everything that does not change from line to line.
	var p strings.Builder
	p.WriteString(`","app":"monsieur-logger","kind":"test","run_id":`)
	p.WriteString(strconv.Quote(spec.RunID)) // run_id is validated to be [A-Za-z0-9._-]
	p.WriteString(`,"seq":`)
	r.prefix = []byte(p.String())

	var s strings.Builder
	if len(spec.Labels) > 0 {
		lb, _ := json.Marshal(spec.Labels)
		s.WriteString(`,"labels":`)
		s.Write(lb)
	}
	writeHost(&s, host)
	s.WriteString(`,"message_length":`)
	s.WriteString(strconv.Itoa(spec.LogLength))
	s.WriteString(`,"message":"`)
	r.suffix = []byte(s.String())

	r.filler = buildFiller(spec.LogLength)
	return r
}

func writeHost(b *strings.Builder, h Host) {
	for _, kv := range [][2]string{{"pod", h.Pod}, {"namespace", h.Namespace}, {"node", h.Node}} {
		if kv[1] == "" {
			continue
		}
		v, _ := json.Marshal(kv[1])
		b.WriteString(`,"` + kv[0] + `":`)
		b.Write(v)
	}
}

// French-flavoured, JSON-safe ASCII filler text.
const fillerText = "Bonjour Fluent Bit! Un croissant, une baguette et un cafe au lait, s'il vous plait. " +
	"Le camembert est pret, la tour Eiffel brille, et Monsieur Logger ecrit encore une ligne de log. " +
	"Ooh la la, pas de perte de logs aujourd'hui! Vive le fromage, vive la ratatouille, vive Kubernetes. "

func buildFiller(n int) string {
	var b strings.Builder
	b.Grow(n)
	for b.Len() < n {
		b.WriteString(fillerText)
	}
	return b.String()[:n]
}

// appendLine renders test line number seq into buf.
func (r *Run) appendLine(buf []byte, now time.Time, seq int64, level string, levelSeq int64) []byte {
	buf = append(buf, `{"timestamp":"`...)
	buf = now.UTC().AppendFormat(buf, timeLayout)
	buf = append(buf, `","level":"`...)
	buf = append(buf, level...)
	buf = append(buf, r.prefix...)
	buf = strconv.AppendInt(buf, seq, 10)
	buf = append(buf, `,"level_seq":`...)
	buf = strconv.AppendInt(buf, levelSeq, 10)
	buf = append(buf, `,"total_expected":`...)
	buf = strconv.AppendInt(buf, r.expected, 10)
	buf = append(buf, r.suffix...)

	// Message: "[seq=N] " header followed by filler, exactly LogLength bytes.
	n := r.spec.LogLength
	start := len(buf)
	buf = append(buf, "[seq="...)
	buf = strconv.AppendInt(buf, seq, 10)
	buf = append(buf, "] "...)
	if hdr := len(buf) - start; hdr >= n {
		buf = buf[:start+n]
	} else {
		buf = append(buf, r.filler[:n-hdr]...)
	}
	return append(buf, `"}`...)
}

// tickInterval picks how often the emit loop wakes up.
func tickInterval(rate int) time.Duration {
	iv := time.Second / time.Duration(rate)
	if iv < 10*time.Millisecond {
		iv = 10 * time.Millisecond
	}
	return iv
}

func (r *Run) loop(ctx context.Context) (completed bool) {
	start := r.startedAt
	rate := float64(r.spec.LogsPerSecond)
	buf := make([]byte, 0, r.spec.LogLength+512)
	var seq int64
	levelSeq := map[string]int64{}

	// emitDue emits every line that is due at time now. It returns true when
	// all expected lines were written.
	emitDue := func(now time.Time) bool {
		due := int64(now.Sub(start).Seconds()*rate) + 1
		if r.expected > 0 && due > r.expected {
			due = r.expected
		}
		for seq < due {
			if seq%1024 == 0 && ctx.Err() != nil {
				break
			}
			seq++
			level := r.pattern[(seq-1)%int64(len(r.pattern))]
			levelSeq[level]++
			buf = r.appendLine(buf[:0], time.Now(), seq, level, levelSeq[level])
			_ = r.out.WriteLine(buf)
			r.emitted.Add(1)
			r.emittedByLevel[level].Add(1)
		}
		_ = r.out.Flush()
		return r.expected > 0 && seq >= r.expected
	}

	if emitDue(start) {
		return true
	}
	ticker := time.NewTicker(tickInterval(r.spec.LogsPerSecond))
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case now := <-ticker.C:
			if emitDue(now) {
				return true
			}
		}
	}
}

// Status returns a snapshot of the run.
func (r *Run) Status() Status {
	r.mu.Lock()
	state, started, finished := r.state, r.startedAt, r.finishedAt
	r.mu.Unlock()

	st := Status{
		Spec:            r.spec,
		State:           state,
		TotalExpected:   r.expected,
		ExpectedByLevel: r.spec.ExpectedByLevel(),
		Emitted:         r.emitted.Load(),
		EmittedByLevel:  map[string]int64{},
		StartedAt:       started,
	}
	for l, c := range r.emittedByLevel {
		st.EmittedByLevel[l] = c.Load()
	}
	end := time.Now()
	if !finished.IsZero() {
		f := finished
		st.FinishedAt = &f
		end = finished
	}
	st.ElapsedSeconds = end.Sub(started).Seconds()
	if st.ElapsedSeconds > 0 {
		st.ActualRate = float64(st.Emitted) / st.ElapsedSeconds
	}
	return st
}

// Done is closed once the run has finished and its summary was written.
func (r *Run) Done() <-chan struct{} { return r.done }

// Stop cancels the run. It is safe to call multiple times.
func (r *Run) Stop() { r.cancel() }
