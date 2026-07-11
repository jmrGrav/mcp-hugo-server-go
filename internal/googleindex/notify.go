package googleindex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"golang.org/x/oauth2/google"
)

const (
	indexingAPIURL = "https://indexing.googleapis.com/v3/urlNotifications:publish"
	indexingScope  = "https://www.googleapis.com/auth/indexing"
)

var httpClient = &http.Client{Timeout: 10 * time.Second}

type NotifyType string

const (
	TypeUpdated NotifyType = "URL_UPDATED"
	TypeDeleted NotifyType = "URL_DELETED"
)

// tokenSource is a lazy, cached token source derived from the service account.
var (
	tokenMu     sync.Mutex
	cachedToken string
	tokenExpiry time.Time
)

type urlNotification struct {
	URL  string `json:"url"`
	Type string `json:"type"`
}

type quotaState struct {
	Date string `json:"date"`
	Used int    `json:"used"`
}

// Submit notifies Google of URL updates or deletions.
// No-op when cfg is not enabled or urls is empty.
func Submit(cfg config.GoogleIndexConfig, urls []string, notifyType NotifyType) error {
	if !cfg.Enabled() || len(urls) == 0 {
		return nil
	}

	daily := cfg.DailyQuotaLimit
	if daily <= 0 {
		daily = 180
	}

	allowed, err := checkQuota(cfg, len(urls), daily)
	if err != nil {
		slog.Warn("googleindex: quota check error, proceeding without guard", "error", err)
		allowed = len(urls)
	}
	if allowed == 0 {
		slog.Warn("googleindex: daily quota exhausted", "limit", daily)
		return nil
	}
	if allowed < len(urls) {
		slog.Warn("googleindex: submitting partial batch due to quota", "requested", len(urls), "allowed", allowed)
		urls = urls[:allowed]
	}

	token, err := fetchToken(cfg.ServiceAccountPath)
	if err != nil {
		return fmt.Errorf("googleindex: auth: %w", err)
	}

	var errs []error
	for _, u := range urls {
		if submitErr := notifyOne(token, u, string(notifyType)); submitErr != nil {
			slog.Warn("googleindex: notify failed", "url", u, "error", submitErr)
			errs = append(errs, submitErr)
		}
	}

	ok := len(urls) - len(errs)
	slog.Info("googleindex: submitted", "type", notifyType, "ok", ok, "errors", len(errs))
	if len(errs) == len(urls) {
		return fmt.Errorf("googleindex: all %d notifications failed", len(urls))
	}
	return nil
}

func notifyOne(accessToken, url, notifyType string) error {
	body, _ := json.Marshal(urlNotification{URL: url, Type: notifyType})
	req, err := http.NewRequest(http.MethodPost, indexingAPIURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))

	if resp.StatusCode != 200 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

func fetchToken(saPath string) (string, error) {
	tokenMu.Lock()
	defer tokenMu.Unlock()

	if cachedToken != "" && time.Now().Before(tokenExpiry.Add(-2*time.Minute)) {
		return cachedToken, nil
	}

	saJSON, err := os.ReadFile(saPath)
	if err != nil {
		return "", fmt.Errorf("read service account: %w", err)
	}

	jwtCfg, err := google.JWTConfigFromJSON(saJSON, indexingScope)
	if err != nil {
		return "", fmt.Errorf("parse service account: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	token, err := jwtCfg.TokenSource(ctx).Token()
	if err != nil {
		return "", fmt.Errorf("fetch token: %w", err)
	}

	cachedToken = token.AccessToken
	tokenExpiry = token.Expiry
	return cachedToken, nil
}

func checkQuota(cfg config.GoogleIndexConfig, needed, limit int) (int, error) {
	statePath := cfg.QuotaStatePath
	if statePath == "" {
		statePath = "/var/lib/mcp-hugo-server-go/google-index-quota.json"
	}

	today := time.Now().UTC().Format("2006-01-02")

	var state quotaState
	raw, err := os.ReadFile(statePath)
	if err == nil {
		_ = json.Unmarshal(raw, &state)
	}
	if state.Date != today {
		state = quotaState{Date: today, Used: 0}
	}

	remaining := limit - state.Used
	if remaining <= 0 {
		return 0, nil
	}
	allowed := needed
	if allowed > remaining {
		allowed = remaining
	}
	state.Used += allowed

	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err == nil {
		out, _ := json.Marshal(state)
		_ = os.WriteFile(statePath, out, 0o644)
	}
	return allowed, nil
}
