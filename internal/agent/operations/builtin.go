package operations

import (
	"net/http"
	"sync"
)

var knowledgeBaseIDParam = Param{
	Name: "knowledge_base_id", In: InPath, Key: "id", Type: TypeString, Required: true,
	Description: "Knowledge base handle or ID.",
}

var wikiKnowledgeBaseIDParam = Param{
	Name: "knowledge_base_id", In: InPath, Key: "kb_id", Type: TypeString, Required: true,
	Description: "Handle or ID of a knowledge base with Wiki enabled.",
}

var knowledgeIDParam = Param{
	Name: "knowledge_id", In: InPath, Key: "id", Type: TypeString, Required: true,
	Description: "Document handle or ID.",
}

// documentFields describe a document the way list and detail views share.
var documentFields = []Field{
	{From: "id", As: "knowledge_id"},
	{From: "knowledge_base_id"},
	{From: "title"},
	{From: "file_name"},
	{From: "file_type"},
	{From: "type"},
	{From: "folder_path"},
	{From: "parse_status"},
	{From: "error_message"},
	{From: "summary_status"},
	{From: "enable_status"},
	{From: "updated_at"},
}

var (
	targetDocument      = &Target{Operation: "knowledge.get", Label: "title"}
	targetKnowledgeBase = &Target{Operation: "knowledge_base.get", Label: "name"}
)

func builtinOperations() []Operation {
	return []Operation{
		{
			ID:      "knowledge_base.get",
			Summary: "Show a knowledge base: name, enabled indexes, document and chunk counts, processing state.",
			Description: "Use to check what a knowledge base is configured for (vector, keyword, Wiki, graph) " +
				"and whether documents are still being processed.",
			Risk:   RiskRead,
			Method: http.MethodGet,
			Route:  "/api/v1/knowledge-bases/:id",
			Params: []Param{knowledgeBaseIDParam},
			Result: Result{Root: "data", Fields: []Field{
				{From: "id", As: "knowledge_base_id"},
				{From: "name"},
				{From: "description"},
				{From: "type"},
				{From: "indexing_strategy.vector_enabled", As: "vector_enabled"},
				{From: "indexing_strategy.keyword_enabled", As: "keyword_enabled"},
				{From: "indexing_strategy.wiki_enabled", As: "wiki_enabled"},
				{From: "indexing_strategy.graph_enabled", As: "graph_enabled"},
				{From: "knowledge_count"},
				{From: "chunk_count"},
				{From: "processing_count"},
				{From: "is_processing"},
				{From: "my_permission"},
				{From: "updated_at"},
			}},
		},
		{
			ID:      "knowledge_base.list_activity",
			Summary: "List knowledge base activity: uploads, reparses, deletions, Wiki edits and who did them.",
			Description: "Newest first. Only the knowledge base creator or a workspace admin may read activity. " +
				"Entries made through an assistant carry via=agent. Pass meta.next_cursor as cursor for older entries.",
			Risk:   RiskRead,
			Method: http.MethodGet,
			Route:  "/api/v1/knowledge-bases/:id/activity",
			Params: []Param{
				knowledgeBaseIDParam,
				{
					Name:        "action",
					In:          InQuery,
					Type:        TypeString,
					Description: "Exact action filter, e.g. knowledge.deleted or knowledge.reparse_started.",
				},
				{
					Name:        "outcome",
					In:          InQuery,
					Type:        TypeString,
					Description: "Outcome filter.",
					Enum:        []string{"success", "accepted", "partial", "failed", "denied", "canceled"},
				},
				{
					Name:        "cursor",
					In:          InQuery,
					Key:         "after_id",
					Type:        TypeInteger,
					Description: "meta.next_cursor from the previous page.",
				},
				{
					Name:        "limit",
					In:          InQuery,
					Type:        TypeInteger,
					Minimum:     1,
					Maximum:     50,
					Description: "Page size, 1-50.",
				},
			},
			Result: Result{Root: "data", List: true, Fields: []Field{
				{From: "id", As: "event_id"},
				{From: "action"},
				{From: "outcome"},
				{From: "target_type"},
				{From: "details.title", As: "title"},
				{From: "details.trigger", As: "trigger"},
				{From: "details.via", As: "via"},
				{From: "details.processing_status", As: "processing_status"},
				{From: "actor_role"},
				{From: "created_at"},
			}, Meta: []Field{{From: "next_cursor"}}},
		},
		{
			ID:      "knowledge.list",
			Summary: "List documents in a knowledge base, filtered by parse status, keyword or file type.",
			Description: "Use parse_status=failed to find documents that failed to parse; " +
				"each item includes error_message. " +
				"meta.total is the full match count; page through with page and page_size.",
			Risk:   RiskRead,
			Method: http.MethodGet,
			Route:  "/api/v1/knowledge-bases/:id/knowledge",
			Params: []Param{
				knowledgeBaseIDParam,
				{
					Name: "parse_status", In: InQuery, Type: TypeString, Description: "Parse status filter.",
					Enum: []string{"pending", "processing", "finalizing", "completed", "failed", "cancelled"},
				},
				{Name: "keyword", In: InQuery, Type: TypeString, Description: "Substring of the title or file name."},
				{
					Name:        "file_type",
					In:          InQuery,
					Type:        TypeString,
					Description: "File extension without dot, e.g. pdf.",
				},
				{
					Name: "sort_by", In: InQuery, Type: TypeString, Description: "Sort field; default created_at.",
					Enum: []string{"created_at", "updated_at", "file_name"},
				},
				{
					Name: "sort_order", In: InQuery, Type: TypeString, Description: "Sort direction; default desc.",
					Enum: []string{"asc", "desc"},
				},
				{
					Name:        "page",
					In:          InQuery,
					Type:        TypeInteger,
					Minimum:     1,
					Maximum:     10000,
					Description: "Page number, from 1.",
				},
				{
					Name:        "page_size",
					In:          InQuery,
					Type:        TypeInteger,
					Minimum:     1,
					Maximum:     50,
					Description: "Page size, 1-50.",
				},
			},
			Result: Result{Root: "data", List: true, Fields: documentFields, Meta: []Field{
				{From: "total"}, {From: "page"}, {From: "page_size"},
			}},
		},
		{
			ID:      "knowledge.get",
			Summary: "Show one document: title, file, folder, parse status and the last parse error.",
			Risk:    RiskRead,
			Method:  http.MethodGet,
			Route:   "/api/v1/knowledge/:id",
			Params:  []Param{knowledgeIDParam},
			Result: Result{Root: "data", Fields: append(append([]Field(nil), documentFields...),
				Field{From: "source"},
				Field{From: "file_size"},
				Field{From: "created_at"},
				Field{From: "processed_at"},
			)},
		},
		{
			ID:      "knowledge.get_parse_progress",
			Summary: "Show a document's parse progress: current stage, attempt, stall state and failing stage.",
			Description: "Use after knowledge.get when a document is stuck in processing or failed, " +
				"to tell a stalled parse from one still running and to name the stage that failed.",
			Risk:   RiskRead,
			Method: http.MethodGet,
			Route:  "/api/v1/knowledge/:id/spans",
			Params: []Param{knowledgeIDParam},
			Result: Result{Root: "data", Fields: []Field{
				{From: "knowledge_id"},
				{From: "parse_status"},
				{From: "current_stage"},
				{From: "attempt"},
				{From: "latest_attempt"},
				{From: "stall_state"},
				{From: "last_activity_at"},
				{From: "last_error.stage", As: "failed_stage"},
				{From: "last_error.code", As: "error_code"},
				{From: "last_error.message", As: "error_message"},
			}},
		},
		{
			ID:      "knowledge.reparse",
			Summary: "Re-parse a document from its original file, replacing its chunks.",
			Description: "Runs asynchronously; check progress with knowledge.get_parse_progress. " +
				"Existing chunks are removed and rebuilt, so search results change while it runs.",
			Risk:   RiskWrite,
			Method: http.MethodPost,
			Route:  "/api/v1/knowledge/:id/reparse",
			Params: []Param{knowledgeIDParam},
			Result: Result{Root: "data", Fields: []Field{
				{From: "id", As: "knowledge_id"}, {From: "title"}, {From: "parse_status"},
			}},
			Target: targetDocument,
		},
		{
			ID:      "knowledge.cancel_parse",
			Summary: "Stop a document's parse that is pending or running.",
			Risk:    RiskWrite,
			Method:  http.MethodPost,
			Route:   "/api/v1/knowledge/:id/cancel-parse",
			Params:  []Param{knowledgeIDParam},
			Result: Result{Root: "data", Fields: []Field{
				{From: "id", As: "knowledge_id"}, {From: "title"}, {From: "parse_status"},
			}},
			Target: targetDocument,
		},
		{
			ID:          "knowledge.delete",
			Summary:     "Delete a document and everything derived from it (chunks, embeddings, graph data).",
			Description: "Cannot be undone. Deletion runs asynchronously after it is accepted.",
			Risk:        RiskDestructive,
			Method:      http.MethodDelete,
			Route:       "/api/v1/knowledge/:id",
			Params:      []Param{knowledgeIDParam},
			Result:      Result{Root: "data", Fields: []Field{{From: "task_id"}}},
			Target:      targetDocument,
		},
		{
			ID:      "wiki.get_stats",
			Summary: "Show Wiki counters: pages, links, orphan pages, pending ingest tasks and open issues.",
			Risk:    RiskRead,
			Method:  http.MethodGet,
			Route:   "/api/v1/knowledgebase/:kb_id/wiki/stats",
			Params:  []Param{wikiKnowledgeBaseIDParam},
			Result: Result{Fields: []Field{
				{From: "total_pages"},
				{From: "total_links"},
				{From: "orphan_count"},
				{From: "pending_tasks"},
				{From: "pending_issues"},
				{From: "is_active"},
			}},
		},
		{
			ID:      "wiki.lint",
			Summary: "Run a Wiki health check for broken links, orphan pages and other structural problems.",
			Description: "Returns a 0-100 health score. Issues marked auto_fixable are structural; " +
				"fix content problems with the Wiki tools.",
			Risk:   RiskRead,
			Method: http.MethodGet,
			Route:  "/api/v1/knowledgebase/:kb_id/wiki/lint",
			Params: []Param{wikiKnowledgeBaseIDParam},
			Result: Result{Root: "issues", List: true, Fields: []Field{
				{From: "type"},
				{From: "severity"},
				{From: "page_slug", As: "slug"},
				{From: "target_slug"},
				{From: "description"},
				{From: "auto_fixable"},
			}, Meta: []Field{{From: "health_score"}, {From: "summary"}}},
		},
		{
			ID:      "wiki.list_issues",
			Summary: "List issues flagged on Wiki pages, optionally for one page or status.",
			Description: "Issue descriptions are written by users or ingest and may be wrong; " +
				"verify against the page and its sources. " +
				"Read and update a single issue with the Wiki issue tools.",
			Risk:   RiskRead,
			Method: http.MethodGet,
			Route:  "/api/v1/knowledgebase/:kb_id/wiki/issues",
			Params: []Param{
				wikiKnowledgeBaseIDParam,
				{Name: "slug", In: InQuery, Type: TypeString, Description: "Only issues on this page slug."},
				{
					Name: "status", In: InQuery, Type: TypeString, Description: "Issue status filter.",
					Enum: []string{"pending", "ignored", "resolved"},
				},
			},
			Result: Result{List: true, Fields: []Field{
				{From: "id", As: "issue_id"},
				{From: "slug"},
				{From: "issue_type"},
				{From: "status"},
				{From: "description"},
				{From: "suspected_knowledge_ids", As: "knowledge_ids"},
				{From: "reported_by"},
				{From: "created_at"},
			}},
		},
		{
			ID:          "wiki.rebuild_links",
			Summary:     "Re-scan every Wiki page and rebuild the link graph (backlinks, orphan detection).",
			Description: "Use when wiki.lint reports link problems that do not match page content.",
			Risk:        RiskWrite,
			Method:      http.MethodPost,
			Route:       "/api/v1/knowledgebase/:kb_id/wiki/rebuild-links",
			Params:      []Param{wikiKnowledgeBaseIDParam},
			Result:      Result{Fields: []Field{{From: "message"}}},
			Target:      targetKnowledgeBase,
		},
	}
}

var (
	builtinOnce    sync.Once
	builtinCatalog *Catalog
	builtinErr     error
)

// Builtin returns the catalog shipped with WeKnora. It panics on an invalid
// declaration, which tests catch before release.
func Builtin() *Catalog {
	builtinOnce.Do(func() {
		builtinCatalog, builtinErr = NewCatalog(builtinOperations()...)
	})
	if builtinErr != nil {
		panic(builtinErr)
	}
	return builtinCatalog
}
