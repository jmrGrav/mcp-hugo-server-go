package admin

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/previewstore"
)

const (
	previewExternalVerificationTimeout  = 12 * time.Second
	previewExternalVerificationMaxBytes = 8 * 1024 * 1024
)

type previewProbeTarget struct {
	route string
	path  string
}

func verifyExternalPreview(ctx context.Context, store *previewstore.Store, previewID, entryURL, cleanURL, dir string) error {
	htmlTarget, assetTarget, err := previewProbeTargets(dir)
	if err != nil {
		return err
	}

	probeCtx, cancel := context.WithTimeout(ctx, previewExternalVerificationTimeout)
	defer cancel()
	client := &http.Client{
		Timeout: previewExternalVerificationTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// Use the real entry URL and redirect contract. Resetting the temporary
	// session below leaves the entry token available for the caller's browser.
	entryProbeURL := strings.TrimSuffix(entryURL, "/") + "/" + htmlTarget.route
	resp, err := getPreviewProbe(probeCtx, client, entryProbeURL, nil)
	if err != nil {
		return fmt.Errorf("entry request failed")
	}
	if resp.StatusCode != http.StatusFound {
		resp.Body.Close()
		return fmt.Errorf("entry request returned HTTP %d, want 302", resp.StatusCode)
	}
	var sessionCookie *http.Cookie
	for _, cookie := range resp.Cookies() {
		if cookie.Name == previewstore.CookieName(previewID) {
			sessionCookie = cookie
			break
		}
	}
	location := resp.Header.Get("Location")
	resp.Body.Close()
	if sessionCookie == nil || !sessionCookie.HttpOnly || !sessionCookie.Secure || sessionCookie.SameSite != http.SameSiteStrictMode || sessionCookie.Path != previewstore.CleanPath(previewID, "") {
		return fmt.Errorf("entry request did not establish the expected HttpOnly preview cookie")
	}
	defer store.ResetSessionForProbe(previewID)

	cleanBase, err := url.Parse(cleanURL)
	if err != nil {
		return fmt.Errorf("clean preview URL is invalid")
	}
	redirectURL, err := cleanBase.Parse(location)
	if err != nil || redirectURL.Scheme != cleanBase.Scheme || redirectURL.Host != cleanBase.Host || redirectURL.Path != previewstore.CleanPath(previewID, htmlTarget.route) {
		return fmt.Errorf("entry redirect did not preserve the nested preview route")
	}
	if err := verifyPreviewFile(probeCtx, client, redirectURL.String(), sessionCookie, htmlTarget.path); err != nil {
		return fmt.Errorf("nested page verification failed: %w", err)
	}
	assetURL, _ := cleanBase.Parse(previewstore.CleanPath(previewID, assetTarget.route))
	if err := verifyPreviewFile(probeCtx, client, assetURL.String(), sessionCookie, assetTarget.path); err != nil {
		return fmt.Errorf("asset verification failed: %w", err)
	}

	missingRoute := "__mcp_preview_probe_missing_" + previewID + "__"
	missingURL, _ := cleanBase.Parse(previewstore.CleanPath(previewID, missingRoute))
	missingResp, err := getPreviewProbe(probeCtx, client, missingURL.String(), sessionCookie)
	if err != nil {
		return fmt.Errorf("missing-route request failed")
	}
	missingResp.Body.Close()
	if missingResp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("missing route returned HTTP %d, want 404", missingResp.StatusCode)
	}
	return nil
}

func getPreviewProbe(ctx context.Context, client *http.Client, target string, cookie *http.Cookie) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	return client.Do(req)
}

func verifyPreviewFile(ctx context.Context, client *http.Client, target string, cookie *http.Cookie, localPath string) error {
	want, err := os.ReadFile(localPath)
	if err != nil {
		return fmt.Errorf("local probe target became unreadable")
	}
	if len(want) > previewExternalVerificationMaxBytes {
		return fmt.Errorf("local probe target exceeds the bounded verification size")
	}
	resp, err := getPreviewProbe(ctx, client, target, cookie)
	if err != nil {
		return fmt.Errorf("request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("returned HTTP %d, want 200", resp.StatusCode)
	}
	got, err := io.ReadAll(io.LimitReader(resp.Body, int64(len(want))+1))
	if err != nil {
		return fmt.Errorf("response body could not be read")
	}
	if !bytes.Equal(got, want) {
		return fmt.Errorf("served bytes do not match the isolated preview output")
	}
	return nil
}

func previewProbeTargets(dir string) (previewProbeTarget, previewProbeTarget, error) {
	var htmlTargets, assetTargets []previewProbeTarget
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || !info.Mode().IsRegular() {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "index.html" || info.Size() > previewExternalVerificationMaxBytes {
			return nil
		}
		if strings.HasSuffix(rel, "/index.html") {
			htmlTargets = append(htmlTargets, previewProbeTarget{route: strings.TrimSuffix(rel, "index.html"), path: path})
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(rel), ".html") {
			assetTargets = append(assetTargets, previewProbeTarget{route: rel, path: path})
		}
		return nil
	})
	if err != nil {
		return previewProbeTarget{}, previewProbeTarget{}, fmt.Errorf("isolated preview output could not be inventoried")
	}
	sort.Slice(htmlTargets, func(i, j int) bool { return htmlTargets[i].route < htmlTargets[j].route })
	sort.Slice(assetTargets, func(i, j int) bool { return assetTargets[i].route < assetTargets[j].route })
	if len(htmlTargets) == 0 {
		return previewProbeTarget{}, previewProbeTarget{}, fmt.Errorf("isolated preview has no nested HTML route to verify")
	}
	if len(assetTargets) == 0 {
		return previewProbeTarget{}, previewProbeTarget{}, fmt.Errorf("isolated preview has no asset route to verify")
	}
	return htmlTargets[0], assetTargets[0], nil
}
