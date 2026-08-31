// Package obs provides the platform observability primitives: metrics,
// structured logs, traces, and alerts.
//
// The metrics store is Prometheus-compatible (text exposition) with an
// in-memory TSDB suitable for short retention. Logs are structured JSON
// records with a per-topic ring buffer. Traces are OTLP-compatible
// span records. Alerts are evaluated by the controller in the alerts
// package.
package obs

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// ----- Metrics -----

// Metric is a single time series.
type Metric struct {
	Name   string
	Help   string
	Type   string // counter | gauge | histogram
	Labels map[string]string
	Value  float64
	// Histogram bucket boundaries for "histogram" type, ascending.
	Buckets []float64
	// Histogram counts per bucket + sum + count.
	BucketCounts []uint64
	Sum          float64
	Count        uint64
}

// Metrics is a thread-safe in-memory metrics registry.
type Metrics struct {
	mu   sync.RWMutex
	mets map[string]*Metric // name+labels -> Metric
}

func NewMetrics() *Metrics {
	return &Metrics{mets: map[string]*Metric{}}
}

// Incr increments a counter.
func (m *Metrics) Incr(name string, labels map[string]string) {
	m.Add(name, 1, labels)
}

// Add adds delta to a counter.
func (m *Metrics) Add(name string, delta float64, labels map[string]string) {
	m.Set(name, m.Get(name, labels)+delta, labels)
}

// Set sets a gauge.
func (m *Metrics) Set(name string, v float64, labels map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := metricKey(name, labels)
	cur, ok := m.mets[key]
	if !ok {
		cur = &Metric{Name: name, Type: "counter", Labels: copyMap(labels), Value: v}
		m.mets[key] = cur
		return
	}
	cur.Value = v
}

// Observe records a histogram sample.
func (m *Metrics) Observe(name string, v float64, labels map[string]string, buckets []float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := metricKey(name, labels)
	cur, ok := m.mets[key]
	if !ok {
		cur = &Metric{Name: name, Type: "histogram", Labels: copyMap(labels), Buckets: append([]float64{}, buckets...), BucketCounts: make([]uint64, len(buckets)+1)}
		m.mets[key] = cur
	}
	for i, b := range cur.Buckets {
		if v <= b {
			cur.BucketCounts[i]++
		}
	}
	cur.BucketCounts[len(cur.BucketCounts)-1]++ // +Inf
	cur.Sum += v
	cur.Count++
}

// Get returns the current value for a counter or gauge.
func (m *Metrics) Get(name string, labels map[string]string) float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cur, ok := m.mets[metricKey(name, labels)]
	if !ok {
		return 0
	}
	return cur.Value
}

// Snapshot returns all metrics, sorted by name.
func (m *Metrics) Snapshot() []*Metric {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Metric, 0, len(m.mets))
	for _, v := range m.mets {
		cp := *v
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// TextExposition returns the registry in Prometheus text format.
func (m *Metrics) TextExposition() string {
	mts := m.Snapshot()
	var b strings.Builder
	seenHelp := map[string]bool{}
	for _, mm := range mts {
		if !seenHelp[mm.Name] {
			fmt.Fprintf(&b, "# HELP %s %s\n", mm.Name, mm.Help)
			fmt.Fprintf(&b, "# TYPE %s %s\n", mm.Name, mm.Type)
			seenHelp[mm.Name] = true
		}
		switch mm.Type {
		case "histogram":
			for i, bk := range mm.Buckets {
				labels := mergeLabels(mm.Labels, map[string]string{"le": formatFloat(bk)})
				fmt.Fprintf(&b, "%s_bucket{%s} %d\n", mm.Name, labelsString(labels), mm.BucketCounts[i])
			}
			labels := mergeLabels(mm.Labels, map[string]string{"le": "+Inf"})
			fmt.Fprintf(&b, "%s_bucket{%s} %d\n", mm.Name, labelsString(labels), mm.BucketCounts[len(mm.BucketCounts)-1])
			fmt.Fprintf(&b, "%s_sum %s\n", mm.Name, formatFloat(mm.Sum))
			fmt.Fprintf(&b, "%s_count %d\n", mm.Name, mm.Count)
		default:
			fmt.Fprintf(&b, "%s{%s} %s\n", mm.Name, labelsString(mm.Labels), formatFloat(mm.Value))
		}
	}
	return b.String()
}

func metricKey(name string, labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(name)
	for _, k := range keys {
		b.WriteString("|")
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(labels[k])
	}
	return b.String()
}

func labelsString(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%q", k, labels[k]))
	}
	return strings.Join(parts, ",")
}

func mergeLabels(a, b map[string]string) map[string]string {
	out := make(map[string]string, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func copyMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func formatFloat(f float64) string {
	return fmt.Sprintf("%g", f)
}

// ----- Logs -----

// LogRecord is a structured log entry.
type LogRecord struct {
	Time    time.Time
	Level   string
	Message string
	Labels  map[string]string
}

// Logs is a per-topic ring buffer of records.
type Logs struct {
	mu      sync.Mutex
	ring    []LogRecord
	next    int
	full    bool
	maxSize int
}

func NewLogs(maxSize int) *Logs {
	if maxSize <= 0 {
		maxSize = 1024
	}
	return &Logs{ring: make([]LogRecord, maxSize), maxSize: maxSize}
}

func (l *Logs) Append(rec LogRecord) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ring[l.next] = rec
	l.next = (l.next + 1) % l.maxSize
	if l.next == 0 {
		l.full = true
	}
}

func (l *Logs) Tail(n int) []LogRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	if n > l.maxSize {
		n = l.maxSize
	}
	out := make([]LogRecord, 0, n)
	count := l.maxSize
	if !l.full {
		count = l.next
	}
	for i := 0; i < n && i < count; i++ {
		idx := l.next - 1 - i
		if idx < 0 {
			idx += l.maxSize
		}
		out = append(out, l.ring[idx])
	}
	return out
}

// ----- Traces -----

// Span is an OTLP-compatible span record.
type Span struct {
	TraceID    string
	SpanID     string
	ParentID   string
	Name       string
	StartTime  time.Time
	EndTime    time.Time
	Attributes map[string]string
	Status     string // OK|ERROR
}

// Tracer is a span recorder.
type Tracer struct {
	mu    sync.Mutex
	spans []Span
}

func NewTracer() *Tracer { return &Tracer{} }

func (t *Tracer) Start(name string) *SpanBuilder {
	return &SpanBuilder{tr: t, span: Span{Name: name, StartTime: time.Now().UTC()}}
}

// Spans returns recorded spans.
func (t *Tracer) Spans() []Span {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Span, len(t.spans))
	copy(out, t.spans)
	return out
}

// SpanBuilder is a fluent builder.
type SpanBuilder struct {
	tr   *Tracer
	span Span
}

func (b *SpanBuilder) WithTraceID(id string) *SpanBuilder  { b.span.TraceID = id; return b }
func (b *SpanBuilder) WithParent(id string) *SpanBuilder  { b.span.ParentID = id; return b }
func (b *SpanBuilder) WithAttr(k, v string) *SpanBuilder {
	if b.span.Attributes == nil {
		b.span.Attributes = map[string]string{}
	}
	b.span.Attributes[k] = v
	return b
}
func (b *SpanBuilder) End(status string) *Span {
	b.span.EndTime = time.Now().UTC()
	if status != "" {
		b.span.Status = status
	}
	b.tr.mu.Lock()
	b.tr.spans = append(b.tr.spans, b.span)
	b.tr.mu.Unlock()
	return &b.span
}
