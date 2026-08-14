package modelcontext

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	toolDatabaseQuery   = "database_query"
	toolDataAnalysis    = "data_analysis"
	toolWikiReadIssue   = "wiki_read_issue"
	toolWikiUpdateIssue = "wiki_update_issue"
)

var issueHandleShapeRE = regexp.MustCompile(`^i[1-9][0-9]*$`)

// sourceKeySpace identifies which source-handle space an ID-bearing JSON key
// belongs to.
type sourceKeySpace int

const (
	spaceChunk sourceKeySpace = iota
	spaceDocument
	spaceDocumentRef // "knowledgeID|title" stored refs; only the ID is durable
	spaceKnowledgeBase
	spaceWeb
)

// sourceKeySpaces is the single table of ID-bearing keys the source codec
// understands. It drives both handle registration (registerSourceIDByKey) and
// the handle-shaped-value decode gate (walkJSON), so registration and decoding
// cannot drift apart. Every key referenced by a toolHandlePolicies sourceIDKeys
// set must appear here (enforced by a test).
var sourceKeySpaces = map[string]sourceKeySpace{
	"chunk_id": spaceChunk, "faq_id": spaceChunk, "chunk_ids": spaceChunk, "faq_ids": spaceChunk,
	"knowledge_id": spaceDocument, "knowledge_ids": spaceDocument, "suspected_knowledge_ids": spaceDocument,
	"source_refs":    spaceDocumentRef,
	"knowledge_base": spaceKnowledgeBase, "knowledge_base_id": spaceKnowledgeBase,
	"knowledge_base_ids": spaceKnowledgeBase, "kb_id": spaceKnowledgeBase, "kb_ids": spaceKnowledgeBase,
	"url": spaceWeb, "urls": spaceWeb,
}

type toolHandlePolicy struct {
	sourceIDKeys        map[string]struct{}
	sourceTextKeys      map[string]struct{}
	sourceOutput        bool
	decodedIssueIDKeys  map[string]struct{}
	encodedIssueIDKeys  map[string]struct{}
	encodeKnownIssueIDs bool
}

// toolHandlePolicies is the complete allowlist for built-in tool fields whose
// identifiers need more than the generic resource/source codecs. Field names
// alone are deliberately insufficient: a dynamic MCP tool may use the same
// name with unrelated semantics and must remain opaque.
var toolHandlePolicies = map[string]toolHandlePolicy{
	"knowledge_search": {
		sourceIDKeys: map[string]struct{}{"knowledge_base_ids": {}},
		sourceOutput: true,
	},
	"grep_chunks": {
		sourceOutput: true,
	},
	"list_knowledge_chunks": {
		sourceIDKeys: map[string]struct{}{"knowledge_id": {}, "faq_id": {}, "chunk_id": {}},
		sourceOutput: true,
	},
	"get_document_info": {
		sourceIDKeys: map[string]struct{}{"knowledge_ids": {}, "faq_ids": {}},
		sourceOutput: true,
	},
	// Past conversations carry no durable chunk or document IDs, so there is
	// nothing to compact; the output is prose the model may quote.
	"search_conversations": {},
	// Memories are single sentences about the person. They carry no chunk or
	// document IDs of their own, and the memory item IDs never leave the
	// service, so there is nothing here for the model to hold a handle on.
	"search_memory": {},
	"query_knowledge_graph": {
		sourceIDKeys: map[string]struct{}{"knowledge_base_ids": {}},
		sourceOutput: true,
	},
	toolDatabaseQuery: {
		sourceTextKeys: map[string]struct{}{"sql": {}},
		sourceOutput:   true,
	},
	toolDataAnalysis: {
		sourceIDKeys:   map[string]struct{}{"knowledge_id": {}},
		sourceTextKeys: map[string]struct{}{"sql": {}},
	},
	"data_schema": {
		sourceIDKeys: map[string]struct{}{"knowledge_id": {}},
	},
	"web_fetch": {
		sourceIDKeys: map[string]struct{}{"url": {}, "urls": {}},
		sourceOutput: true,
	},
	"web_search": {
		sourceOutput: true,
	},
	"wiki_read_page": {
		sourceOutput: true,
	},
	"wiki_read_source_doc": {
		sourceIDKeys: map[string]struct{}{"knowledge_id": {}},
		sourceOutput: true,
	},
	"wiki_write_page": {
		sourceIDKeys: map[string]struct{}{"source_refs": {}},
	},
	"wiki_replace_text": {
		sourceIDKeys: map[string]struct{}{"source_refs": {}},
	},
	"wiki_flag_issue": {
		sourceIDKeys: map[string]struct{}{"suspected_knowledge_ids": {}},
	},
	"wiki_search": {
		sourceIDKeys: map[string]struct{}{"knowledge_base_id": {}},
		sourceOutput: true,
	},
	toolWikiReadIssue: {
		decodedIssueIDKeys:  map[string]struct{}{"issue_id": {}},
		encodedIssueIDKeys:  map[string]struct{}{"id": {}},
		encodeKnownIssueIDs: true,
		sourceOutput:        true,
	},
	toolWikiUpdateIssue: {
		decodedIssueIDKeys:  map[string]struct{}{"issue_id": {}},
		encodeKnownIssueIDs: true,
	},
	// Mutation tools echo the slug they acted on and surface validation errors
	// that can quote a durable KB or document ID. They own no source arguments,
	// but their output must still be compacted before the model sees it.
	"wiki_rename_page": {
		sourceOutput: true,
	},
	"wiki_delete_page": {
		sourceOutput: true,
	},
	// Agent-private bookkeeping tools echo model-authored text. They need
	// compaction so a durable ID quoted back by the model is re-compacted, but
	// never structured source rendering.
	"thinking":   {},
	"todo_write": {},
	// Skill output is user-authored content of unknown shape. Compact known
	// durable IDs, but do not mine it for source keys: a skill's JSON may use
	// "url"/"knowledge_id" with unrelated semantics.
	"read_skill":           {},
	"execute_skill_script": {},
	// Sandbox shell/file tools return whatever a skill script produced, so they
	// carry the same unknown-shape caveat as skill output above.
	"shell_exec":         {},
	"list_sandbox_files": {},
	"read_sandbox_file":  {},
}

// HasToolPolicy reports whether a tool has an explicit model-handle policy.
// Built-in tools must declare one (even an empty policy) so that adding a tool
// is a deliberate decision about what the model may see, rather than a silent
// fall-through to fully opaque output.
func HasToolPolicy(toolName string) bool {
	_, ok := toolHandlePolicies[toolName]
	return ok
}

func sourceArgumentAllowed(toolName, key string) bool {
	policy, ok := toolHandlePolicies[toolName]
	if !ok {
		return false
	}
	_, ok = policy.sourceIDKeys[strings.ToLower(key)]
	return ok
}

func sourceOutputAllowed(toolName string) bool {
	if toolName == "" {
		// Preserve the generic low-level facade for non-Agent callers. Agent
		// lifecycles always provide the concrete tool name.
		return true
	}
	policy, ok := toolHandlePolicies[toolName]
	return ok && policy.sourceOutput
}

func sourceCompactionAllowed(toolName string) bool {
	if toolName == "" {
		return true
	}
	_, ok := toolHandlePolicies[toolName]
	return ok
}

// decodeToolPolicies handles the deliberately small set of arguments whose
// model handles are embedded in structured text or belong to a tool-private
// identity space. Generic free text is never rewritten.
func (r *Registry) decodeToolPolicies(call *types.LLMToolCall) {
	if r == nil || call == nil {
		return
	}
	policy, ok := toolHandlePolicies[call.Function.Name]
	if !ok {
		return
	}
	call.Function.Arguments = rewriteJSONStringValues(
		call.Function.Arguments,
		func(key, value string) string {
			if _, ok := policy.sourceTextKeys[key]; ok {
				return r.sources.DecodeKnownQuotedText(value)
			}
			if _, ok := policy.decodedIssueIDKeys[key]; ok && issueHandleShapeRE.MatchString(strings.TrimSpace(value)) {
				if durable, ok := r.issues.Resolve(value); ok {
					return durable
				}
			}
			return value
		},
	)
}

// encodeReplayedToolPolicies applies the exact inverse of decodeToolPolicies
// to assistant tool calls replayed into a later model round. Only durable
// values already proven and registered in this request are compacted.
func (r *Registry) encodeReplayedToolPolicies(call *chat.ToolCall) {
	if r == nil || call == nil {
		return
	}
	policy, ok := toolHandlePolicies[call.Function.Name]
	if !ok {
		return
	}
	call.Function.Arguments = rewriteJSONStringValues(
		call.Function.Arguments,
		func(key, value string) string {
			if _, ok := policy.sourceTextKeys[key]; ok {
				return r.sources.CompactKnownText(value)
			}
			if _, ok := policy.decodedIssueIDKeys[key]; ok {
				return r.issues.EncodeKnownText(value)
			}
			return value
		},
	)
}

func (r *Registry) unresolvedPrivateToolHandles(toolName, raw string) []string {
	if r == nil || raw == "" {
		return nil
	}
	policy, ok := toolHandlePolicies[toolName]
	if !ok || (len(policy.decodedIssueIDKeys) == 0 && len(policy.sourceTextKeys) == 0) {
		return nil
	}
	var unresolved []string
	_ = walkJSONStringValues(raw, func(key, value string) string {
		value = strings.TrimSpace(value)
		if _, ok := policy.sourceTextKeys[key]; ok {
			unresolved = append(unresolved, r.sources.UnresolvedQuotedTextHandles(value)...)
		}
		if _, ok := policy.decodedIssueIDKeys[key]; ok && issueHandleShapeRE.MatchString(value) {
			if _, ok := r.issues.Resolve(value); !ok {
				unresolved = append(unresolved, value)
			}
		}
		return value
	})
	return unresolved
}

// encodeToolPrivateResult registers and compacts identifiers that are local to
// one built-in tool family. Wiki issue IDs are the only such identity today;
// MCP tools remain opaque unless they add an explicit policy here.
func (r *Registry) encodeToolPrivateResult(toolName, output string) string {
	if r == nil || output == "" {
		return output
	}
	policy, ok := toolHandlePolicies[toolName]
	if !ok {
		return output
	}
	if len(policy.encodedIssueIDKeys) > 0 {
		output = rewriteJSONStringValues(output, func(key, value string) string {
			if _, ok := policy.encodedIssueIDKeys[key]; ok && strings.TrimSpace(value) != "" {
				if issueHandleShapeRE.MatchString(strings.TrimSpace(value)) {
					// Model-facing tool messages can be replayed across several
					// rounds. Never treat an existing temporary handle as a new
					// durable issue identity and allocate i2/i3 drift.
					return value
				}
				return r.issues.Register(value)
			}
			return value
		})
	}
	if policy.encodeKnownIssueIDs {
		output = r.issues.EncodeKnownText(output)
	}
	return output
}

func rewriteJSONStringValues(raw string, rewrite func(key, value string) string) string {
	var value interface{}
	if json.Unmarshal([]byte(raw), &value) != nil {
		return raw
	}
	value = walkJSONValue("", value, rewrite)
	encoded, err := json.Marshal(value)
	if err != nil {
		return raw
	}
	return string(encoded)
}

func walkJSONStringValues(raw string, rewrite func(key, value string) string) error {
	var value interface{}
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return err
	}
	walkJSONValue("", value, rewrite)
	return nil
}

func walkJSONValue(key string, value interface{}, rewrite func(key, value string) string) interface{} {
	switch typed := value.(type) {
	case string:
		return rewrite(strings.ToLower(key), typed)
	case []interface{}:
		for i := range typed {
			typed[i] = walkJSONValue(key, typed[i], rewrite)
		}
	case map[string]interface{}:
		for childKey, item := range typed {
			typed[childKey] = walkJSONValue(childKey, item, rewrite)
		}
	}
	return value
}
