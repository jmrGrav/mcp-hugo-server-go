package admin

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"html"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildinfo"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/fileutil"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gopkg.in/yaml.v3"
)

var sriIntegrityRe = regexp.MustCompile(`(?:integrity:\s*|integrity="|hash:\s*|sha:\s*)["']?(sha(?:256|384|512)-[A-Za-z0-9+/=]+)["']?`)
var sriHashRe = regexp.MustCompile(`sha(?:256|384|512)-[A-Za-z0-9+/=]+`)
var sriURLRe = regexp.MustCompile(`(?:src|href)="(https?://[^"]+)"`)
var sriDataLookupRe = regexp.MustCompile(`index\s+site\.Data\.sri\b`)
var sriTagRe = regexp.MustCompile(`(?is)<(?:script|link)\b[^>]*>`)

type sriCheckInput struct{}

type sriCheckEntry struct {
	URL          string `json:"url"`
	TemplateHash string `json:"template_hash"`
	CurrentHash  string `json:"current_hash,omitempty"`
	Match        bool   `json:"match"`
	Error        string `json:"error,omitempty"`
}

// sriCheckData is the canonical data.* payload (#552).
type sriCheckData struct {
	FilesScanned           int             `json:"files_scanned"`
	FilesWithSRIAttributes int             `json:"files_with_sri_attributes"`
	SRIEntriesLoaded       int             `json:"sri_entries_loaded"`
	SRIChecked             int             `json:"sri_checked"`
	Status                 string          `json:"status"`
	Summary                string          `json:"summary"`
	Findings               []sriCheckEntry `json:"findings"`
}

// sriCheckOutput carries the same fields at the root as compatibility
// aliases alongside the structured envelope (#552) — this tool previously
// had no envelope at all, so this is purely additive, not a breaking change.
type sriCheckOutput struct {
	toolcontract.ToolResponse[sriCheckData]
	FilesScanned           int             `json:"files_scanned"`
	FilesWithSRIAttributes int             `json:"files_with_sri_attributes"`
	SRIEntriesLoaded       int             `json:"sri_entries_loaded"`
	SRIChecked             int             `json:"sri_checked"`
	Status                 string          `json:"status"`
	Summary                string          `json:"summary"`
	Findings               []sriCheckEntry `json:"findings"`
}

func sriSuccessEnvelope[T any](data T) toolcontract.ToolResponse[T] {
	return toolcontract.Success(data, toolcontract.NewMeta(buildinfo.Version, time.Now().UTC()))
}

func newSRICheckOutput(data sriCheckData) sriCheckOutput {
	return sriCheckOutput{
		ToolResponse:           sriSuccessEnvelope(data),
		FilesScanned:           data.FilesScanned,
		FilesWithSRIAttributes: data.FilesWithSRIAttributes,
		SRIEntriesLoaded:       data.SRIEntriesLoaded,
		SRIChecked:             data.SRIChecked,
		Status:                 data.Status,
		Summary:                data.Summary,
		Findings:               data.Findings,
	}
}

type sriDataEntry struct {
	URL  string
	Hash string
}

func RegisterSRI(s *mcp.Server, cfg config.Config) {
	if s == nil {
		return
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:         "check_sri_versions",
		Title:        "Verify SRI integrity",
		Description:  "Scan Hugo layouts for CDN integrity attributes and verify each URL's current SHA-384 hash matches the template.",
		InputSchema:  tools.MustSchema[sriCheckInput](),
		OutputSchema: tools.MustSchema[sriCheckOutput](),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    true,
			DestructiveHint: fileutil.BoolPtr(false),
			IdempotentHint:  true,
			OpenWorldHint:   fileutil.BoolPtr(true),
		},
	}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, _ sriCheckInput) (*mcp.CallToolResult, sriCheckOutput, error) {
		out, err := runSRICheck(ctx, cfg)
		if err != nil {
			return nil, sriCheckOutput{}, err
		}
		return nil, newSRICheckOutput(out), nil
	}))
}

func runSRICheck(ctx context.Context, cfg config.Config) (sriCheckData, error) {
	if cfg.HugoRoot == "" {
		return sriCheckData{}, fmt.Errorf("config_error: hugo_root is not configured")
	}
	dataEntries, err := loadSRIDataFile(filepath.Join(cfg.HugoRoot, "data", "sri.yaml"))
	if err != nil {
		return sriCheckData{}, err
	}
	pairs, scanStats, err := scanSRIReferences(cfg, dataEntries)
	if err != nil {
		slog.Error("check_sri_versions: scan failed", "error", err)
		return sriCheckData{}, fmt.Errorf("scan_error: failed to scan SRI references")
	}

	client := &http.Client{Timeout: 10 * time.Second}
	findings := make([]sriCheckEntry, 0, len(pairs))
	for _, p := range pairs {
		entry := verifySRIEntry(ctx, client, p.URL, p.Hash)
		findings = append(findings, entry)
	}

	status := sriStatus(len(dataEntries), len(findings), findings)
	summary := buildSRISummary(status, len(dataEntries), len(findings), findings)
	return sriCheckData{
		FilesScanned:           scanStats.FilesScanned,
		FilesWithSRIAttributes: scanStats.FilesWithSRIAttributes,
		SRIEntriesLoaded:       len(dataEntries),
		SRIChecked:             len(findings),
		Status:                 status,
		Summary:                summary,
		Findings:               findings,
	}, nil
}

func sriStatus(dataEntries, checked int, findings []sriCheckEntry) string {
	if checked == 0 {
		if dataEntries > 0 {
			return "configured_no_references"
		}
		return "not_configured"
	}
	for _, f := range findings {
		if !f.Match || f.Error != "" {
			return "findings_present"
		}
	}
	return "clean"
}

func buildSRISummary(status string, dataEntries, checked int, findings []sriCheckEntry) string {
	if checked == 0 {
		if status == "configured_no_references" {
			return fmt.Sprintf("Loaded %d SRI entrie(s) from data/sri.yaml, but no matching layout or public HTML integrity references were found.", dataEntries)
		}
		return "No SRI configuration or integrity attributes found."
	}
	passed := 0
	for _, f := range findings {
		if f.Match && f.Error == "" {
			passed++
		}
	}
	mismatches := checked - passed
	if mismatches == 0 {
		return fmt.Sprintf("All %d SRI integrity check(s) passed.", checked)
	}
	return fmt.Sprintf("%d/%d SRI integrity check(s) passed. %d mismatch(es) found.", passed, checked, mismatches)
}

type sriPair struct {
	URL  string
	Hash string
}

type sriScanStats struct {
	FilesScanned           int
	FilesWithSRIAttributes int
	UsesDataLookup         bool
}

func scanSRIReferences(cfg config.Config, dataEntries []sriDataEntry) ([]sriPair, sriScanStats, error) {
	var all []sriPair
	var stats sriScanStats
	seen := map[string]bool{}
	for _, dir := range []string{filepath.Join(cfg.HugoRoot, "layouts"), cfg.SiteRoot} {
		pairs, dirStats, err := scanDirForSRI(dir)
		if err != nil {
			return nil, sriScanStats{}, err
		}
		stats.FilesScanned += dirStats.FilesScanned
		stats.FilesWithSRIAttributes += dirStats.FilesWithSRIAttributes
		stats.UsesDataLookup = stats.UsesDataLookup || dirStats.UsesDataLookup
		for _, p := range pairs {
			key := p.URL + "|" + p.Hash
			if !seen[key] {
				seen[key] = true
				all = append(all, p)
			}
		}
	}
	if stats.UsesDataLookup {
		for _, entry := range dataEntries {
			key := entry.URL + "|" + entry.Hash
			if seen[key] {
				continue
			}
			seen[key] = true
			all = append(all, sriPair(entry))
		}
	}
	return all, stats, nil
}

func scanDirForSRI(dir string) ([]sriPair, sriScanStats, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, sriScanStats{}, nil
	}
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return nil, sriScanStats{}, nil
		}
		return nil, sriScanStats{}, err
	}
	var pairs []sriPair
	stats := sriScanStats{}
	seen := map[string]bool{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkerr error) error {
		if walkerr != nil {
			if os.IsNotExist(walkerr) {
				return nil
			}
			return walkerr
		}
		if d.IsDir() {
			return nil
		}
		if !sriScannableFile(path) {
			return nil
		}
		stats.FilesScanned++
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if sriDataLookupRe.Match(data) {
			stats.UsesDataLookup = true
			stats.FilesWithSRIAttributes++
		}
		extracted := extractSRIPairs(html.UnescapeString(string(data)))
		if len(extracted) > 0 {
			stats.FilesWithSRIAttributes++
		}
		for _, p := range extracted {
			key := p.URL + "|" + p.Hash
			if !seen[key] {
				seen[key] = true
				pairs = append(pairs, p)
			}
		}
		return nil
	})
	if err != nil {
		return nil, sriScanStats{}, err
	}
	return pairs, stats, nil
}

func sriScannableFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".html", ".htm", ".xml":
		return true
	default:
		return false
	}
}

func extractSRIPairs(content string) []sriPair {
	var pairs []sriPair
	for _, tag := range sriTagRe.FindAllString(content, -1) {
		hashes := sriIntegrityRe.FindAllStringSubmatch(tag, -1)
		urls := sriURLRe.FindAllStringSubmatch(tag, -1)
		if len(hashes) == 0 || len(urls) == 0 {
			continue
		}
		for _, hash := range hashes {
			if len(hash) < 2 {
				continue
			}
			for _, url := range urls {
				if len(url) < 2 {
					continue
				}
				pairs = append(pairs, sriPair{URL: url[1], Hash: hash[1]})
			}
		}
	}
	return pairs
}

func verifySRIEntry(ctx context.Context, client *http.Client, url, templateHash string) sriCheckEntry {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return sriCheckEntry{URL: url, TemplateHash: templateHash, Error: err.Error()}
	}
	resp, err := client.Do(req)
	if err != nil {
		return sriCheckEntry{URL: url, TemplateHash: templateHash, Error: err.Error()}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return sriCheckEntry{URL: url, TemplateHash: templateHash, Error: err.Error()}
	}
	currentHash, err := computeHashForTemplate(body, templateHash)
	if err != nil {
		return sriCheckEntry{URL: url, TemplateHash: templateHash, Error: err.Error()}
	}
	return sriCheckEntry{
		URL:          url,
		TemplateHash: templateHash,
		CurrentHash:  currentHash,
		Match:        currentHash == templateHash,
	}
}

func computeHashForTemplate(data []byte, templateHash string) (string, error) {
	switch {
	case strings.HasPrefix(templateHash, "sha256-"):
		h := sha256.Sum256(data)
		return "sha256-" + base64.StdEncoding.EncodeToString(h[:]), nil
	case strings.HasPrefix(templateHash, "sha384-"):
		return computeSHA384(data), nil
	case strings.HasPrefix(templateHash, "sha512-"):
		h := sha512.Sum512(data)
		return "sha512-" + base64.StdEncoding.EncodeToString(h[:]), nil
	default:
		return "", fmt.Errorf("unsupported SRI algorithm")
	}
}

func computeSHA384(data []byte) string {
	h := sha512.New384()
	h.Write(data)
	return "sha384-" + base64.StdEncoding.EncodeToString(h.Sum(nil))
}

func loadSRIDataFile(path string) ([]sriDataEntry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan_error: failed to read data/sri.yaml")
	}
	var doc any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("scan_error: failed to parse data/sri.yaml")
	}
	seen := map[string]bool{}
	var out []sriDataEntry
	collectSRIEntries(doc, "", seen, &out)
	return out, nil
}

func collectSRIEntries(v any, currentURL string, seen map[string]bool, out *[]sriDataEntry) {
	switch x := v.(type) {
	case map[string]any:
		url := currentURL
		if rawURL, ok := x["url"].(string); ok && strings.HasPrefix(rawURL, "http") {
			url = rawURL
		}
		for key, child := range x {
			if strings.HasPrefix(key, "http") {
				appendHashesForURL(key, extractHashes(child), seen, out)
			}
		}
		if url != "" {
			appendHashesForURL(url, extractHashesFromFields(x), seen, out)
		}
		for _, child := range x {
			collectSRIEntries(child, url, seen, out)
		}
	case []any:
		for _, child := range x {
			collectSRIEntries(child, currentURL, seen, out)
		}
	case string:
		if currentURL != "" {
			appendHashesForURL(currentURL, sriHashRe.FindAllString(x, -1), seen, out)
		}
	}
}

func extractHashes(v any) []string {
	switch x := v.(type) {
	case string:
		return sriHashRe.FindAllString(x, -1)
	case map[string]any:
		return extractHashesFromFields(x)
	default:
		return nil
	}
}

func extractHashesFromFields(m map[string]any) []string {
	var hashes []string
	for _, key := range []string{"integrity", "hash", "sha"} {
		if raw, ok := m[key].(string); ok {
			hashes = append(hashes, sriHashRe.FindAllString(raw, -1)...)
		}
	}
	return hashes
}

func appendHashesForURL(url string, hashes []string, seen map[string]bool, out *[]sriDataEntry) {
	if url == "" {
		return
	}
	for _, hash := range hashes {
		key := url + "|" + hash
		if seen[key] {
			continue
		}
		seen[key] = true
		*out = append(*out, sriDataEntry{URL: url, Hash: hash})
	}
}
