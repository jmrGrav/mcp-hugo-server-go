package write

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildinfo"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/db"
)

type recoveryIdempotency struct {
	CallerKey   string          `json:"caller_key"`
	Tool        string          `json:"tool"`
	Key         string          `json:"key"`
	RequestHash string          `json:"request_hash"`
	ResultJSON  json.RawMessage `json:"result_json,omitempty"`
}

func recoveryIdempotencyFor(ctx context.Context, tool, key, requestHash string, seed ...map[string]any) *recoveryIdempotency {
	if key == "" {
		return nil
	}
	return &recoveryIdempotency{
		CallerKey: idempotencyCallerKey(ctx), Tool: tool, Key: key, RequestHash: requestHash,
		ResultJSON: recoveryFallbackResult(tool, seed...),
	}
}

// recoveryFallbackResult is persisted with the pre-write intent. If the
// process dies after the atomic filesystem transition but before the handler
// can stage its richer final response, startup can still turn the landed
// operation into a successful idempotent replay rather than re-executing it.
// stageResult replaces this receipt with the exact normal response whenever
// execution reaches that point.
func recoveryFallbackResult(tool string, seed ...map[string]any) json.RawMessage {
	status := "updated"
	bundleStatus := "applied"
	switch tool {
	case "create_page", "create_bundle":
		status, bundleStatus = "created", "created"
	case "delete_page", "delete_bundle":
		status, bundleStatus = "deleted", "deleted"
	case "rollback_change", "rollback_bundle":
		status, bundleStatus = "restored", "restored"
	}
	generatedAt := time.Now().UTC().Format(time.RFC3339Nano)
	data := map[string]any{
		"status": status, "bundle_status": bundleStatus,
		"languages": []string{}, "translations": []any{}, "revisions": map[string]string{},
		"validation": "recovered", "warning": "response reconstructed from the durable recovery journal after restart",
	}
	if len(seed) > 0 {
		for key, value := range seed[0] {
			data[key] = value
		}
	}
	raw, _ := json.Marshal(map[string]any{
		"success": true,
		"data":    data,
		"errors":  []any{}, "warnings": []string{"response reconstructed from the durable recovery journal after restart"},
		"meta": map[string]any{
			"generated_at": generatedAt, "release_version": buildinfo.Version,
			"schema_version": buildinfo.SchemaVersion, "build_channel": buildinfo.EffectiveBuildChannel(),
		},
		"generated_at": generatedAt, "rate_limit_remaining": 0,
	})
	return raw
}

type sourceWriteRecovery struct {
	Path           string               `json:"path"`
	BeforeRevision string               `json:"before_revision"`
	AfterRevision  string               `json:"after_revision"`
	Idempotency    *recoveryIdempotency `json:"idempotency,omitempty"`
	CleanupPaths   []string             `json:"cleanup_paths,omitempty"`
}

type bundleWriteRecovery struct {
	BundleDir    string               `json:"bundle_dir"`
	Files        []bundleRecoveryFile `json:"files"`
	Idempotency  *recoveryIdempotency `json:"idempotency,omitempty"`
	CleanupPaths []string             `json:"cleanup_paths,omitempty"`
}

type bundleRecoveryFile struct {
	Path   string  `json:"path"`
	Before *string `json:"before,omitempty"`
	After  *string `json:"after,omitempty"`
}

type recoveryOperation struct {
	ID      string
	Kind    string
	Payload any
}

// afterRecoveryResultHook is a deterministic test seam for the process-loss
// boundary after the filesystem result is durable in the recovery journal but
// before the normal idempotency store is updated.
var afterRecoveryResultHook func(tool string) error
var afterFilesystemWriteHook func(tool, stage string) error

// planConsumeFailureHook is a deterministic test seam for the narrow window
// after a plan's file write (and recovery-journal record) already landed but
// before plans.consume() has freed the plan slot — see planConsumeFailure.
var planConsumeFailureHook func(tool string) error

func recoveryFilesystemBoundary(tool, stage string) error {
	if afterFilesystemWriteHook != nil {
		return afterFilesystemWriteHook(tool, stage)
	}
	return nil
}

// planConsumeFailure lets a test force plans.consume()'s post-write soft-degrade
// branch without needing to fabricate a real store/persistence failure.
func planConsumeFailure(tool string) error {
	if planConsumeFailureHook != nil {
		return planConsumeFailureHook(tool)
	}
	return nil
}

func newRecoveryOperationID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "src_" + hex.EncodeToString(b), nil
}

func beginRecovery(journal *db.DB, kind string, payload any) (*recoveryOperation, error) {
	op := &recoveryOperation{Kind: kind, Payload: payload}
	if journal == nil {
		return op, nil
	}
	id, err := newRecoveryOperationID()
	if err != nil {
		return nil, fmt.Errorf("new recovery operation id: %w", err)
	}
	op.ID = id
	if err := op.record(journal, "in_progress"); err != nil {
		return nil, err
	}
	return op, nil
}

func beginSourceWriteRecovery(journal *db.DB, path, beforeRevision, afterRevision string, idem *recoveryIdempotency, cleanupPaths ...string) (*recoveryOperation, error) {
	for i := range cleanupPaths {
		cleanupPaths[i] = filepath.Clean(cleanupPaths[i])
	}
	return beginRecovery(journal, "content_write", &sourceWriteRecovery{
		Path: filepath.Clean(path), BeforeRevision: beforeRevision, AfterRevision: afterRevision, Idempotency: idem, CleanupPaths: cleanupPaths,
	})
}

func beginBundleWriteRecovery(journal *db.DB, dir string, files []bundleRecoveryFile, idem *recoveryIdempotency, cleanupPaths ...string) (*recoveryOperation, error) {
	for i := range cleanupPaths {
		cleanupPaths[i] = filepath.Clean(cleanupPaths[i])
	}
	return beginRecovery(journal, "bundle_write", &bundleWriteRecovery{BundleDir: filepath.Clean(dir), Files: files, Idempotency: idem, CleanupPaths: cleanupPaths})
}

func (op *recoveryOperation) record(journal *db.DB, state string) error {
	if op == nil || journal == nil || op.ID == "" {
		return nil
	}
	payload, err := json.Marshal(op.Payload)
	if err != nil {
		return err
	}
	return journal.RecordRecovery(db.RecoveryEntry{OperationID: op.ID, Kind: op.Kind, State: state, Payload: payload, UpdatedAt: time.Now().UTC()})
}

func (op *recoveryOperation) stageResult(journal *db.DB, result any) error {
	if op == nil || journal == nil || op.ID == "" {
		return nil
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	switch payload := op.Payload.(type) {
	case *sourceWriteRecovery:
		if payload.Idempotency != nil {
			payload.Idempotency.ResultJSON = raw
		}
	case *bundleWriteRecovery:
		if payload.Idempotency != nil {
			payload.Idempotency.ResultJSON = raw
		}
	}
	if err := op.record(journal, "file_written"); err != nil {
		return err
	}
	var tool string
	switch payload := op.Payload.(type) {
	case *sourceWriteRecovery:
		if payload.Idempotency != nil {
			tool = payload.Idempotency.Tool
		}
	case *bundleWriteRecovery:
		if payload.Idempotency != nil {
			tool = payload.Idempotency.Tool
		}
	}
	if afterRecoveryResultHook != nil {
		return afterRecoveryResultHook(tool)
	}
	return nil
}

func recoveryFile(path string, before, after *string) bundleRecoveryFile {
	return bundleRecoveryFile{Path: filepath.Clean(path), Before: before, After: after}
}

func captureBundleRecoveryFile(path string, after *string) (bundleRecoveryFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return bundleRecoveryFile{}, err
	}
	var before *string
	if err == nil {
		content := string(raw)
		before = &content
	}
	return recoveryFile(path, before, after), nil
}
