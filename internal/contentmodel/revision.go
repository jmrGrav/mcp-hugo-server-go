package contentmodel

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
)

// SourceRevisionBytes returns the stable optimistic-concurrency revision for a
// source file payload. It hashes the raw source bytes so the revision changes
// whenever the on-disk editable source changes, including front matter fields
// that a partial update might otherwise preserve implicitly.
func SourceRevisionBytes(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// SourceRevision reads a source file and returns its stable revision hash.
func SourceRevision(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read source for revision: %w", err)
	}
	return SourceRevisionBytes(raw), nil
}
