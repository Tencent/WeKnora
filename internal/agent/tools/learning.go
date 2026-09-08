package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// LearningWebCallerAllowed rejects synthetic identities and borrowed execution
// tenants. The service repeats this boundary for HTTP and background operations.
func LearningWebCallerAllowed(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	caller, ok := ctx.Value(types.CallerContextKey).(types.Caller)
	principal, explicit := ctx.Value(types.PrincipalContextKey).(types.Principal)
	tenant, _ := types.TenantIDFromContext(ctx)
	_, machine := types.TenantAPIKeyScopeFromContext(ctx)
	return ok && explicit && !machine && caller.TenantID > 0 && caller.UserID != "" &&
		tenant == caller.TenantID && principal.Type == types.PrincipalWebUser && principal.ID == caller.UserID
}

func IsLearningTool(name string) bool {
	return name == ToolGetLearningProfile || name == ToolRecommendLearningTopics || name == ToolPrepareLearningQuiz
}

// LearningWholeKBIDs admits only complete, workspace-owned Wiki scopes. A
// document/tag mention cannot silently widen into a whole learning graph.
func LearningWholeKBIDs(targets types.SearchTargets, ownedWikiIDs []string) []string {
	owned := make(map[string]bool, len(ownedWikiIDs))
	for _, id := range ownedWikiIDs {
		owned[id] = true
	}
	var ids []string
	for _, target := range targets {
		if searchTargetIsWholeKB(target) && owned[target.KnowledgeBaseID] {
			ids = append(ids, target.KnowledgeBaseID)
		}
	}
	return dedupNonEmptyStrings(ids)
}

type learningTool struct {
	BaseTool
	service       interfaces.LearningService
	wiki          interfaces.WikiPageService
	kbIDs         []string
	memoryEnabled *bool
}

func NewLearningTool(name string, svc interfaces.LearningService, wiki interfaces.WikiPageService, kbIDs []string, memoryEnabled *bool) types.Tool {
	description := "Read your opted-in learning overview for a bound Wiki knowledge base. Mastery is a BKT estimate from assessed practice, not document familiarity."
	properties := map[string]any{
		"knowledge_base_id": map[string]any{"type": "string", "description": "A bound, workspace-owned Wiki knowledge base ID (bN)."},
	}
	required := []string{"knowledge_base_id"}
	switch name {
	case ToolRecommendLearningTopics:
		description = "Recommend next Wiki topics using the user's assessed practice, related pages and optional interests. Quote only the returned reason codes. Related links are not prerequisites. Read the recommended Wiki page before explaining its content."
		properties["limit"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 10}
	case ToolPrepareLearningQuiz:
		description = "Prepare a source-backed quiz for a Wiki page. Returns a quiz card reference only; the human answers in the interface. Never answer for the learner or infer mastery from conversation. A pending quiz is generated asynchronously; do not poll with repeated tool calls."
		properties["slug"] = map[string]any{"type": "string", "description": "Exact Wiki slug from wiki_search/wiki_read_page, such as concept/rag."}
		required = append(required, "slug")
	case ToolGetLearningProfile:
	default:
		return nil
	}
	schema, _ := json.Marshal(map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false})
	return &learningTool{BaseTool: NewBaseTool(name, description, schema), service: svc, wiki: wiki,
		kbIDs: append([]string(nil), kbIDs...), memoryEnabled: memoryEnabled}
}

func (t *learningTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	if !LearningWebCallerAllowed(ctx) || t.service == nil {
		return learningFailure(types.ErrLearningForbidden), nil
	}
	var input struct {
		KnowledgeBaseID string `json:"knowledge_base_id"`
		Slug            string `json:"slug"`
		Limit           int    `json:"limit"`
	}
	if json.Unmarshal(args, &input) != nil || strings.TrimSpace(input.KnowledgeBaseID) == "" {
		return learningFailure(types.ErrLearningInvalid), nil
	}
	allowed := false
	for _, id := range t.kbIDs {
		allowed = allowed || id == input.KnowledgeBaseID
	}
	if !allowed {
		return learningFailure(types.ErrLearningForbidden), nil
	}
	ctx = types.ApplyAgentMemoryPreference(ctx, t.memoryEnabled)
	data := map[string]any{"knowledge_base_id": input.KnowledgeBaseID}
	switch t.Name() {
	case ToolGetLearningProfile:
		profile, err := t.service.Overview(ctx, input.KnowledgeBaseID)
		if err != nil {
			return learningFailure(err), nil
		}
		data["display_type"], data["profile"] = "learning_profile", profile
	case ToolRecommendLearningTopics:
		if input.Limit == 0 {
			input.Limit = 5
		}
		if input.Limit < 1 || input.Limit > 10 {
			return learningFailure(types.ErrLearningInvalid), nil
		}
		recommendations, err := t.service.Recommend(ctx, input.KnowledgeBaseID, input.Limit)
		if err != nil {
			return learningFailure(err), nil
		}
		data["display_type"], data["recommendations"] = "learning_recommendations", recommendations
	case ToolPrepareLearningQuiz:
		if t.wiki == nil || strings.TrimSpace(input.Slug) == "" {
			return learningFailure(types.ErrLearningInvalid), nil
		}
		page, err := t.wiki.GetPageBySlug(ctx, input.KnowledgeBaseID, input.Slug)
		if err != nil || page == nil {
			return learningFailure(types.ErrLearningNotFound), nil
		}
		quiz, err := t.service.PrepareQuiz(ctx, page.ID)
		if err != nil {
			return learningFailure(err), nil
		}
		// Do not marshal the quiz: it can contain already-answered explanations.
		// The authenticated UI fetches the current quiz separately from the model.
		data["display_type"], data["quiz_id"] = "learning_quiz", quiz.ID
		data["page_id"], data["title"], data["status"] = page.ID, page.Title, quiz.Status
		data["instruction"] = "The learner can open the quiz card. Do not supply answers or repeatedly poll a pending quiz."
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return learningFailure(err), nil
	}
	return &types.ToolResult{Success: true, Output: string(encoded), Data: data}, nil
}

func learningFailure(err error) *types.ToolResult {
	message := "Learning request failed. Try again later."
	switch {
	case errors.Is(err, types.ErrLearningForbidden), errors.Is(err, types.ErrLearningNotFound):
		message = "Learning is unavailable for this caller or knowledge scope. It requires a workspace-owned Wiki KB and a Web user."
	case errors.Is(err, types.ErrLearningDisabled):
		message = "Learning is not enabled by the user. They can enable it in the Wiki learning panel; tools cannot opt in for them."
	case errors.Is(err, types.ErrLearningStale):
		message = "The source changed. Open the Wiki learning panel to prepare a fresh quiz."
	case errors.Is(err, types.ErrLearningEvidence):
		message = "There is not enough current source evidence for a quiz. Read the Wiki sources instead."
	case errors.Is(err, types.ErrLearningBusy), errors.Is(err, types.ErrLearningNotReady):
		message = "Quiz generation is pending. The interface will refresh it; do not repeat the tool call."
	case errors.Is(err, types.ErrLearningInvalid):
		message = "Invalid learning arguments. Use an in-scope Wiki knowledge base and an exact page slug."
	}
	return &types.ToolResult{Success: false, Error: message}
}
