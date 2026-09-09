package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// RemoteModel is a model advertised by a provider's listing API
// (design §5.10.1). Meta carries vendor metadata when the listing provides
// it; most OpenAI-compatible /models responses carry ids only, so parameters
// come from the catalog tier instead.
type RemoteModel struct {
	ID          string       `json:"id"`
	DisplayName string       `json:"display_name,omitempty"`
	OwnedBy     string       `json:"owned_by,omitempty"`
	Meta        CatalogModel `json:"meta,omitempty"`
}

// defaultProbeTimeout bounds remote listing probes (design §5.10.2: 8s).
const defaultProbeTimeout = 8 * time.Second

// maxRemoteModels caps listing results (design §5.10.2: 500).
const maxRemoteModels = 500

// anthropicVersion matches the Messages API version used by the chat adapter.
const anthropicVersion = "2023-06-01"

// probeTimeout reads WEKNORA_REMOTE_CATALOG_TIMEOUT_SECONDS, defaulting to 8s.
func probeTimeout() time.Duration {
	if raw := strings.TrimSpace(os.Getenv("WEKNORA_REMOTE_CATALOG_TIMEOUT_SECONDS")); raw != "" {
		if secs, err := strconv.Atoi(raw); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return defaultProbeTimeout
}

// ListRemoteModels lists the models a provider endpoint advertises, over the
// protocol family implied by the provider (design §5.10.1): Anthropic-
// compatible endpoints use GET {base}/v1/models with x-api-key; everything
// else is OpenAI-compatible (GET {base}/models with Bearer). The base URL
// goes through the same SSRF gate as every user-supplied endpoint.
func ListRemoteModels(ctx context.Context, providerName, baseURL, apiKey string) ([]RemoteModel, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("base URL is required")
	}
	if err := secutils.ValidateURLForSSRF(baseURL); err != nil {
		return nil, fmt.Errorf("base URL SSRF check failed: %w", err)
	}

	listURL, headers := listRequestFor(providerName, baseURL, apiKey)
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout())
	defer cancel()

	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, listURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("list models: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, fmt.Errorf("read list response: %w", err)
	}

	var parsed struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
			OwnedBy     string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("decode list response: %w", err)
	}

	models := make([]RemoteModel, 0, len(parsed.Data))
	for _, m := range parsed.Data {
		if m.ID == "" {
			continue
		}
		models = append(models, RemoteModel{
			ID:          m.ID,
			DisplayName: m.DisplayName,
			OwnedBy:     m.OwnedBy,
		})
		if len(models) >= maxRemoteModels {
			break // design §5.10.2 result cap
		}
	}
	return models, nil
}

// listRequestFor derives the listing URL and auth headers for a provider.
// OpenAI-compatible bases usually already include the version segment
// (/v1, /v1beta, /compatible-mode/v1); those append /models directly, bare
// bases get /v1/models (vLLM/LiteLLM convention).
func listRequestFor(providerName, baseURL, apiKey string) (string, map[string]string) {
	if strings.EqualFold(providerName, "anthropic") {
		u := anthropicListURL(baseURL)
		return u, map[string]string{
			"x-api-key":         apiKey,
			"anthropic-version": anthropicVersion,
		}
	}
	return openAIListURL(baseURL), map[string]string{
		"Authorization": "Bearer " + apiKey,
	}
}

func openAIListURL(base string) string {
	u, err := url.Parse(base)
	if err != nil {
		return base + "/models"
	}
	path := strings.TrimRight(u.Path, "/")
	if strings.HasSuffix(path, "/v1") || strings.HasSuffix(path, "/v1beta") ||
		strings.HasSuffix(path, "/compatible-mode/v1") {
		u.Path = path + "/models"
	} else {
		u.Path = path + "/v1/models"
	}
	return u.String()
}

func anthropicListURL(base string) string {
	u, err := url.Parse(base)
	if err != nil {
		return base + "/v1/models"
	}
	path := strings.TrimRight(u.Path, "/")
	if strings.HasSuffix(path, "/v1") {
		u.Path = path + "/models"
	} else {
		u.Path = path + "/v1/models"
	}
	return u.String()
}
