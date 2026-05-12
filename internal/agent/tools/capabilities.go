// Package tools — capability requirements for built-in agent tools.
//
// This file is the Go mirror of `frontend/src/utils/tool-capabilities.ts`.
// They MUST be kept in sync: whenever you add a tool to the registry with
// specific KB requirements, update both maps.
//
// Why duplicate it? The frontend uses it to gray out tools and filter KBs
// in the agent editor and `@` mention menu; the backend uses it as the
// authoritative last line of defense in the retrieval pipeline — a client
// that skips the frontend filter (old tab, curl, rogue plugin) shouldn't be
// able to hand incompatible KBs/files to a tool that would just silently
// skip them.
package tools

import (
	"github.com/Tencent/WeKnora/internal/types"
)

// KBCapability names the capability flags a knowledge base can expose.
// Values mirror the keys of `types.KBCapabilities`.
type KBCapability string

const (
	CapVector  KBCapability = "vector"
	CapKeyword KBCapability = "keyword"
	CapWiki    KBCapability = "wiki"
	CapGraph   KBCapability = "graph"
	CapFAQ     KBCapability = "faq"
)

// ToolRequirement declares what a tool needs from the KB scope.
//
//   - AnyOf: scope must expose at least ONE listed capability.
//   - AllOf: scope must expose ALL listed capabilities.
//   - ConsumesFiles: the tool reads user-provided file refs from
//     `knowledge_ids`; the chat input uses this to decide whether to
//     even offer the `@file` list to the user.
//
// A nil/zero requirement means "no KB dependency" — the tool is always
// available and doesn't care about files.
type ToolRequirement struct {
	AnyOf         []KBCapability
	AllOf         []KBCapability
	ConsumesFiles bool
}

// ToolCapabilityRequirements maps tool names to their capability needs.
// Keep this aligned with `frontend/src/utils/tool-capabilities.ts`.
//
// Tools absent from this map default to "no requirement" and are treated
// as always available / file-consuming (permissive fallback: unknown MCP
// tools shouldn't silently break).
var ToolCapabilityRequirements = map[string]ToolRequirement{
	// ---- base / reasoning (no KB dependency, no file consumption) ----
	"thinking":     {},
	"todo_write":   {},
	"final_answer": {},

	// ---- RAG / chunk retrieval (need at least one chunk-indexed KB) ----
	"knowledge_search":      {AnyOf: []KBCapability{CapVector, CapKeyword}, ConsumesFiles: true},
	"grep_chunks":           {AnyOf: []KBCapability{CapVector, CapKeyword}, ConsumesFiles: true},
	"list_knowledge_chunks": {AnyOf: []KBCapability{CapVector, CapKeyword}, ConsumesFiles: true},
	"query_knowledge_graph": {AnyOf: []KBCapability{CapVector, CapKeyword}, ConsumesFiles: true},
	"get_document_info":     {AnyOf: []KBCapability{CapVector, CapKeyword}, ConsumesFiles: true},
	"database_query":        {AnyOf: []KBCapability{CapVector, CapKeyword}, ConsumesFiles: true},

	// ---- Wiki (operates on wiki pages; doesn't consume arbitrary file IDs) ----
	"wiki_search":          {AllOf: []KBCapability{CapWiki}},
	"wiki_read_page":       {AllOf: []KBCapability{CapWiki}},
	"wiki_read_source_doc": {AllOf: []KBCapability{CapWiki}},
	"wiki_flag_issue":      {AllOf: []KBCapability{CapWiki}},
	"wiki_write_page":      {AllOf: []KBCapability{CapWiki}},
	"wiki_replace_text":    {AllOf: []KBCapability{CapWiki}},
	"wiki_rename_page":     {AllOf: []KBCapability{CapWiki}},
	"wiki_delete_page":     {AllOf: []KBCapability{CapWiki}},
	"wiki_read_issue":      {AllOf: []KBCapability{CapWiki}},
	"wiki_update_issue":    {AllOf: []KBCapability{CapWiki}},

	// ---- Data analysis (reads table summary/column chunks from RAG ingest) ----
	"data_analysis": {AnyOf: []KBCapability{CapVector, CapKeyword}, ConsumesFiles: true},
	"data_schema":   {AnyOf: []KBCapability{CapVector, CapKeyword}, ConsumesFiles: true},
}

func hasCap(caps types.KBCapabilities, c KBCapability) bool {
	switch c {
	case CapVector:
		return caps.Vector
	case CapKeyword:
		return caps.Keyword
	case CapWiki:
		return caps.Wiki
	case CapGraph:
		return caps.Graph
	case CapFAQ:
		return caps.FAQ
	}
	return false
}

// KBFilter is the derived "KB must expose at least one of these capabilities"
// predicate for a set of allowed tools (see DeriveKBFilterFromTools).
type KBFilter struct {
	AnyOf []KBCapability
}

// IsEmpty reports whether the filter imposes no constraint
// (either no capability accumulated, or explicit zero value).
func (f KBFilter) IsEmpty() bool { return len(f.AnyOf) == 0 }

// DeriveKBFilterFromTools derives a capability filter such that a KB passes
// iff at least ONE of the allowed tools has a requirement the KB satisfies.
// Tools without any KB requirement don't contribute — if the allowed-tools
// list contains only such tools, the returned filter is empty (accept all).
func DeriveKBFilterFromTools(allowedTools []string) KBFilter {
	seen := make(map[KBCapability]struct{})
	for _, t := range allowedTools {
		req, ok := ToolCapabilityRequirements[t]
		if !ok {
			continue
		}
		for _, c := range req.AnyOf {
			seen[c] = struct{}{}
		}
		for _, c := range req.AllOf {
			seen[c] = struct{}{}
		}
	}
	if len(seen) == 0 {
		return KBFilter{}
	}
	out := make([]KBCapability, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	return KBFilter{AnyOf: out}
}

// KBSatisfiesToolRequirements reports whether a single KB is compatible
// with the agent's tool set. "Compatible" means: the KB exposes at least
// one capability that some tool in `allowedTools` requires. When the tool
// set imposes no KB requirement at all, every KB is considered compatible.
func KBSatisfiesToolRequirements(caps types.KBCapabilities, allowedTools []string) bool {
	f := DeriveKBFilterFromTools(allowedTools)
	if f.IsEmpty() {
		return true
	}
	for _, c := range f.AnyOf {
		if hasCap(caps, c) {
			return true
		}
	}
	return false
}

// quickAnswerKBFilter is the implicit capability requirement for the
// "quick-answer" (RAG) agent mode. Quick-answer drives retrieval purely
// through vector/keyword chunk search, so a KB with neither chunk index
// (e.g. wiki-only) cannot contribute any context and should be filtered
// out everywhere the user can pick a KB (agent KB scope, `@` mention list,
// chat KB selector).
//
// We treat this as a property of the agent MODE rather than of any
// specific tool, because quick-answer mode doesn't ship with an
// `allowed_tools` list — its retrieval is implicit.
var quickAnswerKBFilter = KBFilter{AnyOf: []KBCapability{CapVector, CapKeyword}}

// DeriveKBFilterForAgent derives the effective KB capability filter for a
// given agent configuration. It combines the implicit constraint from
// `agentMode` (quick-answer forces vector|keyword) with the tool-derived
// filter (any_of capabilities required by some allowed tool).
//
// The returned filter is the UNION of both contributions, which matches
// the existing any_of semantics: a KB passes iff it exposes at least one
// of the listed capabilities.
func DeriveKBFilterForAgent(agentMode string, allowedTools []string) KBFilter {
	seen := make(map[KBCapability]struct{})
	if agentMode == "quick-answer" {
		for _, c := range quickAnswerKBFilter.AnyOf {
			seen[c] = struct{}{}
		}
	}
	for _, c := range DeriveKBFilterFromTools(allowedTools).AnyOf {
		seen[c] = struct{}{}
	}
	if len(seen) == 0 {
		return KBFilter{}
	}
	out := make([]KBCapability, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	return KBFilter{AnyOf: out}
}

// KBSatisfiesAgentRequirements is the agent-aware variant of
// KBSatisfiesToolRequirements: it also enforces the implicit capability
// constraints of `agentMode` (currently: quick-answer requires vector or
// keyword indexing on the KB).
func KBSatisfiesAgentRequirements(caps types.KBCapabilities, agentMode string, allowedTools []string) bool {
	f := DeriveKBFilterForAgent(agentMode, allowedTools)
	if f.IsEmpty() {
		return true
	}
	for _, c := range f.AnyOf {
		if hasCap(caps, c) {
			return true
		}
	}
	return false
}

// ToolsConsumeFiles reports whether any tool in the allowed-tools list can
// use user-provided file references. Used to gate the `@file` listing in
// the chat input (and potentially SearchKnowledge defensively on the
// backend). An empty allowed-tools list is treated as "unknown → permissive".
func ToolsConsumeFiles(allowedTools []string) bool {
	if len(allowedTools) == 0 {
		return true
	}
	for _, t := range allowedTools {
		req, ok := ToolCapabilityRequirements[t]
		// Unknown tools (e.g. MCP tools, new builtin not yet registered here):
		// assume potentially file-consuming to avoid accidentally hiding the
		// file picker for users who just added a custom tool.
		if !ok {
			return true
		}
		if req.ConsumesFiles {
			return true
		}
	}
	return false
}
