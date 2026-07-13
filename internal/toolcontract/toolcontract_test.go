package toolcontract

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNewError(t *testing.T) {
	got := NewError("ambiguous_language", "Multiple language variants exist.")
	if got.Code != "ambiguous_language" {
		t.Fatalf("NewError().Code = %q", got.Code)
	}
	if got.Message != "Multiple language variants exist." {
		t.Fatalf("NewError().Message = %q", got.Message)
	}
	if got.Retryable {
		t.Fatal("NewError().Retryable = true, want false")
	}
}

func TestSuccessInitializesSlicesAndMeta(t *testing.T) {
	meta := NewMeta("1.4.0", time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC))
	got := Success(map[string]string{"status": "ok"}, meta)

	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if decoded["success"] != true {
		t.Fatalf("success = %v, want true", decoded["success"])
	}
	if _, ok := decoded["errors"].([]any); !ok {
		t.Fatalf("errors = %#v, want []", decoded["errors"])
	}
	if _, ok := decoded["warnings"].([]any); !ok {
		t.Fatalf("warnings = %#v, want []", decoded["warnings"])
	}
	metaMap, ok := decoded["meta"].(map[string]any)
	if !ok {
		t.Fatalf("meta = %T, want map", decoded["meta"])
	}
	if metaMap["server_version"] != "1.4.0" {
		t.Fatalf("meta.server_version = %v, want 1.4.0", metaMap["server_version"])
	}
}
