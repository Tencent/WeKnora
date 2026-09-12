package invoke

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// Remote-catalog probe orchestration (design §5.10.2 / §7.2): the wire
// knowledge lives in the adapters' ListModels facet; this entry owns the
// deployment concerns that v1 catalog/remote.go carried — the SSRF gate, the
// probe timeout, and the result cap. HTTP contract unchanged (8s default
// timeout, 500-result cap, keys never logged).
const (
	// defaultProbeTimeout bounds remote listing probes (design §5.10.2: 8s).
	defaultProbeTimeout = 8 * time.Second
	// maxRemoteModels caps listing results (design §5.10.2: 500).
	maxRemoteModels = 500
	// maxListPages bounds the pagination loop even when every page comes
	// back full (defense in depth on top of the result cap).
	maxListPages = 10
)

// probeTimeout reads WEKNORA_REMOTE_CATALOG_TIMEOUT_SECONDS, defaulting to 8s.
func probeTimeout() time.Duration {
	if raw := strings.TrimSpace(os.Getenv("WEKNORA_REMOTE_CATALOG_TIMEOUT_SECONDS")); raw != "" {
		if secs, err := strconv.Atoi(raw); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return defaultProbeTimeout
}

// List lists the models a provider endpoint advertises via its adapter's
// ListModels facet (fifth facet, §6.2): URL/auth come from the vendor wire
// knowledge; the base URL goes through the same SSRF gate as every
// user-supplied endpoint and the probe timeout/cap match the v1 remote-catalog
// contract. Providers without a usable listing path fail before or at the
// wire — azure deployment mode and unregistered names with an explicit error,
// jina at the facet dispatch, weknoracloud by the server rejecting the
// unsigned request — and the handler degrades to manual entry, never blocks.
func List(ctx context.Context, providerName string, opts *ListOptions) ([]RemoteModel, error) {
	if opts == nil {
		opts = &ListOptions{}
	}
	baseURL := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if baseURL == "" {
		return nil, ClassifyError(fmt.Errorf("base URL is required"))
	}
	if err := secutils.ValidateURLForSSRF(baseURL); err != nil {
		return nil, ClassifyError(fmt.Errorf("base URL SSRF check failed: %w", err))
	}

	a, err := resolveAdapter(providerName)
	if err != nil {
		return nil, err
	}
	la, ok := a.(ListModelsAdapter)
	if !ok {
		return nil, &ProviderError{
			Kind:    ErrUnsupportedType,
			Message: "provider " + providerName + " does not implement model listing",
		}
	}
	// Pagination (2026-09-12 ruling): adapters opting in via PaginatedLister
	// get page-looped probes — a FULL page fetches the next, bounded by the
	// result cap and a hard page ceiling; non-paginated adapters keep the
	// v1 single-shot contract. Each page rides the same probe timeout.
	pageSize := 0
	if pl, ok := la.(PaginatedLister); ok {
		pageSize = pl.ListPageSize()
	}
	startPage := opts.PageNo
	if startPage <= 0 {
		startPage = 1
	}
	var models []RemoteModel
	for pageNo := startPage; ; pageNo++ {
		pageOpts := *opts
		pageOpts.PageNo = pageNo
		req, err := la.BuildListRequest(Endpoint{BaseURL: baseURL, Credentials: opts.Credentials}, pageOpts)
		if err != nil {
			return nil, ClassifyError(err)
		}
		// Probe timeout overrides the executor's per-kind default (Request.Timeout
		// semantics, §6.3): the listing probe is a UI affordance, not a model call.
		// NOTE (裁定 #20): the executor applies req.Timeout only when the outer ctx
		// carries no deadline — if a per-request deadline is ever introduced
		// upstream, the effective probe cap becomes min(deadline, 8s) and the env
		// knob alone will not extend it. Current deployment: no per-request
		// deadline (no http.Server timeouts, no Timeout middleware).
		req.Timeout = probeTimeout()
		result, err := defaultExecutor.Do(ctx, ModelKey{
			ModelID: providerName + ":list", ModelName: providerName, Kind: ModelKindChat,
		}, req)
		if err != nil {
			return nil, err
		}
		page, err := la.ParseListResponse(result.Status, result.Header, result.Body)
		if err != nil {
			return nil, normalizeErr(err)
		}
		models = append(models, page...)
		fetched := pageNo - startPage + 1
		if pageSize <= 0 || len(page) < pageSize || len(models) >= maxRemoteModels || fetched >= maxListPages {
			break
		}
	}
	if len(models) > maxRemoteModels {
		models = models[:maxRemoteModels] // design §5.10.2 result cap
	}
	return models, nil
}
