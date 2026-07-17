package toolcontract

import "fmt"

// ResponseMode is the shared vocabulary tools use to shape a response
// payload down from the default. Only "standard" and "compact" are
// implemented today. "full" and "ids_only" are reserved for future work
// and rejected as invalid_params until an implementation lands, so a
// caller never silently gets a mode that looks supported but isn't.
type ResponseMode string

const (
	ResponseModeStandard ResponseMode = "standard"
	ResponseModeCompact  ResponseMode = "compact"
)

// ResolveResponseMode validates and normalizes a raw response_mode input
// value. An empty string defaults to standard (the pre-existing, unshaped
// behavior), so omitting the parameter never changes a tool's output.
func ResolveResponseMode(raw string) (ResponseMode, error) {
	switch raw {
	case "", string(ResponseModeStandard):
		return ResponseModeStandard, nil
	case string(ResponseModeCompact):
		return ResponseModeCompact, nil
	case "full", "ids_only":
		return "", fmt.Errorf("invalid_params: response_mode %q is reserved for future use and not yet implemented (available: standard, compact)", raw)
	default:
		return "", fmt.Errorf("invalid_params: response_mode must be one of: standard, compact (got %q)", raw)
	}
}

// ResolveIncludeBody applies the repo-wide nil-means-true default for the
// include_body toggle (see export_agent_context, #325), so every tool that
// adopts include_body shares one meaning of the flag.
func ResolveIncludeBody(v *bool) bool {
	if v == nil {
		return true
	}
	return *v
}

// TruncateBody truncates s to maxChars runes, returning the (possibly
// truncated) string and whether truncation occurred. maxChars<=0 disables
// truncation and returns s unchanged. Truncation is rune-aware: byte-slicing
// a UTF-8 string at an arbitrary offset can split a multibyte rune,
// producing invalid UTF-8 that json.Marshal silently replaces with U+FFFD.
func TruncateBody(s string, maxChars int) (string, bool) {
	if maxChars <= 0 || len(s) <= maxChars {
		// Byte length is always >= rune length, so if the byte length is
		// already within budget, the rune length is too — no need to
		// convert to []rune on the common (short-body) path.
		return s, false
	}
	r := []rune(s)
	if len(r) <= maxChars {
		return s, false
	}
	return string(r[:maxChars]), true
}

// SelectFields returns a copy of row containing only the keys present in
// fields. Unknown field names are silently ignored (no error), matching
// this repo's existing tolerant-input convention (e.g. pagination clamps
// out-of-range values instead of erroring). An empty fields list is a
// no-op — callers should only invoke SelectFields when fields is non-empty.
func SelectFields(row map[string]any, fields []string) map[string]any {
	out := make(map[string]any, len(fields))
	for _, f := range fields {
		if v, ok := row[f]; ok {
			out[f] = v
		}
	}
	return out
}
