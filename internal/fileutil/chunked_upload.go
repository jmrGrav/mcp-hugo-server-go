package fileutil

// UploadStagingPrefix/UploadStagingSuffix name the chunked page-asset-upload
// (#1196) staging file convention: ".upload-<upload_id>.part", staged
// directly inside a live page bundle directory by internal/tools/write's
// commit_asset_upload flow while a chunk sequence is in progress.
//
// Defined here — a shared low-level package with no import-cycle risk —
// rather than duplicated as separate literals in internal/tools/write
// (which creates/renames these files) and internal/tools/admin (whose
// hashTree must exclude them from source_revision/public_revision the same
// way it excludes .mcp-audit.log, #1180). Both packages already import
// fileutil, and write already imports admin, so admin cannot import write
// directly — this constant is the single source of truth either side can
// reference without that cycle.
const (
	UploadStagingPrefix = ".upload-"
	UploadStagingSuffix = ".part"
)
