package opensearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"

	osapi "github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)

// BatchUpdateChunkEnabledStatus flips is_enabled for the given chunks. The
// status map is grouped by value into one _update_by_query per distinct value
// (mirrors the Qdrant grouping pattern), so the request body carries the
// chunk ids via a terms filter and the new value via bound script params —
// never per-chunk string interpolation.
//
// Targets the cross-dim <base>_* pattern: a chunk's embedding dimension is
// not known here, and the same chunk_id is unique across the store's dim
// indices + the keyword-only index.
func (r *Repository) BatchUpdateChunkEnabledStatus(ctx context.Context, chunkStatusMap map[string]bool) error {
	if len(chunkStatusMap) == 0 {
		return nil
	}
	groups := map[bool][]string{}
	for id, v := range chunkStatusMap {
		groups[v] = append(groups[v], id)
	}
	// Deterministic order (false then true) for predictable behavior/tests.
	for _, v := range []bool{false, true} {
		ids := groups[v]
		if len(ids) == 0 {
			continue
		}
		sort.Strings(ids)
		if err := r.updateByQueryScript(ctx, ids,
			"ctx._source.is_enabled = params.v", map[string]any{"v": v}); err != nil {
			return err
		}
	}
	return nil
}

// BatchUpdateChunkTagID sets tag_id for the given chunks, grouped by tag.
func (r *Repository) BatchUpdateChunkTagID(ctx context.Context, chunkTagMap map[string]string) error {
	if len(chunkTagMap) == 0 {
		return nil
	}
	groups := map[string][]string{}
	for id, tag := range chunkTagMap {
		groups[tag] = append(groups[tag], id)
	}
	tags := make([]string, 0, len(groups))
	for tag := range groups {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	for _, tag := range tags {
		ids := groups[tag]
		sort.Strings(ids)
		if err := r.updateByQueryScript(ctx, ids,
			"ctx._source.tag_id = params.v", map[string]any{"v": tag}); err != nil {
			return err
		}
	}
	return nil
}

// updateByQueryScript runs an _update_by_query over the cross-dim <base>_*
// pattern, matching the given chunk ids via a terms filter and applying a
// constant Painless source with caller values flowing only through bound
// params (Painless-injection-safe).
func (r *Repository) updateByQueryScript(
	ctx context.Context, chunkIDs []string, source string, params map[string]any,
) error {
	body, err := json.Marshal(map[string]any{
		"query": map[string]any{
			"terms": map[string]any{"chunk_id": chunkIDs},
		},
		"script": map[string]any{
			"lang":   "painless",
			"source": source,
			"params": params,
		},
	})
	if err != nil {
		return fmt.Errorf("opensearch: marshal update_by_query body: %w", err)
	}
	// Q2: UpdateByQueryParams.Refresh is *bool — the wire value "wait_for" is
	// not expressible via the typed SDK, so we force an immediate refresh.
	refresh := true
	resp, err := r.client.UpdateByQuery(ctx, osapi.UpdateByQueryReq{
		Indices: []string{r.baseIndex + "_*"},
		Body:    bytes.NewReader(body),
		Params:  osapi.UpdateByQueryParams{Refresh: &refresh},
	})
	if err != nil {
		return wrapTransport(err)
	}
	if resp == nil {
		return nil
	}
	defer drainAndClose(resp.Inspect().Response.Body)
	return inspectByQueryResponse(io.LimitReader(resp.Inspect().Response.Body, 16<<20))
}
