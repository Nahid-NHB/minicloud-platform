package obs

import (
	"strings"
	"testing"
	"time"
)

func TestMetricsTextExposition(t *testing.T) {
	m := NewMetrics()
	m.Incr("requests_total", map[string]string{"path": "/v1/workloads"})
	m.Incr("requests_total", map[string]string{"path": "/v1/workloads"})
	m.Observe("latency_seconds", 0.1, map[string]string{"path": "/v1/workloads"}, []float64{0.05, 0.1, 0.5})
	out := m.TextExposition()
	if !strings.Contains(out, "requests_total") {
		t.Fatalf("missing counter: %s", out)
	}
	if !strings.Contains(out, "latency_seconds_bucket") {
		t.Fatalf("missing histogram: %s", out)
	}
}

func TestLogsTail(t *testing.T) {
	l := NewLogs(8)
	for i := 0; i < 5; i++ {
		l.Append(LogRecord{Time: time.Now(), Level: "info", Message: "hello"})
	}
	got := l.Tail(3)
	if len(got) != 3 {
		t.Fatalf("len=%d", len(got))
	}
}

func TestTracer(t *testing.T) {
	tr := NewTracer()
	b := tr.Start("do-thing")
	b.WithAttr("k", "v").End("OK")
	spans := tr.Spans()
	if len(spans) != 1 || spans[0].Name != "do-thing" {
		t.Fatalf("bad spans: %+v", spans)
	}
}

func TestAlertsFireAndResolve(t *testing.T) {
	m := NewMetrics()
	a := NewAlerts(m)
	a.Add(AlertRule{
		ID: "high-cpu", Name: "HighCPU", Severity: "warning",
		Expression: "cpu_usage > 80", For: 100 * time.Millisecond,
	})
	m.Set("cpu_usage", 50, nil)
	_, resolved := a.Evaluate(time.Now())
	if len(resolved) != 0 {
		t.Fatalf("unexpected resolved: %+v", resolved)
	}
	m.Set("cpu_usage", 95, nil)
	now := time.Now()
	_, _ = a.Evaluate(now)
	time.Sleep(120 * time.Millisecond)
	firing, _ := a.Evaluate(time.Now())
	if len(firing) != 1 {
		t.Fatalf("expected 1 firing, got %d", len(firing))
	}
	m.Set("cpu_usage", 30, nil)
	_, resolved = a.Evaluate(time.Now())
	if len(resolved) != 1 {
		t.Fatalf("expected 1 resolved, got %d", len(resolved))
	}
}
