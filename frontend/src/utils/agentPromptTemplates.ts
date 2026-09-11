import type { CustomAgentConfig } from '../api/agent'
import type { PromptTemplate, PromptTemplatesConfig } from '../api/system'

const systemTemplates = (config: CustomAgentConfig, templates: PromptTemplatesConfig) =>
  config.agent_mode === 'smart-reasoning' ? templates.agent_system_prompt || [] : templates.system_prompt

function matchingTemplate(body: string | undefined, id: string | undefined, list: PromptTemplate[]) {
  // Preserve the selected identity if multiple templates have identical bodies.
  return list.find(t => t.id === id && t.content === body)
    || list.find(t => t.content === body && !!body)
}

// The textarea shows effective content. Persist untouched templates as references;
// preserve every character of actual custom text and clear any stale reference.
export function serializeAgentPrompts(config: CustomAgentConfig, templates: PromptTemplatesConfig | null): CustomAgentConfig {
  if (!templates) return { ...config }
  const result = { ...config }
  const system = matchingTemplate(config.system_prompt, config.system_prompt_id, systemTemplates(config, templates))
  const context = matchingTemplate(config.context_template, config.context_template_id, templates.context_template)
  if (system) {
    result.system_prompt = ''
    result.system_prompt_id = system.id
  } else if (config.system_prompt) {
    delete result.system_prompt_id
  }
  if (context) {
    result.context_template = ''
    result.context_template_id = context.id
  } else if (config.context_template) {
    delete result.context_template_id
  }
  // These fields already inherit the global defaults when empty.
  const rewrite = templates.rewrite.find(t => t.default)
  const fallback = templates.fallback.find(t => t.mode === 'model' && t.default)
    || templates.fallback.find(t => t.mode === 'model')
  const fixed = templates.fallback.find(t => t.default && t.mode !== 'model')
  for (const [field, value] of [
    ['rewrite_prompt_system', rewrite?.content], ['rewrite_prompt_user', rewrite?.user],
    ['fallback_prompt', fallback?.content], ['fallback_response', fixed?.content],
  ] as const) {
    if (value && result[field] === value) result[field] = ''
  }
  return result
}

export function hydrateAgentPromptRefs(config: CustomAgentConfig, templates: PromptTemplatesConfig | null): CustomAgentConfig {
  if (!templates) return { ...config }
  return {
    ...config,
    system_prompt: config.system_prompt || systemTemplates(config, templates).find(t => t.id === config.system_prompt_id)?.content || '',
    context_template: config.context_template || templates.context_template.find(t => t.id === config.context_template_id)?.content || '',
  }
}
