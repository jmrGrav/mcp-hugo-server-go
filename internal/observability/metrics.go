package observability

import (
	"fmt"
	"strings"
	"sync/atomic"
)

type Metrics struct {
	legacyScopeRequests atomic.Uint64
}

func NewMetrics() *Metrics {
	return &Metrics{}
}

func (m *Metrics) RecordLegacyScope(scope string) {
	if scope == "" {
		return
	}
	m.legacyScopeRequests.Add(1)
}

func (m *Metrics) RenderPrometheus() string {
	var b strings.Builder
	_, _ = fmt.Fprintln(&b, "# TYPE mcp_legacy_scope_requests_total counter")
	_, _ = fmt.Fprintf(&b, "mcp_legacy_scope_requests_total{scope=%q} %d\n", "mcp", m.legacyScopeRequests.Load())
	return b.String()
}
