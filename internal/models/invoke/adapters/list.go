// list.go — remote model listing facet (design §6.2 fifth facet / §7.2): BuildListRequest
// derives the GET {base}/models call for the provider's protocol family and
// ParseListResponse decodes the shared {data:[...]} envelope. The URL derivation
// is a byte-for-byte port of v1 catalog/remote.go listRequestFor (P4 分派删除) —
// parity pinned by list_test.go. The probe orchestration itself (SSRF gate,
// probe timeout, result cap) lives in the invoke.List entry.

package adapters

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/invoke"
)

var (
	_ invoke.ListModelsAdapter = (*openaiAdapter)(nil)
	_ invoke.ListModelsAdapter = (*AnthropicAdapter)(nil)
)

// BuildListRequest ports v1 openAIListURL + Bearer auth. The openaiAdapter
// methods promote to every embedding/rerank/ASR composite that embeds it, and
// weKnoraCloudAdapter (sign-type vendor: the listing probe carries neither the
// Bearer header nor the body signature, so the server rejects it — same
// degrade as v1's empty Bearer).
func (a *openaiAdapter) BuildListRequest(ep invoke.Endpoint) (*invoke.Request, error) {
	if a.spec.azure {
		// Azure deployment mode has no OpenAI-shaped /models endpoint. v1 let
		// the doomed request go out and come back 404 (probe degraded to
		// available:false); we fail at Build with the same handler outcome.
		// DRIFT: the degrade reason text differs.
		return nil, fmt.Errorf("azure deployment mode has no model listing endpoint")
	}
	header := make(http.Header)
	header.Set("Content-Type", "application/json")
	a.setAuthHeader(header, ep)
	return &invoke.Request{
		Method: http.MethodGet,
		URL:    openAIListURL(ep.BaseURL),
		Header: header,
	}, nil
}

// ParseListResponse decodes the OpenAI-shaped {data:[{id,display_name,owned_by}]}
// envelope, dropping empty ids (v1 ListRemoteModels semantics).
func (a *openaiAdapter) ParseListResponse(_ int, _ http.Header, body []byte) ([]invoke.RemoteModel, error) {
	return parseModelListEnvelope(body)
}

// BuildListRequest ports v1 anthropicListURL + x-api-key/anthropic-version.
func (a *AnthropicAdapter) BuildListRequest(ep invoke.Endpoint) (*invoke.Request, error) {
	header := make(http.Header)
	header.Set("x-api-key", ep.Credentials.APIKey)
	header.Set("anthropic-version", anthropicVersion)
	return &invoke.Request{
		Method: http.MethodGet,
		URL:    anthropicListURL(ep.BaseURL),
		Header: header,
	}, nil
}

// ParseListResponse decodes the shared {data:[...]} envelope (v1 used one
// decoder for both protocol families).
func (a *AnthropicAdapter) ParseListResponse(_ int, _ http.Header, body []byte) ([]invoke.RemoteModel, error) {
	return parseModelListEnvelope(body)
}

// parseModelListEnvelope decodes the listing response shared by the OpenAI-
// compatible and Anthropic /models endpoints.
func parseModelListEnvelope(body []byte) ([]invoke.RemoteModel, error) {
	var parsed struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
			OwnedBy     string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decode list response: %w", err)
	}
	models := make([]invoke.RemoteModel, 0, len(parsed.Data))
	for _, m := range parsed.Data {
		if m.ID == "" {
			continue
		}
		models = append(models, invoke.RemoteModel{
			ID:          m.ID,
			DisplayName: m.DisplayName,
			OwnedBy:     m.OwnedBy,
		})
	}
	return models, nil
}

// openAIListURL derives the listing URL: OpenAI-compatible bases usually
// already include the version segment (/v1, /v1beta, /compatible-mode/v1);
// those append /models directly, bare bases get /v1/models (vLLM/LiteLLM
// convention).
func openAIListURL(base string) string {
	return listURLFor(base, "/models", "/v1", "/v1beta", "/compatible-mode/v1")
}

// anthropicListURL derives the Anthropic listing URL (base or /v1 base both
// land on {base}/v1/models).
func anthropicListURL(base string) string {
	return listURLFor(base, "/v1/models", "/v1")
}

// listURLFor appends /models to bases already carrying one of versionedSuffixes
// and /v1/models to bare bases; unparsable bases fall back to base+fallback.
// The suffix sets and fallbacks are the v1 catalog/remote.go behavior verbatim.
func listURLFor(base, fallback string, versionedSuffixes ...string) string {
	u, err := url.Parse(base)
	if err != nil {
		return base + fallback
	}
	path := strings.TrimRight(u.Path, "/")
	for _, suffix := range versionedSuffixes {
		if strings.HasSuffix(path, suffix) {
			u.Path = path + "/models"
			return u.String()
		}
	}
	u.Path = path + "/v1/models"
	return u.String()
}
