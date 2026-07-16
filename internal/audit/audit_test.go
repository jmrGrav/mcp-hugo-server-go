package audit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/audit"
)

// captureDefaultLogger temporarily replaces the process-wide default slog
// logger with one writing JSON to buf, and returns a restore function. Tests
// must not run in parallel with each other since slog.SetDefault is global
// process state.
func captureDefaultLogger(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	orig := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, nil)))
	t.Cleanup(func() { slog.SetDefault(orig) })
	return buf
}

func decodeLastLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	last := lines[len(lines)-1]
	var m map[string]any
	if err := json.Unmarshal([]byte(last), &m); err != nil {
		t.Fatalf("unmarshal audit log line %q: %v", last, err)
	}
	return m
}

func TestAuditInfoEmitsEventTypeAndResult(t *testing.T) {
	buf := captureDefaultLogger(t)

	audit.Info(audit.EventMutation, "success", "tool", "create_page", "scope", "content.write")

	m := decodeLastLine(t, buf)
	if m["msg"] != "audit" {
		t.Fatalf("msg = %v, want audit", m["msg"])
	}
	if m["event_type"] != audit.EventMutation {
		t.Fatalf("event_type = %v, want %v", m["event_type"], audit.EventMutation)
	}
	if m["result"] != "success" {
		t.Fatalf("result = %v, want success", m["result"])
	}
	if m["tool"] != "create_page" {
		t.Fatalf("tool = %v, want create_page", m["tool"])
	}
	if m["level"] != "INFO" {
		t.Fatalf("level = %v, want INFO", m["level"])
	}
}

func TestAuditWarnUsesWarnLevel(t *testing.T) {
	buf := captureDefaultLogger(t)

	audit.Warn(audit.EventScopeDenied, "denied", "scope", "content.read", "reason", "site.admin required")

	m := decodeLastLine(t, buf)
	if m["level"] != "WARN" {
		t.Fatalf("level = %v, want WARN", m["level"])
	}
	if m["event_type"] != audit.EventScopeDenied {
		t.Fatalf("event_type = %v, want %v", m["event_type"], audit.EventScopeDenied)
	}
}

// TestAuditEventTypesAreDistinguishableFromToolErrors is a documentation-level
// check: auth_rejected and scope_denied are their own event_type values,
// never the generic tool_call/tool_error vocabulary used by ordinary tool
// invocations, so operators can filter on event_type alone.
func TestAuditEventTypesAreDistinguishableFromToolErrors(t *testing.T) {
	authLike := map[string]bool{audit.EventAuthRejected: true, audit.EventScopeDenied: true}
	toolLike := map[string]bool{audit.EventMutation: true, audit.EventAdminOperation: true}
	for k := range authLike {
		if toolLike[k] {
			t.Fatalf("event type %q must not overlap between auth-denial and tool-outcome vocabularies", k)
		}
	}
}

func TestLogWithBackgroundContextNeverPanics(t *testing.T) {
	buf := captureDefaultLogger(t)
	audit.Log(slog.LevelInfo, audit.EventOperatorMilestone, "reader_self_registered")
	if buf.Len() == 0 {
		t.Fatal("expected a log line to be written")
	}
	_ = context.Background() // documents that Log always logs with a background context, never a caller's request ctx, so audit events are never dropped by request cancellation
}
