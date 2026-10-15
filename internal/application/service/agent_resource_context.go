package service

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/agent/operations"
	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/types"
)

// Context hints use the same projected, authenticated GETs as explicit reads.
// A model ID or connector ID in a client payload is never an access grant.
func (s *agentService) readPageResource(
	ctx context.Context, operation string, params map[string]any,
) *operations.Projected {
	if s.operationDispatcher == nil {
		return nil
	}
	identity, ok := tools.OperationIdentityFromContext(ctx)
	if !ok {
		return nil
	}
	op, ok := operations.Builtin().Get(operation)
	if !ok || op.Risk != operations.RiskRead || op.Method != http.MethodGet {
		return nil
	}
	raw := make(map[string]json.RawMessage, len(params))
	for key, value := range params {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil
		}
		raw[key] = encoded
	}
	call, err := op.Build(raw)
	if err != nil {
		return nil
	}
	response, err := s.operationDispatcher.Dispatch(ctx, identity, call)
	if err != nil || response == nil || response.Status != http.StatusOK {
		return nil
	}
	projected, err := op.Project(response.Body)
	if err != nil {
		return nil
	}
	return &projected
}

func (s *agentService) resolveResourcePageContext(
	ctx context.Context, page *types.PageContext, info *agent.PageContextInfo,
) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if page.ModelID != "" {
		if p := s.readPageResource(ctx, "model.get", map[string]any{"model_id": page.ModelID}); p != nil {
			info.Model = &agent.PageContextResource{ID: page.ModelID, Name: p.Label("name")}
		}
	}
	if page.DataSourceID != "" {
		p := s.readPageResource(ctx, "data_source.get", map[string]any{"data_source_id": page.DataSourceID})
		if p != nil &&
			(page.KnowledgeBaseID == "" || p.Label("knowledge_base_id") == page.KnowledgeBaseID) {
			info.DataSource = &agent.PageContextResource{ID: page.DataSourceID, Name: p.Label("name")}
		}
	}
}

func (s *agentService) resolveFAQPageContext(
	ctx context.Context, page *types.PageContext, info *agent.PageContextInfo,
) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for _, id := range page.FAQEntryIDs {
		if ctx.Err() != nil {
			break
		}
		p := s.readPageResource(ctx, "faq.get_entry", map[string]any{
			"knowledge_base_id": page.KnowledgeBaseID, "entry_id": id,
		})
		if p != nil {
			info.FAQEntries = append(info.FAQEntries, &agent.PageContextResource{
				ID: strconv.FormatInt(id, 10), Name: p.Label("standard_question"),
			})
		}
	}
}

func recognizedPageType(kind string) string {
	switch kind {
	case "models", "data_sources", "faq":
		return kind
	default:
		return ""
	}
}
