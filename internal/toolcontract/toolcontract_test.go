package toolcontract

import (
	"encoding/json"
	"fmt"
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

func TestParseToolErrorAmbiguousLanguage(t *testing.T) {
	got := ParseToolError(fmt.Errorf("ambiguous_language: page %q has multiple language files; specify lang (available: en, fr)", "posts/hello"))
	if got.Code != "ambiguous_language" {
		t.Fatalf("Code = %q", got.Code)
	}
	if got.Field != "lang" || !got.Retryable {
		t.Fatalf("Field/Retryable = %q/%v", got.Field, got.Retryable)
	}
	if got.Resolution == nil || got.Resolution.Action != "retry_with_parameter" || got.Resolution.Parameter != "lang" {
		t.Fatalf("Resolution = %#v", got.Resolution)
	}
	if len(got.Resolution.AllowedValues) != 2 || got.Resolution.AllowedValues[0] != "en" || got.Resolution.AllowedValues[1] != "fr" {
		t.Fatalf("AllowedValues = %#v", got.Resolution.AllowedValues)
	}
}

func TestParseToolErrorMissingRequiredParameter(t *testing.T) {
	got := ParseToolError(fmt.Errorf("invalid_params: slug must not be empty"))
	if got.Code != "missing_required_parameter" {
		t.Fatalf("Code = %q, want missing_required_parameter", got.Code)
	}
	if got.Field != "slug" || !got.Retryable {
		t.Fatalf("Field/Retryable = %q/%v", got.Field, got.Retryable)
	}
	if got.Resolution == nil || got.Resolution.Parameter != "slug" {
		t.Fatalf("Resolution = %#v", got.Resolution)
	}
}

func TestParseToolErrorRejectsNonMachinePrefix(t *testing.T) {
	got := ParseToolError(fmt.Errorf("unexpected content-type: text/html"))
	if got.Code != "tool_error" {
		t.Fatalf("Code = %q, want tool_error", got.Code)
	}
	if got.Message != "unexpected content-type: text/html" {
		t.Fatalf("Message = %q", got.Message)
	}
}
