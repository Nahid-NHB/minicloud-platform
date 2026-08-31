package obs

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// AlertRule defines when an alert should fire.
type AlertRule struct {
	ID       string
	Name     string
	Severity string
	// Expression is a tiny comparison DSL: "<metric>{labels} <op> <value>".
	// Supported operators: >, <, >=, <=, ==, !=.
	Expression string
	For        time.Duration
}

// AlertState is the runtime evaluation state of an alert rule.
type AlertState struct {
	Rule          AlertRule
	Firing        bool
	PendingSince  time.Time
	LastEvaluated time.Time
	LastValue     float64
}

// Alerts evaluates a small expression language over the metrics registry.
type Alerts struct {
	mu    sync.Mutex
	rules []AlertRule
	state map[string]*AlertState
	m     *Metrics
}

func NewAlerts(m *Metrics) *Alerts {
	return &Alerts{m: m, state: map[string]*AlertState{}}
}

// Add registers a rule.
func (a *Alerts) Add(r AlertRule) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.rules = append(a.rules, r)
	a.state[r.ID] = &AlertState{Rule: r}
}

// Evaluate runs all rules and updates state. Returns newly-firing and
// newly-resolved alerts.
func (a *Alerts) Evaluate(now time.Time) (firing, resolved []AlertState) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, r := range a.rules {
		st := a.state[r.ID]
		v, ok := evalExpr(r.Expression, a.m)
		st.LastValue = v
		st.LastEvaluated = now
		if !ok {
			continue
		}
		if matchesThreshold(r.Expression, v) {
			if !st.Firing {
				if st.PendingSince.IsZero() {
					st.PendingSince = now
					continue
				}
				if now.Sub(st.PendingSince) >= r.For {
					st.Firing = true
					firing = append(firing, *st)
				}
			}
		} else {
			if st.Firing {
				resolved = append(resolved, *st)
			}
			st.Firing = false
			st.PendingSince = time.Time{}
		}
	}
	return
}

// State returns a copy of all alert states.
func (a *Alerts) State() []AlertState {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]AlertState, 0, len(a.state))
	for _, st := range a.state {
		out = append(out, *st)
	}
	return out
}

// evalExpr extracts the metric name and returns its current value.
func evalExpr(expr string, m *Metrics) (float64, bool) {
	name, _, ok := splitExpr(expr)
	if !ok {
		return 0, false
	}
	// Sum across all label sets for the metric.
	mts := m.Snapshot()
	var sum float64
	found := false
	for _, mm := range mts {
		if mm.Name == name {
			sum += mm.Value
			found = true
		}
	}
	return sum, found
}

func matchesThreshold(expr string, v float64) bool {
	_, opVal, ok := splitExpr(expr)
	if !ok {
		return false
	}
	return compareOp(opVal.op, v, opVal.threshold)
}

type opThreshold struct {
	op        string
	threshold float64
}

func splitExpr(expr string) (string, opThreshold, bool) {
	// <metric>{labels} <op> <value>
	// Find first occurrence of >, <, >=, <=, ==, != (not inside labels).
	// Labels block is optional and not parsed here.
	e := expr
	for _, op := range []string{">=", "<=", "==", "!=", ">", "<"} {
		if i := indexOutside(e, op); i >= 0 {
			name := strings.TrimSpace(e[:i])
			rest := strings.TrimSpace(e[i+len(op):])
			var v float64
			_, err := fmt.Sscanf(rest, "%f", &v)
			if err != nil {
				return "", opThreshold{}, false
			}
			return name, opThreshold{op: op, threshold: v}, true
		}
	}
	return "", opThreshold{}, false
}

func indexOutside(s, op string) int {
	for i := 0; i+len(op) <= len(s); i++ {
		if s[i] == '{' {
			// skip label block
			j := strings.IndexByte(s[i:], '}')
			if j < 0 {
				return -1
			}
			i += j
			continue
		}
		if strings.HasPrefix(s[i:], op) {
			return i
		}
	}
	return -1
}

func compareOp(op string, v, t float64) bool {
	switch op {
	case ">":
		return v > t
	case "<":
		return v < t
	case ">=":
		return v >= t
	case "<=":
		return v <= t
	case "==":
		return v == t
	case "!=":
		return v != t
	}
	return false
}
