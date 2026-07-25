package toolcontract

import (
	"errors"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildinfo"
)

type shapeFixture struct {
	ToolResponse[map[string]any]
}

type responseModeInput struct {
	ResponseMode string
}

type noResponseModeInput struct {
	Limit int
}

func TestComputePagination(t *testing.T) {
	t.Run("no next page", func(t *testing.T) {
		got := ComputePagination(10, 5, 5, 5)
		if got.HasMore || got.NextOffset != nil {
			t.Fatalf("ComputePagination(no next page) = %#v, want HasMore=false and nil NextOffset", got)
		}
	})

	t.Run("exact boundary", func(t *testing.T) {
		got := ComputePagination(10, 10, 0, 10)
		if got.HasMore || got.NextOffset != nil {
			t.Fatalf("ComputePagination(exact boundary) = %#v, want HasMore=false and nil NextOffset", got)
		}
	})

	t.Run("has more", func(t *testing.T) {
		got := ComputePagination(10, 3, 3, 3)
		if !got.HasMore || got.NextOffset == nil || *got.NextOffset != 6 {
			t.Fatalf("ComputePagination(has more) = %#v, want HasMore=true NextOffset=6", got)
		}
	})
}

func TestCompactMetaMap(t *testing.T) {
	meta := map[string]any{
		"schema_version":  "v1.0.0",
		"release_version": "v1.6.3",
		"commit":          "abc123",
		"build_channel":   "release",
		"generated_at":    "2026-07-25T10:00:00Z",
		"extra":           "drop-me",
	}
	got := compactMetaMap(meta)
	if len(got) != 4 {
		t.Fatalf("compactMetaMap() len = %d, want 4: %#v", len(got), got)
	}
	if got["schema_version"] != "v1.0.0" || got["release_version"] != "v1.6.3" || got["commit"] != "abc123" || got["build_channel"] != "release" {
		t.Fatalf("compactMetaMap() = %#v, want release identity fields only", got)
	}
	if _, ok := got["generated_at"]; ok {
		t.Fatalf("compactMetaMap() leaked generated_at: %#v", got)
	}
	if _, ok := got["extra"]; ok {
		t.Fatalf("compactMetaMap() leaked extra field: %#v", got)
	}
}

func TestShapeSuccessOutput(t *testing.T) {
	origCommit := buildinfo.Commit
	origChannel := buildinfo.BuildChannel
	buildinfo.Commit = "abc123"
	buildinfo.BuildChannel = "release"
	t.Cleanup(func() {
		buildinfo.Commit = origCommit
		buildinfo.BuildChannel = origChannel
	})

	meta := NewMeta("v1.6.3", time.Date(2026, 7, 25, 10, 0, 0, 0, time.UTC))
	out := shapeFixture{
		ToolResponse: Success(map[string]any{"status": "ok"}, meta),
	}

	t.Run("standard unchanged", func(t *testing.T) {
		got := ShapeSuccessOutput(out, ResponseModeStandard)
		if got.Meta.GeneratedAt == "" || got.GeneratedAt == "" {
			t.Fatalf("ShapeSuccessOutput(standard) stripped generated_at: %#v", got)
		}
	})

	t.Run("compact trims meta only", func(t *testing.T) {
		got := ShapeSuccessOutput(out, ResponseModeCompact)
		if got.GeneratedAt == "" {
			t.Fatalf("ShapeSuccessOutput(compact) removed root generated_at: %#v", got)
		}
		if got.Meta.GeneratedAt != "" {
			t.Fatalf("ShapeSuccessOutput(compact) kept meta.generated_at = %q, want empty", got.Meta.GeneratedAt)
		}
		if got.Meta.ReleaseVersion != "v1.6.3" || got.Meta.Commit != "abc123" || got.Meta.BuildChannel != "release" || got.Meta.SchemaVersion != ToolResultVersion {
			t.Fatalf("ShapeSuccessOutput(compact) = %#v, want compact release identity preserved", got.Meta)
		}
		if got.Data["status"] != "ok" {
			t.Fatalf("ShapeSuccessOutput(compact) changed data = %#v", got.Data)
		}
	})
}

func TestWithRequestContextAndExtraction(t *testing.T) {
	ctx := RequestContext{Slug: "posts/demo", RequestedLang: "fr"}
	baseErr := errors.New("boom")
	wrapped := WithRequestContext(baseErr, ctx)
	if wrapped == nil {
		t.Fatal("WithRequestContext() returned nil")
	}
	got := requestContextFrom(wrapped)
	if got == nil || *got != ctx {
		t.Fatalf("requestContextFrom(wrapped) = %#v, want %#v", got, ctx)
	}
	if requestContextFrom(baseErr) != nil {
		t.Fatalf("requestContextFrom(baseErr) = %#v, want nil", requestContextFrom(baseErr))
	}
	if WithRequestContext(nil, ctx) != nil {
		t.Fatal("WithRequestContext(nil, ctx) should return nil")
	}
}

func TestResponseModeFromInput(t *testing.T) {
	tests := []struct {
		name     string
		in       any
		wantMode ResponseMode
		wantSeen bool
		wantErr  bool
	}{
		{name: "nil", in: nil, wantMode: ResponseModeStandard, wantSeen: false},
		{name: "pointer nil", in: (*responseModeInput)(nil), wantMode: ResponseModeStandard, wantSeen: false},
		{name: "non struct", in: "compact", wantMode: ResponseModeStandard, wantSeen: false},
		{name: "missing field", in: noResponseModeInput{Limit: 5}, wantMode: ResponseModeStandard, wantSeen: false},
		{name: "default empty", in: responseModeInput{}, wantMode: ResponseModeStandard, wantSeen: true},
		{name: "explicit compact", in: responseModeInput{ResponseMode: "compact"}, wantMode: ResponseModeCompact, wantSeen: true},
		{name: "pointer explicit standard", in: &responseModeInput{ResponseMode: "standard"}, wantMode: ResponseModeStandard, wantSeen: true},
		{name: "reserved", in: responseModeInput{ResponseMode: "full"}, wantSeen: true, wantErr: true},
		{name: "invalid", in: responseModeInput{ResponseMode: "bogus"}, wantSeen: true, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, seen, err := ResponseModeFromInput(tt.in)
			if seen != tt.wantSeen {
				t.Fatalf("ResponseModeFromInput(%s) seen = %v, want %v", tt.name, seen, tt.wantSeen)
			}
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ResponseModeFromInput(%s) err = nil, want error", tt.name)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResponseModeFromInput(%s) unexpected err = %v", tt.name, err)
			}
			if got != tt.wantMode {
				t.Fatalf("ResponseModeFromInput(%s) = %q, want %q", tt.name, got, tt.wantMode)
			}
		})
	}
}
