package indexnow

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
)

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func withIndexNowClient(t *testing.T, rt rtFunc) {
	t.Helper()
	prev := httpClient
	httpClient = &http.Client{Transport: rt}
	t.Cleanup(func() { httpClient = prev })
}

func idxResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func TestSubmitNoopAndFilterIndexable(t *testing.T) {
	if err := Submit(config.IndexNowConfig{}, []string{"https://example.test/a"}); err != nil {
		t.Fatalf("Submit(disabled) error = %v", err)
	}
	if err := Submit(config.IndexNowConfig{Key: "k"}, nil); err != nil {
		t.Fatalf("Submit(empty) error = %v", err)
	}
	got := filterIndexable([]string{
		"https://example.test/posts/a/",
		"https://example.test/tags/go/",
		"https://example.test/search/",
		"https://example.test/en/categories/docs/",
	})
	if len(got) != 1 || got[0] != "https://example.test/posts/a/" {
		t.Fatalf("filterIndexable() = %#v", got)
	}
}

func TestSubmitBuildsRequestAndDefaultKeyLocation(t *testing.T) {
	cfg := config.IndexNowConfig{
		Key:  "abc123",
		Host: "example.test",
	}
	withIndexNowClient(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s want POST", r.Method)
		}
		if got := r.URL.String(); got != defaultEndpoint {
			t.Fatalf("endpoint = %s", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json; charset=utf-8" {
			t.Fatalf("Content-Type = %q", got)
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll(body) error = %v", err)
		}
		body := string(raw)
		if !strings.Contains(body, `"host":"example.test"`) ||
			!strings.Contains(body, `"key":"abc123"`) ||
			!strings.Contains(body, `"keyLocation":"https://example.test/abc123.txt"`) ||
			!strings.Contains(body, `"urlList":["https://example.test/posts/a/"]`) {
			t.Fatalf("body = %s", body)
		}
		return idxResp(http.StatusAccepted, `accepted`), nil
	})

	if err := Submit(cfg, []string{
		"https://example.test/posts/a/",
		"https://example.test/tags/go/",
	}); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
}

func TestSubmitAndSubmitURLErrors(t *testing.T) {
	cfg := config.IndexNowConfig{
		Key:         "abc123",
		Host:        "example.test",
		KeyLocation: "https://cdn.example.test/abc123.txt",
		Endpoint:    "https://example.test/indexnow",
	}

	t.Run("HTTP error", func(t *testing.T) {
		withIndexNowClient(t, func(r *http.Request) (*http.Response, error) {
			return nil, errors.New("down")
		})
		err := Submit(cfg, []string{"https://example.test/posts/a/"})
		if err == nil || !strings.Contains(err.Error(), "HTTP error") {
			t.Fatalf("Submit() error = %v", err)
		}
	})

	t.Run("unexpected status", func(t *testing.T) {
		withIndexNowClient(t, func(r *http.Request) (*http.Response, error) {
			return idxResp(http.StatusBadGateway, `bad`), nil
		})
		err := SubmitURL(cfg, "https://example.test/posts/a/")
		if err == nil || !strings.Contains(err.Error(), "unexpected status 502") {
			t.Fatalf("SubmitURL() error = %v", err)
		}
	})
}
