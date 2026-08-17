package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"omnibridge/internal/config"
	"strings"
)

var ErrUnsupportedAPI = errors.New("provider does not support requested API type")

func endpoint(base, apiType string) string {
	base = strings.TrimRight(base, "/")
	switch apiType {
	case "responses":
		if strings.HasSuffix(base, "/responses") {
			return base
		}
		return base + "/responses"
	case "messages":
		if strings.HasSuffix(base, "/messages") {
			return base
		}
		return base + "/messages"
	case "embeddings":
		if strings.HasSuffix(base, "/embeddings") {
			return base
		}
		return base + "/embeddings"
	default:
		if strings.HasSuffix(base, "/chat/completions") {
			return base
		}
		return base + "/chat/completions"
	}
}

func modelsEndpoint(base string) string {
	b := strings.TrimRight(base, "/")
	for _, suffix := range []string{"/chat/completions", "/responses", "/messages", "/embeddings"} {
		if strings.HasSuffix(b, suffix) {
			b = strings.TrimSuffix(b, suffix)
		}
	}
	return b + "/models"
}

func addAuth(req *http.Request, p config.Provider, key string) {
	for k, v := range p.Headers {
		req.Header.Set(k, v)
	}
	if strings.TrimSpace(key) == "" || p.AuthType == "none" {
		return
	}
	switch p.AuthType {
	case "x-api-key":
		req.Header.Set("x-api-key", key)
	case "api-key":
		req.Header.Set("api-key", key)
	case "yandex-api-key":
		req.Header.Set("Authorization", "Api-Key "+key)
	case "x-goog-api-key":
		req.Header.Set("x-goog-api-key", key)
	case "query-key":
		q := req.URL.Query()
		q.Set("key", key)
		req.URL.RawQuery = q.Encode()
	default:
		req.Header.Set("Authorization", "Bearer "+key)
	}
}

func doJSON(ctx context.Context, client *http.Client, method, rawURL string, p config.Provider, key string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	addAuth(req, p, key)
	return client.Do(req)
}

func decodeError(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return fmt.Errorf("upstream HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
}

func rewriteModel(body []byte, native string) ([]byte, error) {
	var v map[string]any
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, err
	}
	v["model"] = native
	return json.Marshal(v)
}

func nativeFromQualified(s string) (provider, native string) {
	if i := strings.IndexByte(s, '/'); i > 0 {
		return s[:i], s[i+1:]
	}
	return "", s
}

func setQuery(rawURL, k, v string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set(k, v)
	u.RawQuery = q.Encode()
	return u.String(), nil
}
