package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	customknowledge "github.com/Tencent/WeKnora/internal/custom/service/knowledge"
	"github.com/Tencent/WeKnora/internal/custom/service/knowledgegraph"
	transcriptservice "github.com/Tencent/WeKnora/internal/custom/service/transcript"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/wikiaudit"
)

type wikiWritePageTool struct {
	BaseTool
	wikiPageService  interfaces.WikiPageService
	knowledgeService interfaces.KnowledgeService
	kbIDs            []string
	routes           *WikiRouteResolver
	searchTargets    types.SearchTargets
	scopeEnforced    bool
	semanticAdapter  customknowledge.SemanticIdentityAdapter
}

// WithSemanticIdentityAdapter injects the WeKnora semantic identity service.
// Knowledge-object comparisons fail closed when no adapter is configured.
func (t *wikiWritePageTool) WithSemanticIdentityAdapter(adapter customknowledge.SemanticIdentityAdapter) *wikiWritePageTool {
	t.semanticAdapter = adapter
	return t
}

// Canonical writes are serialized in-process. The second identity scan below
// remains mandatory because a process may race with another instance or a
// background ingest worker.
var canonicalWriteMu sync.Mutex

// NewWikiWritePageTool creates a new wiki_write_page tool
func NewWikiWritePageTool(
	wikiPageService interfaces.WikiPageService,
	kbIDs []string,
	knowledgeService interfaces.KnowledgeService,
	routes ...*WikiRouteResolver,
) *wikiWritePageTool {
	return &wikiWritePageTool{
		BaseTool: NewBaseTool(
			ToolWikiWritePage,
			"Create a new Wiki page or completely overwrite an existing one. Automatically handles outbound links.",
			json.RawMessage(`{
				"type": "object",
				"properties": {
					"slug": {
						"type": "string",
						"description": "The slug of the Wiki page (e.g. 'entity/hunyuan-damoxing')"
					},
					"title": {
						"type": "string",
						"description": "The title of the page"
					},
					"summary": {
						"type": "string",
						"description": "A one-sentence summary for the index listing"
					},
					"content": {
						"type": "string",
						"description": "The FULL, complete Markdown content of the page. Do NOT use placeholders."
					},
					"page_type": {
						"type": "string",
						"description": "The storage page type. For VidSage knowledge-object pages whose frontmatter contains knowledge_object_id, source_video_id, and transcript_generation, this MUST be 'index'; their business type belongs in frontmatter.type. Other authored Wiki pages may use types such as 'entity', 'concept', 'synthesis', or 'comparison'."
					},
					"aliases": {
						"type": "array",
						"items": {"type": "string"},
						"description": "A list of aliases for the page (optional). If provided, these will COMPLETELY REPLACE the existing aliases of the page."
					},
					"source_refs": {
						"type": "array",
						"items": {"type": "string"},
						"description": "A list of short dN source document IDs that contributed to this page. If provided, these will COMPLETELY REPLACE the existing source_refs of the page."
					}
				},
				"required": ["slug", "title", "summary", "content", "page_type"]
			}`),
		),
		wikiPageService:  wikiPageService,
		knowledgeService: knowledgeService,
		kbIDs:            kbIDs,
		routes:           firstWikiRoute(routes),
	}
}

// WithSearchTargets enables the Agent authorization boundary for source_refs.
// An Agent turn with no search target must reject every source document.
func (t *wikiWritePageTool) WithSearchTargets(searchTargets types.SearchTargets) *wikiWritePageTool {
	t.searchTargets = searchTargets
	t.scopeEnforced = true
	return t
}

func (t *wikiWritePageTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	var probe struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(args, &probe); err != nil {
		return t.executeUnlocked(ctx, args)
	}
	if !customknowledge.IsWikiObjectCandidate(probe.Content) {
		return t.executeUnlocked(ctx, args)
	}
	var result *types.ToolResult
	var innerErr error
	locked := false
	if locker, ok := t.wikiPageService.(interface {
		WithCanonicalIdentityLock(context.Context, string, string, func(context.Context) error) error
	}); ok {
		locked = true
		lockScope := strings.Join(t.kbIDs, ",")
		lockErr := locker.WithCanonicalIdentityLock(ctx, lockScope, probe.Title, func(lockCtx context.Context) error {
			result, innerErr = t.executeUnlocked(lockCtx, args)
			return innerErr
		})
		if lockErr != nil {
			if innerErr != nil {
				return result, innerErr
			}
			return &types.ToolResult{Success: false, Error: "Canonical identity lock failed: " + lockErr.Error()}, nil
		}
	}
	if locked {
		return result, nil
	}
	return t.executeUnlocked(ctx, args)
}

func (t *wikiWritePageTool) executeUnlocked(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	// Attribute every page write performed by this tool to the agent so
	// revision history distinguishes agent edits from pipeline/user ones.
	ctx = types.WithWikiEditSource(ctx, types.WikiEditSourceAgent)
	var params struct {
		Slug       string    `json:"slug"`
		Title      string    `json:"title"`
		Summary    string    `json:"summary"`
		Content    string    `json:"content"`
		PageType   string    `json:"page_type"`
		Aliases    *[]string `json:"aliases"`
		SourceRefs *[]string `json:"source_refs"`
	}

	if err := json.Unmarshal(args, &params); err != nil {
		return &types.ToolResult{Success: false, Error: "Failed to parse arguments: " + err.Error()}, nil
	}

	if len(t.kbIDs) == 0 {
		return &types.ToolResult{Success: false, Error: "No knowledge bases available for editing"}, nil
	}
	if params.Title == "" || params.PageType == "" || params.Content == "" || params.Summary == "" {
		return &types.ToolResult{Success: false, Error: "title, summary, content, and page_type are required for write action"}, nil
	}

	var sourceObject *customknowledge.WikiObjectValidation
	var productionSourceRefs []string
	var productionContent string
	if customknowledge.IsWikiObjectCandidate(params.Content) {
		validation, validationErr := customknowledge.ValidateWikiObjectWritePage(params.Content, params.PageType, "", "")
		if validationErr != nil {
			return &types.ToolResult{Success: false, Error: "Invalid knowledge object content contract: " + validationErr.Error()}, nil
		}
		relations, relationErr := customknowledge.ParseWikiObjectRelations(params.Content)
		if relationErr != nil {
			return &types.ToolResult{Success: false, Error: "Invalid knowledge object relation contract: " + relationErr.Error()}, nil
		}
		validation.Relations = relations
		canonicalTitle := customknowledge.CanonicalKnowledgeTitle(validation.Title)
		if canonicalTitle != validation.Title {
			canonicalContent, rewriteErr := customknowledge.RewriteWikiObjectIdentity(
				params.Content, validation.KnowledgeObjectID, canonicalTitle,
			)
			if rewriteErr != nil {
				return &types.ToolResult{Success: false, Error: "Failed to normalize knowledge object title: " + rewriteErr.Error()}, nil
			}
			params.Content = canonicalContent
			params.Title = canonicalTitle
			validation, validationErr = customknowledge.ValidateWikiObjectWritePage(params.Content, params.PageType, "", "")
			if validationErr != nil {
				return &types.ToolResult{Success: false, Error: "Normalized knowledge object content is invalid: " + validationErr.Error()}, nil
			}
			validation.Relations = relations
		}
		sourceObject = &validation
		productionSourceRefs = append([]string(nil), validation.SourceRefs...)
		productionContent = params.Content
	}
	if sourceObject != nil {
		canonicalWriteMu.Lock()
		defer canonicalWriteMu.Unlock()
	}

	// Validate + normalize the slug up front. The model routinely emits
	// malformed slugs (stray characters, mangled UUIDs); persisting them
	// verbatim creates unreachable pages and dead cross-links.
	normalizedSlug, slugErr := normalizeAndValidateWikiSlug(params.Slug)
	if slugErr != nil {
		return &types.ToolResult{Success: false, Error: slugErr.Error()}, nil
	}
	params.Slug = normalizedSlug

	if strings.HasPrefix(params.Slug, "video/") {
		// The video namespace is system-owned. The model may describe its business
		// type as knowledge_base or copy an object type into page_type; neither may
		// change the storage role of the fixed video index page.
		params.PageType = "index"
		if err := customknowledge.ValidateVideoKnowledgeIndexPage(
			params.Content, params.PageType, params.Slug, strings.TrimPrefix(params.Slug, "video/"), "", "",
		); err != nil {
			return &types.ToolResult{Success: false, Error: "Invalid video knowledge index contract: " + err.Error()}, nil
		}
	} else {
		// WeKnora reserves summary and knowledge_base as storage page types. Keep
		// authored non-video pages on a legal storage type while their business
		// type remains in frontmatter.
		switch strings.ToLower(params.PageType) {
		case types.WikiPageTypeSummary, "knowledge_base":
			slog.WarnContext(ctx, "wiki_WritePage: page_type 命中保留字，兜底改为 synthesis",
				"original", params.PageType, "title", params.Title, "slug", params.Slug)
			params.PageType = types.WikiPageTypeSynthesis
		}
	}

	// Resolve and authorize provenance before choosing a creation target. In a
	// multi-Wiki Agent, source documents provide a server-owned KB hint and
	// remove the need for a model-visible knowledge_base_id argument.
	var resolvedRefs []string
	var err error
	if params.SourceRefs != nil {
		if t.scopeEnforced {
			resolvedRefs, err = resolveAuthorizedSourceRefs(ctx, t.searchTargets, *params.SourceRefs, t.knowledgeService)
			if err != nil {
				return &types.ToolResult{Success: false, Error: "Invalid source_refs: " + err.Error()}, nil
			}
		} else {
			resolvedRefs = resolveSourceRefs(ctx, t.knowledgeService, *params.SourceRefs)
		}
	}
	if sourceObject != nil {
		// Validate the submitted contribution before canonical merging. A
		// canonical page may contain several source documents, while each Agent
		// write must still prove exactly one current transcript source.
		if validationErr := t.validateKnowledgeObjectEvidence(ctx, *sourceObject); validationErr != nil {
			return &types.ToolResult{Success: false, Error: "Invalid knowledge object evidence contract: " + validationErr.Error()}, nil
		}
	}
	// Resolve existing pages across every legal Wiki KB. Cached provenance only
	// influences lookup order; ambiguous slugs are never silently written to
	// the first KB. New pages require one unambiguous creation target.
	existingPage, kbID, err := resolveUniqueWikiPage(ctx, t.wikiPageService, params.Slug, t.kbIDs, t.routes)
	if errors.Is(err, errWikiPageNotFoundInScope) {
		existingPage = nil
		sourceKBHints, sourceErr := wikiKnowledgeBasesForSourceRefs(
			ctx, resolvedRefs, t.knowledgeService, t.kbIDs,
		)
		if sourceErr != nil {
			return &types.ToolResult{Success: false, Error: "Failed to resolve source_refs routing: " + sourceErr.Error()}, nil
		}
		kbID, err = resolveWikiCreateKB(params.Slug, t.kbIDs, t.routes, sourceKBHints...)
	}
	if err != nil {
		return &types.ToolResult{Success: false, Error: "Failed to resolve wiki target: " + err.Error()}, nil
	}
	if sourceObject != nil {
		var semanticPage *types.WikiPage
		var semanticValidation customknowledge.WikiObjectValidation
		var semanticErr error
		semanticPage, semanticValidation, semanticErr = t.resolveSemanticKnowledgeObject(ctx, kbID, existingPage, *sourceObject)
		if semanticErr != nil {
			return &types.ToolResult{Success: false, Error: "Semantic knowledge identity is unresolved: " + semanticErr.Error()}, nil
		}
		if semanticValidation.KnowledgeObjectID != "" {
			canonicalContent, rewriteErr := customknowledge.RewriteWikiObjectIdentity(
				params.Content, semanticValidation.KnowledgeObjectID, semanticValidation.Title,
			)
			if rewriteErr != nil {
				return &types.ToolResult{Success: false, Error: "Failed to apply canonical knowledge identity: " + rewriteErr.Error()}, nil
			}
			params.Content = canonicalContent
			params.Title = semanticValidation.Title
			if semanticPage != nil {
				params.Slug = semanticPage.Slug
				existingPage = semanticPage
			}
			mergedContent, mergeErr := customknowledge.MergeCanonicalWikiObject(
				pageContent(existingPage), params.Content,
			)
			if mergeErr != nil {
				return &types.ToolResult{Success: false, Error: "Failed to merge canonical evidence contributions: " + mergeErr.Error()}, nil
			}
			params.Content = mergedContent
			updatedValidation, validationErr := customknowledge.ValidateWikiObjectWritePage(params.Content, params.PageType, "", "")
			if validationErr != nil {
				return &types.ToolResult{Success: false, Error: "Canonical knowledge object content is invalid: " + validationErr.Error()}, nil
			}
			updatedValidation.Relations = sourceObject.Relations
			sourceObject = &updatedValidation
		}
		if validationErr := t.validateKnowledgeObjectRelations(ctx, kbID, *sourceObject); validationErr != nil {
			return &types.ToolResult{Success: false, Error: "Invalid knowledge object relation contract: " + validationErr.Error()}, nil
		}
	}

	// Summary pages are system-owned: they are generated deterministically
	// from a source document and keyed by its knowledge ID
	// (summary/<knowledgeID>). Letting the agent CREATE one with an
	// arbitrary/hand-typed slug is precisely how mangled-UUID ghost summary
	// existing summary page, but never fabricating a new one.
	if existingPage == nil &&
		(isSummaryNamespace(params.Slug) || strings.EqualFold(params.PageType, types.WikiPageTypeSummary)) {
		return &types.ToolResult{
			Success: false,
			Error: "summary pages are generated automatically from source documents and cannot be created manually. " +
				"Use page_type 'synthesis'/'comparison'/'entity'/'concept' for authored pages, " +
				"or target an existing summary page to update it.",
		}, nil
	}

	// Auto-repair dead [[slug]] references in the body before persisting.
	// This rewrites LLM-mangled links (most importantly UUID-based summary
	// slugs) back to their real target when a confident match exists, and
	// leaves everything else untouched. Best-effort — never block the write.
	if repaired, changed, rerr := t.wikiPageService.RepairContentLinks(ctx, kbID, params.Slug, params.Content); rerr == nil && changed {
		params.Content = repaired
	}
	existingV2Page := isProtectedKnowledgeV2Page(existingPage)
	if existingV2Page && sourceObject == nil && !strings.HasPrefix(params.Slug, "video/") {
		return &types.ToolResult{Success: false, Error: protectedKnowledgeV2MutationError}, nil
	}
	requiresV2Provenance := sourceObject != nil || strings.HasPrefix(params.Slug, "video/") || existingV2Page
	auditSourceRefs := resolvedRefs
	if len(auditSourceRefs) == 0 && sourceObject != nil {
		auditSourceRefs = productionSourceRefs
	}
	auditContent := params.Content
	if sourceObject != nil && productionContent != "" {
		auditContent = productionContent
	}
	auditSeed, auditErr := t.prepareKnowledgeV2PageWrite(ctx, kbID, auditContent, auditSourceRefs, requiresV2Provenance)
	if auditErr != nil {
		return &types.ToolResult{Success: false, Error: "Invalid V2 production provenance: " + auditErr.Error()}, nil
	}

	var action string
	var persistedPage *types.WikiPage
	if existingPage != nil {
		// Update
		existingPage.Title = params.Title
		existingPage.Summary = params.Summary
		existingPage.Content = params.Content
		existingPage.PageType = params.PageType
		if params.Aliases != nil {
			existingPage.Aliases = *params.Aliases
		}

		if params.SourceRefs != nil || sourceObject != nil {
			existingPage.SourceRefs = sourceRefsForPage(params.Content, resolvedRefs)
		}

		persistedPage, err = t.wikiPageService.UpdatePage(ctx, existingPage)
		if err != nil {
			return &types.ToolResult{Success: false, Error: "Failed to update page: " + err.Error()}, nil
		}
		action = "updated"
	} else {
		// Create
		newPage := &types.WikiPage{
			KnowledgeBaseID: kbID,
			Slug:            params.Slug,
			Title:           params.Title,
			Summary:         params.Summary,
			Content:         params.Content,
			PageType:        params.PageType,
			SourceRefs:      sourceRefsForPage(params.Content, resolvedRefs),
		}
		if params.Aliases != nil {
			newPage.Aliases = *params.Aliases
		}
		persistedPage, err = t.wikiPageService.CreatePage(ctx, newPage)
		if err != nil {
			return &types.ToolResult{Success: false, Error: "Failed to create page: " + err.Error()}, nil
		}
		action = "created"
	}
	if persistedPage == nil || strings.TrimSpace(persistedPage.ID) == "" {
		return &types.ToolResult{Success: false, Error: "Wiki page write succeeded without returning a page ID"}, nil
	}
	t.routes.remember(params.Slug, kbID)

	// Inject cross-links so other pages know about this new/updated entity
	t.wikiPageService.InjectCrossLinks(ctx, kbID, []string{params.Slug})

	// Rebuild the index page to reflect the new/updated summary
	_ = t.wikiPageService.RebuildIndexPage(ctx, kbID)

	output := fmt.Sprintf("Successfully %s page [[%s]].\n- Wiki page ID: %s\n- Title: %s\n- Type: %s\n- Summary: %s\n- Content length: %d chars", action, params.Slug, persistedPage.ID, params.Title, params.PageType, params.Summary, len(params.Content))
	if params.Aliases != nil && len(*params.Aliases) > 0 {
		output += fmt.Sprintf("\n- Aliases: %s", strings.Join(*params.Aliases, ", "))
	}
	if params.SourceRefs != nil {
		output += fmt.Sprintf("\n- Source refs: %d document(s)", len(resolvedRefs))
	}
	data := map[string]interface{}{
		"display_type": "wiki_write_page",
		"action":       action,
		"wiki_page_id": persistedPage.ID,
		"slug":         params.Slug,
		"title":        params.Title,
		"page_type":    params.PageType,
		"summary":      params.Summary,
	}
	if sourceObject != nil {
		data["knowledge_object_id"] = sourceObject.KnowledgeObjectID
		data["canonical_wiki_page_id"] = persistedPage.ID
		output += fmt.Sprintf("\n- Knowledge object ID: %s", sourceObject.KnowledgeObjectID)
	}
	if auditSeed != nil {
		productionSource, auditErr := recordKnowledgeV2PageWrite(
			ctx, *auditSeed, action, persistedPage.ID, params.Slug, params.PageType, persistedPage.Version,
		)
		if auditErr != nil {
			return &types.ToolResult{Success: false, Error: "Failed to record V2 production provenance: " + auditErr.Error()}, nil
		}
		data["production_source"] = productionSource
	}

	return &types.ToolResult{
		Success: true,
		Output:  output,
		Data:    data,
	}, nil
}

type knowledgeV2AuditSeed struct {
	identity   wikiaudit.SourceIdentity
	taskID     string
	sessionID  string
	skillName  string
	toolCallID string
}

func (t *wikiWritePageTool) prepareKnowledgeV2PageWrite(
	ctx context.Context,
	kbID string,
	content string,
	resolvedSourceRefs []string,
	required bool,
) (*knowledgeV2AuditSeed, error) {
	if !required {
		return nil, nil
	}
	execMeta, ok := ToolExecFromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("wiki_audit_event:knowledge_v2_execution_context_missing")
	}
	skillName := knowledgeV2SkillName(execMeta.PinnedSkillNames)
	if skillName == "" {
		return nil, fmt.Errorf("wiki_audit_event:knowledge_v2_skill_missing")
	}
	identity, err := wikiaudit.ParseKnowledgeV2PageIdentity(content, kbID, resolvedSourceRefs)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(execMeta.ProductionVideoID) != identity.VideoID ||
		strings.TrimSpace(execMeta.ProductionGeneration) != identity.TranscriptGeneration {
		return nil, fmt.Errorf("wiki_audit_event:production_task_identity_mismatch")
	}
	if err := t.validateKnowledgeV2SourceIdentity(ctx, identity); err != nil {
		return nil, err
	}
	seed := &knowledgeV2AuditSeed{
		identity: identity, taskID: strings.TrimSpace(execMeta.ProductionTaskID),
		sessionID: strings.TrimSpace(execMeta.SessionID), skillName: skillName,
		toolCallID: strings.TrimSpace(execMeta.ToolCallID),
	}
	probe := wikiaudit.NewKnowledgeV2PageWrite(
		seed.identity, seed.taskID, seed.sessionID, seed.skillName, seed.toolCallID,
		"pending", "pending", "pending", "pending", 0,
	)
	if err := probe.Validate(); err != nil {
		return nil, err
	}
	return seed, nil
}

func (t *wikiWritePageTool) validateKnowledgeV2SourceIdentity(ctx context.Context, identity wikiaudit.SourceIdentity) error {
	if !t.scopeEnforced {
		return nil
	}
	knowledge, err := authorizeKnowledgeInSearchTargets(ctx, t.searchTargets, identity.SourceKnowledgeID, t.knowledgeService)
	if err != nil {
		return fmt.Errorf("wiki_audit_event:source_document_out_of_scope: %w", err)
	}
	metadata, err := knowledge.ManualMetadata()
	if err != nil {
		return fmt.Errorf("wiki_audit_event:source_document_metadata_invalid: %w", err)
	}
	if metadata == nil || strings.TrimSpace(metadata.Content) == "" {
		return fmt.Errorf("wiki_audit_event:source_document_content_missing")
	}
	document, err := transcriptservice.ParseSourceContent(metadata.Content)
	if err != nil {
		return fmt.Errorf("wiki_audit_event:source_document_invalid: %w", err)
	}
	if document.VideoID != identity.VideoID || document.TranscriptGeneration != identity.TranscriptGeneration {
		return fmt.Errorf("wiki_audit_event:source_document_identity_mismatch")
	}
	return nil
}

func knowledgeV2SkillName(names []string) string {
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "extract-video-knowledge" || name == "extract-video-knowledge-v2" {
			return name
		}
	}
	return ""
}

func recordKnowledgeV2PageWrite(
	ctx context.Context,
	seed knowledgeV2AuditSeed,
	action string,
	pageID string,
	slug string,
	pageType string,
	version int,
) (map[string]interface{}, error) {
	event := wikiaudit.NewKnowledgeV2PageWrite(
		seed.identity, seed.taskID, seed.sessionID, seed.skillName, seed.toolCallID,
		action, pageID, slug, pageType, version,
	)
	auditJSON, err := event.JSON()
	if err != nil {
		return nil, err
	}
	slog.InfoContext(ctx, "wiki audit event",
		"event", auditJSON,
		"page_producer", event.PageProducer,
		"task_id", event.TaskID,
		"session_id", event.SessionID,
		"skill_name", event.SkillName,
		"tool_name", event.ToolName,
		"tool_call_id", event.ToolCallID,
		"source_knowledge_id", event.SourceKnowledgeID,
		"page_id", event.PageID,
		"slug", event.Slug,
		"version", version,
	)
	return map[string]interface{}{
		"page_producer":      event.PageProducer,
		"task_id":            event.TaskID,
		"session_id":         event.SessionID,
		"skill_name":         event.SkillName,
		"tool_name":          event.ToolName,
		"tool_call_id":       event.ToolCallID,
		"source_document_id": event.SourceKnowledgeID,
		"event_id":           event.EventID,
	}, nil
}

func (t *wikiWritePageTool) resolveSemanticKnowledgeObject(
	ctx context.Context,
	kbID string,
	existingPage *types.WikiPage,
	source customknowledge.WikiObjectValidation,
) (*types.WikiPage, customknowledge.WikiObjectValidation, error) {
	sourceCandidate := customknowledge.IdentityCandidate{
		KnowledgeObjectID: source.KnowledgeObjectID, KnowledgeType: source.KnowledgeType,
		EntitySubType: source.EntitySubType, Title: source.Title, Aliases: source.Aliases, CoreContent: source.CoreContent,
		SourceVideoID: source.SourceVideoID, TranscriptGeneration: source.TranscriptGeneration,
		StructureFields: source.StructureFields, EvidenceIDs: source.EvidenceIDs,
	}
	type semanticMatch struct {
		page       *types.WikiPage
		validation customknowledge.WikiObjectValidation
		identity   customknowledge.IdentityCandidate
	}
	matches := make([]semanticMatch, 0)
	if existingPage != nil && existingPage.Status == types.WikiPageStatusPublished {
		if candidate, validationErr := customknowledge.ValidateWikiObjectPage(existingPage.Content, existingPage.PageType, "", ""); validationErr == nil {
			candidateIdentity := customknowledge.IdentityCandidate{
				KnowledgeObjectID: candidate.KnowledgeObjectID, KnowledgeType: candidate.KnowledgeType,
				EntitySubType: candidate.EntitySubType, Title: candidate.Title,
				Aliases: append(append([]string(nil), existingPage.Aliases...), candidate.Aliases...), CoreContent: candidate.CoreContent,
				SourceVideoID: candidate.SourceVideoID, TranscriptGeneration: candidate.TranscriptGeneration,
				StructureFields: candidate.StructureFields, EvidenceIDs: candidate.EvidenceIDs,
			}
			assessment, compareErr := t.compareSemanticIdentity(ctx, sourceCandidate, candidateIdentity)
			if compareErr != nil {
				return nil, customknowledge.WikiObjectValidation{}, fmt.Errorf("semantic identity adapter failed: %w", compareErr)
			}
			if assessment.Decision == "uncertain" {
				return nil, customknowledge.WikiObjectValidation{}, fmt.Errorf("semantic identity is uncertain: %s", assessment.Reason)
			}
			if assessment.Decision == "same_object" && assessment.Confidence <= 0 {
				return nil, customknowledge.WikiObjectValidation{}, fmt.Errorf("semantic identity adapter returned invalid confidence")
			}
			if assessment.Decision == "same_object" || sourceCandidate.KnowledgeObjectID == candidate.KnowledgeObjectID {
				return existingPage, candidate, nil
			}
			if assessment.Decision == "different_object" {
				return nil, customknowledge.WikiObjectValidation{}, fmt.Errorf(
					"Wiki page %s already belongs to a different knowledge object: %s",
					existingPage.ID, assessment.Reason,
				)
			}
			return nil, customknowledge.WikiObjectValidation{}, fmt.Errorf(
				"semantic identity adapter returned invalid decision %q", assessment.Decision,
			)
		}
	}
	cursor := ""
	for {
		pages, next, err := t.wikiPageService.ListPagesCursor(ctx, kbID, cursor, 200)
		if err != nil {
			return nil, customknowledge.WikiObjectValidation{}, fmt.Errorf("list semantic identity candidates: %w", err)
		}
		for _, page := range pages {
			if page == nil || page.Status != types.WikiPageStatusPublished || strings.TrimSpace(page.Content) == "" {
				continue
			}
			if existingPage != nil && page.ID == existingPage.ID {
				continue
			}
			candidate, validationErr := customknowledge.ValidateWikiObjectPage(page.Content, page.PageType, "", "")
			if validationErr != nil {
				continue
			}
			candidateIdentity := customknowledge.IdentityCandidate{
				KnowledgeObjectID: candidate.KnowledgeObjectID, KnowledgeType: candidate.KnowledgeType,
				EntitySubType: candidate.EntitySubType, Title: candidate.Title,
				Aliases: append(append([]string(nil), page.Aliases...), candidate.Aliases...), CoreContent: candidate.CoreContent,
				SourceVideoID: candidate.SourceVideoID, TranscriptGeneration: candidate.TranscriptGeneration,
				StructureFields: candidate.StructureFields, EvidenceIDs: candidate.EvidenceIDs,
			}
			if !customknowledge.IdentityRecall(sourceCandidate, candidateIdentity) {
				continue
			}
			assessment, compareErr := t.compareSemanticIdentity(ctx, sourceCandidate, candidateIdentity)
			if compareErr != nil {
				return nil, customknowledge.WikiObjectValidation{}, fmt.Errorf("semantic identity adapter failed: %w", compareErr)
			}
			switch assessment.Decision {
			case "uncertain":
				return nil, customknowledge.WikiObjectValidation{}, fmt.Errorf(
					"candidate %s (%s) is uncertain against Wiki page %s (%s): %s",
					source.Title, source.KnowledgeType, page.ID, candidate.KnowledgeType, assessment.Reason,
				)
			case "same_object":
				if assessment.Confidence <= 0 {
					return nil, customknowledge.WikiObjectValidation{}, fmt.Errorf("semantic identity adapter returned invalid confidence")
				}
				matches = append(matches, semanticMatch{page: page, validation: candidate, identity: candidateIdentity})
			case "different_object":
			default:
				return nil, customknowledge.WikiObjectValidation{}, fmt.Errorf(
					"semantic identity adapter returned invalid decision %q", assessment.Decision,
				)
			}
		}
		if next == "" {
			break
		}
		if next == cursor {
			return nil, customknowledge.WikiObjectValidation{}, fmt.Errorf("semantic identity cursor did not advance")
		}
		cursor = next
	}
	if len(matches) == 0 {
		return nil, customknowledge.WikiObjectValidation{}, nil
	}
	canonicalObjectID := strings.TrimSpace(matches[0].validation.KnowledgeObjectID)
	canonicalIndex := 0
	for index, match := range matches {
		if strings.TrimSpace(match.validation.KnowledgeObjectID) != canonicalObjectID {
			return nil, customknowledge.WikiObjectValidation{}, fmt.Errorf(
				"candidate %s matches multiple canonical object IDs %s and %s; reconcile historical duplicates first",
				source.Title, canonicalObjectID, match.validation.KnowledgeObjectID,
			)
		}
		if customknowledge.CanonicalIdentityLess(match.identity, matches[canonicalIndex].identity) {
			canonicalIndex = index
		}
	}
	return matches[canonicalIndex].page, matches[canonicalIndex].validation, nil
}

func (t *wikiWritePageTool) compareSemanticIdentity(ctx context.Context, left, right customknowledge.IdentityCandidate) (customknowledge.SemanticIdentityAssessment, error) {
	if t.semanticAdapter == nil {
		return customknowledge.SemanticIdentityAssessment{}, fmt.Errorf("semantic identity adapter is not configured")
	}
	return t.semanticAdapter.Compare(ctx, left, right)
}

func pageContent(page *types.WikiPage) string {
	if page == nil {
		return ""
	}
	return page.Content
}

func sourceRefsForPage(content string, fallback []string) []string {
	if validation, err := customknowledge.ValidateWikiObjectPage(content, "index", "", ""); err == nil && len(validation.SourceRefs) > 0 {
		return validation.SourceRefs
	}
	return fallback
}

func (t *wikiWritePageTool) validateKnowledgeObjectRelations(
	ctx context.Context,
	kbID string,
	source customknowledge.WikiObjectValidation,
) error {
	evidenceSet := make(map[string]struct{}, len(source.EvidenceIDs))
	for _, evidenceID := range source.EvidenceIDs {
		evidenceSet[strings.TrimSpace(evidenceID)] = struct{}{}
	}
	for index, relation := range source.Relations {
		for _, evidenceID := range relation.EvidenceIDs {
			if _, ok := evidenceSet[strings.TrimSpace(evidenceID)]; !ok {
				return fmt.Errorf("relations[%d].evidence_ids must belong to the source object evidence_ids", index)
			}
		}
		if !knowledgegraph.IsFormalRelationType(relation.RelationType) {
			return fmt.Errorf("relations[%d].relation_type %q is not allowed", index, relation.RelationType)
		}
		if relation.Confidence < knowledgegraph.FormalRelationConfidenceThreshold {
			return fmt.Errorf("relations[%d].confidence must be at least %.2f", index, knowledgegraph.FormalRelationConfidenceThreshold)
		}
		if relation.TargetObjectID == source.KnowledgeObjectID {
			return fmt.Errorf("relations[%d] cannot target the source object itself", index)
		}
		targetPage, err := t.wikiPageService.GetPageByID(ctx, relation.TargetWikiPageID)
		if err != nil || targetPage == nil || strings.TrimSpace(targetPage.ID) != relation.TargetWikiPageID {
			return fmt.Errorf("relations[%d].target_wiki_page_id %q does not resolve to a readable Wiki page", index, relation.TargetWikiPageID)
		}
		if strings.TrimSpace(targetPage.KnowledgeBaseID) != strings.TrimSpace(kbID) {
			return fmt.Errorf("relations[%d].target_wiki_page_id %q is outside the current knowledge base", index, relation.TargetWikiPageID)
		}
		if targetPage.Status != types.WikiPageStatusPublished {
			return fmt.Errorf("relations[%d].target_wiki_page_id %q is not a published Wiki page", index, relation.TargetWikiPageID)
		}
		target, err := customknowledge.ValidateWikiObjectPage(
			targetPage.Content,
			targetPage.PageType,
			source.SourceVideoID,
			source.TranscriptGeneration,
		)
		if err != nil {
			return fmt.Errorf("relations[%d] target page does not satisfy the knowledge object contract: %w", index, err)
		}
		if target.KnowledgeObjectID != relation.TargetObjectID {
			return fmt.Errorf(
				"relations[%d].target_object_id %q does not match target Wiki page object %q",
				index, relation.TargetObjectID, target.KnowledgeObjectID,
			)
		}
		if !knowledgegraph.IsRelationAllowedForTypes(relation.RelationType, source.KnowledgeType, target.KnowledgeType) {
			return fmt.Errorf(
				"relations[%d] type %q is not allowed from %s to %s",
				index, relation.RelationType, source.KnowledgeType, target.KnowledgeType,
			)
		}
	}
	return nil
}

func (t *wikiWritePageTool) validateKnowledgeObjectEvidence(
	ctx context.Context,
	source customknowledge.WikiObjectValidation,
) error {
	// The strict source-document gate is an Agent execution boundary. Legacy
	// direct callers do not carry a server-owned search scope and retain their
	// existing behavior.
	if !t.scopeEnforced {
		return nil
	}
	if len(source.SourceRefs) != 1 {
		return fmt.Errorf("source_refs must contain exactly one transcript source document ID")
	}
	sourceID := strings.TrimSpace(strings.SplitN(source.SourceRefs[0], "|", 2)[0])
	knowledge, err := authorizeKnowledgeInSearchTargets(ctx, t.searchTargets, sourceID, t.knowledgeService)
	if err != nil {
		return fmt.Errorf("source document is outside the current Agent scope: %w", err)
	}
	metadata, err := knowledge.ManualMetadata()
	if err != nil {
		return fmt.Errorf("read transcript source document metadata: %w", err)
	}
	if metadata == nil || strings.TrimSpace(metadata.Content) == "" {
		return fmt.Errorf("transcript source document content is unavailable")
	}
	document, err := transcriptservice.ParseSourceContent(metadata.Content)
	if err != nil {
		return fmt.Errorf("parse transcript source document: %w", err)
	}
	if document.VideoID != strings.TrimSpace(source.SourceVideoID) ||
		document.TranscriptGeneration != strings.TrimSpace(source.TranscriptGeneration) {
		return fmt.Errorf("source document does not match the knowledge object's video and transcript generation")
	}
	allowed := make(map[string]struct{})
	for _, chapter := range document.Chapters {
		for _, paragraph := range chapter.Paragraphs {
			for _, mark := range paragraph.TimeMarks {
				if evidenceID := strings.TrimSpace(mark.EvidenceSentenceID); evidenceID != "" {
					allowed[evidenceID] = struct{}{}
				}
			}
		}
	}
	for index, evidenceID := range source.EvidenceIDs {
		evidenceID = strings.TrimSpace(evidenceID)
		if _, ok := allowed[evidenceID]; !ok {
			return fmt.Errorf("evidence_ids[%d] %q is missing from the current transcript generation", index, evidenceID)
		}
	}
	return nil
}

// normalizeAndValidateWikiSlug lowercases + trims a model-supplied slug and
// rejects malformed ones. A valid slug contains only lowercase ASCII letters,
// digits, '-', '/', or CJK characters, has no leading/trailing/duplicate '/',
// and is non-empty. Keeping this strict stops the agent from persisting
// unreachable pages built from stray characters or garbled identifiers.
func normalizeAndValidateWikiSlug(raw string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.ReplaceAll(s, " ", "-")
	if s == "" {
		return "", errors.New("slug is required and must be non-empty")
	}
	if strings.Contains(s, "//") || strings.HasPrefix(s, "/") || strings.HasSuffix(s, "/") {
		return "", fmt.Errorf("invalid slug %q: '/' separators are malformed", raw)
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '/':
		case r >= 0x4E00 && r <= 0x9FFF: // keep CJK characters
		default:
			return "", fmt.Errorf(
				"invalid slug %q: character %q is not allowed (use lowercase letters, digits, '-', '/', or CJK)",
				raw, string(r),
			)
		}
	}
	return s, nil
}

// isSummaryNamespace reports whether a slug lives in the system-owned summary
// namespace (summary/…).
func isSummaryNamespace(slug string) bool {
	return strings.HasPrefix(slug, types.WikiPageTypeSummary+"/")
}
