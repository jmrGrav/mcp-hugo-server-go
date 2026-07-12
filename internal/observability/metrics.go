package observability

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
)

type Metrics struct {
	legacyScopeRequests atomic.Uint64

	toolMu         sync.Mutex
	toolCallTotal  map[string]*atomic.Uint64 // key: "tool:scope:result"
	toolDurationMs map[string]*atomic.Uint64 // key: "tool:scope" → cumulative ms
}

func NewMetrics() *Metrics {
	return &Metrics{
		toolCallTotal:  make(map[string]*atomic.Uint64),
		toolDurationMs: make(map[string]*atomic.Uint64),
	}
}

func (m *Metrics) RecordLegacyScope(scope string) {
	if scope == "" {
		return
	}
	m.legacyScopeRequests.Add(1)
}

// RecordToolCall records one tool invocation. toolName, scope, and resultClass
// must not contain user-supplied content (they come from fixed tool names and
// server-controlled scope/result values — safe as Prometheus label values).
func (m *Metrics) RecordToolCall(toolName, scope, resultClass string, durationMs int64) {
	totalKey := toolName + ":" + scope + ":" + resultClass
	durKey := toolName + ":" + scope

	m.toolMu.Lock()
	if m.toolCallTotal[totalKey] == nil {
		m.toolCallTotal[totalKey] = new(atomic.Uint64)
	}
	if m.toolDurationMs[durKey] == nil {
		m.toolDurationMs[durKey] = new(atomic.Uint64)
	}
	total := m.toolCallTotal[totalKey]
	dur := m.toolDurationMs[durKey]
	m.toolMu.Unlock()

	total.Add(1)
	if durationMs > 0 {
		dur.Add(uint64(durationMs))
	}
}

func (m *Metrics) RenderPrometheus() string {
	var b strings.Builder

	_, _ = fmt.Fprintln(&b, "# TYPE mcp_legacy_scope_requests_total counter")
	_, _ = fmt.Fprintf(&b, "mcp_legacy_scope_requests_total{scope=%q} %d\n", "mcp", m.legacyScopeRequests.Load())

	m.toolMu.Lock()
	totalSnap := make(map[string]uint64, len(m.toolCallTotal))
	for k, v := range m.toolCallTotal {
		totalSnap[k] = v.Load()
	}
	durSnap := make(map[string]uint64, len(m.toolDurationMs))
	for k, v := range m.toolDurationMs {
		durSnap[k] = v.Load()
	}
	m.toolMu.Unlock()

	if len(totalSnap) > 0 {
		_, _ = fmt.Fprintln(&b, "# TYPE mcp_tool_calls_total counter")
		for key, count := range totalSnap {
			parts := strings.SplitN(key, ":", 3)
			if len(parts) == 3 {
				_, _ = fmt.Fprintf(&b, "mcp_tool_calls_total{tool=%q,scope=%q,result=%q} %d\n",
					parts[0], parts[1], parts[2], count)
			}
		}
	}
	if len(durSnap) > 0 {
		_, _ = fmt.Fprintln(&b, "# TYPE mcp_tool_call_duration_ms_total counter")
		for key, ms := range durSnap {
			parts := strings.SplitN(key, ":", 2)
			if len(parts) == 2 {
				_, _ = fmt.Fprintf(&b, "mcp_tool_call_duration_ms_total{tool=%q,scope=%q} %d\n",
					parts[0], parts[1], ms)
			}
		}
	}
	return b.String()
}
