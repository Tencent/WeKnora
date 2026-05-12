/**
 * Tool Capability Requirements
 *
 * Single source of truth for the mapping from agent tool names to the
 * knowledge-base capabilities each tool depends on. Used to:
 *   - Gray out tools whose dependencies aren't satisfied by the current scope
 *     (see `AgentEditorModal.vue` → `availableTools`).
 *   - Derive `kb_filter`-style predicates for agent type presets so the same
 *     declarative map drives both the tool allowlist and the KB allowlist
 *     (see `deriveKbFilterFromTools`).
 *
 * Capability names mirror the backend's KB capability set:
 *   - vector:  vector chunk index (embedding-based retrieval)
 *   - keyword: keyword/BM25 chunk index
 *   - wiki:    Wiki page indexing
 *   - graph:   knowledge graph index
 *   - faq:     FAQ-type KB (question/answer pairs as chunks)
 *
 * Requirement semantics:
 *   - `anyOf`: KB scope must expose at least ONE listed capability.
 *   - `allOf`: KB scope must expose ALL listed capabilities.
 *   - empty  : tool has no KB requirement (always available).
 *
 * IMPORTANT: keep this list aligned with backend tool definitions
 * (`internal/agent/tools/`). A tool missing from this map defaults to
 * "always available" so new tools don't silently start disabled.
 */

export type KBCapability = 'vector' | 'keyword' | 'wiki' | 'graph' | 'faq';

export interface ToolRequirement {
  anyOf?: KBCapability[];
  allOf?: KBCapability[];
  /**
   * Whether this tool can use user-provided file references (via @ 提及) as
   * an additional retrieval scope. Tools with `consumesFiles: false` ignore
   * `knowledge_ids`; we use this flag in the chat `@` dropdown to decide
   * whether to even offer the "文件" tab to the user.
   */
  consumesFiles?: boolean;
}

export const TOOL_CAPABILITY_REQUIREMENTS: Record<string, ToolRequirement> = {
  // ---- base / reasoning (no KB dependency) ----
  thinking: {},
  todo_write: {},
  final_answer: {},

  // ---- RAG / chunk retrieval (need at least one chunk-indexed KB) ----
  // We use vector|keyword as the canonical "has RAG chunks" signal. FAQ KBs
  // also expose chunks, but the current UX message bucket is "RAG KB"; once
  // we add a dedicated `requiresFaqKb` i18n key we can include `faq` here.
  knowledge_search:      { anyOf: ['vector', 'keyword'], consumesFiles: true },
  grep_chunks:           { anyOf: ['vector', 'keyword'], consumesFiles: true },
  list_knowledge_chunks: { anyOf: ['vector', 'keyword'], consumesFiles: true },
  query_knowledge_graph: { anyOf: ['vector', 'keyword'], consumesFiles: true },
  get_document_info:     { anyOf: ['vector', 'keyword'], consumesFiles: true },
  database_query:        { anyOf: ['vector', 'keyword'], consumesFiles: true },

  // ---- Wiki (operates on wiki pages referenced by the wiki machinery;
  //      arbitrary user-picked file IDs aren't meaningful here) ----
  wiki_search:          { allOf: ['wiki'] },
  wiki_read_page:       { allOf: ['wiki'] },
  wiki_read_source_doc: { allOf: ['wiki'] },
  wiki_flag_issue:      { allOf: ['wiki'] },
  wiki_write_page:      { allOf: ['wiki'] },
  wiki_replace_text:    { allOf: ['wiki'] },
  wiki_rename_page:     { allOf: ['wiki'] },
  wiki_delete_page:     { allOf: ['wiki'] },
  wiki_read_issue:      { allOf: ['wiki'] },
  wiki_update_issue:    { allOf: ['wiki'] },

  // ---- Data analysis (reads table summary/column chunks produced by RAG ingest) ----
  data_analysis: { anyOf: ['vector', 'keyword'], consumesFiles: true },
  data_schema:   { anyOf: ['vector', 'keyword'], consumesFiles: true },
};

/**
 * Aggregate KB-capability set available somewhere in the agent's KB scope.
 * All fields default to `false` for unknown capabilities.
 */
export interface ScopeCapabilities {
  vector: boolean;
  keyword: boolean;
  wiki: boolean;
  graph: boolean;
  faq: boolean;
}

/**
 * Machine-readable reason a tool is unsatisfiable. Map to a user-facing
 * string via i18n on the caller side (see `AgentEditorModal.vue`).
 */
export type RequirementMissKind = 'none' | 'needsKb' | 'needsRag' | 'needsWiki' | 'needsGraph' | 'needsFaq';

/**
 * Evaluate whether a tool's requirements are satisfied by the scope.
 *
 * @param toolName  the tool identifier (see `TOOL_CAPABILITY_REQUIREMENTS`)
 * @param scope     aggregate capabilities exposed by KBs currently in scope
 * @param hasAnyKb  whether the agent has at least one KB in scope
 *                  (i.e. `kb_selection_mode !== 'none'`)
 */
export function evaluateToolRequirement(
  toolName: string,
  scope: ScopeCapabilities,
  hasAnyKb: boolean,
): { ok: boolean; missKind: RequirementMissKind } {
  const req = TOOL_CAPABILITY_REQUIREMENTS[toolName];
  // Tools absent from the map or with no requirements: always available.
  if (!req || (!req.anyOf?.length && !req.allOf?.length)) {
    return { ok: true, missKind: 'none' };
  }

  // Any capability requirement implies needing at least one KB in scope.
  if (!hasAnyKb) return { ok: false, missKind: 'needsKb' };

  const has = (c: KBCapability): boolean => !!scope[c];

  if (req.allOf && req.allOf.length > 0) {
    for (const c of req.allOf) {
      if (!has(c)) return { ok: false, missKind: primaryMissKind(c) };
    }
  }
  if (req.anyOf && req.anyOf.length > 0) {
    if (!req.anyOf.some(has)) {
      return { ok: false, missKind: primaryMissKind(req.anyOf[0]) };
    }
  }
  return { ok: true, missKind: 'none' };
}

function primaryMissKind(c: KBCapability): RequirementMissKind {
  switch (c) {
    case 'wiki':    return 'needsWiki';
    case 'graph':   return 'needsGraph';
    case 'faq':     return 'needsFaq';
    case 'vector':
    case 'keyword': return 'needsRag';
  }
}

/**
 * Derive a `kb_filter`-style predicate for a list of tools: a KB satisfies
 * the derived filter iff at least ONE of the listed tools would be usable
 * on a scope consisting of just that KB.
 *
 * Used by Step 3 so presets don't have to hand-maintain `kb_filter` next
 * to their `allowed_tools` — the two are always derived from the same map.
 *
 * Returns `null` when none of the input tools have any KB requirement
 * (i.e. any KB is acceptable).
 */
export function deriveKbFilterFromTools(
  tools: string[],
): { any_of: KBCapability[] } | null {
  const caps = new Set<KBCapability>();
  for (const t of tools) {
    const req = TOOL_CAPABILITY_REQUIREMENTS[t];
    if (!req) continue;
    req.anyOf?.forEach(c => caps.add(c));
    req.allOf?.forEach(c => caps.add(c));
  }
  if (caps.size === 0) return null;
  return { any_of: Array.from(caps) };
}

/**
 * Implicit KB capability requirement for the "quick-answer" (RAG) agent
 * mode. Quick-answer drives retrieval purely through vector/keyword chunk
 * search and ships with NO `allowed_tools`, so the tool-derived filter
 * alone would let wiki-only KBs through even though they can't contribute
 * anything to a RAG answer. Treat this as a property of the agent MODE.
 */
const QUICK_ANSWER_KB_FILTER: { any_of: KBCapability[] } = {
  any_of: ['vector', 'keyword'],
};

/**
 * Agent-mode aware version of `deriveKbFilterFromTools`: unions the
 * tool-derived `any_of` with the implicit requirement of `agentMode`
 * (currently: quick-answer → vector|keyword).
 *
 * Returns `null` when neither the agent mode nor the tools impose any
 * capability constraint (i.e. any KB is acceptable).
 */
export function deriveKbFilterForAgent(
  agentMode: string | undefined | null,
  allowedTools: string[] | undefined | null,
): { any_of: KBCapability[] } | null {
  const caps = new Set<KBCapability>();
  if (agentMode === 'quick-answer') {
    QUICK_ANSWER_KB_FILTER.any_of.forEach(c => caps.add(c));
  }
  const fromTools = deriveKbFilterFromTools(allowedTools || []);
  fromTools?.any_of.forEach(c => caps.add(c));
  if (caps.size === 0) return null;
  return { any_of: Array.from(caps) };
}

/**
 * Agent-mode aware version of `kbSatisfiesToolRequirements`: ALSO honours
 * the implicit capability requirement of `agentMode` (quick-answer needs
 * vector or keyword indexing). Use this anywhere the user is choosing a
 * KB for an agent — agent editor "specified KB" dropdown, chat `@`
 * mention list, etc.
 */
export function kbSatisfiesAgentRequirements(
  kbCaps: Partial<ScopeCapabilities> | undefined | null,
  agentMode: string | undefined | null,
  allowedTools: string[] | undefined | null,
): boolean {
  const filter = deriveKbFilterForAgent(agentMode, allowedTools);
  if (!filter) return true;
  if (!kbCaps) return false;
  return filter.any_of.some(c => !!kbCaps[c]);
}

/**
 * Decide whether a single KB is compatible with an agent's tool set.
 *
 * "Compatible" here means: at least ONE tool in `allowedTools` requires a
 * capability that this KB exposes — i.e. the agent has SOMETHING useful to
 * do against this KB. Tools with no KB requirement don't contribute.
 *
 * If `allowedTools` contains no KB-dependent tool, every KB is considered
 * compatible (the agent won't retrieve anything either way, so filtering
 * is not meaningful).
 */
export function kbSatisfiesToolRequirements(
  kbCaps: Partial<ScopeCapabilities> | undefined | null,
  allowedTools: string[] | undefined | null,
): boolean {
  const filter = deriveKbFilterFromTools(allowedTools || []);
  if (!filter) return true;
  if (!kbCaps) return false;
  return filter.any_of.some(c => !!kbCaps[c]);
}

/**
 * True iff any of the agent's allowed tools can consume user-provided file
 * references (`knowledge_ids`). Used by the chat `@` menu to decide whether
 * showing the "文件" list makes sense at all — e.g. a pure Wiki agent has no
 * tool that would read an arbitrary file the user picks, so we don't offer
 * it. `undefined`/`null`/empty → true (permissive fallback: if we don't
 * know, show files).
 */
export function toolsConsumeFiles(
  allowedTools: string[] | undefined | null,
): boolean {
  if (!allowedTools || allowedTools.length === 0) return true;
  for (const t of allowedTools) {
    const req = TOOL_CAPABILITY_REQUIREMENTS[t];
    // Unknown tools (e.g. MCP tools) are treated as potentially file-consuming.
    if (!req) return true;
    if (req.consumesFiles) return true;
  }
  return false;
}
