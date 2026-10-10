package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/mcp/headertemplate"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// HeaderTemplateError only contains configuration names and reasons, never
// request values. Callers may safely show it in settings and tool failures.
type HeaderTemplateError struct{ Header, Reason string }

func (e *HeaderTemplateError) Error() string {
	return fmt.Sprintf("MCP header %q: %s", e.Header, e.Reason)
}

func headerLayers(service *types.MCPService) []map[string]string {
	layers := []map[string]string{service.Headers}
	if service.AuthConfig != nil {
		layers = append(layers, service.AuthConfig.CustomHeaders)
	}
	return layers
}

func protectedDynamicHeader(name string, service *types.MCPService) bool {
	if headertemplate.ProtocolHeader(name) {
		return true
	}
	switch strings.ToLower(name) {
	case "authorization", "cookie", "set-cookie", "x-api-key":
		return true
	}
	return service.AuthConfig != nil && service.AuthConfig.APIKeyHeader != "" && strings.EqualFold(name, service.AuthConfig.APIKeyHeader)
}

func ValidateHeaderTemplates(service *types.MCPService) error {
	if service == nil {
		return &HeaderTemplateError{Reason: "service is required"}
	}
	for _, layer := range headerLayers(service) {
		seen := map[string]bool{}
		for name, value := range layer {
			if !headertemplate.ValidHeaderName(name) {
				return &HeaderTemplateError{Reason: "invalid header name"}
			}
			key := http.CanonicalHeaderKey(name)
			if seen[key] {
				return &HeaderTemplateError{Header: key, Reason: "duplicate header name (case insensitive)"}
			}
			seen[key] = true
			if !headertemplate.ValidHeaderValue(value) {
				return &HeaderTemplateError{Header: key, Reason: "invalid header value"}
			}
			template, err := headertemplate.Parse(value)
			if err != nil {
				return &HeaderTemplateError{Header: key, Reason: err.Error()}
			}
			if template.Dynamic() && protectedDynamicHeader(name, service) {
				return &HeaderTemplateError{Header: key, Reason: "dynamic values are not allowed for credential or protocol headers"}
			}
		}
	}
	return nil
}

// Invalid templates are treated as dynamic so they can never use a shared
// connection before validation rejects them.
func HasDynamicHeaders(service *types.MCPService) bool {
	if service == nil {
		return false
	}
	for _, layer := range headerLayers(service) {
		for _, value := range layer {
			t, err := headertemplate.Parse(value)
			if err != nil || t.Dynamic() {
				return true
			}
		}
	}
	return false
}

func PrepareClientConfig(ctx context.Context, service *types.MCPService, repo interfaces.MCPOAuthRepository) (*ClientConfig, error) {
	if err := ValidateHeaderTemplates(service); err != nil {
		return nil, err
	}
	headers := make(map[string]string)
	dynamic := make(map[string]string)
	resolve := func(layer map[string]string) error {
		for name, value := range layer {
			t, _ := headertemplate.Parse(value)
			resolved, present, err := t.ResolveOptional(types.MCPHeaderContextFromContext(ctx))
			if err != nil {
				return &HeaderTemplateError{Header: name, Reason: err.Error()}
			}
			key := http.CanonicalHeaderKey(name)
			delete(dynamic, key)
			if !present {
				delete(headers, key)
				continue
			}
			headers[key] = resolved
			if t.Dynamic() {
				dynamic[key] = resolved
			}
		}
		return nil
	}
	if err := resolve(service.Headers); err != nil {
		return nil, err
	}
	if service.AuthConfig != nil {
		ac := *service.AuthConfig
		ac.CustomHeaders = nil
		auth := map[string]string{}
		applyAuthHeaders(auth, &ac)
		for name, value := range auth {
			headers[http.CanonicalHeaderKey(name)] = value
		}
		if err := resolve(service.AuthConfig.CustomHeaders); err != nil {
			return nil, err
		}
	}
	tenant, _ := types.TenantIDFromContext(ctx)
	principal, _ := types.PrincipalFromContext(ctx)
	config := &ClientConfig{Service: service, TenantID: tenant, Principal: types.MCPOAuthPrincipalFromContext(ctx), OAuthRepo: repo, headers: headers}
	if HasDynamicHeaders(service) {
		if !principal.Valid() || tenant == 0 {
			return nil, &HeaderTemplateError{Reason: "authenticated principal and workspace are required"}
		}
		raw, _ := json.Marshal(struct {
			Tenant, CallerTenant      uint64
			Principal, OAuthPrincipal string
			Headers                   map[string]string
		}{tenant, types.CallerFromContext(ctx).TenantID, principal.StorageID(), config.Principal.StorageID(), dynamic})
		sum := sha256.Sum256(raw)
		config.headerScope = "headers:v1:" + hex.EncodeToString(sum[:])
	}
	return config, nil
}
