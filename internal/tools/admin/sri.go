package admin

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/fileutil"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gopkg.in/yaml.v3"
)

var sriIntegrityRe = regexp.MustCompile(`(?:integrity:\s*|integrity="|hash:\s*|sha:\s*|:\s*)["']?(sha(?:256|384|512)-[A-Za-z0-9+/=]+)["']?`)
var sriHashRe = regexp.MustCompile(`sha(?:256|384|512)-[A-Za-z0-9+/=]+`)
var sriURLRe = regexp.MustCompile(`(?:src|href)="(https?://[^"]+)"`)

type sriCheckInput struct{}

type sriCheckEntry struct {
	URL          string `json:"url"`
	TemplateHash string `json:"template_hash"`
	CurrentHash  string `json:"current_hash,omitempty"`
	Match        bool   `json:"match"`
	Error        string `json:"error,omitempty"`
}

type sriCheckOutput struct {
	FilesScanned           int             `json:"files_scanned"`
	FilesWithSRIAttributes int             `json:"files_with_sri_attributes"`
	SRIEntriesLoaded       int             `json:"sri_entries_loaded"`
	SRIChecked             int             `json:"sri_checked"`
	Status                 string          `json:"status"`
	Summary                string          `json:"summary"`
	Findings               []sriCheckEntry `json:"findings"`
}

func RegisterSRI(s *mcp.Server, cfg config.Config) {
	if s == nil {
		return
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        "check_sri_versions",
		Title:       "Verify SRI integrity",
		Description: "Scan Hugo layouts for CDN integrity attributes and verify each URL's current SHA-384 hash matches the template.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    true,
			DestructiveHint: fileutil.BoolPtr(false),
			IdempotentHint:  true,
			OpenWorldHint:   fileutil.BoolPtr(true),
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ sriCheckInput) (*mcp.CallToolResult, sriCheckOutput, error) {
		out, err := runSRICheck(ctx, cfg)
		if err != nil {
			return nil, sriCheckOutput{}, err
		}
		return nil, out, nil
	})
}

func runSRICheck(ctx context.Context, cfg config.Config) (sriCheckOutput, error) {
	if cfg.HugoRoot == "" {
		return sriCheckOutput{}, fmt.Errorf("config_error: hugo_root is not configured")
	}
	dataEntries, err := loadSRIDataFile(filepath.Join(cfg.HugoRoot, "data", "sri.yaml"))
	if err != nil {
		return sriCheckOutput{}, err
	}
	pairs, scanStats, err := scanSRIReferences(cfg)
	if err != nil {
		slog.Error("check_sri_versions: scan failed", "error", err)
		return sriCheckOutput{}, fmt.Errorf("scan_error: failed to scan SRI references")
	}

	client := &http.Client{Timeout: 10 * time.Second}
	findings := make([]sriCheckEntry, 0, len(pairs))
	for _, p := range pairs {
		entry := verifySRIEntry(ctx, client, p.URL, p.Hash)
		findings = append(findings, entry)
	}

	status := sriStatus(len(dataEntries), len(findings), findings)
	summary := buildSRISummary(status, len(dataEntries), len(findings), findings)
	return sriCheckOutput{
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
}

func scanSRIReferences(cfg config.Config) ([]sriPair, sriScanStats, error) {
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
		for _, p := range pairs {
			key := p.URL + "|" + p.Hash
			if !seen[key] {
				seen[key] = true
				all = append(all, p)
			}
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
		stats.FilesScanned++
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		extracted := extractSRIPairs(string(data))
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

func extractSRIPairs(content string) []sriPair {
	var pairs []sriPair
	for _, m := range sriIntegrityRe.FindAllStringIndex(content, -1) {
		sub := sriIntegrityRe.FindStringSubmatch(content[m[0]:m[1]])
		if len(sub) < 2 {
			continue
		}
		hash := sub[1]
		pos := m[0]

		// Look backward 500 chars from integrity match
		backStart := pos - 500
		if backStart < 0 {
			backStart = 0
		}
		backwardMatches := sriURLRe.FindAllStringSubmatchIndex(content[backStart:pos], -1)

		// Look forward 500 chars from integrity match
		forwardEnd := pos + 500
		if forwardEnd > len(content) {
			forwardEnd = len(content)
		}
		forwardMatches := sriURLRe.FindAllStringSubmatchIndex(content[pos:forwardEnd], -1)

		// Find the closest URL (forward or backward)
		var bestURL string
		var bestDistance int = 1000000

		// Check backward matches
		if len(backwardMatches) > 0 {
			// Get the last backward match (closest to integrity)
			lastMatch := backwardMatches[len(backwardMatches)-1]
			// lastMatch[0] and lastMatch[1] are the bounds in the substring
			distance := pos - (backStart + lastMatch[1])
			if distance < bestDistance {
				bestDistance = distance
				// Extract the URL from the submatch
				urlStart := backStart + lastMatch[2]
				urlEnd := backStart + lastMatch[3]
				bestURL = content[urlStart:urlEnd]
			}
		}

		// Check forward matches
		if len(forwardMatches) > 0 {
			// Get the first forward match (closest to integrity)
			firstMatch := forwardMatches[0]
			distance := firstMatch[0]
			if distance < bestDistance {
				urlStart := pos + firstMatch[2]
				urlEnd := pos + firstMatch[3]
				bestURL = content[urlStart:urlEnd]
			}
		}

		if bestURL != "" {
			pairs = append(pairs, sriPair{URL: bestURL, Hash: hash})
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

func loadSRIDataFile(path string) ([]string, error) {
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
	var out []string
	collectSRIHashes(doc, seen, &out)
	return out, nil
}

func collectSRIHashes(v any, seen map[string]bool, out *[]string) {
	switch x := v.(type) {
	case map[string]any:
		for _, child := range x {
			collectSRIHashes(child, seen, out)
		}
	case []any:
		for _, child := range x {
			collectSRIHashes(child, seen, out)
		}
	case string:
		for _, hash := range sriHashRe.FindAllString(x, -1) {
			if !seen[hash] {
				seen[hash] = true
				*out = append(*out, hash)
			}
		}
	}
}
