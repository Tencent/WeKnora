/**
 * Agent editor tool list.
 *
 * Builds what the agent editor shows for `allowed_tools` from the backend
 * tool catalog (`GET /api/v1/agents/tools`), so that the checkboxes, the
 * stored config and the tools the runtime registers can be read off the same
 * source. Two rules keep stored values reachable:
 *   - a capability that the scope lacks only blocks ticking a tool; a ticked
 *     tool stays clickable so it can be unticked;
 *   - stored names the catalog does not offer are shown, ticked, so they can
 *     be removed instead of being saved back unseen.
 */
import type { AgentToolCatalog, AgentToolRequirement } from '../api/agent'
import {
  evaluateRequirement,
  type RequirementMissKind,
  type ScopeCapabilities,
} from './tool-capabilities'

export interface ToolText {
  label: string
  description: string
}

export interface EditorToolOption extends ToolText {
  value: string
  group: string
  danger: boolean
  /** The current knowledge base scope lacks what the tool needs. */
  unmet: boolean
  missKind: RequirementMissKind
  /** Only adding is blocked, so a ticked tool is never disabled. */
  disabled: boolean
}

export type StoredExtraKind = 'switch_managed' | 'unrecognized'

export interface StoredExtraTool {
  value: string
  kind: StoredExtraKind
  /** Once unticked it cannot be ticked again; the catalog does not offer it. */
  disabled: boolean
}

function toRequirement(requirement: AgentToolRequirement | undefined) {
  return { anyOf: requirement?.any_of, allOf: requirement?.all_of }
}

/**
 * The selectable tools in catalog order. `text` supplies translated labels;
 * when it returns nothing for a tool, the catalog's own label is used, so a
 * tool the frontend has no translation for still appears.
 */
export function buildToolOptions(
  catalog: AgentToolCatalog | null | undefined,
  selected: readonly string[],
  scope: ScopeCapabilities,
  hasAnyKb: boolean,
  text: (name: string) => ToolText | undefined,
): EditorToolOption[] {
  if (!catalog) return []
  const chosen = new Set(selected)
  return catalog.tools.map((tool) => {
    const { ok, missKind } = evaluateRequirement(toRequirement(tool.requirement), scope, hasAnyKb)
    const translated = text(tool.name)
    return {
      value: tool.name,
      label: translated?.label || tool.label || tool.name,
      description: translated?.description || tool.description || '',
      group: tool.group,
      danger: !!tool.danger,
      unmet: !ok,
      missKind,
      disabled: !ok && !chosen.has(tool.name),
    }
  })
}

/**
 * Stored or selected names the catalog does not offer as checkboxes, in the
 * order they appear. Pass the names stored when the editor opened together
 * with the current selection, so an entry stays visible after it is unticked.
 */
export function storedExtraTools(
  catalog: AgentToolCatalog | null | undefined,
  names: readonly string[],
  selected: readonly string[],
): StoredExtraTool[] {
  // Without a catalog nothing can be classified; the values are left as they are.
  if (!catalog) return []
  const offered = new Set(catalog.tools.map((tool) => tool.name))
  const switchManaged = new Set(catalog.switch_managed_tools)
  const chosen = new Set(selected)
  const seen = new Set<string>()
  const extras: StoredExtraTool[] = []
  for (const name of names) {
    if (!name || offered.has(name) || seen.has(name)) continue
    seen.add(name)
    extras.push({
      value: name,
      kind: switchManaged.has(name) ? 'switch_managed' : 'unrecognized',
      disabled: !chosen.has(name),
    })
  }
  return extras
}

export interface EffectiveToolSelection {
  /** Tools the runtime registers from the list, in display order. */
  names: string[]
  /** Ticked tools the current scope cannot serve. */
  inactive: string[]
  /** The list was never set, so the runtime uses the default tools. */
  usesDefaults: boolean
}

/**
 * The built-in tools a smart-reasoning run registers from `allowed_tools`.
 * Mirrors the runtime: an unset list means the catalog's default tools and an
 * empty one means none, names the catalog does not offer register nothing,
 * and tools whose capability is missing are dropped. Switch-managed tools are
 * left to their switches.
 */
export function effectiveToolSelection(
  catalog: AgentToolCatalog | null | undefined,
  selected: readonly string[] | null | undefined,
  scope: ScopeCapabilities,
  hasAnyKb: boolean,
): EffectiveToolSelection {
  if (!catalog) return { names: [], inactive: [], usesDefaults: false }
  const usesDefaults = selected == null
  const requested = new Set(selected ?? catalog.default_tools)
  const names: string[] = []
  const inactive: string[] = []
  for (const tool of catalog.tools) {
    if (!requested.has(tool.name)) continue
    if (evaluateRequirement(toRequirement(tool.requirement), scope, hasAnyKb).ok) {
      names.push(tool.name)
    } else if (!usesDefaults) {
      inactive.push(tool.name)
    }
  }
  return { names, inactive, usesDefaults }
}

export interface LoadedAllowedTools {
  /** The list to edit, with retired names already rewritten by the caller. */
  tools: string[]
  /**
   * The stored list was never set and stays unset: saving an untouched,
   * empty list sends null so the agent keeps the default tools.
   */
  keepUnset: boolean
}

/**
 * How the editor loads a stored `allowed_tools`. An unset list selects the
 * default tools at runtime, while an empty one means none, so an unset list
 * must not turn into an empty one on save. For a smart-reasoning agent the
 * catalog's default tools are written in and ticked, so the editor shows what
 * runs; without a catalog, or for another mode, the list stays unset.
 */
export function resolveLoadedAllowedTools(
  stored: readonly string[] | null | undefined,
  agentMode: string | undefined,
  catalog: AgentToolCatalog | null | undefined,
): LoadedAllowedTools {
  if (stored != null) return { tools: [...stored], keepUnset: false }
  if (agentMode === 'smart-reasoning' && catalog) {
    return { tools: [...catalog.default_tools], keepUnset: false }
  }
  return { tools: [], keepUnset: true }
}

/** The `allowed_tools` value to send on save. */
export function allowedToolsToSave(tools: readonly string[], keepUnset: boolean): string[] | null {
  return keepUnset && tools.length === 0 ? null : [...tools]
}
