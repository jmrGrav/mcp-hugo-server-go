package admin

import (
	"context"
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var sriIntegrityRe = regexp.MustCompile(`integrity="(sha384-[A-Za-z0-9+/=]+)"`)
var sriURLRe = regexp.MustCompile(`(?:src|href)="(https?://[^"]+)"`)

type sriCheckInput struct{}

type sriCheckEntry struct {
	URL          string `json:"url"`
	TemplateHash string `json:"template_hash"`
	CurrentHash  string `json:"current_hash,omitempty"`
	Match        bool   `json:"match"`
	Error        string `json:"error,omitempty"`
}

func RegisterSRI(s *mcp.Server, cfg config.Config) {
	if s == nil {
		return
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        "check_sri_versions",
		Title:       "Check SRI versions",
		Description: "[RequiredScope: system.admin] Scan Hugo layouts for CDN integrity attributes and verify each URL's current SHA-384 hash matches the template.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    true,
			DestructiveHint: boolPtr(false),
			IdempotentHint:  true,
			OpenWorldHint:   boolPtr(true),
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ sriCheckInput) (*mcp.CallToolResult, any, error) {
		results, err := runSRICheck(ctx, cfg)
		if err != nil {
			return nil, nil, err
		}
		return nil, results, nil
	})
}

func runSRICheck(ctx context.Context, cfg config.Config) ([]sriCheckEntry, error) {
	if cfg.HugoRoot == "" {
		return nil, fmt.Errorf("config_error: hugo_root is not configured")
	}
	layoutsDir := filepath.Join(cfg.HugoRoot, "layouts")
	pairs, err := scanLayoutsForSRI(layoutsDir)
	if err != nil {
		return nil, fmt.Errorf("scan_error: %w", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	results := make([]sriCheckEntry, 0, len(pairs))
	for _, p := range pairs {
		entry := verifySRIEntry(ctx, client, p.URL, p.Hash)
		results = append(results, entry)
	}
	return results, nil
}

type sriPair struct {
	URL  string
	Hash string
}

func scanLayoutsForSRI(layoutsDir string) ([]sriPair, error) {
	var pairs []sriPair
	seen := map[string]bool{}
	err := filepath.WalkDir(layoutsDir, func(path string, d fs.DirEntry, walkerr error) error {
		if walkerr != nil {
			if os.IsNotExist(walkerr) {
				return nil
			}
			return walkerr
		}
		if d.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		for _, p := range extractSRIPairs(string(data)) {
			key := p.URL + "|" + p.Hash
			if !seen[key] {
				seen[key] = true
				pairs = append(pairs, p)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return pairs, nil
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
			distance := (pos + firstMatch[0]) - pos
			if distance < bestDistance {
				bestDistance = distance
				// Extract the URL from the submatch
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
	currentHash := computeSHA384(body)
	return sriCheckEntry{
		URL:          url,
		TemplateHash: templateHash,
		CurrentHash:  currentHash,
		Match:        currentHash == templateHash,
	}
}

func computeSHA384(data []byte) string {
	h := sha512.New384()
	h.Write(data)
	return "sha384-" + base64.StdEncoding.EncodeToString(h.Sum(nil))
}
